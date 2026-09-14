package tfprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type queuedRequester struct {
	responses map[string][]map[string]any
	calls     []string
}

func (q *queuedRequester) Req(_ context.Context, typ string, _ map[string]any) (map[string]any, error) {
	q.calls = append(q.calls, typ)
	queue := q.responses[typ]
	if len(queue) == 0 {
		return nil, fmt.Errorf("unexpected %s", typ)
	}
	out := queue[0]
	q.responses[typ] = queue[1:]
	return out, nil
}

func bridgeOuter(outcome string, attempted, verified bool) map[string]any {
	code := "ok"
	if outcome != "completed" {
		code = outcome + "_code"
	}
	return map[string]any{"responseData": map[string]any{
		"contractVersion": 1.0, "bridgeVersion": bridgeSupportedVersion, "requestType": "CreateGroup",
		"clientOperationId": "apply-1/group", "outcome": outcome, "code": code, "message": "result",
		"mutationAttempted": attempted, "postconditionVerified": verified, "result": map[string]any{"group": map[string]any{}},
	}}
}

func TestParseBridgeEnvelopeOutcomesAndOperationIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		outer     map[string]any
		operation string
		wantErr   string
	}{
		{"completed", bridgeOuter("completed", true, true), "apply-1/group", ""},
		{"uncertain", bridgeOuter("uncertain", true, false), "apply-1/group", ""},
		{"unknown outcome", bridgeOuter("maybe", true, false), "apply-1/group", "invalid"},
		{"completed without postcondition", bridgeOuter("completed", true, false), "apply-1/group", "without verified"},
		{"operation mismatch", bridgeOuter("completed", true, true), "different", "echoed"},
		{"bridge version mismatch", func() map[string]any {
			x := bridgeOuter("completed", true, true)
			x["responseData"].(map[string]any)["bridgeVersion"] = "0.1.1"
			return x
		}(), "apply-1/group", "incomplete or mismatched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := parseBridgeEnvelope(tc.outer, "CreateGroup", tc.operation)
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err=%v", err)
			}
			if e.Outcome == "" {
				t.Fatal("outcome was discarded")
			}
		})
	}
}

func TestBridgeCapabilitiesRequiresExactCollectionVersionsOutputsAndRequest(t *testing.T) {
	capabilities := testCapabilitiesEnvelope()
	f := &fakeRequester{responses: map[string]map[string]any{"GetVersion": testOrdinaryVersion(), "CallVendorRequest": capabilities}}
	if _, err := bridgeCapabilities(context.Background(), f, "Lab", "CreateGroup", true); err != nil {
		t.Fatal(err)
	}
	if _, err := bridgeCapabilities(context.Background(), f, "Wrong", "CreateGroup", true); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("wrong collection err=%v", err)
	}
	if _, err := bridgeCapabilities(context.Background(), f, "Lab", "Ungroup", true); err == nil || !strings.Contains(err.Error(), "does not advertise") {
		t.Fatalf("missing request err=%v", err)
	}
}

func testCapabilitiesEnvelope() map[string]any {
	return map[string]any{"responseData": map[string]any{
		"contractVersion": 1.0, "bridgeVersion": bridgeSupportedVersion, "requestType": "GetCapabilities", "clientOperationId": "",
		"outcome": "completed", "code": "ok", "message": "capabilities", "mutationAttempted": false, "postconditionVerified": true,
		"result": testCapabilitiesResult("CreateGroup"),
	}}
}

func testCapabilitiesResult(requestType string) map[string]any {
	mutation := bridgeRequestRequiresAbsoluteRuntime(requestType)
	return map[string]any{
		"vendorName": "obstf", "contractVersion": 1.0, "bridgeVersion": bridgeSupportedVersion,
		"currentSceneCollectionName": "Lab", "obsVersion": "32.2.2", "obsWebSocketApiVersion": 3.0,
		"platform": "macos", "architecture": "arm64", "outputStateObserved": true,
		"coordinateModeKnown": true, "absoluteCoordinates": true, "mainCanvasKnown": true, "baseWidth": 1920.0, "baseHeight": 1080.0,
		"streamingActive": false, "recordingActive": false, "replayBufferActive": false, "virtualCameraActive": false,
		"absoluteRuntimeGuard": map[string]any{"status": "supported", "supported": true, "obsVersion": "32.2.2", "coordinateModeKnown": true, "absoluteCoordinates": true, "mainCanvasKnown": true, "baseWidth": 1920.0, "baseHeight": 1080.0, "comparisonMode": "absolute-raw-exact"},
		"supports": map[string]any{
			"absoluteGroupGraphMutations": true, "relativeGroupGraphMutations": false, "placementCanonicalization": false,
			"realGroupCreate": true, "strictJsonRequestTypes": true, "directInputCreateInGroup": true,
			"declaredInputSettingsPostcondition": true, "directExistingSourcePlacementInGroup": true, "moveParentItemToGroup": true,
			"exactGroupChildObservation": true, "groupChildUpdate": false, "groupChildUpdateViaCoreWebSocket": true,
			"groupChildPlacementRemoval": true, "moveChildToParent": true, "ungroup": true,
			"ungroupReturnsReassignedItemIds": true, "sourceDelete": false, "frontendSaveCheckpoint": true, "diskDurabilityReceipt": false,
		},
		"execution": map[string]any{"vendorCallbackThread": "obs-websocket request thread", "graphOperationThread": "OBS_TASK_UI", "requestSynchronous": true, "multiRequestAtomic": false, "compareAndSwap": false},
		"requests":  []any{map[string]any{"requestType": requestType, "mutation": mutation, "requiresAbsoluteRuntime": mutation}},
	}
}

func testOrdinaryVersion() map[string]any {
	return map[string]any{"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "rpcVersion": 1.0, "availableRequests": []any{"CallVendorRequest"}}
}

func relativeCapabilitiesEnvelope(requestType string) map[string]any {
	outer := testCapabilitiesEnvelope()
	result := outer["responseData"].(map[string]any)["result"].(map[string]any)
	result["requests"] = []any{map[string]any{"requestType": requestType, "mutation": false, "requiresAbsoluteRuntime": false}}
	result["absoluteCoordinates"] = false
	guard := result["absoluteRuntimeGuard"].(map[string]any)
	guard["status"], guard["supported"], guard["absoluteCoordinates"], guard["comparisonMode"] = "relative_mode_unsupported", false, false, "ineligible"
	result["supports"].(map[string]any)["absoluteGroupGraphMutations"] = false
	return outer
}

func TestBridgeCapabilitiesAllowsReadButRefusesMutationInRelativeMode(t *testing.T) {
	read := &fakeRequester{responses: map[string]map[string]any{"GetVersion": testOrdinaryVersion(), "CallVendorRequest": relativeCapabilitiesEnvelope("GetGroup")}}
	if _, err := bridgeCapabilities(context.Background(), read, "Lab", "GetGroup", false); err != nil {
		t.Fatalf("read-only group observation was blocked: %v", err)
	}
	mutate := &fakeRequester{responses: map[string]map[string]any{"GetVersion": testOrdinaryVersion(), "CallVendorRequest": relativeCapabilitiesEnvelope("GetGroup")}}
	if _, err := bridgeCapabilities(context.Background(), mutate, "Lab", "GetGroup", true); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("relative mutation guard err=%v", err)
	}
}

func TestUpdateBridgeItemRefusesRelativeBeforeOrdinarySetter(t *testing.T) {
	q := &queuedRequester{responses: map[string][]map[string]any{
		"GetVersion":        {testOrdinaryVersion()},
		"CallVendorRequest": {relativeCapabilitiesEnvelope("GetGroup")},
	}}
	observed, want := testBridgePlacement(), testBridgePlacement()
	wantTransform := *want.Transform
	wantTransform.X = types.Float64Value(321)
	want.Transform = &wantTransform
	if err := updateBridgeItem(context.Background(), q, "Lab", "Party Lights", 7, observed, &want); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("relative setter guard err=%v", err)
	}
	for _, typ := range q.calls {
		if typ == "SetSceneItemTransform" {
			t.Fatalf("setter was sent after failed absolute guard: %v", q.calls)
		}
	}
}

func TestGroupAnchorRepairRefusesRelativeBeforeCreateSceneItem(t *testing.T) {
	ctx := context.Background()
	q := &queuedRequester{responses: map[string][]map[string]any{
		"GetSceneCollectionList": {{"currentSceneCollectionName": lunaLifecycleCollection}},
		"GetSceneList":           {lunaLifecycleSceneList()},
		"GetGroupList":           {{"groups": []any{lunaLifecycleGroupName}}},
		"GetVersion":             {testOrdinaryVersion()},
		"CallVendorRequest":      {relativeCapabilitiesEnvelope("GetGroup")},
	}}
	stateModel := lunaLifecycleGroupModel(7, "")
	planModel := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), planModel)
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), planModel)}
	resourceUnderTest := &groupResource{client: &client{testRequester: q}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), stateModel)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("relative-mode anchor repair was accepted")
	}
	for _, typ := range q.calls {
		if typ == "CreateSceneItem" {
			t.Fatalf("CreateSceneItem was sent before absolute guard: %v", q.calls)
		}
	}
}

func TestBridgeMutationTraceUsesInnerOutcome(t *testing.T) {
	for _, outcome := range []string{"completed", "uncertain", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "trace.jsonl")
			t.Setenv("OBSTF_TF_TRACE_PATH", trace)
			verified := outcome != "uncertain"
			attempted := outcome != "failed"
			q := &queuedRequester{responses: map[string][]map[string]any{
				"CallVendorRequest":      {testCapabilitiesEnvelope(), bridgeOuter(outcome, attempted, verified)},
				"GetVersion":             {testOrdinaryVersion(), map[string]any{"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"CallVendorRequest"}}},
				"GetSceneCollectionList": {map[string]any{"currentSceneCollectionName": "Lab"}},
				"GetVideoSettings":       {map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}},
			}}
			envelope, err := bridgeMutation(context.Background(), q, "Lab", "CreateGroup", "apply-1/group", map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Outcome != outcome {
				t.Fatalf("outcome=%s", envelope.Outcome)
			}
			b, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := `"status":"` + outcome + `"`
			if outcome == "failed" {
				wantStatus = `"status":"failed-no-write"`
			}
			if !strings.Contains(string(b), wantStatus) {
				t.Fatalf("trace=%s want %s", b, wantStatus)
			}
			if outcome != "completed" && strings.Contains(string(b), `"status":"completed"`) {
				t.Fatalf("outer success was falsely traced complete: %s", b)
			}
		})
	}
}
