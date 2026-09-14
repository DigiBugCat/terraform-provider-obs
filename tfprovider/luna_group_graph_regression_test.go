package tfprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func lunaGraphTestAsset(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("synthetic asset"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func lunaGraphTestChild(t *testing.T, key, name, asset string, x float64, itemID int64) groupGraphChildModel {
	t.Helper()
	placement := lunaLifecyclePlacement()
	placement.Transform.X = types.Float64Value(x)
	placement.Index = types.Int64Value(itemID - 1)
	return groupGraphChildModel{
		StableKey: types.StringValue(key), Name: types.StringValue(name), Kind: types.StringValue("image_source"), File: types.StringValue(asset),
		Unload: types.BoolValue(false), Present: types.BoolValue(true), Placement: &placement,
		SourceUUID: types.StringUnknown(), ItemID: types.Int64Unknown(),
	}
}

func lunaGraphTestModel(t *testing.T, children []groupGraphChildModel) groupGraphModel {
	t.Helper()
	parent := lunaLifecyclePlacement()
	return groupGraphModel{
		ID: types.StringUnknown(), Collection: types.StringValue(lunaLifecycleCollection), SceneName: types.StringValue(lunaLifecycleSceneName),
		SceneUUID: types.StringValue(lunaLifecycleSceneUUID), Name: types.StringValue(lunaLifecycleGroupName), ParentPresent: types.BoolValue(true),
		ParentPlacement: &parent, GroupUUID: types.StringUnknown(), ParentItemID: types.Int64Unknown(), Children: children,
		ObservedComplete: types.BoolUnknown(), ObservedGroupWidth: types.Int64Unknown(), ObservedGroupHeight: types.Int64Unknown(), RepairNonce: types.StringUnknown(),
	}
}

func lunaGraphTestClonePlacement(in *bridgePlacementModel) *bridgePlacementModel {
	if in == nil {
		return nil
	}
	out := *in
	if in.Transform != nil {
		transform := *in.Transform
		out.Transform = &transform
	}
	return &out
}

func lunaGraphSchemaAttribute(t *testing.T, sc schema.Schema, key string) schema.Attribute {
	t.Helper()
	attribute, ok := sc.Attributes[key]
	if !ok {
		t.Fatalf("schema missing attribute %q", key)
	}
	return attribute
}

func TestLunaGroupGraphSchemaPreservesOrderedRosterAndMutableBindings(t *testing.T) {
	ctx := context.Background()
	sc := groupGraphSchema()
	if sc.Version != 1 {
		t.Fatalf("schema version=%d want 1", sc.Version)
	}
	if _, ok := lunaGraphSchemaAttribute(t, sc, "children").(schema.ListNestedAttribute); !ok {
		t.Fatalf("children schema is not an ordered ListNestedAttribute: %T", lunaGraphSchemaAttribute(t, sc, "children"))
	}
	if _, ok := lunaGraphSchemaAttribute(t, sc, "parent_item_id").(schema.Int64Attribute); !ok {
		t.Fatalf("parent_item_id schema is not Int64Attribute: %T", lunaGraphSchemaAttribute(t, sc, "parent_item_id"))
	}
	if _, ok := lunaGraphSchemaAttribute(t, sc, "group_uuid").(schema.StringAttribute); !ok {
		t.Fatalf("group_uuid schema is not StringAttribute: %T", lunaGraphSchemaAttribute(t, sc, "group_uuid"))
	}

	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	model := lunaGraphTestModel(t, []groupGraphChildModel{
		lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		lunaGraphTestChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	plan := lunaLifecycleSchemaAndPlan(t, ctx, sc, model)
	var got groupGraphModel
	if diags := plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("plan read diagnostics: %v", diags)
	}
	if len(got.Children) != 2 || got.Children[0].StableKey.ValueString() != "left" || got.Children[1].StableKey.ValueString() != "right" {
		t.Fatalf("children order=%#v", got.Children)
	}
	if !got.GroupUUID.IsUnknown() || !got.ParentItemID.IsUnknown() || !got.Children[0].SourceUUID.IsUnknown() || !got.Children[1].ItemID.IsUnknown() {
		t.Fatalf("computed binding values were not unknown in initial plan: %#v", got)
	}
}

func lunaGraphTestChildResult(itemID int64, inputUUID, inputName string, placement bridgePlacementModel, width, height int64) map[string]any {
	native, _ := placement.request()
	return map[string]any{"sourceName": inputName, "sourceUuid": inputUUID, "inputKind": "image_source", "sceneItemId": float64(itemID), "sourceWidth": float64(width), "sourceHeight": float64(height), "placement": native}
}

func lunaGraphTestGroupResult(itemID int64, groupUUID, groupName string, placement bridgePlacementModel, width, height int64, children []any) map[string]any {
	native, _ := placement.request()
	return map[string]any{
		"scene":    map[string]any{"sourceName": lunaLifecycleSceneName, "sourceUuid": lunaLifecycleSceneUUID},
		"group":    map[string]any{"sourceName": groupName, "sourceUuid": groupUUID, "sceneItemId": float64(itemID), "groupWidth": float64(width), "groupHeight": float64(height), "placement": native},
		"children": children,
	}
}

func lunaGraphTestScreenshot() map[string]any {
	return map[string]any{"imageData": "data:image/png;base64,aGVsbG8="}
}

func lunaGraphTestRelevantOperations(script *lunaLifecycleScript) []string {
	var operations []string
	for _, call := range script.calls {
		if call.typ == "GetSourceScreenshot" {
			operations = append(operations, call.typ)
			continue
		}
		if call.typ == "SetSceneItemTransform" {
			operations = append(operations, call.typ)
			continue
		}
		if call.typ != "CallVendorRequest" {
			continue
		}
		requestType := stringValue(call.data["requestType"])
		switch requestType {
		case "CreateGroup", "CreateInputInGroup", "AddSourceToGroup", "GetGroup", "GetSourceScreenshot":
			operations = append(operations, requestType)
		}
	}
	return operations
}

func lunaGraphTestConfig(t *testing.T, ctx context.Context, sc schema.Schema, value groupGraphModel) tfsdk.Config {
	t.Helper()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, sc, value)
	return tfsdk.Config{Raw: plan.Raw, Schema: plan.Schema}
}

func lunaGraphTestEmptyState(ctx context.Context, sc schema.Schema) tfsdk.State {
	return tfsdk.State{Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil), Schema: sc}
}

func lunaGraphTestStateWithBindings(t *testing.T, ctx context.Context, sc schema.Schema, model groupGraphModel) tfsdk.State {
	t.Helper()
	return lunaLifecycleSchemaAndState(t, ctx, sc, model)
}

func lunaGraphTestCompleteModel(t *testing.T, leftAsset, rightAsset string) groupGraphModel {
	t.Helper()
	left := lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1)
	right := lunaGraphTestChild(t, "right", "Party Light Right", rightAsset, 960, 2)
	left.SourceUUID, left.ItemID = types.StringValue("left-uuid"), types.Int64Value(1)
	right.SourceUUID, right.ItemID = types.StringValue("right-uuid"), types.Int64Value(2)
	model := lunaGraphTestModel(t, []groupGraphChildModel{left, right})
	model.ID = types.StringValue(groupGraphID(lunaLifecycleCollection, lunaLifecycleSceneUUID, "group-stable"))
	model.GroupUUID, model.ParentItemID = types.StringValue("group-stable"), types.Int64Value(7)
	model.ObservedComplete = types.BoolValue(true)
	return model
}

func lunaGraphTestCompleteGroup(leftAsset, rightAsset string) (map[string]any, map[string]any, map[string]any) {
	parent := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	left := lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080)
	right := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{left, right})
	return group, left, right
}

func lunaGraphTestCompleteInputResponses(leftAsset, rightAsset string) []lunaLifecycleDirectReply {
	return []lunaLifecycleDirectReply{
		lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}),
		lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()),
		lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}}),
		lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset)), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()),
	}
}

type lunaGraphCapabilitiesRequester struct {
	base            *lunaLifecycleScript
	mutate          func(map[string]any)
	mutateOnCall    int
	capabilityCalls int
}

func (r *lunaGraphCapabilitiesRequester) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if typ == "CallVendorRequest" && stringValue(data["requestType"]) == "GetCapabilities" {
		r.base.calls = append(r.base.calls, lunaLifecycleCall{typ: typ, data: data})
		r.capabilityCalls++
		result := lunaLifecycleCapabilities(r.base.capabilityRequests)
		if r.mutateOnCall == 0 || r.capabilityCalls == r.mutateOnCall {
			r.mutate(result)
		}
		return lunaLifecycleBridgeEnvelope("GetCapabilities", "", lunaLifecycleBridgeReply{outcome: "completed", postconditionVerified: true, result: result}), nil
	}
	return r.base.Req(ctx, typ, data)
}

func lunaGraphTestBroadVersion() map[string]any {
	return lunaLifecycleVersion("CallVendorRequest", "CreateInputInGroup", "AddSourceToGroup", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "RemoveGroupChildPlacement", "RemoveSceneItem", "SetInputSettings")
}

func TestLunaGroupGraphCreateRunsOrderedChildrenAndFrameBarriers(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	parentPlacement := lunaLifecyclePlacement()
	groupAfterCreate := lunaGraphTestGroupResult(7, "group-created", lunaLifecycleGroupName, parentPlacement, 1920, 1080, []any{})
	left := lunaGraphTestChildResult(1, "left-created", "Party Light Left", leftPlacement, 960, 1080)
	right := lunaGraphTestChildResult(2, "right-created", "Party Light Right", rightPlacement, 960, 1080)
	groupAfterLeft := lunaGraphTestGroupResult(7, "group-created", lunaLifecycleGroupName, parentPlacement, 1920, 1080, []any{left})
	groupAfterRight := lunaGraphTestGroupResult(7, "group-created", lunaLifecycleGroupName, parentPlacement, 1920, 1080, []any{left, right})

	emptyInputs := map[string]any{"inputs": []any{}}
	inputs := map[string]any{"inputs": []any{
		map[string]any{"inputName": "Party Light Left", "inputUuid": "left-created", "inputKind": "image_source"},
		map[string]any{"inputName": "Party Light Right", "inputUuid": "right-created", "inputKind": "image_source"},
	}}
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup", "CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetGroupList":            {lunaLifecycleDirect(map[string]any{"groups": []any{}})},
			"GetInputList":            {lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(inputs), lunaLifecycleDirect(inputs)},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
			"GetVersion":              {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":        {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":     {lunaLifecycleDirect(lunaGraphTestScreenshot()), lunaLifecycleDirect(lunaGraphTestScreenshot()), lunaLifecycleDirect(lunaGraphTestScreenshot())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup":        {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: groupAfterCreate}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}, {outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: right}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterCreate}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterLeft}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterRight}},
		},
	}
	model := lunaGraphTestModel(t, []groupGraphChildModel{
		lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		lunaGraphTestChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ParentItemID.ValueInt64() != 7 || !got.ObservedComplete.ValueBool() || len(got.Children) != 2 {
		t.Fatalf("state=%#v; successful aggregate create did not persist complete graph", got)
	}
	if got.Children[0].SourceUUID.ValueString() != "left-created" || got.Children[1].SourceUUID.ValueString() != "right-created" || got.Children[0].ItemID.ValueInt64() != 1 || got.Children[1].ItemID.ValueInt64() != 2 {
		t.Fatalf("children=%#v; ordered child identities were not checkpointed", got.Children)
	}
	wantOperations := []string{"CreateGroup", "GetSourceScreenshot", "GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup"}
	if gotOperations := lunaGraphTestRelevantOperations(script); len(gotOperations) != len(wantOperations) {
		t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
	} else {
		for i := range wantOperations {
			if gotOperations[i] != wantOperations[i] {
				t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
			}
		}
	}
	if lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemEnabled") != 0 {
		t.Fatal("initial exact graph create emitted redundant placement setters")
	}
	for _, call := range script.calls {
		if call.typ != "GetSourceScreenshot" {
			continue
		}
		if stringValue(call.data["sourceUuid"]) != lunaLifecycleSceneUUID || stringValue(call.data["imageFormat"]) != "png" || numberOrZero(call.data["imageWidth"]) != 64 || numberOrZero(call.data["imageHeight"]) != 36 {
			t.Fatalf("frame barrier request=%#v; want parent scene png 64x36", call.data)
		}
	}
}

func TestLunaGroupGraphValidateConfigRejectsUnsupportedPresenceAndRoster(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	base := lunaGraphTestModel(t, []groupGraphChildModel{
		lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		lunaGraphTestChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	for _, tc := range []struct {
		name string
		edit func(*groupGraphModel)
		want string
	}{
		{name: "parent absent", edit: func(m *groupGraphModel) { m.ParentPresent = types.BoolValue(false) }, want: "parent_present must be true"},
		{name: "child absent", edit: func(m *groupGraphModel) { m.Children[0].Present = types.BoolValue(false) }, want: "children[0].present must be true"},
		{name: "duplicate stable key", edit: func(m *groupGraphModel) { m.Children[1].StableKey = types.StringValue("left") }, want: "stable_key \"left\" is duplicated"},
		{name: "unsupported child kind", edit: func(m *groupGraphModel) { m.Children[0].Kind = types.StringValue("ffmpeg_source") }, want: "currently supports only image_source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := base
			model.Children = append([]groupGraphChildModel(nil), base.Children...)
			tc.edit(&model)
			response := resource.ValidateConfigResponse{}
			resourceUnderTest := &groupGraphResource{}
			resourceUnderTest.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: lunaGraphTestConfig(t, ctx, groupGraphSchema(), model)}, &response)
			lunaLifecycleAssertDiagnostic(t, response.Diagnostics, tc.want)
		})
	}
}

func TestLunaGroupGraphModifyPlanKeepsParentItemTurnoverInPlace(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	planModel := stateModel
	planModel.ParentItemID = types.Int64Value(99)
	sc := groupGraphSchema()
	resourceUnderTest := &groupGraphResource{}
	plan := lunaLifecycleSchemaAndPlan(t, ctx, sc, planModel)
	response := resource.ModifyPlanResponse{Plan: plan}
	resourceUnderTest.ModifyPlan(ctx, resource.ModifyPlanRequest{State: lunaGraphTestStateWithBindings(t, ctx, sc, stateModel), Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected parent-item turnover diagnostics: %v", response.Diagnostics)
	}
	if len(response.RequiresReplace) != 0 {
		t.Fatalf("parent item turnover unexpectedly requires replacement: %v", response.RequiresReplace)
	}

	for _, tc := range []struct {
		name string
		edit func(*groupGraphModel)
		want string
	}{
		{name: "group name", edit: func(m *groupGraphModel) { m.Name = types.StringValue("Renamed") }, want: "group name changes are not supported"},
		{name: "stable key", edit: func(m *groupGraphModel) { m.Children[0].StableKey = types.StringValue("renamed") }, want: "stable_key changes are not supported"},
		{name: "child name", edit: func(m *groupGraphModel) { m.Children[0].Name = types.StringValue("Renamed Child") }, want: "children[0].name changes are not supported"},
		{name: "child kind", edit: func(m *groupGraphModel) { m.Children[0].Kind = types.StringValue("ffmpeg_source") }, want: "children[0].kind changes are not supported"},
		{name: "child removal", edit: func(m *groupGraphModel) { m.Children = m.Children[:1] }, want: "removing an existing child is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := stateModel
			candidate.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
			tc.edit(&candidate)
			candidatePlan := lunaLifecycleSchemaAndPlan(t, ctx, sc, candidate)
			candidateResponse := resource.ModifyPlanResponse{Plan: candidatePlan}
			resourceUnderTest.ModifyPlan(ctx, resource.ModifyPlanRequest{State: lunaGraphTestStateWithBindings(t, ctx, sc, stateModel), Plan: candidatePlan}, &candidateResponse)
			lunaLifecycleAssertDiagnostic(t, candidateResponse.Diagnostics, tc.want)
		})
	}
}

func TestLunaGroupGraphImportSeedsExactOrderedBindings(t *testing.T) {
	ctx := context.Background()
	sc := groupGraphSchema()
	response := resource.ImportStateResponse{State: lunaGraphTestEmptyState(ctx, sc)}
	resourceUnderTest := &groupGraphResource{}
	resourceUnderTest.ImportState(ctx, resource.ImportStateRequest{ID: lunaLifecycleCollection + "|" + lunaLifecycleSceneUUID + "|group-imported|7|left=left-uuid:1,right=right-uuid:2"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.ID.ValueString() != groupGraphID(lunaLifecycleCollection, lunaLifecycleSceneUUID, "group-imported") || got.ParentItemID.ValueInt64() != 7 || len(got.Children) != 2 {
		t.Fatalf("imported state=%#v", got)
	}
	if got.Children[0].StableKey.ValueString() != "left" || got.Children[0].SourceUUID.ValueString() != "left-uuid" || got.Children[0].ItemID.ValueInt64() != 1 || got.Children[1].StableKey.ValueString() != "right" || got.Children[1].SourceUUID.ValueString() != "right-uuid" || got.Children[1].ItemID.ValueInt64() != 2 {
		t.Fatalf("imported children=%#v", got.Children)
	}

	for _, id := range []string{
		"bad",
		lunaLifecycleCollection + "|" + lunaLifecycleSceneUUID + "|group|0|left=left-uuid:1",
		lunaLifecycleCollection + "|" + lunaLifecycleSceneUUID + "|group|7|left=left-uuid:1,left=other:2",
	} {
		invalid := resource.ImportStateResponse{State: lunaGraphTestEmptyState(ctx, sc)}
		resourceUnderTest.ImportState(ctx, resource.ImportStateRequest{ID: id}, &invalid)
		if !invalid.Diagnostics.HasError() {
			t.Fatalf("invalid import %q was accepted", id)
		}
	}
}

func TestLunaGroupGraphReadRecordsCompleteRosterAndParent(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	group, _, _ := lunaGraphTestCompleteGroup(leftAsset, rightAsset)
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetVersion":              {lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetInputList":            lunaGraphTestCompleteInputResponses(leftAsset, rightAsset)[0:1],
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	// The list responses are interleaved with settings/default responses by
	// inputByUUID; spell that sequence out so a wrong read order fails loudly.
	script.direct["GetInputList"] = []lunaLifecycleDirectReply{
		lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}),
		lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}}),
	}
	response := resource.ReadResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected Read diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if !got.ObservedComplete.ValueBool() || !got.ParentPresent.ValueBool() || got.ObservedGroupWidth.ValueInt64() != 1920 || got.ObservedGroupHeight.ValueInt64() != 1080 || len(got.ObservedChildren) != 2 {
		t.Fatalf("read state=%#v", got)
	}
	if got.Children[0].File.ValueString() != leftAsset || got.Children[1].File.ValueString() != rightAsset || got.Children[0].SourceUUID.ValueString() != "left-uuid" || got.Children[1].SourceUUID.ValueString() != "right-uuid" {
		t.Fatalf("read children=%#v", got.Children)
	}
	if lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 0 {
		t.Fatal("Read sent a placement mutation")
	}
}

func TestLunaGroupGraphReadUnknownOrMalformedPreservesState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		reply lunaLifecycleBridgeReply
	}{
		{name: "uncertain", reply: lunaLifecycleBridgeReply{outcome: "uncertain", code: "transport_unknown", message: "group read uncertain", mutationAttempted: true, postconditionVerified: false, result: map[string]any{}}},
		{name: "malformed", reply: lunaLifecycleBridgeReply{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: map[string]any{"scene": map[string]any{"sourceName": lunaLifecycleSceneName, "sourceUuid": lunaLifecycleSceneUUID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leftAsset := lunaGraphTestAsset(t, "left.png")
			rightAsset := lunaGraphTestAsset(t, "right.png")
			stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
			script := &lunaLifecycleScript{
				capabilityRequests: []string{"GetGroup"},
				direct: map[string][]lunaLifecycleDirectReply{
					"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
					"GetVersion":             {lunaLifecycleDirect(lunaGraphTestBroadVersion())},
				},
				bridge: map[string][]lunaLifecycleBridgeReply{"GetGroup": {tc.reply}},
			}
			response := resource.ReadResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
			resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
			resourceUnderTest.Read(ctx, resource.ReadRequest{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("unknown/malformed group read was reported successful")
			}
			if response.State.Raw.IsNull() {
				t.Fatal("unknown/malformed group read removed state")
			}
			if calls := lunaLifecycleMutationCount(script, "GetGroup"); calls != 1 {
				t.Fatalf("GetGroup calls=%d want exactly one", calls)
			}
			var got groupGraphModel
			lunaLifecycleRequireState(t, ctx, response.State, &got)
			if got.GroupUUID.ValueString() != stateModel.GroupUUID.ValueString() || got.ParentItemID.ValueInt64() != stateModel.ParentItemID.ValueInt64() {
				t.Fatalf("identity changed after failed read: %#v", got)
			}
		})
	}
}

func TestLunaGroupGraphUpdateConvergedGraphSendsNoWrites(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	model := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	group, _, _ := lunaGraphTestCompleteGroup(leftAsset, rightAsset)
	script := &lunaLifecycleScript{
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetInputList":            {lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}}},
	}
	sc := groupGraphSchema()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, sc, model)
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaLifecycleSchemaAndState(t, ctx, sc, model)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected converged Update diagnostics: %v calls=%#v", response.Diagnostics, script.calls)
	}
	for _, typ := range []string{"CreateInputInGroup", "AddSourceToGroup", "SetInputSettings", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "CreateSceneItem"} {
		if calls := lunaLifecycleMutationCount(script, typ); calls != 0 {
			t.Fatalf("converged Update sent %s (%d calls)", typ, calls)
		}
	}
}

func TestLunaGroupGraphDeleteRefusesChildFilterBeforeMutation(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	model := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	group, _, _ := lunaGraphTestCompleteGroup(leftAsset, rightAsset)
	script := &lunaLifecycleScript{
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSourceFilterList":    {lunaLifecycleDirect(lunaLifecycleAttachedFilterList())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}}},
	}
	response := resource.DeleteResponse{}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), model)}, &response)
	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "attached filters")
	if lunaLifecycleMutationCount(script, "RemoveGroupChildPlacement") != 0 || lunaLifecycleMutationCount(script, "RemoveSceneItem") != 0 {
		t.Fatal("group graph Delete mutated despite attached child filter")
	}
}

func lunaGraphTestSetterTargets(script *lunaLifecycleScript) []struct {
	scene string
	item  int64
	x     float64
} {
	var targets []struct {
		scene string
		item  int64
		x     float64
	}
	for _, call := range script.calls {
		if call.typ != "SetSceneItemTransform" {
			continue
		}
		transform := mapValue(call.data["sceneItemTransform"])
		targets = append(targets, struct {
			scene string
			item  int64
			x     float64
		}{scene: stringValue(call.data["sceneName"]), item: int64Value(call.data["sceneItemId"]), x: numberOrZero(transform["positionX"])})
	}
	return targets
}

func numberOrZero(value any) float64 {
	got, _ := numberValue(value)
	return got
}

func TestLunaGroupGraphUpdateRepairsMissingLeftWithOrderedCompensation(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	// Update's stale-write guard compares the first native observation with the
	// last accepted state. Model the preceding Read accurately: losing the left
	// child rebased the parent to x=960 and the surviving right child to x=0.
	stateModel.ParentPlacement = lunaGraphTestClonePlacement(stateModel.ParentPlacement)
	stateModel.ParentPlacement.Transform.X = types.Float64Value(960)
	stateModel.Children[0].SourceUUID = types.StringValue("left-old")
	stateModel.Children[0].Present = types.BoolValue(false)
	stateModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
	stateModel.Children[1].Placement.Transform.X = types.Float64Value(0)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.ParentPlacement = lunaGraphTestClonePlacement(stateModel.ParentPlacement)
	planModel.ParentPlacement.Transform.X = types.Float64Value(0)
	for i := range planModel.Children {
		planModel.Children[i].Placement = lunaGraphTestClonePlacement(stateModel.Children[i].Placement)
	}
	planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
	planModel.Children[0].Present = types.BoolValue(true)

	parentRebased := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	leftPlacement.Index = types.Int64Value(0)
	rightRebased := lunaLifecyclePlacement()
	rightRebased.Transform.X = types.Float64Value(0)
	rightRebased.Index = types.Int64Value(1)
	rightRestored := lunaLifecyclePlacement()
	rightRestored.Transform.X = types.Float64Value(960)
	rightRestored.Index = types.Int64Value(1)
	parentRestored := lunaLifecyclePlacement()
	parentRestored.Transform.X = types.Float64Value(0)
	left := lunaGraphTestChildResult(3, "left-new", "Party Light Left", leftPlacement, 960, 1080)
	rightAtZero := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightRebased, 960, 1080)
	rightAt960 := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightRestored, 960, 1080)
	groupBefore := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parentRebased, 960, 1080, []any{left, rightAtZero})
	groupAfterChild := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parentRebased, 1920, 1080, []any{left, rightAt960})
	groupFinal := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parentRestored, 1920, 1080, []any{left, rightAt960})
	inputs := map[string]any{"inputs": []any{
		map[string]any{"inputName": "Party Light Left", "inputUuid": "left-new", "inputKind": "image_source"},
		map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"},
	}}
	emptyInputs := map[string]any{"inputs": []any{}}
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateInputInGroup", "GetGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetInputList":            {lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(inputs), lunaLifecycleDirect(inputs)},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
			"GetVersion":              {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":        {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":     {lunaLifecycleDirect(lunaGraphTestScreenshot()), lunaLifecycleDirect(lunaGraphTestScreenshot()), lunaLifecycleDirect(lunaGraphTestScreenshot())},
			"SetSceneItemTransform":   {lunaLifecycleDirect(map[string]any{}), lunaLifecycleDirect(map[string]any{})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parentRebased, 960, 1080, []any{rightAtZero})}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupBefore}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterChild}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupFinal}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}},
		},
	}
	// The initial observation contains only the surviving right child at the
	// rebased local origin. The replacement frame then contains both children;
	// later frames prove child-first and parent-last compensation.
	// The first GetGroup response above is the pre-update read, while the
	// following three are the membership, child-compensation and final barriers.
	sc := groupGraphSchema()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, sc, planModel)
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, planModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaLifecycleSchemaAndState(t, ctx, sc, stateModel)}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected ARR-57 repair diagnostics: %v calls=%#v", response.Diagnostics, script.calls)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if !got.ObservedComplete.ValueBool() || got.ParentItemID.ValueInt64() != 7 || got.Children[0].SourceUUID.ValueString() != "left-new" || got.Children[0].ItemID.ValueInt64() != 3 || got.Children[1].SourceUUID.ValueString() != "right-uuid" || got.Children[1].ItemID.ValueInt64() != 2 {
		t.Fatalf("repaired state=%#v; survivor/replacement bindings are wrong", got)
	}
	if got.ParentPlacement.Transform.X.ValueFloat64() != 0 || got.Children[0].Placement.Transform.X.ValueFloat64() != 0 || got.Children[1].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("repaired placements parent=%v left=%v right=%v", got.ParentPlacement.Transform.X.ValueFloat64(), got.Children[0].Placement.Transform.X.ValueFloat64(), got.Children[1].Placement.Transform.X.ValueFloat64())
	}
	if calls := lunaLifecycleMutationCount(script, "CreateInputInGroup"); calls != 1 {
		t.Fatalf("replacement child creates=%d want one", calls)
	}
	if calls := lunaLifecycleMutationCount(script, "SetSceneItemTransform"); calls != 2 {
		t.Fatalf("compensation setters=%d want right+parent only", calls)
	}
	setters := lunaGraphTestSetterTargets(script)
	if len(setters) != 2 || setters[0].item != 2 || setters[0].x != 960 || setters[1].item != 7 || setters[1].x != 0 {
		t.Fatalf("setter targets=%#v want right item 2 x960 then parent item 7 x0", setters)
	}
	wantOperations := []string{"GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup", "SetSceneItemTransform", "GetSourceScreenshot", "GetGroup", "SetSceneItemTransform", "GetSourceScreenshot", "GetGroup"}
	gotOperations := lunaGraphTestRelevantOperations(script)
	if len(gotOperations) != len(wantOperations) {
		t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
	}
	for i := range wantOperations {
		if gotOperations[i] != wantOperations[i] {
			t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
		}
	}
}

func TestLunaGroupGraphUpdateRefusesActiveOrUnknownOutputsBeforeMembership(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		key   string
		value any
	}{
		{name: "streaming", key: "streamingActive", value: true},
		{name: "recording", key: "recordingActive", value: true},
		{name: "replay buffer", key: "replayBufferActive", value: true},
		{name: "virtual camera", key: "virtualCameraActive", value: true},
		{name: "streaming unknown", key: "streamingActive", value: "unknown"},
		{name: "recording unknown", key: "recordingActive", value: "unknown"},
		{name: "replay buffer unknown", key: "replayBufferActive", value: "unknown"},
		{name: "virtual camera unknown", key: "virtualCameraActive", value: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leftAsset := lunaGraphTestAsset(t, "left.png")
			rightAsset := lunaGraphTestAsset(t, "right.png")
			stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
			stateModel.Children[0].SourceUUID = types.StringValue("left-old")
			stateModel.Children[0].Present = types.BoolValue(false)
			planModel := stateModel
			planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
			planModel.Children[0].Present = types.BoolValue(true)

			parent := lunaLifecyclePlacement()
			rightPlacement := lunaLifecyclePlacement()
			rightPlacement.Transform.X = types.Float64Value(960)
			rightPlacement.Index = types.Int64Value(1)
			right := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
			group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 960, 1080, []any{right})
			emptyInputs := map[string]any{"inputs": []any{}}
			script := &lunaLifecycleScript{
				capabilityRequests: []string{"CreateInputInGroup"},
				direct: map[string][]lunaLifecycleDirectReply{
					"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
					"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
					"GetInputList":           {lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs)},
					"GetVersion":             {lunaLifecycleDirect(lunaGraphTestBroadVersion())},
				},
				bridge: map[string][]lunaLifecycleBridgeReply{
					"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
				},
			}
			requester := &lunaGraphCapabilitiesRequester{base: script, mutate: func(result map[string]any) { result[tc.key] = tc.value }}
			plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
			response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
			resourceUnderTest := &groupGraphResource{client: &client{testRequester: requester}}
			resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

			lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "bridge capability "+tc.key+" is active or unknown")
			if lunaLifecycleMutationCount(script, "CreateInputInGroup") != 0 || lunaLifecycleMutationCount(script, "AddSourceToGroup") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 0 {
				t.Fatalf("output guard allowed membership/placement mutation: calls=%#v", script.calls)
			}
			if lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 {
				t.Fatal("output guard reached a frame barrier")
			}
		})
	}
}

func TestLunaGroupGraphUpdateRefusesActiveOrUnknownOutputsBeforeSetterPhases(t *testing.T) {
	ctx := context.Background()
	outputs := []struct {
		name  string
		key   string
		value any
	}{
		{name: "streaming", key: "streamingActive", value: true},
		{name: "recording", key: "recordingActive", value: true},
		{name: "replay buffer", key: "replayBufferActive", value: true},
		{name: "virtual camera", key: "virtualCameraActive", value: true},
		{name: "streaming unknown", key: "streamingActive", value: "unknown"},
		{name: "recording unknown", key: "recordingActive", value: "unknown"},
		{name: "replay buffer unknown", key: "replayBufferActive", value: "unknown"},
		{name: "virtual camera unknown", key: "virtualCameraActive", value: "unknown"},
	}
	for _, phase := range []string{"child setter", "parent setter"} {
		for _, output := range outputs {
			t.Run(phase+"/"+output.name, func(t *testing.T) {
				leftAsset := lunaGraphTestAsset(t, "left.png")
				rightAsset := lunaGraphTestAsset(t, "right.png")
				stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
				planModel := stateModel
				planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)

				parent := lunaLifecyclePlacement()
				leftPlacement := lunaLifecyclePlacement()
				leftPlacement.Transform.X = types.Float64Value(0)
				rightPlacement := lunaLifecyclePlacement()
				rightPlacement.Transform.X = types.Float64Value(960)
				rightPlacement.Index = types.Int64Value(1)
				if phase == "child setter" {
					stateModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
					stateModel.Children[1].Placement.Transform.X = types.Float64Value(900)
					planModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
					planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
					rightPlacement.Transform.X = types.Float64Value(900)
				}
				if phase == "parent setter" {
					stateModel.ParentPlacement = lunaGraphTestClonePlacement(stateModel.ParentPlacement)
					stateModel.ParentPlacement.Transform.X = types.Float64Value(900)
					planModel.ParentPlacement = lunaGraphTestClonePlacement(stateModel.ParentPlacement)
					planModel.ParentPlacement.Transform.X = types.Float64Value(960)
					parent.Transform.X = types.Float64Value(900)
				}
				group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{
					lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
					lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
				})
				script := &lunaLifecycleScript{
					capabilityRequests: []string{"GetGroup"},
					direct: map[string][]lunaLifecycleDirectReply{
						"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
						"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList())},
						"GetInputList":            {lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
						"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
						"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
						"GetVersion":              {lunaLifecycleDirect(lunaGraphTestBroadVersion())},
					},
					bridge: map[string][]lunaLifecycleBridgeReply{
						"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
					},
				}
				requester := &lunaGraphCapabilitiesRequester{
					base: script, mutateOnCall: 1,
					mutate: func(result map[string]any) { result[output.key] = output.value },
				}
				plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
				response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
				resourceUnderTest := &groupGraphResource{client: &client{testRequester: requester}}
				resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

				lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "bridge capability "+output.key+" is active or unknown")
				if requester.capabilityCalls != 1 {
					t.Fatalf("%s guard calls=%d want one", output.key, requester.capabilityCalls)
				}
				for _, typ := range []string{"SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "SetInputSettings", "GetSourceScreenshot"} {
					if calls := lunaLifecycleMutationCount(script, typ); calls != 0 {
						t.Fatalf("%s guard allowed %s (%d calls): %#v", output.key, typ, calls, script.calls)
					}
				}
			})
		}
	}
}

func TestLunaGroupGraphUpdateRefusesWrongUUIDAndUnmanagedExtraBeforeMembership(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	parent := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	left := lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080)
	right := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	wrongLeft := lunaGraphTestChildResult(1, "left-other", "Party Light Left", leftPlacement, 960, 1080)
	extra := lunaGraphTestChildResult(3, "unmanaged-uuid", "Unmanaged Child", lunaLifecyclePlacement(), 960, 1080)
	for _, tc := range []struct {
		name  string
		group map[string]any
		want  string
	}{
		{name: "same-name wrong UUID", group: lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{wrongLeft, right}), want: "rebound"},
		{name: "unmanaged extra child", group: lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{left, right, extra}), want: "unexpected unmanaged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &lunaLifecycleScript{
				direct: map[string][]lunaLifecycleDirectReply{
					"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
					"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
				},
				bridge: map[string][]lunaLifecycleBridgeReply{
					"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: tc.group}},
				},
			}
			plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), stateModel)
			response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
			resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
			resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

			lunaLifecycleAssertDiagnostic(t, response.Diagnostics, tc.want)
			for _, typ := range []string{"CreateInputInGroup", "AddSourceToGroup", "SetSceneItemTransform", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemIndex"} {
				if calls := lunaLifecycleMutationCount(script, typ); calls != 0 {
					t.Fatalf("%s mutated after pre-membership refusal (%d calls)", typ, calls)
				}
			}
		})
	}
}

func TestLunaGroupGraphCreateRefusesGroupSourceFrameBarrier(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	model := lunaGraphTestModel(t, []groupGraphChildModel{lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1)})
	parent := lunaLifecyclePlacement()
	group := lunaGraphTestGroupResult(7, lunaLifecycleSceneUUID, lunaLifecycleGroupName, parent, 1920, 1080, []any{})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetGroupList":           {lunaLifecycleDirect(map[string]any{"groups": []any{}})},
			"GetInputList":           {lunaLifecycleDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "frame barrier refuses group-source screenshot")
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != lunaLifecycleSceneUUID || got.ParentItemID.ValueInt64() != 7 || got.ID.ValueString() != groupGraphID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleSceneUUID) {
		t.Fatalf("checkpointed state=%#v; CreateGroup identity was not retained", got)
	}
	if lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 || lunaLifecycleMutationCount(script, "CreateInputInGroup") != 0 {
		t.Fatalf("group-source barrier allowed later work: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphUpdateRefusesMissingZeroAnchorBeforeMutation(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	model := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	model.Children[0].Placement = lunaGraphTestClonePlacement(model.Children[0].Placement)
	model.Children[0].Placement.Transform.X = types.Float64Value(100)
	model.Children[1].Placement = lunaGraphTestClonePlacement(model.Children[1].Placement)
	model.Children[1].Placement.Transform.X = types.Float64Value(1060)
	parent := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(100)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(1060)
	rightPlacement.Index = types.Int64Value(1)
	group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{
		lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &lunaLifecycleScript{
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), model)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "managed transformed minimum")
	if lunaLifecycleMutationCount(script, "GetCapabilities") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 0 || lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 {
		t.Fatalf("zero-anchor refusal allowed native work: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphUpdateRefusesConcurrentPlacementDriftBeforeMutation(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	parent := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(900)
	rightPlacement.Index = types.Int64Value(1)
	group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{
		lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &lunaLifecycleScript{
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), stateModel)
	response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "changed concurrently after refresh")
	if lunaLifecycleMutationCount(script, "GetCapabilities") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 0 || lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 {
		t.Fatalf("concurrent-drift refusal allowed native work: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphCreateRetainsGroupIdentityAfterAuthoritativeInterruption(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	model := lunaGraphTestModel(t, []groupGraphChildModel{lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1)})
	parent := lunaLifecyclePlacement()
	group := lunaGraphTestGroupResult(7, "group-created", lunaLifecycleGroupName, parent, 1920, 1080, []any{})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetGroupList":           {lunaLifecycleDirect(map[string]any{"groups": []any{}})},
			"GetInputList":           {lunaLifecycleDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateGroup")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ParentItemID.ValueInt64() != 7 || got.ID.ValueString() != groupGraphID(lunaLifecycleCollection, lunaLifecycleSceneUUID, "group-created") {
		t.Fatalf("checkpointed state=%#v; authoritative CreateGroup identity was not retained", got)
	}
	if got.Children[0].Present.ValueBool() || !got.Children[0].SourceUUID.IsUnknown() || !got.Children[0].ItemID.IsUnknown() || len(got.ObservedChildren) != 0 {
		t.Fatalf("uncreated child was reported as applied after CreateGroup interruption: %#v", got)
	}
	if lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 || lunaLifecycleMutationCount(script, "CreateInputInGroup") != 0 || lunaLifecycleMutationCount(script, "GetGroup") != 0 {
		t.Fatalf("Create continued after authoritative interruption: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphCreateBarrierFailureAfterFirstChildRetainsPrefix(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	model := lunaGraphTestModel(t, []groupGraphChildModel{
		lunaGraphTestChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		lunaGraphTestChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	parent := lunaLifecyclePlacement()
	groupAfterCreate := lunaGraphTestGroupResult(7, "group-created", lunaLifecycleGroupName, parent, 1920, 1080, []any{})
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	left := lunaGraphTestChildResult(1, "left-created", "Party Light Left", leftPlacement, 960, 1080)
	emptyInputs := map[string]any{"inputs": []any{}}
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup", "CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetGroupList":           {lunaLifecycleDirect(map[string]any{"groups": []any{}})},
			"GetInputList":           {lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs)},
			"GetVersion":             {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":    {lunaLifecycleDirect(lunaGraphTestScreenshot()), {err: errors.New("synthetic frame barrier failure")}},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup":        {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: groupAfterCreate}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterCreate}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "parent-scene frame barrier failed")
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ParentItemID.ValueInt64() != 7 || len(got.Children) != 2 || got.Children[0].SourceUUID.ValueString() != "left-created" || got.Children[0].ItemID.ValueInt64() != 1 || !got.Children[0].Present.ValueBool() {
		t.Fatalf("prefix checkpoint state=%#v; first child identity was not retained", got)
	}
	if got.Children[1].Present.ValueBool() || (!got.Children[1].SourceUUID.IsUnknown() && !got.Children[1].SourceUUID.IsNull()) || (!got.Children[1].ItemID.IsUnknown() && !got.Children[1].ItemID.IsNull()) {
		t.Fatalf("right child unexpectedly reported as applied after failed first-child barrier: %#v", got.Children[1])
	}
	for _, observed := range got.ObservedChildren {
		if observed.StableKey.ValueString() == "right" {
			t.Fatalf("observed roster reported an untouched right child after failed first-child barrier: %#v", got.ObservedChildren)
		}
	}
	if lunaLifecycleMutationCount(script, "CreateInputInGroup") != 1 || lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 2 || lunaLifecycleMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("Create crossed failed barrier: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphUpdateReplacementRetainsChildIdentityAfterInterruption(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	stateModel.Children[0].SourceUUID = types.StringValue("left-old")
	stateModel.Children[0].Present = types.BoolValue(false)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.Children[0].Present = types.BoolValue(true)
	parent := lunaLifecyclePlacement()
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	right := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 960, 1080, []any{right})
	emptyInputs := map[string]any{"inputs": []any{}}
	left := lunaGraphTestChildResult(3, "left-new", "Party Light Left", lunaLifecyclePlacement(), 960, 1080)
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetInputList":           {lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs), lunaLifecycleDirect(emptyInputs)},
			"GetVersion":             {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateInputInGroup")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
	response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.Children[0].SourceUUID.ValueString() != "left-new" || got.Children[0].ItemID.ValueInt64() != 3 || !got.Children[0].Present.ValueBool() {
		t.Fatalf("replacement checkpoint state=%#v; authoritative child binding was not retained", got.Children[0])
	}
	if lunaLifecycleMutationCount(script, "CreateInputInGroup") != 1 || lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 0 || lunaLifecycleMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("Update continued after replacement interruption: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphUpdateSetterInterruptionRetainsPlacementCheckpoint(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	stateModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
	stateModel.Children[1].Placement.Transform.X = types.Float64Value(900)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
	planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
	parent := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(900)
	rightPlacement.Index = types.Int64Value(1)
	group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{
		lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetInputList":            {lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
			"GetVersion":              {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":        {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"SetSceneItemTransform":   {lunaLifecycleDirect(map[string]any{})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "SetSceneItemTransform")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
	response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "interruption")
	if calls := lunaLifecycleMutationCount(script, "SetSceneItemTransform"); calls != 1 {
		t.Fatalf("setter calls=%d want one authoritative interrupted setter", calls)
	}
	targets := lunaGraphTestSetterTargets(script)
	if len(targets) != 1 || targets[0].item != 2 || targets[0].x != 960 {
		t.Fatalf("setter targets=%#v want right item 2 x960", targets)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if len(got.ObservedChildren) != 2 || got.ObservedChildren[1].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("state=%#v; authoritative setter placement was not checkpointed", got)
	}
	if lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemEnabled") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemLocked") != 0 || lunaLifecycleMutationCount(script, "SetSceneItemIndex") != 0 {
		t.Fatalf("Update continued after setter interruption: calls=%#v", script.calls)
	}
}

func TestLunaGroupGraphUpdateFrameBarrierFailureRetainsSetterCheckpoint(t *testing.T) {
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	stateModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
	stateModel.Children[1].Placement.Transform.X = types.Float64Value(900)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.Children[1].Placement = lunaGraphTestClonePlacement(stateModel.Children[1].Placement)
	planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
	parent := lunaLifecyclePlacement()
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(900)
	rightPlacement.Index = types.Int64Value(1)
	group := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, parent, 1920, 1080, []any{
		lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":            {lunaLifecycleDirect(lunaLifecycleSceneList()), lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetInputList":            {lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), lunaLifecycleDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(leftAsset)), lunaLifecycleDirect(lunaLifecycleImageSettings(rightAsset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings()), lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
			"GetVersion":              {lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion()), lunaLifecycleDirect(lunaGraphTestBroadVersion())},
			"GetVideoSettings":        {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":     {{err: errors.New("synthetic post-setter frame barrier failure")}},
			"SetSceneItemTransform":   {lunaLifecycleDirect(map[string]any{})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
	response := resource.UpdateResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "parent-scene frame barrier failed")
	if lunaLifecycleMutationCount(script, "SetSceneItemTransform") != 1 || lunaLifecycleMutationCount(script, "GetSourceScreenshot") != 1 || lunaLifecycleMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("frame barrier failure crossed its boundary: calls=%#v", script.calls)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if len(got.ObservedChildren) != 2 || got.ObservedChildren[1].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("state=%#v; successful setter checkpoint was lost", got)
	}
	for _, target := range lunaGraphTestSetterTargets(script) {
		if target.item == 7 {
			t.Fatalf("parent setter was sent after failed child frame barrier: %#v", target)
		}
	}
}

type lunaGraphDeleteResumeRequester struct {
	calls             []lunaLifecycleCall
	groupInitial      map[string]any
	groupStale        map[string]any
	groupSettled      map[string]any
	groupEmpty        map[string]any
	rightAsset        string
	failResumeBarrier bool
	settled           bool
	removals          int
}

func (r *lunaGraphDeleteResumeRequester) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	r.calls = append(r.calls, lunaLifecycleCall{typ: typ, data: data})
	if typ == "CallVendorRequest" {
		requestType := stringValue(data["requestType"])
		requestData := mapValue(data["requestData"])
		switch requestType {
		case "GetCapabilities":
			return lunaLifecycleBridgeEnvelope("GetCapabilities", "", lunaLifecycleBridgeReply{outcome: "completed", postconditionVerified: true, result: lunaLifecycleCapabilities([]string{"GetGroup", "RemoveGroupChildPlacement"})}), nil
		case "GetGroup":
			group := r.groupInitial
			if r.removals == 1 {
				group = r.groupStale
				if r.settled {
					group = r.groupSettled
				}
			} else if r.removals >= 2 {
				group = r.groupEmpty
			}
			return lunaLifecycleBridgeEnvelope("GetGroup", "", lunaLifecycleBridgeReply{outcome: "completed", postconditionVerified: true, result: group}), nil
		case "RemoveGroupChildPlacement":
			r.removals++
			return lunaLifecycleBridgeEnvelope(requestType, stringValue(requestData["clientOperationId"]), lunaLifecycleBridgeReply{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: map[string]any{"sourceStillExists": false}}), nil
		default:
			return nil, errors.New("unexpected bridge request " + requestType)
		}
	}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}, nil
	case "GetSceneList":
		return lunaLifecycleSceneList(), nil
	case "GetGroupList":
		return map[string]any{"groups": []any{}}, nil
	case "GetSceneItemList", "GetGroupSceneItemList":
		return map[string]any{"sceneItems": []any{}}, nil
	case "GetSourceFilterList":
		return map[string]any{"filters": []any{}}, nil
	case "GetInputList":
		if r.removals == 1 {
			return map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}}, nil
		}
		return map[string]any{"inputs": []any{}}, nil
	case "GetInputSettings":
		return lunaLifecycleImageSettings(r.rightAsset), nil
	case "GetInputDefaultSettings":
		return lunaLifecycleDefaultImageSettings(), nil
	case "GetVersion":
		return lunaGraphTestBroadVersion(), nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}, nil
	case "GetSourceScreenshot":
		if r.failResumeBarrier && r.removals == 1 {
			return nil, errors.New("synthetic resumed Delete frame barrier failure")
		}
		if r.removals == 1 {
			r.settled = true
		}
		return lunaGraphTestScreenshot(), nil
	case "RemoveSceneItem":
		return map[string]any{}, nil
	default:
		return nil, errors.New("unexpected direct request " + typ)
	}
}

func lunaGraphDeleteResumeRelevantOperations(calls []lunaLifecycleCall) []string {
	var operations []string
	for _, call := range calls {
		if call.typ == "GetSourceScreenshot" {
			operations = append(operations, call.typ)
			continue
		}
		if call.typ != "CallVendorRequest" {
			continue
		}
		switch lunaLifecycleBridgeRequestType(call) {
		case "GetGroup", "RemoveGroupChildPlacement":
			operations = append(operations, lunaLifecycleBridgeRequestType(call))
		}
	}
	return operations
}

func lunaGraphDeleteResumeFixture(t *testing.T, failResumeBarrier bool) (*groupGraphResource, *lunaGraphDeleteResumeRequester, resource.DeleteResponse) {
	t.Helper()
	ctx := context.Background()
	leftAsset := lunaGraphTestAsset(t, "left.png")
	rightAsset := lunaGraphTestAsset(t, "right.png")
	stateModel := lunaGraphTestCompleteModel(t, leftAsset, rightAsset)
	stateModel.ParentPlacement = lunaGraphTestClonePlacement(stateModel.ParentPlacement)
	stateModel.ParentPlacement.Transform.X = types.Float64Value(0)
	groupInitialParent := lunaLifecyclePlacement()
	groupInitialParent.Transform.X = types.Float64Value(0)
	leftPlacement := lunaLifecyclePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := lunaLifecyclePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	left := lunaGraphTestChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080)
	right := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	groupInitial := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, groupInitialParent, 1920, 1080, []any{left, right})
	groupStaleParent := lunaLifecyclePlacement()
	groupStaleParent.Transform.X = types.Float64Value(0)
	groupStale := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, groupStaleParent, 1920, 1080, []any{right})
	groupSettledParent := lunaLifecyclePlacement()
	groupSettledParent.Transform.X = types.Float64Value(960)
	settledRightPlacement := lunaLifecyclePlacement()
	settledRightPlacement.Transform.X = types.Float64Value(0)
	settledRightPlacement.Index = types.Int64Value(1)
	settledRight := lunaGraphTestChildResult(2, "right-uuid", "Party Light Right", settledRightPlacement, 960, 1080)
	groupSettled := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, groupSettledParent, 960, 1080, []any{settledRight})
	groupEmpty := lunaGraphTestGroupResult(7, "group-stable", lunaLifecycleGroupName, groupSettledParent, 0, 0, []any{})
	requester := &lunaGraphDeleteResumeRequester{groupInitial: groupInitial, groupStale: groupStale, groupSettled: groupSettled, groupEmpty: groupEmpty, rightAsset: rightAsset, failResumeBarrier: failResumeBarrier}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: requester}}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:RemoveGroupChildPlacement")
	first := resource.DeleteResponse{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: lunaGraphTestStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &first)
	lunaLifecycleAssertDiagnostic(t, first.Diagnostics, "interruption")
	var partial groupGraphModel
	lunaLifecycleRequireState(t, ctx, first.State, &partial)
	if partial.Children[0].Present.ValueBool() || partial.Children[0].SourceUUID.ValueString() != "left-uuid" || partial.Children[0].ItemID.ValueInt64() != 1 || partial.Children[1].Present.IsNull() || !partial.Children[1].Present.ValueBool() {
		t.Fatalf("partial Delete state=%#v; authoritative absent prefix was not retained", partial.Children)
	}
	if requester.removals != 1 {
		t.Fatalf("first Delete removals=%d want one", requester.removals)
	}
	if got := lunaGraphDeleteResumeMutationCount(requester.calls, "GetSourceScreenshot"); got != 0 {
		t.Fatalf("Delete crossed interruption into a frame barrier (%d calls)", got)
	}
	return resourceUnderTest, requester, first
}

func TestLunaGroupGraphDeleteResumesAfterAuthoritativeChildInterruption(t *testing.T) {
	ctx := context.Background()
	resourceUnderTest, requester, first := lunaGraphDeleteResumeFixture(t, false)

	read := resource.ReadResponse{State: first.State}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: first.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("partial Delete Read diagnostics: %v", read.Diagnostics)
	}
	readEnd := len(requester.calls)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	second := resource.DeleteResponse{State: read.State}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: read.State}, &second)
	if second.Diagnostics.HasError() {
		t.Fatalf("Delete resumption failed after skipping proven-absent prefix: %v", second.Diagnostics)
	}
	if requester.removals != 2 {
		t.Fatalf("resumed Delete removals=%d want two", requester.removals)
	}
	var removed []string
	var expectedX []float64
	for _, call := range requester.calls {
		if lunaLifecycleBridgeRequestType(call) != "RemoveGroupChildPlacement" {
			continue
		}
		requestData := mapValue(call.data["requestData"])
		child := mapValue(requestData["child"])
		removed = append(removed, stringValue(child["sourceUuid"]))
		expectedPlacement := mapValue(requestData["expectedPlacement"])
		expectedTransform := mapValue(expectedPlacement["sceneItemTransform"])
		expectedX = append(expectedX, numberOrZero(expectedTransform["positionX"]))
	}
	if len(removed) != 2 || removed[0] != "left-uuid" || removed[1] != "right-uuid" {
		t.Fatalf("resumed Delete removal order=%v want [left-uuid right-uuid]", removed)
	}
	if len(expectedX) != 2 || expectedX[1] != 0 {
		t.Fatalf("resumed Delete expectedPlacement X=%v; settled right child must be removed at local x=0", expectedX)
	}
	want := []string{"GetGroup", "GetSourceScreenshot", "GetGroup", "RemoveGroupChildPlacement", "GetSourceScreenshot", "GetGroup"}
	got := lunaGraphDeleteResumeRelevantOperations(requester.calls[readEnd:])
	if len(got) != len(want) {
		t.Fatalf("resumed Delete operations=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resumed Delete operations=%v want %v", got, want)
		}
	}
}

func TestLunaGroupGraphDeleteResumeStopsOnRequiredFrameBarrierFailure(t *testing.T) {
	ctx := context.Background()
	resourceUnderTest, requester, first := lunaGraphDeleteResumeFixture(t, true)

	read := resource.ReadResponse{State: first.State}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: first.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("partial Delete Read diagnostics: %v", read.Diagnostics)
	}
	readEnd := len(requester.calls)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	second := resource.DeleteResponse{State: read.State}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: read.State}, &second)
	lunaLifecycleAssertDiagnostic(t, second.Diagnostics, "parent-scene frame barrier failed")
	if requester.removals != 1 {
		t.Fatalf("barrier-failed resumed Delete removals=%d want one", requester.removals)
	}
	want := []string{"GetGroup", "GetSourceScreenshot"}
	got := lunaGraphDeleteResumeRelevantOperations(requester.calls[readEnd:])
	if len(got) != len(want) {
		t.Fatalf("barrier-failed resumed Delete operations=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("barrier-failed resumed Delete operations=%v want %v", got, want)
		}
	}
}

func lunaGraphDeleteResumeMutationCount(calls []lunaLifecycleCall, typ string) int {
	count := 0
	for _, call := range calls {
		if call.typ == typ || lunaLifecycleBridgeRequestType(call) == typ {
			count++
		}
	}
	return count
}
