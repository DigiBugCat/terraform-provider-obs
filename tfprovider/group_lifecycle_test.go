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
	groupFixtureCollection = "Reference Terraform Lab"
	groupFixtureSceneName  = "Office"
	groupFixtureSceneUUID  = "scene-office"
	groupFixtureGroupName  = "Party Lights"
	groupFixtureGroupUUID  = "group-party"
	groupFixtureInputName  = "Camera Left"
	groupFixtureInputUUID  = "input-camera-left"
)

type groupFixtureCall struct {
	typ  string
	data map[string]any
}

type groupFixtureDirectReply struct {
	response map[string]any
	err      error
}

type groupFixtureBridgeReply struct {
	outcome               string
	code                  string
	message               string
	mutationAttempted     bool
	postconditionVerified bool
	result                map[string]any
	err                   error
}

// groupFixtureScript is an in-process WebSocket-shaped requester. It keeps
// protocol replies explicit while allowing each callback test to verify that
// no later mutation was sent after an uncertain or interrupted response.
type groupFixtureScript struct {
	capabilityRequests []string
	direct             map[string][]groupFixtureDirectReply
	bridge             map[string][]groupFixtureBridgeReply
	calls              []groupFixtureCall
}

func (s *groupFixtureScript) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	s.calls = append(s.calls, groupFixtureCall{typ: typ, data: data})
	if typ == "CallVendorRequest" {
		requestType := stringValue(data["requestType"])
		requestData := mapValue(data["requestData"])
		if requestType == "GetCapabilities" {
			return groupFixtureBridgeEnvelope("GetCapabilities", "", groupFixtureBridgeReply{
				outcome:               "completed",
				mutationAttempted:     false,
				postconditionVerified: true,
				result:                groupFixtureCapabilities(s.capabilityRequests),
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
		return groupFixtureBridgeEnvelope(requestType, stringValue(requestData["clientOperationId"]), reply), nil
	}
	replies := s.direct[typ]
	if len(replies) == 0 {
		// Create clamps its requested index to the current sibling count; an unscripted
		// count read means "empty container" rather than an unexpected request.
		if typ == "GetSceneItemList" || typ == "GetGroupSceneItemList" {
			return scriptedContainerListDefault(), nil
		}
		return nil, fmt.Errorf("unexpected %s", typ)
	}
	reply := replies[0]
	s.direct[typ] = replies[1:]
	return reply.response, reply.err
}

func groupFixtureBridgeEnvelope(requestType, operationID string, reply groupFixtureBridgeReply) map[string]any {
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
		"bridgeVersion":         bridgeTestVersion,
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

func groupFixtureCapabilities(requests []string) map[string]any {
	advertised := make([]any, 0, len(requests))
	for _, requestType := range requests {
		requiresAbsolute := bridgeRequestRequiresAbsoluteRuntime(requestType)
		advertised = append(advertised, map[string]any{"requestType": requestType, "mutation": requiresAbsolute, "requiresAbsoluteRuntime": requiresAbsolute})
	}
	return map[string]any{
		"vendorName":                 "obstf",
		"contractVersion":            1.0,
		"bridgeVersion":              bridgeTestVersion,
		"currentSceneCollectionName": groupFixtureCollection,
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

func groupFixtureDirect(response map[string]any) groupFixtureDirectReply {
	return groupFixtureDirectReply{response: response}
}

func groupFixtureVersion(requests ...string) map[string]any {
	available := make([]any, 0, len(requests))
	for _, requestType := range requests {
		available = append(available, requestType)
	}
	return map[string]any{"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "rpcVersion": 1.0, "availableRequests": available}
}

func groupFixturePlacement() bridgePlacementModel {
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

func groupFixtureGroupResult(itemID int64, groupUUID, groupName string, placement bridgePlacementModel, children []any) map[string]any {
	native, _ := placement.request()
	return map[string]any{
		"scene":    map[string]any{"sourceName": groupFixtureSceneName, "sourceUuid": groupFixtureSceneUUID},
		"group":    map[string]any{"sourceName": groupName, "sourceUuid": groupUUID, "sceneItemId": float64(itemID), "groupWidth": 1920.0, "groupHeight": 1080.0, "placement": native},
		"children": children,
	}
}

func groupFixtureChildResult(itemID int64, inputUUID, inputName string, placement bridgePlacementModel) map[string]any {
	native, _ := placement.request()
	return map[string]any{"sourceName": inputName, "sourceUuid": inputUUID, "inputKind": "image_source", "sceneItemId": float64(itemID), "sourceWidth": 1920.0, "sourceHeight": 1080.0, "placement": native}
}

func groupFixtureGroupModel(itemID int64, sceneName string) groupModel {
	scene := types.StringNull()
	if sceneName != "" {
		scene = types.StringValue(sceneName)
	}
	placement := groupFixturePlacement()
	return groupModel{
		ID: types.StringValue(groupID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, itemID)), Collection: types.StringValue(groupFixtureCollection),
		SceneName: scene, SceneUUID: types.StringValue(groupFixtureSceneUUID), Name: types.StringValue(groupFixtureGroupName),
		GroupUUID: types.StringValue(groupFixtureGroupUUID), ItemID: types.Int64Value(itemID), Placement: &placement,
		GroupWidth: types.Int64Value(1920), GroupHeight: types.Int64Value(1080),
	}
}

func groupFixtureGroupInputModel(file string, groupItemID, itemID int64, groupName string) groupInputModel {
	placement := groupFixturePlacement()
	group := types.StringNull()
	if groupName != "" {
		group = types.StringValue(groupName)
	}
	return groupInputModel{
		ID:         types.StringValue(groupInputID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, groupFixtureInputUUID, itemID)),
		Collection: types.StringValue(groupFixtureCollection), SceneName: types.StringValue(groupFixtureSceneName), SceneUUID: types.StringValue(groupFixtureSceneUUID),
		GroupName: group, GroupUUID: types.StringValue(groupFixtureGroupUUID), GroupItemID: types.Int64Value(groupItemID),
		Name: types.StringValue(groupFixtureInputName), Kind: types.StringValue("image_source"), File: types.StringValue(file), Unload: types.BoolValue(false),
		InputUUID: types.StringValue(groupFixtureInputUUID), ItemID: types.Int64Value(itemID), Placement: &placement,
		SourceWidth: types.Int64Value(1920), SourceHeight: types.Int64Value(1080),
	}
}

func groupFixtureSchemaAndPlan(t *testing.T, ctx context.Context, sc resourceschema.Schema, value any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil), Schema: sc}
	if diags := plan.Set(ctx, value); diags.HasError() {
		t.Fatalf("plan setup diagnostics: %v", diags)
	}
	return plan
}

func groupFixtureSchemaAndState(t *testing.T, ctx context.Context, sc resourceschema.Schema, value any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil), Schema: sc}
	if diags := state.Set(ctx, value); diags.HasError() {
		t.Fatalf("state setup diagnostics: %v", diags)
	}
	return state
}

func groupFixtureGroupSchema(ctx context.Context) resourceschema.Schema {
	var response resource.SchemaResponse
	(&groupResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	return response.Schema
}

func groupFixtureGroupInputSchema(ctx context.Context) resourceschema.Schema {
	var response resource.SchemaResponse
	(&groupInputResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	return response.Schema
}

func groupFixtureAssertDiagnostic(t *testing.T, diags diag.Diagnostics, text string) {
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

func groupFixtureBridgeRequestType(call groupFixtureCall) string {
	if call.typ != "CallVendorRequest" {
		return ""
	}
	return stringValue(call.data["requestType"])
}

func groupFixtureMutationCount(script *groupFixtureScript, typ string) int {
	count := 0
	for _, call := range script.calls {
		if call.typ == typ || groupFixtureBridgeRequestType(call) == typ {
			count++
		}
	}
	return count
}

func groupFixtureReadGroupItemIDs(script *groupFixtureScript) []int64 {
	var ids []int64
	for _, call := range script.calls {
		if groupFixtureBridgeRequestType(call) != "GetGroup" {
			continue
		}
		requestData := mapValue(call.data["requestData"])
		group := mapValue(requestData["group"])
		ids = append(ids, int64Value(group["sceneItemId"]))
	}
	return ids
}

func groupFixtureImageSettings(file string) map[string]any {
	return map[string]any{"inputKind": "image_source", "inputSettings": map[string]any{"file": file, "unload": false}}
}

func groupFixtureInputList() map[string]any {
	return map[string]any{"inputs": []any{map[string]any{"inputName": groupFixtureInputName, "inputUuid": groupFixtureInputUUID, "inputKind": "image_source"}}}
}

func groupFixtureDefaultImageSettings() map[string]any {
	return map[string]any{"defaultInputSettings": map[string]any{"file": "", "unload": false}}
}

func groupFixtureAttachedFilterList() map[string]any {
	return map[string]any{"filters": []any{map[string]any{
		"filterName": "Unmanaged Filter", "filterKind": "color_filter_v2", "filterSettings": map[string]any{}, "filterEnabled": true, "filterIndex": 0.0,
	}}}
}

func groupFixtureSceneList() map[string]any {
	return map[string]any{"scenes": []any{map[string]any{"sceneName": groupFixtureSceneName, "sceneUuid": groupFixtureSceneUUID}}}
}

func groupFixtureRequireState(t *testing.T, ctx context.Context, state tfsdk.State, target any) {
	t.Helper()
	if diags := state.Get(ctx, target); diags.HasError() {
		t.Fatalf("state read diagnostics: %v", diags)
	}
}

func TestLifecycleGroupCreateRetainsIdentityAfterAuthoritativeInterruption(t *testing.T) {
	ctx := context.Background()
	placement := groupFixturePlacement()
	observed := groupFixtureGroupResult(41, "group-created", groupFixtureGroupName, placement, []any{})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: observed}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateGroup")

	model := groupFixtureGroupModel(0, groupFixtureSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ItemID.ValueInt64() != 41 || got.ID.ValueString() != groupID(groupFixtureCollection, groupFixtureSceneUUID, "group-created", 41) {
		t.Fatalf("state=%#v; authoritative assigned identity was not retained", got)
	}
	if groupFixtureMutationCount(script, "CreateGroup") != 1 {
		t.Fatalf("CreateGroup calls=%d", groupFixtureMutationCount(script, "CreateGroup"))
	}
	if groupFixtureMutationCount(script, "GetGroup") != 0 {
		t.Fatalf("unexpected post-interruption GetGroup calls=%d", groupFixtureMutationCount(script, "GetGroup"))
	}
}

func TestLifecycleGroupUpdateRepairsParentAnchorWithNewItemIdentity(t *testing.T) {
	ctx := context.Background()
	old := groupFixtureGroupModel(7, "")
	planModel := groupFixtureGroupModel(7, groupFixtureSceneName)
	script := &groupFixtureScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetGroupList":           {groupFixtureDirect(map[string]any{"groups": []any{groupFixtureGroupName}})},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest", "CreateSceneItem")), groupFixtureDirect(groupFixtureVersion("CreateSceneItem"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"CreateSceneItem":        {groupFixtureDirect(map[string]any{"sceneItemId": 99.0})},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "CreateSceneItem")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupSchema(ctx), planModel)
	response := resource.UpdateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), planModel)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), old)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	wantID := groupID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, 99)
	if got.GroupUUID.ValueString() != groupFixtureGroupUUID || got.ItemID.ValueInt64() != 99 || got.ID.ValueString() != wantID || got.SceneName.ValueString() != groupFixtureSceneName {
		t.Fatalf("state=%#v; repaired parent anchor state is incomplete", got)
	}
	if groupFixtureMutationCount(script, "CreateSceneItem") != 1 {
		t.Fatalf("CreateSceneItem calls=%d", groupFixtureMutationCount(script, "CreateSceneItem"))
	}
	for _, typ := range []string{"SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex"} {
		if count := groupFixtureMutationCount(script, typ); count != 0 {
			t.Fatalf("%s sent after authoritative interruption (%d calls)", typ, count)
		}
	}
}

func TestLifecycleGroupInputCreateRetainsChildIdentityAfterAuthoritativeInterruption(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := groupFixturePlacement()
	child := groupFixtureChildResult(12, "input-created", groupFixtureInputName, placement)
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetInputList":           {groupFixtureDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: child}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateInputInGroup")
	model := groupFixtureGroupInputModel(asset, 7, 0, groupFixtureGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupInputModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	wantID := groupInputID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, "input-created", 12)
	if got.InputUUID.ValueString() != "input-created" || got.ItemID.ValueInt64() != 12 || got.ID.ValueString() != wantID || got.GroupUUID.ValueString() != groupFixtureGroupUUID {
		t.Fatalf("state=%#v; authoritative child identity was not retained", got)
	}
}

func TestLifecycleGroupInputUpdateKeepsSourceIdentityWhenParentItemChanges(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := groupFixturePlacement()
	initialGroup := groupFixtureGroupResult(99, groupFixtureGroupUUID, groupFixtureGroupName, placement, []any{})
	finalChild := groupFixtureChildResult(44, groupFixtureInputUUID, groupFixtureInputName, placement)
	finalGroup := groupFixtureGroupResult(99, groupFixtureGroupUUID, groupFixtureGroupName, placement, []any{finalChild})
	script := &groupFixtureScript{
		capabilityRequests: []string{"AddSourceToGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetInputList":            {groupFixtureDirect(groupFixtureInputList()), groupFixtureDirect(groupFixtureInputList())},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(asset)), groupFixtureDirect(groupFixtureImageSettings(asset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
			"GetVersion":              {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":        {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup":         {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: initialGroup}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: finalGroup}},
			"AddSourceToGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: finalChild}},
		},
	}
	stateModel := groupFixtureGroupInputModel(asset, 7, 12, groupFixtureGroupName)
	planModel := groupFixtureGroupInputModel(asset, 99, 12, groupFixtureGroupName)
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupInputSchema(ctx), planModel)
	response := resource.UpdateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), planModel)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), stateModel)}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupInputModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	wantID := groupInputID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, groupFixtureInputUUID, 44)
	if got.GroupItemID.ValueInt64() != 99 || got.InputUUID.ValueString() != groupFixtureInputUUID || got.ItemID.ValueInt64() != 44 || got.ID.ValueString() != wantID {
		t.Fatalf("state=%#v; parent binding changed the child/source identity", got)
	}
	if groupFixtureMutationCount(script, "CreateInputInGroup") != 0 || groupFixtureMutationCount(script, "AddSourceToGroup") != 1 {
		t.Fatalf("source mutation calls: CreateInputInGroup=%d AddSourceToGroup=%d", groupFixtureMutationCount(script, "CreateInputInGroup"), groupFixtureMutationCount(script, "AddSourceToGroup"))
	}
	for _, itemID := range groupFixtureReadGroupItemIDs(script) {
		if itemID != 99 {
			t.Fatalf("GetGroup request used stale parent item ID %d", itemID)
		}
	}
}

func TestLifecycleGroupInputReadDefersStaleParentWithoutRemovingChildState(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	stateModel := groupFixtureGroupInputModel(asset, 7, 12, groupFixtureGroupName)
	script := &groupFixtureScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetVersion":              {groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetInputList":            {groupFixtureDirect(groupFixtureInputList())},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(asset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings())},
			"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList())},
			"GetSceneItemList":        {groupFixtureDirect(map[string]any{"sceneItems": []any{}})},
			"GetGroupList":            {groupFixtureDirect(map[string]any{"groups": []any{groupFixtureGroupName}})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "failed", code: "ambiguous_group_item_id", message: "parent anchor is absent", mutationAttempted: false, postconditionVerified: true}},
		},
	}
	response := resource.ReadResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), stateModel)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), stateModel)}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected stale-parent diagnostics: %v", response.Diagnostics)
	}
	if response.State.Raw.IsNull() {
		t.Fatal("stale parent read removed the surviving child resource")
	}
	var got groupInputModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.GroupName.ValueString() != groupFixtureGroupName || got.InputUUID.ValueString() != groupFixtureInputUUID || got.ItemID.ValueInt64() != 12 {
		t.Fatalf("state=%#v; stale-parent deferral did not preserve child/source state", got)
	}
	if groupFixtureMutationCount(script, "AddSourceToGroup") != 0 {
		t.Fatal("stale-parent Read sent a mutation")
	}
}

func TestLifecycleGroupReadUnknownOrMalformedNeverRemovesState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		reply groupFixtureBridgeReply
	}{
		{name: "uncertain envelope", reply: groupFixtureBridgeReply{outcome: "uncertain", code: "transport_unknown", message: "response cannot establish postcondition", mutationAttempted: true, postconditionVerified: false, result: map[string]any{}}},
		{name: "completed malformed result", reply: groupFixtureBridgeReply{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: map[string]any{"scene": map[string]any{"sourceName": groupFixtureSceneName, "sourceUuid": groupFixtureSceneUUID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateModel := groupFixtureGroupModel(7, groupFixtureSceneName)
			script := &groupFixtureScript{
				capabilityRequests: []string{"GetGroup"},
				direct: map[string][]groupFixtureDirectReply{
					"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
					"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
				},
				bridge: map[string][]groupFixtureBridgeReply{"GetGroup": {tc.reply}},
			}
			response := resource.ReadResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), stateModel)}
			resourceUnderTest := &groupResource{client: &client{testRequester: script}}
			resourceUnderTest.Read(ctx, resource.ReadRequest{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), stateModel)}, &response)

			if !response.Diagnostics.HasError() {
				t.Fatal("unknown/malformed observation was reported as a successful read")
			}
			if response.State.Raw.IsNull() {
				t.Fatal("unknown/malformed observation removed state")
			}
			if calls := groupFixtureMutationCount(script, "GetGroup"); calls != 1 {
				t.Fatalf("GetGroup calls=%d want exactly one", calls)
			}
			var got groupModel
			groupFixtureRequireState(t, ctx, response.State, &got)
			if got.ID.ValueString() != stateModel.ID.ValueString() || got.GroupUUID.ValueString() != stateModel.GroupUUID.ValueString() || got.ItemID.ValueInt64() != stateModel.ItemID.ValueInt64() {
				t.Fatalf("state=%#v; unknown/malformed read rewrote identity", got)
			}
		})
	}
}

func TestLifecycleGroupCreateDoesNotTreatInnerUncertainAsSuccess(t *testing.T) {
	ctx := context.Background()
	placement := groupFixturePlacement()
	observed := groupFixtureGroupResult(42, "group-uncertain", groupFixtureGroupName, placement, []any{})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup": {{outcome: "uncertain", code: "native_response_unknown", message: "mutation outcome is uncertain", mutationAttempted: true, postconditionVerified: false, result: observed}},
		},
	}
	model := groupFixtureGroupModel(0, groupFixtureSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), model)}
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
	if groupFixtureMutationCount(script, "GetGroup") != 0 {
		t.Fatal("uncertain outcome advanced into a successful postcondition wait")
	}
}

func TestLifecycleGroupInputModifyPlanTreatsParentItemAsMutableBinding(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	stateModel := groupFixtureGroupInputModel(asset, 7, 12, "")
	planModel := groupFixtureGroupInputModel(asset, 99, 12, groupFixtureGroupName)
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupInputSchema(ctx), planModel)
	response := resource.ModifyPlanResponse{Plan: plan}
	resourceUnderTest := &groupInputResource{}
	resourceUnderTest.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), stateModel),
		Plan:  plan,
	}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected ModifyPlan diagnostics: %v", response.Diagnostics)
	}
	if len(response.RequiresReplace) != 0 {
		t.Fatalf("parent item binding unexpectedly requires replacement: %v", response.RequiresReplace)
	}
	var got groupInputModel
	groupFixtureRequireState(t, ctx, tfsdk.State{Raw: response.Plan.Raw, Schema: response.Plan.Schema}, &got)
	if !got.ID.IsUnknown() || !got.ItemID.IsUnknown() || got.GroupItemID.ValueInt64() != 99 {
		t.Fatalf("plan=%#v; parent binding did not preserve in-place child repair semantics", got)
	}
}

func TestLifecycleGroupDeleteRefusesAttachedFilterBeforeMutation(t *testing.T) {
	ctx := context.Background()
	placement := groupFixturePlacement()
	group := groupFixtureGroupResult(7, groupFixtureGroupUUID, groupFixtureGroupName, placement, []any{})
	script := &groupFixtureScript{
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSourceFilterList":    {groupFixtureDirect(groupFixtureAttachedFilterList())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	stateModel := groupFixtureGroupModel(7, groupFixtureSceneName)
	response := resource.DeleteResponse{}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), stateModel)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "attached source filters")
	if groupFixtureMutationCount(script, "RemoveSceneItem") != 0 {
		t.Fatal("group delete sent RemoveSceneItem despite attached filter")
	}
}

func TestLifecycleGroupInputDeleteRefusesAttachedFilterBeforeMutation(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := groupFixturePlacement()
	child := groupFixtureChildResult(12, groupFixtureInputUUID, groupFixtureInputName, placement)
	group := groupFixtureGroupResult(7, groupFixtureGroupUUID, groupFixtureGroupName, placement, []any{child})
	script := &groupFixtureScript{
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSourceFilterList":    {groupFixtureDirect(groupFixtureAttachedFilterList())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	stateModel := groupFixtureGroupInputModel(asset, 7, 12, groupFixtureGroupName)
	response := resource.DeleteResponse{}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), stateModel)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "attached source filters")
	if groupFixtureMutationCount(script, "RemoveGroupChildPlacement") != 0 {
		t.Fatal("group input delete sent RemoveGroupChildPlacement despite attached filter")
	}
}

// scriptedContainerListDefault stands in for an unscripted GetSceneItemList /
// GetGroupSceneItemList: Create and Update clamp the requested index to the container's
// current size, and a well-populated placeholder list keeps that clamp a no-op so the
// scripted setter sequences stay exact.
func scriptedContainerListDefault() map[string]any {
	items := make([]any, 64)
	for i := range items {
		items[i] = map[string]any{"sceneItemId": float64(i + 1)}
	}
	return map[string]any{"sceneItems": items}
}
