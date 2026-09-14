package tfprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type lunaTransformWireCall struct {
	typ  string
	data map[string]any
}

// lunaTransformWireCaptureRequester is intentionally a Resource-callback
// seam, rather than an encoder fixture. It supplies the read/guard replies
// needed by groupResource.Update and records the direct OBS setter requests
// sent after those replies.
type lunaTransformWireCaptureRequester struct {
	initial   bridgePlacementModel
	settled   bridgePlacementModel
	groupRead int
	calls     []lunaTransformWireCall
}

func (r *lunaTransformWireCaptureRequester) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	r.calls = append(r.calls, lunaTransformWireCall{typ: typ, data: data})
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}, nil
	case "GetSceneList":
		return lunaLifecycleSceneList(), nil
	case "GetVersion":
		return lunaTransformWireVersion(), nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}, nil
	case "CallVendorRequest":
		requestType := stringValue(data["requestType"])
		switch requestType {
		case "GetCapabilities":
			return lunaTransformWireEnvelope(requestType, lunaLifecycleCapabilities([]string{"GetGroup"})), nil
		case "GetGroup":
			placement := r.initial
			if r.groupRead > 0 {
				placement = r.settled
			}
			r.groupRead++
			return lunaTransformWireEnvelope(requestType, lunaLifecycleGroupResult(7, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{})), nil
		default:
			return nil, fmt.Errorf("unexpected bridge request %s", requestType)
		}
	case "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex":
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("unexpected direct request %s", typ)
	}
}

func lunaTransformWireVersion() map[string]any {
	return map[string]any{
		"obsVersion":          "32.2.2",
		"obsWebSocketVersion": "5.7.4",
		"rpcVersion":          1.0,
		"availableRequests": []any{
			"CallVendorRequest", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex",
		},
	}
}

func lunaTransformWireEnvelope(requestType string, result map[string]any) map[string]any {
	return map[string]any{"responseData": map[string]any{
		"contractVersion":       float64(bridgeContractVersion),
		"bridgeVersion":         bridgeSupportedVersion,
		"requestType":           requestType,
		"clientOperationId":     "",
		"outcome":               "completed",
		"code":                  "ok",
		"message":               "transform wire capture",
		"mutationAttempted":     false,
		"postconditionVerified": true,
		"result":                result,
	}}
}

func lunaTransformWireClonePlacement(in bridgePlacementModel) bridgePlacementModel {
	out := in
	if in.Transform != nil {
		transform := *in.Transform
		out.Transform = &transform
	}
	return out
}

func lunaTransformWireRunGroupUpdate(t *testing.T, old, want bridgePlacementModel) (*lunaTransformWireCaptureRequester, *resource.UpdateResponse) {
	t.Helper()
	ctx := context.Background()
	stateModel := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
	stateModel.Placement = func() *bridgePlacementModel {
		placement := lunaTransformWireClonePlacement(old)
		return &placement
	}()
	planModel := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
	planModel.Placement = func() *bridgePlacementModel {
		placement := lunaTransformWireClonePlacement(want)
		return &placement
	}()

	sc := lunaLifecycleGroupSchema(ctx)
	requestPlan := lunaLifecycleSchemaAndPlan(t, ctx, sc, planModel)
	response := &resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, planModel)}
	capture := &lunaTransformWireCaptureRequester{
		initial: lunaTransformWireClonePlacement(old),
		settled: lunaTransformWireClonePlacement(want),
	}
	(&groupResource{client: &client{testRequester: capture}}).Update(ctx, resource.UpdateRequest{
		Plan:  requestPlan,
		State: lunaLifecycleSchemaAndState(t, ctx, sc, stateModel),
	}, response)
	return capture, response
}

func lunaTransformWireDirectMutations(capture *lunaTransformWireCaptureRequester) []lunaTransformWireCall {
	var out []lunaTransformWireCall
	for _, call := range capture.calls {
		switch call.typ {
		case "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex":
			out = append(out, call)
		}
	}
	return out
}

func lunaTransformWireDecodeObject(t *testing.T, raw map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal captured transform: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode captured transform: %v; json=%s", err, encoded)
	}
	return decoded
}

func lunaTransformWireAssertKeys(t *testing.T, got map[string]any, included, omitted []string) {
	t.Helper()
	want := make(map[string]bool, len(included))
	for _, key := range included {
		want[key] = true
		if _, ok := got[key]; !ok {
			t.Errorf("captured transform omitted required key %q: %#v", key, got)
		}
	}
	for _, key := range omitted {
		if _, ok := got[key]; ok {
			t.Errorf("captured transform included forbidden key %q: %#v", key, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("captured transform keys=%v, want exactly=%v", got, included)
	}
}

func lunaTransformWireAssertNumber(t *testing.T, object map[string]any, key string, want float64) {
	t.Helper()
	raw, ok := object[key]
	if !ok {
		t.Errorf("captured transform omitted numeric key %q", key)
		return
	}
	number, ok := raw.(json.Number)
	if !ok {
		t.Errorf("captured transform %s has JSON type %T, want number", key, raw)
		return
	}
	got, err := number.Float64()
	if err != nil || got != want {
		t.Errorf("captured transform %s=%q, want %v", key, number, want)
	}
}

func lunaTransformWireAssertNumericTypes(t *testing.T, object map[string]any, expected map[string]float64) {
	t.Helper()
	for key, want := range expected {
		lunaTransformWireAssertNumber(t, object, key, want)
	}
}

func TestLunaTransformWireGroupUpdateOmitsRawNoneBounds(t *testing.T) {
	old := lunaTransformWireClonePlacement(lunaLifecyclePlacement())
	want := lunaTransformWireClonePlacement(old)
	want.Transform.X = types.Float64Value(40)

	capture, response := lunaTransformWireRunGroupUpdate(t, old, want)
	if response.Diagnostics.HasError() {
		t.Fatalf("NONE transform update diagnostics: %v", response.Diagnostics)
	}
	mutations := lunaTransformWireDirectMutations(capture)
	if len(mutations) != 1 || mutations[0].typ != "SetSceneItemTransform" {
		t.Fatalf("direct mutations=%#v, want one SetSceneItemTransform", mutations)
	}
	data := mutations[0].data
	if stringValue(data["sceneName"]) != lunaLifecycleSceneName || int64Value(data["sceneItemId"]) != 7 {
		t.Fatalf("setter identity=%#v, want scene %q item 7", data, lunaLifecycleSceneName)
	}
	outerWire := lunaTransformWireDecodeObject(t, data)
	if _, ok := outerWire["sceneItemId"].(json.Number); !ok {
		t.Fatalf("sceneItemId JSON type=%T, want number", outerWire["sceneItemId"])
	}
	transform := mapValue(data["sceneItemTransform"])
	if transform == nil {
		t.Fatalf("setter omitted sceneItemTransform: %#v", data)
	}
	included := []string{"positionX", "positionY", "scaleX", "scaleY", "rotation", "alignment", "boundsType", "boundsAlignment", "cropToBounds", "cropLeft", "cropTop", "cropRight", "cropBottom"}
	lunaTransformWireAssertKeys(t, transform, included, []string{"boundsWidth", "boundsHeight", "sceneItemEnabled", "sceneItemLocked", "sceneItemIndex"})
	wire := lunaTransformWireDecodeObject(t, transform)
	lunaTransformWireAssertNumericTypes(t, wire, map[string]float64{
		"positionX": 40, "positionY": 0, "scaleX": 1, "scaleY": 1, "rotation": 0,
		"alignment": 5, "boundsAlignment": 0, "cropLeft": 0, "cropTop": 0, "cropRight": 0, "cropBottom": 0,
	})
	if wire["boundsType"] != "OBS_BOUNDS_NONE" || wire["cropToBounds"] != false {
		t.Fatalf("NONE transform semantic fields=%#v", wire)
	}

	var got groupModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("updated state diagnostics: %v", diags)
	}
	if got.Placement == nil || got.Placement.Transform == nil || got.Placement.Transform.BoundsWidth.ValueFloat64() != 0 || got.Placement.Transform.BoundsHeight.ValueFloat64() != 0 {
		t.Fatalf("raw NONE bounds were not retained in state: %#v", got.Placement)
	}
	if capture.groupRead != 2 {
		t.Fatalf("GetGroup reads=%d, want initial plus settled read", capture.groupRead)
	}
}

func TestLunaTransformWireGroupUpdatePreservesPositiveNoneDimensionIndividually(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		width, height               float64
		includeWidth, includeHeight bool
	}{
		{name: "positive_width_zero_height", width: 640, height: 0, includeWidth: true, includeHeight: false},
		{name: "zero_width_positive_height", width: 0, height: 360, includeWidth: false, includeHeight: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := lunaTransformWireClonePlacement(lunaLifecyclePlacement())
			want := lunaTransformWireClonePlacement(old)
			want.Transform.X = types.Float64Value(40)
			want.Transform.BoundsWidth = types.Float64Value(tc.width)
			want.Transform.BoundsHeight = types.Float64Value(tc.height)

			capture, response := lunaTransformWireRunGroupUpdate(t, old, want)
			if response.Diagnostics.HasError() {
				t.Fatalf("NONE dimension update diagnostics: %v", response.Diagnostics)
			}
			mutations := lunaTransformWireDirectMutations(capture)
			if len(mutations) != 1 || mutations[0].typ != "SetSceneItemTransform" {
				t.Fatalf("direct mutations=%#v, want one SetSceneItemTransform", mutations)
			}
			transform := mapValue(mutations[0].data["sceneItemTransform"])
			included := []string{"positionX", "positionY", "scaleX", "scaleY", "rotation", "alignment", "boundsType", "boundsAlignment", "cropToBounds", "cropLeft", "cropTop", "cropRight", "cropBottom"}
			omitted := []string{"sceneItemEnabled", "sceneItemLocked", "sceneItemIndex"}
			if tc.includeWidth {
				included = append(included, "boundsWidth")
			} else {
				omitted = append(omitted, "boundsWidth")
			}
			if tc.includeHeight {
				included = append(included, "boundsHeight")
			} else {
				omitted = append(omitted, "boundsHeight")
			}
			lunaTransformWireAssertKeys(t, transform, included, omitted)
			wire := lunaTransformWireDecodeObject(t, transform)
			lunaTransformWireAssertNumericTypes(t, wire, map[string]float64{
				"positionX": 40, "positionY": 0, "scaleX": 1, "scaleY": 1, "rotation": 0,
				"alignment": 5, "boundsAlignment": 0, "cropLeft": 0, "cropTop": 0, "cropRight": 0, "cropBottom": 0,
			})
			if tc.includeWidth {
				lunaTransformWireAssertNumber(t, wire, "boundsWidth", tc.width)
			}
			if tc.includeHeight {
				lunaTransformWireAssertNumber(t, wire, "boundsHeight", tc.height)
			}
			if wire["boundsType"] != "OBS_BOUNDS_NONE" {
				t.Fatalf("NONE dimension bounds type=%#v", wire["boundsType"])
			}

			var got groupModel
			if diags := response.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("updated state diagnostics: %v", diags)
			}
			if got.Placement == nil || got.Placement.Transform == nil || got.Placement.Transform.BoundsWidth.ValueFloat64() != tc.width || got.Placement.Transform.BoundsHeight.ValueFloat64() != tc.height {
				t.Fatalf("NONE raw dimensions=%#v, want width=%v height=%v", got.Placement, tc.width, tc.height)
			}
		})
	}
}

func TestLunaTransformWireGroupUpdateSeparatesEnabledFromTransform(t *testing.T) {
	old := lunaTransformWireClonePlacement(lunaLifecyclePlacement())
	want := lunaTransformWireClonePlacement(old)
	want.Enabled = types.BoolValue(false)
	want.Transform.X = types.Float64Value(40)

	capture, response := lunaTransformWireRunGroupUpdate(t, old, want)
	if response.Diagnostics.HasError() {
		t.Fatalf("enabled transform update diagnostics: %v", response.Diagnostics)
	}
	mutations := lunaTransformWireDirectMutations(capture)
	if len(mutations) != 2 || mutations[0].typ != "SetSceneItemEnabled" || mutations[1].typ != "SetSceneItemTransform" {
		t.Fatalf("direct mutation order=%#v, want enabled then transform", mutations)
	}
	if enabled, ok := mutations[0].data["sceneItemEnabled"].(bool); !ok || enabled {
		t.Fatalf("enabled setter=%#v, want sceneItemEnabled=false", mutations[0].data)
	}
	transform := mapValue(mutations[1].data["sceneItemTransform"])
	if transform == nil {
		t.Fatalf("enabled mode omitted transform setter: %#v", mutations[1].data)
	}
	included := []string{"positionX", "positionY", "scaleX", "scaleY", "rotation", "alignment", "boundsType", "boundsAlignment", "cropToBounds", "cropLeft", "cropTop", "cropRight", "cropBottom"}
	lunaTransformWireAssertKeys(t, transform, included, []string{"boundsWidth", "boundsHeight", "sceneItemEnabled", "sceneItemLocked", "sceneItemIndex"})
	wire := lunaTransformWireDecodeObject(t, transform)
	lunaTransformWireAssertNumericTypes(t, wire, map[string]float64{
		"positionX": 40, "positionY": 0, "scaleX": 1, "scaleY": 1, "rotation": 0,
		"alignment": 5, "boundsAlignment": 0, "cropLeft": 0, "cropTop": 0, "cropRight": 0, "cropBottom": 0,
	})
	if wire["boundsType"] != "OBS_BOUNDS_NONE" {
		t.Fatalf("enabled mode changed bounds type: %#v", wire)
	}
}

func TestLunaTransformWireGroupUpdateIncludesPositiveBoundedDimensions(t *testing.T) {
	old := lunaTransformWireClonePlacement(lunaLifecyclePlacement())
	want := lunaTransformWireClonePlacement(old)
	want.Transform.X = types.Float64Value(40)
	want.Transform.Y = types.Float64Value(12)
	want.Transform.BoundsType = types.StringValue("OBS_BOUNDS_STRETCH")
	want.Transform.BoundsWidth = types.Float64Value(640)
	want.Transform.BoundsHeight = types.Float64Value(360)

	capture, response := lunaTransformWireRunGroupUpdate(t, old, want)
	if response.Diagnostics.HasError() {
		t.Fatalf("bounded transform update diagnostics: %v", response.Diagnostics)
	}
	mutations := lunaTransformWireDirectMutations(capture)
	if len(mutations) != 1 || mutations[0].typ != "SetSceneItemTransform" {
		t.Fatalf("direct mutations=%#v, want one SetSceneItemTransform", mutations)
	}
	transform := mapValue(mutations[0].data["sceneItemTransform"])
	included := []string{"positionX", "positionY", "scaleX", "scaleY", "rotation", "alignment", "boundsType", "boundsAlignment", "boundsWidth", "boundsHeight", "cropToBounds", "cropLeft", "cropTop", "cropRight", "cropBottom"}
	lunaTransformWireAssertKeys(t, transform, included, []string{"sceneItemEnabled", "sceneItemLocked", "sceneItemIndex"})
	wire := lunaTransformWireDecodeObject(t, transform)
	lunaTransformWireAssertNumericTypes(t, wire, map[string]float64{
		"positionX": 40, "positionY": 12, "scaleX": 1, "scaleY": 1, "rotation": 0,
		"alignment": 5, "boundsAlignment": 0, "boundsWidth": 640, "boundsHeight": 360,
		"cropLeft": 0, "cropTop": 0, "cropRight": 0, "cropBottom": 0,
	})
	if wire["boundsType"] != "OBS_BOUNDS_STRETCH" {
		t.Fatalf("bounded transform type=%#v", wire["boundsType"])
	}
}

func TestLunaTransformWireGroupUpdateRejectsZeroBoundedDimensionBeforeSetter(t *testing.T) {
	old := lunaTransformWireClonePlacement(lunaLifecyclePlacement())
	want := lunaTransformWireClonePlacement(old)
	want.Transform.X = types.Float64Value(40)
	want.Transform.BoundsType = types.StringValue("OBS_BOUNDS_STRETCH")
	want.Transform.BoundsWidth = types.Float64Value(0)
	want.Transform.BoundsHeight = types.Float64Value(360)

	capture, response := lunaTransformWireRunGroupUpdate(t, old, want)
	if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "placement bounds dimensions are invalid") {
		t.Fatalf("diagnostics=%v, want bounded zero rejection", response.Diagnostics)
	}
	if mutations := lunaTransformWireDirectMutations(capture); len(mutations) != 0 {
		t.Fatalf("invalid bounded transform sent direct mutations: %#v", mutations)
	}
	if capture.groupRead != 1 {
		t.Fatalf("invalid transform GetGroup reads=%d, want only initial observation", capture.groupRead)
	}
}

func lunaTransformWireRepoRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), ".."))
}

func lunaTransformWireFileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func lunaTransformWireVectorField(fields []any, name string) map[string]any {
	for _, raw := range fields {
		field := mapValue(raw)
		if stringValue(field["name"]) == name {
			return field
		}
	}
	return nil
}

func lunaTransformWireVectorNamed(vectors []any, name string) map[string]any {
	for _, raw := range vectors {
		vector := mapValue(raw)
		if stringValue(vector["name"]) == name {
			return vector
		}
	}
	return nil
}

func lunaTransformWireContainsString(values []any, want string) bool {
	for _, raw := range values {
		if stringValue(raw) == want {
			return true
		}
	}
	return false
}

func lunaTransformWireNumbers(value any) ([]float64, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]float64, len(values))
	for i, raw := range values {
		number, ok := numberValue(raw)
		if !ok {
			return nil, false
		}
		out[i] = number
	}
	return out, true
}

func lunaTransformWireAssertNumberList(t *testing.T, got any, want []float64, label string) {
	t.Helper()
	actual, ok := lunaTransformWireNumbers(got)
	if !ok || len(actual) != len(want) {
		t.Errorf("%s=%#v, want numeric list %#v", label, got, want)
		return
	}
	for i := range want {
		if actual[i] != want[i] {
			t.Errorf("%s[%d]=%v, want %v", label, i, actual[i], want[i])
		}
	}
}

func TestLunaTransformWireVectorsPinProtocolSchema(t *testing.T) {
	root := lunaTransformWireRepoRoot(t)
	vectorPath := filepath.Join(root, "tools", "transform-protocol-vectors", "set-scene-item-transform.vectors.json")
	data, err := os.ReadFile(vectorPath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode vector fixture: %v", err)
	}
	if number, ok := numberValue(fixture["schema_version"]); !ok || number != 1 {
		t.Fatalf("schema_version=%#v, want 1", fixture["schema_version"])
	}
	if stringValue(fixture["request_type"]) != "SetSceneItemTransform" {
		t.Fatalf("request_type=%q", stringValue(fixture["request_type"]))
	}
	source := mapValue(fixture["source"])
	if stringValue(source["tag"]) != "5.7.4" {
		t.Fatalf("source tag=%q", stringValue(source["tag"]))
	}
	headerPath := filepath.Join(root, filepath.FromSlash(stringValue(source["local_pinned_header"])))
	if _, err := os.Stat(headerPath); err != nil {
		t.Skipf("pinned obs-websocket header not fetched (%s); run `make -C native-bridge fetch` to check the pin", headerPath)
	}
	if got, want := lunaTransformWireFileSHA256(t, headerPath), stringValue(source["local_pinned_header_sha256"]); got != want {
		t.Fatalf("pinned header sha256=%s, want fixture=%s", got, want)
	}
	contractPath := filepath.Join(root, filepath.FromSlash(stringValue(source["local_contract"])))
	if got, want := lunaTransformWireFileSHA256(t, contractPath), stringValue(source["local_contract_sha256"]); got != want {
		t.Fatalf("contract sha256=%s, want fixture=%s", got, want)
	}

	fields := sliceValue(fixture["transform_fields"])
	for _, name := range []string{"positionX", "positionY", "rotation", "scaleX", "scaleY", "alignment", "boundsType", "boundsAlignment", "boundsWidth", "boundsHeight", "cropToBounds", "cropLeft", "cropTop", "cropRight", "cropBottom"} {
		if lunaTransformWireVectorField(fields, name) == nil {
			t.Fatalf("transform field %q missing from vector fixture", name)
		}
	}
	for _, name := range []string{"positionX", "positionY", "rotation", "scaleX", "scaleY", "alignment", "boundsAlignment", "boundsWidth", "boundsHeight", "cropLeft", "cropTop", "cropRight", "cropBottom"} {
		field := lunaTransformWireVectorField(fields, name)
		if stringValue(field["json_type"]) != "number" {
			t.Errorf("field %s json_type=%q, want number", name, stringValue(field["json_type"]))
		}
	}
	width := lunaTransformWireVectorField(fields, "boundsWidth")
	if min, _ := numberValue(width["minimum"]); min != 1 {
		t.Errorf("boundsWidth minimum=%#v, want 1", width["minimum"])
	}
	if max, _ := numberValue(width["maximum"]); max != 90001 {
		t.Errorf("boundsWidth maximum=%#v, want 90001", width["maximum"])
	}
	wantRanges := map[string][2]float64{
		"positionX":       {-90001, 90001},
		"positionY":       {-90001, 90001},
		"rotation":        {-360, 360},
		"alignment":       {0, 4294967295},
		"boundsAlignment": {0, 4294967295},
		"boundsWidth":     {1, 90001},
		"boundsHeight":    {1, 90001},
		"cropLeft":        {0, 100000},
		"cropTop":         {0, 100000},
		"cropRight":       {0, 100000},
		"cropBottom":      {0, 100000},
	}
	for name, wantRange := range wantRanges {
		field := lunaTransformWireVectorField(fields, name)
		if min, ok := numberValue(field["minimum"]); !ok || min != wantRange[0] {
			t.Errorf("%s minimum=%#v, want %v", name, field["minimum"], wantRange[0])
		}
		if max, ok := numberValue(field["maximum"]); !ok || max != wantRange[1] {
			t.Errorf("%s maximum=%#v, want %v", name, field["maximum"], wantRange[1])
		}
	}
	outerFields := sliceValue(fixture["outer_fields"])
	itemIDField := lunaTransformWireVectorField(outerFields, "sceneItemId")
	if stringValue(itemIDField["json_type"]) != "number" || !boolValue(itemIDField["required"]) {
		t.Errorf("sceneItemId metadata=%#v, want required JSON number", itemIDField)
	}
	if minimum, ok := numberValue(itemIDField["minimum"]); !ok || minimum != 0 {
		t.Errorf("sceneItemId minimum=%#v, want 0", itemIDField["minimum"])
	}
	transformField := lunaTransformWireVectorField(outerFields, "sceneItemTransform")
	if stringValue(transformField["json_type"]) != "object" || !boolValue(transformField["required"]) {
		t.Errorf("sceneItemTransform metadata=%#v, want required JSON object", transformField)
	}

	vectors := sliceValue(fixture["vectors"])
	for _, name := range []string{"none_raw_zero_omit", "none_positive_width_zero_height", "none_zero_width_positive_height", "none_enabled_false", "bounded_positive", "bounded_minimum", "bounded_zero_invalid", "omitted_preserves_native"} {
		if lunaTransformWireVectorNamed(vectors, name) == nil {
			t.Fatalf("semantic vector %q missing", name)
		}
	}
	none := lunaTransformWireVectorNamed(vectors, "none_raw_zero_omit")
	expected := mapValue(none["expected"])
	omits := sliceValue(expected["transform_omits"])
	if !lunaTransformWireContainsString(omits, "boundsWidth") || !lunaTransformWireContainsString(omits, "boundsHeight") {
		t.Fatalf("NONE vector omissions=%#v, want both raw bounds dimensions", omits)
	}
	nativeDefaults := sliceValue(fixture["native_defaults"])
	var noneDefault map[string]any
	for _, raw := range nativeDefaults {
		candidate := mapValue(raw)
		if stringValue(candidate["name"]) == "none_readback_raw_zero" {
			noneDefault = candidate
		}
	}
	readback := mapValue(noneDefault["native_readback"])
	if stringValue(readback["boundsType"]) != "OBS_BOUNDS_NONE" {
		t.Errorf("NONE native default type=%#v", readback["boundsType"])
	}
	if width, ok := numberValue(readback["boundsWidth"]); !ok || width != 0 {
		t.Errorf("NONE native default width=%#v, want 0", readback["boundsWidth"])
	}
	if height, ok := numberValue(readback["boundsHeight"]); !ok || height != 0 {
		t.Errorf("NONE native default height=%#v, want 0", readback["boundsHeight"])
	}
	bounded := lunaTransformWireVectorNamed(vectors, "bounded_positive")
	boundedExpected := mapValue(bounded["expected"])
	boundedWire := mapValue(boundedExpected["bounds_wire"])
	if width, ok := numberValue(boundedWire["boundsWidth"]); !ok || width != 640 {
		t.Errorf("bounded positive width=%#v, want 640", boundedWire["boundsWidth"])
	}
	if height, ok := numberValue(boundedWire["boundsHeight"]); !ok || height != 360 {
		t.Errorf("bounded positive height=%#v, want 360", boundedWire["boundsHeight"])
	}

	wantBoundaries := map[string]struct {
		accepted []float64
		rejected []float64
	}{
		"sceneItemId":     {[]float64{0}, []float64{-1}},
		"positionX":       {[]float64{-90001, 90001}, []float64{-90001.000001, 90001.000001}},
		"positionY":       {[]float64{-90001, 90001}, []float64{-90001.000001, 90001.000001}},
		"rotation":        {[]float64{-360, 360}, []float64{-360.000001, 360.000001}},
		"alignment":       {[]float64{0, 4294967295}, []float64{-1, 4294967296}},
		"boundsAlignment": {[]float64{0, 4294967295}, []float64{-1, 4294967296}},
		"boundsWidth":     {[]float64{1, 90001}, []float64{0, 90002}},
		"boundsHeight":    {[]float64{1, 90001}, []float64{0, 90002}},
		"cropLeft":        {[]float64{0, 100000}, []float64{-1, 100001}},
		"cropTop":         {[]float64{0, 100000}, []float64{-1, 100001}},
		"cropRight":       {[]float64{0, 100000}, []float64{-1, 100001}},
		"cropBottom":      {[]float64{0, 100000}, []float64{-1, 100001}},
	}
	boundaryVectors := sliceValue(fixture["numeric_boundary_vectors"])
	seenBoundaries := map[string]bool{}
	for _, raw := range boundaryVectors {
		boundary := mapValue(raw)
		name := stringValue(boundary["field"])
		want, ok := wantBoundaries[name]
		if !ok {
			continue
		}
		seenBoundaries[name] = true
		lunaTransformWireAssertNumberList(t, boundary["accepted"], want.accepted, name+" accepted")
		lunaTransformWireAssertNumberList(t, boundary["rejected"], want.rejected, name+" rejected")
	}
	for name := range wantBoundaries {
		if !seenBoundaries[name] {
			t.Errorf("numeric boundary vector %q missing", name)
		}
	}
}
