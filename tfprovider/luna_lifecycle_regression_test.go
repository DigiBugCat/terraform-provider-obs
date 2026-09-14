package tfprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const (
	lunaLifecycleCollection = "Reference Terraform Lab"
	lunaLifecycleSceneName  = "Office"
	lunaLifecycleSceneUUID  = "scene-office"
	lunaLifecycleGroupName  = "Party Lights"
	lunaLifecycleGroupUUID  = "group-party"
	lunaLifecycleInputName  = "Camera Left"
	lunaLifecycleInputUUID  = "input-camera-left"
)

type lunaLifecycleCall struct {
	typ  string
	data map[string]any
}

type lunaLifecycleDirectReply struct {
	response map[string]any
	err      error
}

type lunaLifecycleBridgeReply struct {
	outcome               string
	code                  string
	message               string
	mutationAttempted     bool
	postconditionVerified bool
	result                map[string]any
	err                   error
}

// lunaLifecycleScript is an in-process WebSocket-shaped requester. It keeps
// protocol replies explicit while allowing each callback test to verify that
// no later mutation was sent after an uncertain or interrupted response.
type lunaLifecycleScript struct {
	capabilityRequests []string
	direct             map[string][]lunaLifecycleDirectReply
	bridge             map[string][]lunaLifecycleBridgeReply
	calls              []lunaLifecycleCall
}

func (s *lunaLifecycleScript) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	s.calls = append(s.calls, lunaLifecycleCall{typ: typ, data: data})
	if typ == "CallVendorRequest" {
		requestType := stringValue(data["requestType"])
		requestData := mapValue(data["requestData"])
		if requestType == "GetCapabilities" {
			return lunaLifecycleBridgeEnvelope("GetCapabilities", "", lunaLifecycleBridgeReply{
				outcome:               "completed",
				mutationAttempted:     false,
				postconditionVerified: true,
				result:                lunaLifecycleCapabilities(s.capabilityRequests),
			}), nil
		}
		replies := s.bridge[requestType]
		if len(replies) == 0 {
			return nil, fmt.Errorf("unexpected CallVendorRequest/%s", requestType)
		}
		reply := replies[0]
		s.bridge[requestType] = replies[1:]
		if reply.err != nil {
			return nil, reply.err
		}
		return lunaLifecycleBridgeEnvelope(requestType, stringValue(requestData["clientOperationId"]), reply), nil
	}
	replies := s.direct[typ]
	if len(replies) == 0 {
		// Create clamps its requested index to the current sibling count; an unscripted
		// count read means "empty container" rather than an unexpected request.
		if typ == "GetSceneItemList" || typ == "GetGroupSceneItemList" {
			return map[string]any{"sceneItems": []any{}}, nil
		}
		return nil, fmt.Errorf("unexpected %s", typ)
	}
	reply := replies[0]
	s.direct[typ] = replies[1:]
	return reply.response, reply.err
}

func lunaLifecycleBridgeEnvelope(requestType, operationID string, reply lunaLifecycleBridgeReply) map[string]any {
	code := reply.code
	if code == "" {
		code = "ok"
		if reply.outcome != "completed" {
			code = reply.outcome + "_code"
		}
	}
	message := reply.message
	if message == "" {
		message = "scripted result"
	}
	return map[string]any{"responseData": map[string]any{
		"contractVersion":       float64(bridgeContractVersion),
		"bridgeVersion":         bridgeSupportedVersion,
		"requestType":           requestType,
		"clientOperationId":     operationID,
		"outcome":               reply.outcome,
		"code":                  code,
		"message":               message,
		"mutationAttempted":     reply.mutationAttempted,
		"postconditionVerified": reply.postconditionVerified,
		"result":                reply.result,
	}}
}

func lunaLifecycleCapabilities(requests []string) map[string]any {
	advertised := make([]any, 0, len(requests))
	for _, requestType := range requests {
		requiresAbsolute := bridgeRequestRequiresAbsoluteRuntime(requestType)
		advertised = append(advertised, map[string]any{"requestType": requestType, "mutation": requiresAbsolute, "requiresAbsoluteRuntime": requiresAbsolute})
	}
	return map[string]any{
		"vendorName":                 "obstf",
		"contractVersion":            1.0,
		"bridgeVersion":              bridgeSupportedVersion,
		"currentSceneCollectionName": lunaLifecycleCollection,
		"obsVersion":                 "32.2.2",
		"obsWebSocketApiVersion":     3.0,
		"platform":                   "macos",
		"architecture":               "arm64",
		"outputStateObserved":        true,
		"baseWidth":                  1920.0,
		"baseHeight":                 1080.0,
		"coordinateModeKnown":        true,
		"absoluteCoordinates":        true,
		"mainCanvasKnown":            true,
		"streamingActive":            false,
		"recordingActive":            false,
		"replayBufferActive":         false,
		"virtualCameraActive":        false,
		"absoluteRuntimeGuard":       map[string]any{"status": "supported", "supported": true, "obsVersion": "32.2.2", "coordinateModeKnown": true, "absoluteCoordinates": true, "mainCanvasKnown": true, "baseWidth": 1920.0, "baseHeight": 1080.0, "comparisonMode": "absolute-raw-exact"},
		"supports": map[string]any{
			"absoluteGroupGraphMutations": true, "relativeGroupGraphMutations": false, "placementCanonicalization": false,
			"realGroupCreate": true, "strictJsonRequestTypes": true, "directInputCreateInGroup": true,
			"declaredInputSettingsPostcondition": true, "directExistingSourcePlacementInGroup": true, "moveParentItemToGroup": true,
			"exactGroupChildObservation": true, "groupChildUpdate": false, "groupChildUpdateViaCoreWebSocket": true,
			"groupChildPlacementRemoval": true, "moveChildToParent": true, "ungroup": true,
			"ungroupReturnsReassignedItemIds": true, "sourceDelete": false, "frontendSaveCheckpoint": true, "diskDurabilityReceipt": false,
		},
		"execution": map[string]any{"vendorCallbackThread": "obs-websocket request thread", "graphOperationThread": "OBS_TASK_UI", "requestSynchronous": true, "multiRequestAtomic": false, "compareAndSwap": false},
		"requests":  advertised,
	}
}

func lunaLifecycleDirect(response map[string]any) lunaLifecycleDirectReply {
	return lunaLifecycleDirectReply{response: response}
}

func lunaLifecycleVersion(requests ...string) map[string]any {
	available := make([]any, 0, len(requests))
	for _, requestType := range requests {
		available = append(available, requestType)
	}
	return map[string]any{"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "rpcVersion": 1.0, "availableRequests": available}
}

func lunaLifecyclePlacement() bridgePlacementModel {
	return bridgePlacementModel{
		Enabled: types.BoolValue(true), Locked: types.BoolValue(false), Index: types.Int64Value(0),
		Transform: &bridgeTransformModel{
			X: types.Float64Value(960), Y: types.Float64Value(0), ScaleX: types.Float64Value(1), ScaleY: types.Float64Value(1), Rotation: types.Float64Value(0),
			Alignment: types.Int64Value(5), BoundsType: types.StringValue("OBS_BOUNDS_NONE"), BoundsAlignment: types.Int64Value(0),
			BoundsWidth: types.Float64Value(0), BoundsHeight: types.Float64Value(0), CropToBounds: types.BoolValue(false),
			CropLeft: types.Int64Value(0), CropTop: types.Int64Value(0), CropRight: types.Int64Value(0), CropBottom: types.Int64Value(0),
		},
	}
}

func lunaLifecycleGroupResult(itemID int64, groupUUID, groupName string, placement bridgePlacementModel, children []any) map[string]any {
	native, _ := placement.request()
	return map[string]any{
		"scene":    map[string]any{"sourceName": lunaLifecycleSceneName, "sourceUuid": lunaLifecycleSceneUUID},
		"group":    map[string]any{"sourceName": groupName, "sourceUuid": groupUUID, "sceneItemId": float64(itemID), "groupWidth": 1920.0, "groupHeight": 1080.0, "placement": native},
		"children": children,
	}
}

func lunaLifecycleChildResult(itemID int64, inputUUID, inputName string, placement bridgePlacementModel) map[string]any {
	native, _ := placement.request()
	return map[string]any{"sourceName": inputName, "sourceUuid": inputUUID, "inputKind": "image_source", "sceneItemId": float64(itemID), "sourceWidth": 1920.0, "sourceHeight": 1080.0, "placement": native}
}

func lunaLifecycleGroupModel(itemID int64, sceneName string) groupModel {
	scene := types.StringNull()
	if sceneName != "" {
		scene = types.StringValue(sceneName)
	}
	placement := lunaLifecyclePlacement()
	return groupModel{
		ID: types.StringValue(groupID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleGroupUUID, itemID)), Collection: types.StringValue(lunaLifecycleCollection),
		SceneName: scene, SceneUUID: types.StringValue(lunaLifecycleSceneUUID), Name: types.StringValue(lunaLifecycleGroupName),
		GroupUUID: types.StringValue(lunaLifecycleGroupUUID), ItemID: types.Int64Value(itemID), Placement: &placement,
		GroupWidth: types.Int64Value(1920), GroupHeight: types.Int64Value(1080),
	}
}

func lunaLifecycleGroupInputModel(file string, groupItemID, itemID int64, groupName string) groupInputModel {
	placement := lunaLifecyclePlacement()
	group := types.StringNull()
	if groupName != "" {
		group = types.StringValue(groupName)
	}
	return groupInputModel{
		ID:         types.StringValue(groupInputID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleGroupUUID, lunaLifecycleInputUUID, itemID)),
		Collection: types.StringValue(lunaLifecycleCollection), SceneName: types.StringValue(lunaLifecycleSceneName), SceneUUID: types.StringValue(lunaLifecycleSceneUUID),
		GroupName: group, GroupUUID: types.StringValue(lunaLifecycleGroupUUID), GroupItemID: types.Int64Value(groupItemID),
		Name: types.StringValue(lunaLifecycleInputName), Kind: types.StringValue("image_source"), File: types.StringValue(file), Unload: types.BoolValue(false),
		InputUUID: types.StringValue(lunaLifecycleInputUUID), ItemID: types.Int64Value(itemID), Placement: &placement,
		SourceWidth: types.Int64Value(1920), SourceHeight: types.Int64Value(1080),
	}
}

func lunaLifecycleSchemaAndPlan(t *testing.T, ctx context.Context, sc resourceschema.Schema, value any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil), Schema: sc}
	if diags := plan.Set(ctx, value); diags.HasError() {
		t.Fatalf("plan setup diagnostics: %v", diags)
	}
	return plan
}

func lunaLifecycleSchemaAndState(t *testing.T, ctx context.Context, sc resourceschema.Schema, value any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil), Schema: sc}
	if diags := state.Set(ctx, value); diags.HasError() {
		t.Fatalf("state setup diagnostics: %v", diags)
	}
	return state
}

func lunaLifecycleGroupSchema(ctx context.Context) resourceschema.Schema {
	var response resource.SchemaResponse
	(&groupResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	return response.Schema
}

func lunaLifecycleGroupInputSchema(ctx context.Context) resourceschema.Schema {
	var response resource.SchemaResponse
	(&groupInputResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	return response.Schema
}

func lunaLifecycleAssertDiagnostic(t *testing.T, diags diag.Diagnostics, text string) {
	t.Helper()
	if !diags.HasError() {
		t.Fatalf("expected diagnostic containing %q", text)
	}
	for _, diagnostic := range diags {
		if strings.Contains(diagnostic.Summary(), text) || strings.Contains(diagnostic.Detail(), text) {
			return
		}
	}
	t.Fatalf("diagnostics=%v want substring %q", diags, text)
}

func lunaLifecycleBridgeRequestType(call lunaLifecycleCall) string {
	if call.typ != "CallVendorRequest" {
		return ""
	}
	return stringValue(call.data["requestType"])
}

func lunaLifecycleMutationCount(script *lunaLifecycleScript, typ string) int {
	count := 0
	for _, call := range script.calls {
		if call.typ == typ || lunaLifecycleBridgeRequestType(call) == typ {
			count++
		}
	}
	return count
}

func lunaLifecycleReadGroupItemIDs(script *lunaLifecycleScript) []int64 {
	var ids []int64
	for _, call := range script.calls {
		if lunaLifecycleBridgeRequestType(call) != "GetGroup" {
			continue
		}
		requestData := mapValue(call.data["requestData"])
		group := mapValue(requestData["group"])
		ids = append(ids, int64Value(group["sceneItemId"]))
	}
	return ids
}

func lunaLifecycleImageSettings(file string) map[string]any {
	return map[string]any{"inputKind": "image_source", "inputSettings": map[string]any{"file": file, "unload": false}}
}

func lunaLifecycleInputList() map[string]any {
	return map[string]any{"inputs": []any{map[string]any{"inputName": lunaLifecycleInputName, "inputUuid": lunaLifecycleInputUUID, "inputKind": "image_source"}}}
}

func lunaLifecycleDefaultImageSettings() map[string]any {
	return map[string]any{"defaultInputSettings": map[string]any{"file": "", "unload": false}}
}

func lunaLifecycleAttachedFilterList() map[string]any {
	return map[string]any{"filters": []any{map[string]any{
		"filterName": "Unmanaged Filter", "filterKind": "color_filter_v2", "filterSettings": map[string]any{}, "filterEnabled": true, "filterIndex": 0.0,
	}}}
}

func lunaLifecycleSceneList() map[string]any {
	return map[string]any{"scenes": []any{map[string]any{"sceneName": lunaLifecycleSceneName, "sceneUuid": lunaLifecycleSceneUUID}}}
}

func lunaLifecycleRequireState(t *testing.T, ctx context.Context, state tfsdk.State, target any) {
	t.Helper()
	if diags := state.Get(ctx, target); diags.HasError() {
		t.Fatalf("state read diagnostics: %v", diags)
	}
}

func TestLunaLifecycleGroupCreateRetainsIdentityAfterAuthoritativeInterruption(t *testing.T) {
	ctx := context.Background()
	placement := lunaLifecyclePlacement()
	observed := lunaLifecycleGroupResult(41, "group-created", lunaLifecycleGroupName, placement, []any{})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: observed}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateGroup")

	model := lunaLifecycleGroupModel(0, lunaLifecycleSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ItemID.ValueInt64() != 41 || got.ID.ValueString() != groupID(lunaLifecycleCollection, lunaLifecycleSceneUUID, "group-created", 41) {
		t.Fatalf("state=%#v; authoritative assigned identity was not retained", got)
	}
	if lunaLifecycleMutationCount(script, "CreateGroup") != 1 {
		t.Fatalf("CreateGroup calls=%d", lunaLifecycleMutationCount(script, "CreateGroup"))
	}
	if lunaLifecycleMutationCount(script, "GetGroup") != 0 {
		t.Fatalf("unexpected post-interruption GetGroup calls=%d", lunaLifecycleMutationCount(script, "GetGroup"))
	}
}

func TestLunaLifecycleGroupUpdateRepairsParentAnchorWithNewItemIdentity(t *testing.T) {
	ctx := context.Background()
	old := lunaLifecycleGroupModel(7, "")
	planModel := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetGroupList":           {lunaLifecycleDirect(map[string]any{"groups": []any{lunaLifecycleGroupName}})},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest", "CreateSceneItem")), lunaLifecycleDirect(lunaLifecycleVersion("CreateSceneItem"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"CreateSceneItem":        {lunaLifecycleDirect(map[string]any{"sceneItemId": 99.0})},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "CreateSceneItem")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), planModel)
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), planModel)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), old)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	wantID := groupID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleGroupUUID, 99)
	if got.GroupUUID.ValueString() != lunaLifecycleGroupUUID || got.ItemID.ValueInt64() != 99 || got.ID.ValueString() != wantID || got.SceneName.ValueString() != lunaLifecycleSceneName {
		t.Fatalf("state=%#v; repaired parent anchor state is incomplete", got)
	}
	if lunaLifecycleMutationCount(script, "CreateSceneItem") != 1 {
		t.Fatalf("CreateSceneItem calls=%d", lunaLifecycleMutationCount(script, "CreateSceneItem"))
	}
	for _, typ := range []string{"SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex"} {
		if count := lunaLifecycleMutationCount(script, typ); count != 0 {
			t.Fatalf("%s sent after authoritative interruption (%d calls)", typ, count)
		}
	}
}

func TestLunaLifecycleGroupInputCreateRetainsChildIdentityAfterAuthoritativeInterruption(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := lunaLifecyclePlacement()
	child := lunaLifecycleChildResult(12, "input-created", lunaLifecycleInputName, placement)
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetInputList":           {lunaLifecycleDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: child}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateInputInGroup")
	model := lunaLifecycleGroupInputModel(asset, 7, 0, lunaLifecycleGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupInputModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	wantID := groupInputID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleGroupUUID, "input-created", 12)
	if got.InputUUID.ValueString() != "input-created" || got.ItemID.ValueInt64() != 12 || got.ID.ValueString() != wantID || got.GroupUUID.ValueString() != lunaLifecycleGroupUUID {
		t.Fatalf("state=%#v; authoritative child identity was not retained", got)
	}
}

func TestLunaLifecycleGroupInputUpdateKeepsSourceIdentityWhenParentItemChanges(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := lunaLifecyclePlacement()
	initialGroup := lunaLifecycleGroupResult(99, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{})
	finalChild := lunaLifecycleChildResult(44, lunaLifecycleInputUUID, lunaLifecycleInputName, placement)
	finalGroup := lunaLifecycleGroupResult(99, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{finalChild})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"AddSourceToGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetInputList":            {lunaLifecycleDirect(lunaLifecycleInputList()), lunaLifecycleDirect(lunaLifecycleInputList())},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(asset)), lunaLifecycleDirect(lunaLifecycleImageSettings(asset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
			"GetVersion":              {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":        {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup":         {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: initialGroup}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: finalGroup}},
			"AddSourceToGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: finalChild}},
		},
	}
	stateModel := lunaLifecycleGroupInputModel(asset, 7, 12, lunaLifecycleGroupName)
	planModel := lunaLifecycleGroupInputModel(asset, 99, 12, lunaLifecycleGroupName)
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupInputSchema(ctx), planModel)
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), planModel)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), stateModel)}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupInputModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	wantID := groupInputID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleGroupUUID, lunaLifecycleInputUUID, 44)
	if got.GroupItemID.ValueInt64() != 99 || got.InputUUID.ValueString() != lunaLifecycleInputUUID || got.ItemID.ValueInt64() != 44 || got.ID.ValueString() != wantID {
		t.Fatalf("state=%#v; parent binding changed the child/source identity", got)
	}
	if lunaLifecycleMutationCount(script, "CreateInputInGroup") != 0 || lunaLifecycleMutationCount(script, "AddSourceToGroup") != 1 {
		t.Fatalf("source mutation calls: CreateInputInGroup=%d AddSourceToGroup=%d", lunaLifecycleMutationCount(script, "CreateInputInGroup"), lunaLifecycleMutationCount(script, "AddSourceToGroup"))
	}
	for _, itemID := range lunaLifecycleReadGroupItemIDs(script) {
		if itemID != 99 {
			t.Fatalf("GetGroup request used stale parent item ID %d", itemID)
		}
	}
}

func TestLunaLifecycleGroupInputReadDefersStaleParentWithoutRemovingChildState(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	stateModel := lunaLifecycleGroupInputModel(asset, 7, 12, lunaLifecycleGroupName)
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetVersion":              {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetInputList":            {lunaLifecycleDirect(lunaLifecycleInputList())},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(asset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
			"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetSceneItemList":        {lunaLifecycleDirect(map[string]any{"sceneItems": []any{}})},
			"GetGroupList":            {lunaLifecycleDirect(map[string]any{"groups": []any{lunaLifecycleGroupName}})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "failed", code: "ambiguous_group_item_id", message: "parent anchor is absent", mutationAttempted: false, postconditionVerified: true}},
		},
	}
	response := resource.ReadResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), stateModel)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), stateModel)}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected stale-parent diagnostics: %v", response.Diagnostics)
	}
	if response.State.Raw.IsNull() {
		t.Fatal("stale parent read removed the surviving child resource")
	}
	var got groupInputModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupName.ValueString() != lunaLifecycleGroupName || got.InputUUID.ValueString() != lunaLifecycleInputUUID || got.ItemID.ValueInt64() != 12 {
		t.Fatalf("state=%#v; stale-parent deferral did not preserve child/source state", got)
	}
	if lunaLifecycleMutationCount(script, "AddSourceToGroup") != 0 {
		t.Fatal("stale-parent Read sent a mutation")
	}
}

func TestLunaLifecycleGroupReadUnknownOrMalformedNeverRemovesState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		reply lunaLifecycleBridgeReply
	}{
		{name: "uncertain envelope", reply: lunaLifecycleBridgeReply{outcome: "uncertain", code: "transport_unknown", message: "response cannot establish postcondition", mutationAttempted: true, postconditionVerified: false, result: map[string]any{}}},
		{name: "completed malformed result", reply: lunaLifecycleBridgeReply{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: map[string]any{"scene": map[string]any{"sourceName": lunaLifecycleSceneName, "sourceUuid": lunaLifecycleSceneUUID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateModel := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
			script := &lunaLifecycleScript{
				capabilityRequests: []string{"GetGroup"},
				direct: map[string][]lunaLifecycleDirectReply{
					"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
					"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
				},
				bridge: map[string][]lunaLifecycleBridgeReply{"GetGroup": {tc.reply}},
			}
			response := resource.ReadResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), stateModel)}
			resourceUnderTest := &groupResource{client: &client{testRequester: script}}
			resourceUnderTest.Read(ctx, resource.ReadRequest{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), stateModel)}, &response)

			if !response.Diagnostics.HasError() {
				t.Fatal("unknown/malformed observation was reported as a successful read")
			}
			if response.State.Raw.IsNull() {
				t.Fatal("unknown/malformed observation removed state")
			}
			if calls := lunaLifecycleMutationCount(script, "GetGroup"); calls != 1 {
				t.Fatalf("GetGroup calls=%d want exactly one", calls)
			}
			var got groupModel
			lunaLifecycleRequireState(t, ctx, response.State, &got)
			if got.ID.ValueString() != stateModel.ID.ValueString() || got.GroupUUID.ValueString() != stateModel.GroupUUID.ValueString() || got.ItemID.ValueInt64() != stateModel.ItemID.ValueInt64() {
				t.Fatalf("state=%#v; unknown/malformed read rewrote identity", got)
			}
		})
	}
}

func TestLunaLifecycleGroupCreateDoesNotTreatInnerUncertainAsSuccess(t *testing.T) {
	ctx := context.Background()
	placement := lunaLifecyclePlacement()
	observed := lunaLifecycleGroupResult(42, "group-uncertain", lunaLifecycleGroupName, placement, []any{})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup": {{outcome: "uncertain", code: "native_response_unknown", message: "mutation outcome is uncertain", mutationAttempted: true, postconditionVerified: false, result: observed}},
		},
	}
	model := lunaLifecycleGroupModel(0, lunaLifecycleSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if !response.Diagnostics.HasError() {
		t.Fatal("inner uncertain bridge outcome was treated as successful Create")
	}
	found := false
	for _, diagnostic := range response.Diagnostics {
		if strings.Contains(diagnostic.Detail(), "uncertain") || strings.Contains(diagnostic.Summary(), "uncertain") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics=%v do not identify the uncertain inner outcome", response.Diagnostics)
	}
	if lunaLifecycleMutationCount(script, "GetGroup") != 0 {
		t.Fatal("uncertain outcome advanced into a successful postcondition wait")
	}
}

func TestLunaLifecycleGroupInputModifyPlanTreatsParentItemAsMutableBinding(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	stateModel := lunaLifecycleGroupInputModel(asset, 7, 12, "")
	planModel := lunaLifecycleGroupInputModel(asset, 99, 12, lunaLifecycleGroupName)
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupInputSchema(ctx), planModel)
	response := resource.ModifyPlanResponse{Plan: plan}
	resourceUnderTest := &groupInputResource{}
	resourceUnderTest.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), stateModel),
		Plan:  plan,
	}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected ModifyPlan diagnostics: %v", response.Diagnostics)
	}
	if len(response.RequiresReplace) != 0 {
		t.Fatalf("parent item binding unexpectedly requires replacement: %v", response.RequiresReplace)
	}
	var got groupInputModel
	lunaLifecycleRequireState(t, ctx, tfsdk.State{Raw: response.Plan.Raw, Schema: response.Plan.Schema}, &got)
	if !got.ID.IsUnknown() || !got.ItemID.IsUnknown() || got.GroupItemID.ValueInt64() != 99 {
		t.Fatalf("plan=%#v; parent binding did not preserve in-place child repair semantics", got)
	}
}

func TestLunaLifecycleGroupDeleteRefusesAttachedFilterBeforeMutation(t *testing.T) {
	ctx := context.Background()
	placement := lunaLifecyclePlacement()
	group := lunaLifecycleGroupResult(7, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{})
	script := &lunaLifecycleScript{
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSourceFilterList":    {lunaLifecycleDirect(lunaLifecycleAttachedFilterList())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	stateModel := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
	response := resource.DeleteResponse{}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), stateModel)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "attached source filters")
	if lunaLifecycleMutationCount(script, "RemoveSceneItem") != 0 {
		t.Fatal("group delete sent RemoveSceneItem despite attached filter")
	}
}

func TestLunaLifecycleGroupInputDeleteRefusesAttachedFilterBeforeMutation(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := lunaLifecyclePlacement()
	child := lunaLifecycleChildResult(12, lunaLifecycleInputUUID, lunaLifecycleInputName, placement)
	group := lunaLifecycleGroupResult(7, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{child})
	script := &lunaLifecycleScript{
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSourceFilterList":    {lunaLifecycleDirect(lunaLifecycleAttachedFilterList())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	stateModel := lunaLifecycleGroupInputModel(asset, 7, 12, lunaLifecycleGroupName)
	response := resource.DeleteResponse{}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), stateModel)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "attached source filters")
	if lunaLifecycleMutationCount(script, "RemoveGroupChildPlacement") != 0 {
		t.Fatal("group input delete sent RemoveGroupChildPlacement despite attached filter")
	}
}
