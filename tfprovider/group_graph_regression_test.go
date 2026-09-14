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

func groupGraphFixtureAsset(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("synthetic asset"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func groupGraphFixtureChild(t *testing.T, key, name, asset string, x float64, itemID int64) groupGraphChildModel {
	t.Helper()
	placement := groupFixturePlacement()
	placement.Transform.X = types.Float64Value(x)
	placement.Index = types.Int64Value(itemID - 1)
	return groupGraphChildModel{
		StableKey: types.StringValue(key), Name: types.StringValue(name), Kind: types.StringValue("image_source"), File: types.StringValue(asset),
		Unload: types.BoolValue(false), Present: types.BoolValue(true), Placement: &placement,
		SourceUUID: types.StringUnknown(), ItemID: types.Int64Unknown(),
	}
}

func groupGraphFixtureModel(t *testing.T, children []groupGraphChildModel) groupGraphModel {
	t.Helper()
	parent := groupFixturePlacement()
	return groupGraphModel{
		ID: types.StringUnknown(), Collection: types.StringValue(groupFixtureCollection), SceneName: types.StringValue(groupFixtureSceneName),
		SceneUUID: types.StringValue(groupFixtureSceneUUID), Name: types.StringValue(groupFixtureGroupName), ParentPresent: types.BoolValue(true),
		ParentPlacement: &parent, GroupUUID: types.StringUnknown(), ParentItemID: types.Int64Unknown(), Children: children,
		ObservedComplete: types.BoolUnknown(), ObservedGroupWidth: types.Int64Unknown(), ObservedGroupHeight: types.Int64Unknown(), RepairNonce: types.StringUnknown(),
	}
}

func groupGraphFixtureClonePlacement(in *bridgePlacementModel) *bridgePlacementModel {
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

func groupGraphFixtureSchemaAttribute(t *testing.T, sc schema.Schema, key string) schema.Attribute {
	t.Helper()
	attribute, ok := sc.Attributes[key]
	if !ok {
		t.Fatalf("schema missing attribute %q", key)
	}
	return attribute
}

func TestGroupGraphSchemaPreservesOrderedRosterAndMutableBindings(t *testing.T) {
	ctx := context.Background()
	sc := groupGraphSchema()
	if sc.Version != 1 {
		t.Fatalf("schema version=%d want 1", sc.Version)
	}
	if _, ok := groupGraphFixtureSchemaAttribute(t, sc, "children").(schema.ListNestedAttribute); !ok {
		t.Fatalf("children schema is not an ordered ListNestedAttribute: %T", groupGraphFixtureSchemaAttribute(t, sc, "children"))
	}
	if _, ok := groupGraphFixtureSchemaAttribute(t, sc, "parent_item_id").(schema.Int64Attribute); !ok {
		t.Fatalf("parent_item_id schema is not Int64Attribute: %T", groupGraphFixtureSchemaAttribute(t, sc, "parent_item_id"))
	}
	if _, ok := groupGraphFixtureSchemaAttribute(t, sc, "group_uuid").(schema.StringAttribute); !ok {
		t.Fatalf("group_uuid schema is not StringAttribute: %T", groupGraphFixtureSchemaAttribute(t, sc, "group_uuid"))
	}

	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	model := groupGraphFixtureModel(t, []groupGraphChildModel{
		groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		groupGraphFixtureChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	plan := groupFixtureSchemaAndPlan(t, ctx, sc, model)
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

func groupGraphFixtureChildResult(itemID int64, inputUUID, inputName string, placement bridgePlacementModel, width, height int64) map[string]any {
	native, _ := placement.request()
	return map[string]any{"sourceName": inputName, "sourceUuid": inputUUID, "inputKind": "image_source", "sceneItemId": float64(itemID), "sourceWidth": float64(width), "sourceHeight": float64(height), "placement": native}
}

func groupGraphFixtureGroupResult(itemID int64, groupUUID, groupName string, placement bridgePlacementModel, width, height int64, children []any) map[string]any {
	native, _ := placement.request()
	return map[string]any{
		"scene":    map[string]any{"sourceName": groupFixtureSceneName, "sourceUuid": groupFixtureSceneUUID},
		"group":    map[string]any{"sourceName": groupName, "sourceUuid": groupUUID, "sceneItemId": float64(itemID), "groupWidth": float64(width), "groupHeight": float64(height), "placement": native},
		"children": children,
	}
}

func groupGraphFixtureScreenshot() map[string]any {
	return map[string]any{"imageData": "data:image/png;base64,aGVsbG8="}
}

func groupGraphFixtureRelevantOperations(script *groupFixtureScript) []string {
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

func groupGraphFixtureConfig(t *testing.T, ctx context.Context, sc schema.Schema, value groupGraphModel) tfsdk.Config {
	t.Helper()
	plan := groupFixtureSchemaAndPlan(t, ctx, sc, value)
	return tfsdk.Config{Raw: plan.Raw, Schema: plan.Schema}
}

func groupGraphFixtureEmptyState(ctx context.Context, sc schema.Schema) tfsdk.State {
	return tfsdk.State{Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil), Schema: sc}
}

func groupGraphFixtureStateWithBindings(t *testing.T, ctx context.Context, sc schema.Schema, model groupGraphModel) tfsdk.State {
	t.Helper()
	return groupFixtureSchemaAndState(t, ctx, sc, model)
}

func groupGraphFixtureCompleteModel(t *testing.T, leftAsset, rightAsset string) groupGraphModel {
	t.Helper()
	left := groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1)
	right := groupGraphFixtureChild(t, "right", "Party Light Right", rightAsset, 960, 2)
	left.SourceUUID, left.ItemID = types.StringValue("left-uuid"), types.Int64Value(1)
	right.SourceUUID, right.ItemID = types.StringValue("right-uuid"), types.Int64Value(2)
	model := groupGraphFixtureModel(t, []groupGraphChildModel{left, right})
	model.ID = types.StringValue(groupGraphID(groupFixtureCollection, groupFixtureSceneUUID, "group-stable"))
	model.GroupUUID, model.ParentItemID = types.StringValue("group-stable"), types.Int64Value(7)
	model.ObservedComplete = types.BoolValue(true)
	return model
}

func groupGraphFixtureCompleteGroup(leftAsset, rightAsset string) (map[string]any, map[string]any, map[string]any) {
	parent := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	left := groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080)
	right := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{left, right})
	return group, left, right
}

func groupGraphFixtureCompleteInputResponses(leftAsset, rightAsset string) []groupFixtureDirectReply {
	return []groupFixtureDirectReply{
		groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}),
		groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureDefaultImageSettings()),
		groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}}),
		groupFixtureDirect(groupFixtureImageSettings(rightAsset)), groupFixtureDirect(groupFixtureDefaultImageSettings()),
	}
}

type groupGraphFixtureCapabilitiesRequester struct {
	base            *groupFixtureScript
	mutate          func(map[string]any)
	mutateOnCall    int
	capabilityCalls int
}

func (r *groupGraphFixtureCapabilitiesRequester) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if typ == "CallVendorRequest" && stringValue(data["requestType"]) == "GetCapabilities" {
		r.base.calls = append(r.base.calls, groupFixtureCall{typ: typ, data: data})
		r.capabilityCalls++
		result := groupFixtureCapabilities(r.base.capabilityRequests)
		if r.mutateOnCall == 0 || r.capabilityCalls == r.mutateOnCall {
			r.mutate(result)
		}
		return groupFixtureBridgeEnvelope("GetCapabilities", "", groupFixtureBridgeReply{outcome: "completed", postconditionVerified: true, result: result}), nil
	}
	return r.base.Req(ctx, typ, data)
}

func groupGraphFixtureBroadVersion() map[string]any {
	return groupFixtureVersion("CallVendorRequest", "CreateInputInGroup", "AddSourceToGroup", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "RemoveGroupChildPlacement", "RemoveSceneItem", "SetInputSettings")
}

func TestGroupGraphCreateRunsOrderedChildrenAndFrameBarriers(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	parentPlacement := groupFixturePlacement()
	groupAfterCreate := groupGraphFixtureGroupResult(7, "group-created", groupFixtureGroupName, parentPlacement, 1920, 1080, []any{})
	left := groupGraphFixtureChildResult(1, "left-created", "Party Light Left", leftPlacement, 960, 1080)
	right := groupGraphFixtureChildResult(2, "right-created", "Party Light Right", rightPlacement, 960, 1080)
	groupAfterLeft := groupGraphFixtureGroupResult(7, "group-created", groupFixtureGroupName, parentPlacement, 1920, 1080, []any{left})
	groupAfterRight := groupGraphFixtureGroupResult(7, "group-created", groupFixtureGroupName, parentPlacement, 1920, 1080, []any{left, right})

	emptyInputs := map[string]any{"inputs": []any{}}
	inputs := map[string]any{"inputs": []any{
		map[string]any{"inputName": "Party Light Left", "inputUuid": "left-created", "inputKind": "image_source"},
		map[string]any{"inputName": "Party Light Right", "inputUuid": "right-created", "inputKind": "image_source"},
	}}
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup", "CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList())},
			"GetGroupList":            {groupFixtureDirect(map[string]any{"groups": []any{}})},
			"GetInputList":            {groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(inputs), groupFixtureDirect(inputs)},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
			"GetVersion":              {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":        {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":     {groupFixtureDirect(groupGraphFixtureScreenshot()), groupFixtureDirect(groupGraphFixtureScreenshot()), groupFixtureDirect(groupGraphFixtureScreenshot())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup":        {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: groupAfterCreate}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}, {outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: right}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterCreate}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterLeft}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterRight}},
		},
	}
	model := groupGraphFixtureModel(t, []groupGraphChildModel{
		groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		groupGraphFixtureChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ParentItemID.ValueInt64() != 7 || !got.ObservedComplete.ValueBool() || len(got.Children) != 2 {
		t.Fatalf("state=%#v; successful aggregate create did not persist complete graph", got)
	}
	if got.Children[0].SourceUUID.ValueString() != "left-created" || got.Children[1].SourceUUID.ValueString() != "right-created" || got.Children[0].ItemID.ValueInt64() != 1 || got.Children[1].ItemID.ValueInt64() != 2 {
		t.Fatalf("children=%#v; ordered child identities were not checkpointed", got.Children)
	}
	wantOperations := []string{"CreateGroup", "GetSourceScreenshot", "GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup"}
	if gotOperations := groupGraphFixtureRelevantOperations(script); len(gotOperations) != len(wantOperations) {
		t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
	} else {
		for i := range wantOperations {
			if gotOperations[i] != wantOperations[i] {
				t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
			}
		}
	}
	if groupFixtureMutationCount(script, "SetSceneItemTransform") != 0 || groupFixtureMutationCount(script, "SetSceneItemEnabled") != 0 {
		t.Fatal("initial exact graph create emitted redundant placement setters")
	}
	for _, call := range script.calls {
		if call.typ != "GetSourceScreenshot" {
			continue
		}
		if stringValue(call.data["sourceUuid"]) != groupFixtureSceneUUID || stringValue(call.data["imageFormat"]) != "png" || numberOrZero(call.data["imageWidth"]) != 64 || numberOrZero(call.data["imageHeight"]) != 36 {
			t.Fatalf("frame barrier request=%#v; want parent scene png 64x36", call.data)
		}
	}
}

func TestGroupGraphValidateConfigRejectsUnsupportedPresenceAndRoster(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	base := groupGraphFixtureModel(t, []groupGraphChildModel{
		groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		groupGraphFixtureChild(t, "right", "Party Light Right", rightAsset, 960, 2),
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
			resourceUnderTest.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: groupGraphFixtureConfig(t, ctx, groupGraphSchema(), model)}, &response)
			groupFixtureAssertDiagnostic(t, response.Diagnostics, tc.want)
		})
	}
}

func TestGroupGraphModifyPlanKeepsParentItemTurnoverInPlace(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	planModel := stateModel
	planModel.ParentItemID = types.Int64Value(99)
	sc := groupGraphSchema()
	resourceUnderTest := &groupGraphResource{}
	plan := groupFixtureSchemaAndPlan(t, ctx, sc, planModel)
	response := resource.ModifyPlanResponse{Plan: plan}
	resourceUnderTest.ModifyPlan(ctx, resource.ModifyPlanRequest{State: groupGraphFixtureStateWithBindings(t, ctx, sc, stateModel), Plan: plan}, &response)
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
			candidatePlan := groupFixtureSchemaAndPlan(t, ctx, sc, candidate)
			candidateResponse := resource.ModifyPlanResponse{Plan: candidatePlan}
			resourceUnderTest.ModifyPlan(ctx, resource.ModifyPlanRequest{State: groupGraphFixtureStateWithBindings(t, ctx, sc, stateModel), Plan: candidatePlan}, &candidateResponse)
			groupFixtureAssertDiagnostic(t, candidateResponse.Diagnostics, tc.want)
		})
	}
}

func TestGroupGraphImportSeedsExactOrderedBindings(t *testing.T) {
	ctx := context.Background()
	sc := groupGraphSchema()
	response := resource.ImportStateResponse{State: groupGraphFixtureEmptyState(ctx, sc)}
	resourceUnderTest := &groupGraphResource{}
	resourceUnderTest.ImportState(ctx, resource.ImportStateRequest{ID: groupFixtureCollection + "|" + groupFixtureSceneUUID + "|group-imported|7|left=left-uuid:1,right=right-uuid:2"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.ID.ValueString() != groupGraphID(groupFixtureCollection, groupFixtureSceneUUID, "group-imported") || got.ParentItemID.ValueInt64() != 7 || len(got.Children) != 2 {
		t.Fatalf("imported state=%#v", got)
	}
	if got.Children[0].StableKey.ValueString() != "left" || got.Children[0].SourceUUID.ValueString() != "left-uuid" || got.Children[0].ItemID.ValueInt64() != 1 || got.Children[1].StableKey.ValueString() != "right" || got.Children[1].SourceUUID.ValueString() != "right-uuid" || got.Children[1].ItemID.ValueInt64() != 2 {
		t.Fatalf("imported children=%#v", got.Children)
	}

	for _, id := range []string{
		"bad",
		groupFixtureCollection + "|" + groupFixtureSceneUUID + "|group|0|left=left-uuid:1",
		groupFixtureCollection + "|" + groupFixtureSceneUUID + "|group|7|left=left-uuid:1,left=other:2",
	} {
		invalid := resource.ImportStateResponse{State: groupGraphFixtureEmptyState(ctx, sc)}
		resourceUnderTest.ImportState(ctx, resource.ImportStateRequest{ID: id}, &invalid)
		if !invalid.Diagnostics.HasError() {
			t.Fatalf("invalid import %q was accepted", id)
		}
	}
}

func TestGroupGraphReadRecordsCompleteRosterAndParent(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	group, _, _ := groupGraphFixtureCompleteGroup(leftAsset, rightAsset)
	script := &groupFixtureScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetVersion":              {groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetInputList":            groupGraphFixtureCompleteInputResponses(leftAsset, rightAsset)[0:1],
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	// The list responses are interleaved with settings/default responses by
	// inputByUUID; spell that sequence out so a wrong read order fails loudly.
	script.direct["GetInputList"] = []groupFixtureDirectReply{
		groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}),
		groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}}),
	}
	response := resource.ReadResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected Read diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if !got.ObservedComplete.ValueBool() || !got.ParentPresent.ValueBool() || got.ObservedGroupWidth.ValueInt64() != 1920 || got.ObservedGroupHeight.ValueInt64() != 1080 || len(got.ObservedChildren) != 2 {
		t.Fatalf("read state=%#v", got)
	}
	if got.Children[0].File.ValueString() != leftAsset || got.Children[1].File.ValueString() != rightAsset || got.Children[0].SourceUUID.ValueString() != "left-uuid" || got.Children[1].SourceUUID.ValueString() != "right-uuid" {
		t.Fatalf("read children=%#v", got.Children)
	}
	if groupFixtureMutationCount(script, "SetSceneItemTransform") != 0 {
		t.Fatal("Read sent a placement mutation")
	}
}

func TestGroupGraphReadUnknownOrMalformedPreservesState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		reply groupFixtureBridgeReply
	}{
		{name: "uncertain", reply: groupFixtureBridgeReply{outcome: "uncertain", code: "transport_unknown", message: "group read uncertain", mutationAttempted: true, postconditionVerified: false, result: map[string]any{}}},
		{name: "malformed", reply: groupFixtureBridgeReply{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: map[string]any{"scene": map[string]any{"sourceName": groupFixtureSceneName, "sourceUuid": groupFixtureSceneUUID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leftAsset := groupGraphFixtureAsset(t, "left.png")
			rightAsset := groupGraphFixtureAsset(t, "right.png")
			stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
			script := &groupFixtureScript{
				capabilityRequests: []string{"GetGroup"},
				direct: map[string][]groupFixtureDirectReply{
					"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
					"GetVersion":             {groupFixtureDirect(groupGraphFixtureBroadVersion())},
				},
				bridge: map[string][]groupFixtureBridgeReply{"GetGroup": {tc.reply}},
			}
			response := resource.ReadResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
			resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
			resourceUnderTest.Read(ctx, resource.ReadRequest{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("unknown/malformed group read was reported successful")
			}
			if response.State.Raw.IsNull() {
				t.Fatal("unknown/malformed group read removed state")
			}
			if calls := groupFixtureMutationCount(script, "GetGroup"); calls != 1 {
				t.Fatalf("GetGroup calls=%d want exactly one", calls)
			}
			var got groupGraphModel
			groupFixtureRequireState(t, ctx, response.State, &got)
			if got.GroupUUID.ValueString() != stateModel.GroupUUID.ValueString() || got.ParentItemID.ValueInt64() != stateModel.ParentItemID.ValueInt64() {
				t.Fatalf("identity changed after failed read: %#v", got)
			}
		})
	}
}

func TestGroupGraphUpdateConvergedGraphSendsNoWrites(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	model := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	group, _, _ := groupGraphFixtureCompleteGroup(leftAsset, rightAsset)
	script := &groupFixtureScript{
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList())},
			"GetInputList":            {groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
		},
		bridge: map[string][]groupFixtureBridgeReply{"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}}},
	}
	sc := groupGraphSchema()
	plan := groupFixtureSchemaAndPlan(t, ctx, sc, model)
	response := resource.UpdateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupFixtureSchemaAndState(t, ctx, sc, model)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected converged Update diagnostics: %v calls=%#v", response.Diagnostics, script.calls)
	}
	for _, typ := range []string{"CreateInputInGroup", "AddSourceToGroup", "SetInputSettings", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "CreateSceneItem"} {
		if calls := groupFixtureMutationCount(script, typ); calls != 0 {
			t.Fatalf("converged Update sent %s (%d calls)", typ, calls)
		}
	}
}

func TestGroupGraphDeleteRefusesChildFilterBeforeMutation(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	model := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	group, _, _ := groupGraphFixtureCompleteGroup(leftAsset, rightAsset)
	script := &groupFixtureScript{
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSourceFilterList":    {groupFixtureDirect(groupFixtureAttachedFilterList())},
		},
		bridge: map[string][]groupFixtureBridgeReply{"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}}},
	}
	response := resource.DeleteResponse{}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), model)}, &response)
	groupFixtureAssertDiagnostic(t, response.Diagnostics, "attached filters")
	if groupFixtureMutationCount(script, "RemoveGroupChildPlacement") != 0 || groupFixtureMutationCount(script, "RemoveSceneItem") != 0 {
		t.Fatal("group graph Delete mutated despite attached child filter")
	}
}

func groupGraphFixtureSetterTargets(script *groupFixtureScript) []struct {
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

func TestGroupGraphUpdateRepairsMissingLeftWithOrderedCompensation(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	// Update's stale-write guard compares the first native observation with the
	// last accepted state. Model the preceding Read accurately: losing the left
	// child rebased the parent to x=960 and the surviving right child to x=0.
	stateModel.ParentPlacement = groupGraphFixtureClonePlacement(stateModel.ParentPlacement)
	stateModel.ParentPlacement.Transform.X = types.Float64Value(960)
	stateModel.Children[0].SourceUUID = types.StringValue("left-old")
	stateModel.Children[0].Present = types.BoolValue(false)
	stateModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
	stateModel.Children[1].Placement.Transform.X = types.Float64Value(0)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.ParentPlacement = groupGraphFixtureClonePlacement(stateModel.ParentPlacement)
	planModel.ParentPlacement.Transform.X = types.Float64Value(0)
	for i := range planModel.Children {
		planModel.Children[i].Placement = groupGraphFixtureClonePlacement(stateModel.Children[i].Placement)
	}
	planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
	planModel.Children[0].Present = types.BoolValue(true)

	parentRebased := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	leftPlacement.Index = types.Int64Value(0)
	rightRebased := groupFixturePlacement()
	rightRebased.Transform.X = types.Float64Value(0)
	rightRebased.Index = types.Int64Value(1)
	rightRestored := groupFixturePlacement()
	rightRestored.Transform.X = types.Float64Value(960)
	rightRestored.Index = types.Int64Value(1)
	parentRestored := groupFixturePlacement()
	parentRestored.Transform.X = types.Float64Value(0)
	left := groupGraphFixtureChildResult(3, "left-new", "Party Light Left", leftPlacement, 960, 1080)
	rightAtZero := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightRebased, 960, 1080)
	rightAt960 := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightRestored, 960, 1080)
	groupBefore := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parentRebased, 960, 1080, []any{left, rightAtZero})
	groupAfterChild := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parentRebased, 1920, 1080, []any{left, rightAt960})
	groupFinal := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parentRestored, 1920, 1080, []any{left, rightAt960})
	inputs := map[string]any{"inputs": []any{
		map[string]any{"inputName": "Party Light Left", "inputUuid": "left-new", "inputKind": "image_source"},
		map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"},
	}}
	emptyInputs := map[string]any{"inputs": []any{}}
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateInputInGroup", "GetGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList())},
			"GetInputList":            {groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(inputs), groupFixtureDirect(inputs)},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
			"GetVersion":              {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":        {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":     {groupFixtureDirect(groupGraphFixtureScreenshot()), groupFixtureDirect(groupGraphFixtureScreenshot()), groupFixtureDirect(groupGraphFixtureScreenshot())},
			"SetSceneItemTransform":   {groupFixtureDirect(map[string]any{}), groupFixtureDirect(map[string]any{})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parentRebased, 960, 1080, []any{rightAtZero})}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupBefore}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterChild}, {outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupFinal}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}},
		},
	}
	// The initial observation contains only the surviving right child at the
	// rebased local origin. The replacement frame then contains both children;
	// later frames prove child-first and parent-last compensation.
	// The first GetGroup response above is the pre-update read, while the
	// following three are the membership, child-compensation and final barriers.
	sc := groupGraphSchema()
	plan := groupFixtureSchemaAndPlan(t, ctx, sc, planModel)
	response := resource.UpdateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, planModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupFixtureSchemaAndState(t, ctx, sc, stateModel)}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected ARR-57 repair diagnostics: %v calls=%#v", response.Diagnostics, script.calls)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if !got.ObservedComplete.ValueBool() || got.ParentItemID.ValueInt64() != 7 || got.Children[0].SourceUUID.ValueString() != "left-new" || got.Children[0].ItemID.ValueInt64() != 3 || got.Children[1].SourceUUID.ValueString() != "right-uuid" || got.Children[1].ItemID.ValueInt64() != 2 {
		t.Fatalf("repaired state=%#v; survivor/replacement bindings are wrong", got)
	}
	if got.ParentPlacement.Transform.X.ValueFloat64() != 0 || got.Children[0].Placement.Transform.X.ValueFloat64() != 0 || got.Children[1].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("repaired placements parent=%v left=%v right=%v", got.ParentPlacement.Transform.X.ValueFloat64(), got.Children[0].Placement.Transform.X.ValueFloat64(), got.Children[1].Placement.Transform.X.ValueFloat64())
	}
	if calls := groupFixtureMutationCount(script, "CreateInputInGroup"); calls != 1 {
		t.Fatalf("replacement child creates=%d want one", calls)
	}
	if calls := groupFixtureMutationCount(script, "SetSceneItemTransform"); calls != 2 {
		t.Fatalf("compensation setters=%d want right+parent only", calls)
	}
	setters := groupGraphFixtureSetterTargets(script)
	if len(setters) != 2 || setters[0].item != 2 || setters[0].x != 960 || setters[1].item != 7 || setters[1].x != 0 {
		t.Fatalf("setter targets=%#v want right item 2 x960 then parent item 7 x0", setters)
	}
	wantOperations := []string{"GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup", "SetSceneItemTransform", "GetSourceScreenshot", "GetGroup", "SetSceneItemTransform", "GetSourceScreenshot", "GetGroup"}
	gotOperations := groupGraphFixtureRelevantOperations(script)
	if len(gotOperations) != len(wantOperations) {
		t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
	}
	for i := range wantOperations {
		if gotOperations[i] != wantOperations[i] {
			t.Fatalf("relevant operations=%v want %v", gotOperations, wantOperations)
		}
	}
}

func TestGroupGraphUpdateRefusesActiveOrUnknownOutputsBeforeMembership(t *testing.T) {
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
			leftAsset := groupGraphFixtureAsset(t, "left.png")
			rightAsset := groupGraphFixtureAsset(t, "right.png")
			stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
			stateModel.Children[0].SourceUUID = types.StringValue("left-old")
			stateModel.Children[0].Present = types.BoolValue(false)
			planModel := stateModel
			planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
			planModel.Children[0].Present = types.BoolValue(true)

			parent := groupFixturePlacement()
			rightPlacement := groupFixturePlacement()
			rightPlacement.Transform.X = types.Float64Value(960)
			rightPlacement.Index = types.Int64Value(1)
			right := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
			group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 960, 1080, []any{right})
			emptyInputs := map[string]any{"inputs": []any{}}
			script := &groupFixtureScript{
				capabilityRequests: []string{"CreateInputInGroup"},
				direct: map[string][]groupFixtureDirectReply{
					"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
					"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
					"GetInputList":           {groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs)},
					"GetVersion":             {groupFixtureDirect(groupGraphFixtureBroadVersion())},
				},
				bridge: map[string][]groupFixtureBridgeReply{
					"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
				},
			}
			requester := &groupGraphFixtureCapabilitiesRequester{base: script, mutate: func(result map[string]any) { result[tc.key] = tc.value }}
			plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
			response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
			resourceUnderTest := &groupGraphResource{client: &client{testRequester: requester}}
			resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

			groupFixtureAssertDiagnostic(t, response.Diagnostics, "bridge capability "+tc.key+" is active or unknown")
			if groupFixtureMutationCount(script, "CreateInputInGroup") != 0 || groupFixtureMutationCount(script, "AddSourceToGroup") != 0 || groupFixtureMutationCount(script, "SetSceneItemTransform") != 0 {
				t.Fatalf("output guard allowed membership/placement mutation: calls=%#v", script.calls)
			}
			if groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 {
				t.Fatal("output guard reached a frame barrier")
			}
		})
	}
}

func TestGroupGraphUpdateRefusesActiveOrUnknownOutputsBeforeSetterPhases(t *testing.T) {
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
				leftAsset := groupGraphFixtureAsset(t, "left.png")
				rightAsset := groupGraphFixtureAsset(t, "right.png")
				stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
				planModel := stateModel
				planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)

				parent := groupFixturePlacement()
				leftPlacement := groupFixturePlacement()
				leftPlacement.Transform.X = types.Float64Value(0)
				rightPlacement := groupFixturePlacement()
				rightPlacement.Transform.X = types.Float64Value(960)
				rightPlacement.Index = types.Int64Value(1)
				if phase == "child setter" {
					stateModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
					stateModel.Children[1].Placement.Transform.X = types.Float64Value(900)
					planModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
					planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
					rightPlacement.Transform.X = types.Float64Value(900)
				}
				if phase == "parent setter" {
					stateModel.ParentPlacement = groupGraphFixtureClonePlacement(stateModel.ParentPlacement)
					stateModel.ParentPlacement.Transform.X = types.Float64Value(900)
					planModel.ParentPlacement = groupGraphFixtureClonePlacement(stateModel.ParentPlacement)
					planModel.ParentPlacement.Transform.X = types.Float64Value(960)
					parent.Transform.X = types.Float64Value(900)
				}
				group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{
					groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
					groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
				})
				script := &groupFixtureScript{
					capabilityRequests: []string{"GetGroup"},
					direct: map[string][]groupFixtureDirectReply{
						"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
						"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList())},
						"GetInputList":            {groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
						"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
						"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
						"GetVersion":              {groupFixtureDirect(groupGraphFixtureBroadVersion())},
					},
					bridge: map[string][]groupFixtureBridgeReply{
						"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
					},
				}
				requester := &groupGraphFixtureCapabilitiesRequester{
					base: script, mutateOnCall: 1,
					mutate: func(result map[string]any) { result[output.key] = output.value },
				}
				plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
				response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
				resourceUnderTest := &groupGraphResource{client: &client{testRequester: requester}}
				resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

				groupFixtureAssertDiagnostic(t, response.Diagnostics, "bridge capability "+output.key+" is active or unknown")
				if requester.capabilityCalls != 1 {
					t.Fatalf("%s guard calls=%d want one", output.key, requester.capabilityCalls)
				}
				for _, typ := range []string{"SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "SetInputSettings", "GetSourceScreenshot"} {
					if calls := groupFixtureMutationCount(script, typ); calls != 0 {
						t.Fatalf("%s guard allowed %s (%d calls): %#v", output.key, typ, calls, script.calls)
					}
				}
			})
		}
	}
}

func TestGroupGraphUpdateRefusesWrongUUIDAndUnmanagedExtraBeforeMembership(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	parent := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	left := groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080)
	right := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	wrongLeft := groupGraphFixtureChildResult(1, "left-other", "Party Light Left", leftPlacement, 960, 1080)
	extra := groupGraphFixtureChildResult(3, "unmanaged-uuid", "Unmanaged Child", groupFixturePlacement(), 960, 1080)
	for _, tc := range []struct {
		name  string
		group map[string]any
		want  string
	}{
		{name: "same-name wrong UUID", group: groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{wrongLeft, right}), want: "rebound"},
		{name: "unmanaged extra child", group: groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{left, right, extra}), want: "unexpected unmanaged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &groupFixtureScript{
				direct: map[string][]groupFixtureDirectReply{
					"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
					"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
				},
				bridge: map[string][]groupFixtureBridgeReply{
					"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: tc.group}},
				},
			}
			plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), stateModel)
			response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
			resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
			resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

			groupFixtureAssertDiagnostic(t, response.Diagnostics, tc.want)
			for _, typ := range []string{"CreateInputInGroup", "AddSourceToGroup", "SetSceneItemTransform", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemIndex"} {
				if calls := groupFixtureMutationCount(script, typ); calls != 0 {
					t.Fatalf("%s mutated after pre-membership refusal (%d calls)", typ, calls)
				}
			}
		})
	}
}

func TestGroupGraphCreateRefusesGroupSourceFrameBarrier(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	model := groupGraphFixtureModel(t, []groupGraphChildModel{groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1)})
	parent := groupFixturePlacement()
	group := groupGraphFixtureGroupResult(7, groupFixtureSceneUUID, groupFixtureGroupName, parent, 1920, 1080, []any{})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList())},
			"GetGroupList":           {groupFixtureDirect(map[string]any{"groups": []any{}})},
			"GetInputList":           {groupFixtureDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "frame barrier refuses group-source screenshot")
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != groupFixtureSceneUUID || got.ParentItemID.ValueInt64() != 7 || got.ID.ValueString() != groupGraphID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureSceneUUID) {
		t.Fatalf("checkpointed state=%#v; CreateGroup identity was not retained", got)
	}
	if groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 || groupFixtureMutationCount(script, "CreateInputInGroup") != 0 {
		t.Fatalf("group-source barrier allowed later work: calls=%#v", script.calls)
	}
}

func TestGroupGraphUpdateRefusesMissingZeroAnchorBeforeMutation(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	model := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	model.Children[0].Placement = groupGraphFixtureClonePlacement(model.Children[0].Placement)
	model.Children[0].Placement.Transform.X = types.Float64Value(100)
	model.Children[1].Placement = groupGraphFixtureClonePlacement(model.Children[1].Placement)
	model.Children[1].Placement.Transform.X = types.Float64Value(1060)
	parent := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(100)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(1060)
	rightPlacement.Index = types.Int64Value(1)
	group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{
		groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &groupFixtureScript{
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), model)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "managed transformed minimum")
	if groupFixtureMutationCount(script, "GetCapabilities") != 0 || groupFixtureMutationCount(script, "SetSceneItemTransform") != 0 || groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 {
		t.Fatalf("zero-anchor refusal allowed native work: calls=%#v", script.calls)
	}
}

func TestGroupGraphUpdateRefusesConcurrentPlacementDriftBeforeMutation(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	parent := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(900)
	rightPlacement.Index = types.Int64Value(1)
	group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{
		groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &groupFixtureScript{
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), stateModel)
	response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "changed concurrently after refresh")
	if groupFixtureMutationCount(script, "GetCapabilities") != 0 || groupFixtureMutationCount(script, "SetSceneItemTransform") != 0 || groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 {
		t.Fatalf("concurrent-drift refusal allowed native work: calls=%#v", script.calls)
	}
}

func TestGroupGraphCreateRetainsGroupIdentityAfterAuthoritativeInterruption(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	model := groupGraphFixtureModel(t, []groupGraphChildModel{groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1)})
	parent := groupFixturePlacement()
	group := groupGraphFixtureGroupResult(7, "group-created", groupFixtureGroupName, parent, 1920, 1080, []any{})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetGroupList":           {groupFixtureDirect(map[string]any{"groups": []any{}})},
			"GetInputList":           {groupFixtureDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateGroup")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-created" || got.ParentItemID.ValueInt64() != 7 || got.ID.ValueString() != groupGraphID(groupFixtureCollection, groupFixtureSceneUUID, "group-created") {
		t.Fatalf("checkpointed state=%#v; authoritative CreateGroup identity was not retained", got)
	}
	if got.Children[0].Present.ValueBool() || !got.Children[0].SourceUUID.IsUnknown() || !got.Children[0].ItemID.IsUnknown() || len(got.ObservedChildren) != 0 {
		t.Fatalf("uncreated child was reported as applied after CreateGroup interruption: %#v", got)
	}
	if groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 || groupFixtureMutationCount(script, "CreateInputInGroup") != 0 || groupFixtureMutationCount(script, "GetGroup") != 0 {
		t.Fatalf("Create continued after authoritative interruption: calls=%#v", script.calls)
	}
}

func TestGroupGraphCreateBarrierFailureAfterFirstChildRetainsPrefix(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	model := groupGraphFixtureModel(t, []groupGraphChildModel{
		groupGraphFixtureChild(t, "left", "Party Light Left", leftAsset, 0, 1),
		groupGraphFixtureChild(t, "right", "Party Light Right", rightAsset, 960, 2),
	})
	parent := groupFixturePlacement()
	groupAfterCreate := groupGraphFixtureGroupResult(7, "group-created", groupFixtureGroupName, parent, 1920, 1080, []any{})
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	left := groupGraphFixtureChildResult(1, "left-created", "Party Light Left", leftPlacement, 960, 1080)
	emptyInputs := map[string]any{"inputs": []any{}}
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup", "CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList())},
			"GetGroupList":           {groupFixtureDirect(map[string]any{"groups": []any{}})},
			"GetInputList":           {groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs)},
			"GetVersion":             {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}), groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":    {groupFixtureDirect(groupGraphFixtureScreenshot()), {err: errors.New("synthetic frame barrier failure")}},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup":        {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: groupAfterCreate}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: groupAfterCreate}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupGraphSchema(), model)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "parent-scene frame barrier failed")
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
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
	if groupFixtureMutationCount(script, "CreateInputInGroup") != 1 || groupFixtureMutationCount(script, "GetSourceScreenshot") != 2 || groupFixtureMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("Create crossed failed barrier: calls=%#v", script.calls)
	}
}

func TestGroupGraphUpdateReplacementRetainsChildIdentityAfterInterruption(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	stateModel.Children[0].SourceUUID = types.StringValue("left-old")
	stateModel.Children[0].Present = types.BoolValue(false)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.Children[0].Present = types.BoolValue(true)
	parent := groupFixturePlacement()
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	right := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 960, 1080, []any{right})
	emptyInputs := map[string]any{"inputs": []any{}}
	left := groupGraphFixtureChildResult(3, "left-new", "Party Light Left", groupFixturePlacement(), 960, 1080)
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetInputList":           {groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs), groupFixtureDirect(emptyInputs)},
			"GetVersion":             {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: left}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateInputInGroup")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
	response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "interruption")
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.Children[0].SourceUUID.ValueString() != "left-new" || got.Children[0].ItemID.ValueInt64() != 3 || !got.Children[0].Present.ValueBool() {
		t.Fatalf("replacement checkpoint state=%#v; authoritative child binding was not retained", got.Children[0])
	}
	if groupFixtureMutationCount(script, "CreateInputInGroup") != 1 || groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 || groupFixtureMutationCount(script, "SetSceneItemTransform") != 0 || groupFixtureMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("Update continued after replacement interruption: calls=%#v", script.calls)
	}
}

func TestGroupGraphUpdateSetterInterruptionRetainsPlacementCheckpoint(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	stateModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
	stateModel.Children[1].Placement.Transform.X = types.Float64Value(900)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
	planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
	parent := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(900)
	rightPlacement.Index = types.Int64Value(1)
	group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{
		groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &groupFixtureScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList())},
			"GetInputList":            {groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
			"GetVersion":              {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":        {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"SetSceneItemTransform":   {groupFixtureDirect(map[string]any{})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "SetSceneItemTransform")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
	response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "interruption")
	if calls := groupFixtureMutationCount(script, "SetSceneItemTransform"); calls != 1 {
		t.Fatalf("setter calls=%d want one authoritative interrupted setter", calls)
	}
	targets := groupGraphFixtureSetterTargets(script)
	if len(targets) != 1 || targets[0].item != 2 || targets[0].x != 960 {
		t.Fatalf("setter targets=%#v want right item 2 x960", targets)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if len(got.ObservedChildren) != 2 || got.ObservedChildren[1].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("state=%#v; authoritative setter placement was not checkpointed", got)
	}
	if groupFixtureMutationCount(script, "GetSourceScreenshot") != 0 || groupFixtureMutationCount(script, "SetSceneItemEnabled") != 0 || groupFixtureMutationCount(script, "SetSceneItemLocked") != 0 || groupFixtureMutationCount(script, "SetSceneItemIndex") != 0 {
		t.Fatalf("Update continued after setter interruption: calls=%#v", script.calls)
	}
}

func TestGroupGraphUpdateFrameBarrierFailureRetainsSetterCheckpoint(t *testing.T) {
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	stateModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
	stateModel.Children[1].Placement.Transform.X = types.Float64Value(900)
	planModel := stateModel
	planModel.Children = append([]groupGraphChildModel(nil), stateModel.Children...)
	planModel.Children[1].Placement = groupGraphFixtureClonePlacement(stateModel.Children[1].Placement)
	planModel.Children[1].Placement.Transform.X = types.Float64Value(960)
	parent := groupFixturePlacement()
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(900)
	rightPlacement.Index = types.Int64Value(1)
	group := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, parent, 1920, 1080, []any{
		groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080),
		groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080),
	})
	script := &groupFixtureScript{
		capabilityRequests: []string{"GetGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":            {groupFixtureDirect(groupFixtureSceneList()), groupFixtureDirect(groupFixtureSceneList())},
			"GetInputList":            {groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Left", "inputUuid": "left-uuid", "inputKind": "image_source"}}}), groupFixtureDirect(map[string]any{"inputs": []any{map[string]any{"inputName": "Party Light Right", "inputUuid": "right-uuid", "inputKind": "image_source"}}})},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(leftAsset)), groupFixtureDirect(groupFixtureImageSettings(rightAsset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings()), groupFixtureDirect(groupFixtureDefaultImageSettings())},
			"GetVersion":              {groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion()), groupFixtureDirect(groupGraphFixtureBroadVersion())},
			"GetVideoSettings":        {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetSourceScreenshot":     {{err: errors.New("synthetic post-setter frame barrier failure")}},
			"SetSceneItemTransform":   {groupFixtureDirect(map[string]any{})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"GetGroup": {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), planModel)
	response := resource.UpdateResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: script}}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "parent-scene frame barrier failed")
	if groupFixtureMutationCount(script, "SetSceneItemTransform") != 1 || groupFixtureMutationCount(script, "GetSourceScreenshot") != 1 || groupFixtureMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("frame barrier failure crossed its boundary: calls=%#v", script.calls)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if len(got.ObservedChildren) != 2 || got.ObservedChildren[1].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("state=%#v; successful setter checkpoint was lost", got)
	}
	for _, target := range groupGraphFixtureSetterTargets(script) {
		if target.item == 7 {
			t.Fatalf("parent setter was sent after failed child frame barrier: %#v", target)
		}
	}
}

type groupGraphFixtureDeleteResumeRequester struct {
	calls             []groupFixtureCall
	groupInitial      map[string]any
	groupStale        map[string]any
	groupSettled      map[string]any
	groupEmpty        map[string]any
	rightAsset        string
	failResumeBarrier bool
	settled           bool
	removals          int
}

func (r *groupGraphFixtureDeleteResumeRequester) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	r.calls = append(r.calls, groupFixtureCall{typ: typ, data: data})
	if typ == "CallVendorRequest" {
		requestType := stringValue(data["requestType"])
		requestData := mapValue(data["requestData"])
		switch requestType {
		case "GetCapabilities":
			return groupFixtureBridgeEnvelope("GetCapabilities", "", groupFixtureBridgeReply{outcome: "completed", postconditionVerified: true, result: groupFixtureCapabilities([]string{"GetGroup", "RemoveGroupChildPlacement"})}), nil
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
			return groupFixtureBridgeEnvelope("GetGroup", "", groupFixtureBridgeReply{outcome: "completed", postconditionVerified: true, result: group}), nil
		case "RemoveGroupChildPlacement":
			r.removals++
			return groupFixtureBridgeEnvelope(requestType, stringValue(requestData["clientOperationId"]), groupFixtureBridgeReply{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: map[string]any{"sourceStillExists": false}}), nil
		default:
			return nil, errors.New("unexpected bridge request " + requestType)
		}
	}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": groupFixtureCollection}, nil
	case "GetSceneList":
		return groupFixtureSceneList(), nil
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
		return groupFixtureImageSettings(r.rightAsset), nil
	case "GetInputDefaultSettings":
		return groupFixtureDefaultImageSettings(), nil
	case "GetVersion":
		return groupGraphFixtureBroadVersion(), nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}, nil
	case "GetSourceScreenshot":
		if r.failResumeBarrier && r.removals == 1 {
			return nil, errors.New("synthetic resumed Delete frame barrier failure")
		}
		if r.removals == 1 {
			r.settled = true
		}
		return groupGraphFixtureScreenshot(), nil
	case "RemoveSceneItem":
		return map[string]any{}, nil
	default:
		return nil, errors.New("unexpected direct request " + typ)
	}
}

func groupGraphFixtureDeleteResumeRelevantOperations(calls []groupFixtureCall) []string {
	var operations []string
	for _, call := range calls {
		if call.typ == "GetSourceScreenshot" {
			operations = append(operations, call.typ)
			continue
		}
		if call.typ != "CallVendorRequest" {
			continue
		}
		switch groupFixtureBridgeRequestType(call) {
		case "GetGroup", "RemoveGroupChildPlacement":
			operations = append(operations, groupFixtureBridgeRequestType(call))
		}
	}
	return operations
}

func groupGraphFixtureDeleteResumeFixture(t *testing.T, failResumeBarrier bool) (*groupGraphResource, *groupGraphFixtureDeleteResumeRequester, resource.DeleteResponse) {
	t.Helper()
	ctx := context.Background()
	leftAsset := groupGraphFixtureAsset(t, "left.png")
	rightAsset := groupGraphFixtureAsset(t, "right.png")
	stateModel := groupGraphFixtureCompleteModel(t, leftAsset, rightAsset)
	stateModel.ParentPlacement = groupGraphFixtureClonePlacement(stateModel.ParentPlacement)
	stateModel.ParentPlacement.Transform.X = types.Float64Value(0)
	groupInitialParent := groupFixturePlacement()
	groupInitialParent.Transform.X = types.Float64Value(0)
	leftPlacement := groupFixturePlacement()
	leftPlacement.Transform.X = types.Float64Value(0)
	rightPlacement := groupFixturePlacement()
	rightPlacement.Transform.X = types.Float64Value(960)
	rightPlacement.Index = types.Int64Value(1)
	left := groupGraphFixtureChildResult(1, "left-uuid", "Party Light Left", leftPlacement, 960, 1080)
	right := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", rightPlacement, 960, 1080)
	groupInitial := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, groupInitialParent, 1920, 1080, []any{left, right})
	groupStaleParent := groupFixturePlacement()
	groupStaleParent.Transform.X = types.Float64Value(0)
	groupStale := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, groupStaleParent, 1920, 1080, []any{right})
	groupSettledParent := groupFixturePlacement()
	groupSettledParent.Transform.X = types.Float64Value(960)
	settledRightPlacement := groupFixturePlacement()
	settledRightPlacement.Transform.X = types.Float64Value(0)
	settledRightPlacement.Index = types.Int64Value(1)
	settledRight := groupGraphFixtureChildResult(2, "right-uuid", "Party Light Right", settledRightPlacement, 960, 1080)
	groupSettled := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, groupSettledParent, 960, 1080, []any{settledRight})
	groupEmpty := groupGraphFixtureGroupResult(7, "group-stable", groupFixtureGroupName, groupSettledParent, 0, 0, []any{})
	requester := &groupGraphFixtureDeleteResumeRequester{groupInitial: groupInitial, groupStale: groupStale, groupSettled: groupSettled, groupEmpty: groupEmpty, rightAsset: rightAsset, failResumeBarrier: failResumeBarrier}
	resourceUnderTest := &groupGraphResource{client: &client{testRequester: requester}}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:RemoveGroupChildPlacement")
	first := resource.DeleteResponse{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: groupGraphFixtureStateWithBindings(t, ctx, groupGraphSchema(), stateModel)}, &first)
	groupFixtureAssertDiagnostic(t, first.Diagnostics, "interruption")
	var partial groupGraphModel
	groupFixtureRequireState(t, ctx, first.State, &partial)
	if partial.Children[0].Present.ValueBool() || partial.Children[0].SourceUUID.ValueString() != "left-uuid" || partial.Children[0].ItemID.ValueInt64() != 1 || partial.Children[1].Present.IsNull() || !partial.Children[1].Present.ValueBool() {
		t.Fatalf("partial Delete state=%#v; authoritative absent prefix was not retained", partial.Children)
	}
	if requester.removals != 1 {
		t.Fatalf("first Delete removals=%d want one", requester.removals)
	}
	if got := groupGraphFixtureDeleteResumeMutationCount(requester.calls, "GetSourceScreenshot"); got != 0 {
		t.Fatalf("Delete crossed interruption into a frame barrier (%d calls)", got)
	}
	return resourceUnderTest, requester, first
}

func TestGroupGraphDeleteResumesAfterAuthoritativeChildInterruption(t *testing.T) {
	ctx := context.Background()
	resourceUnderTest, requester, first := groupGraphFixtureDeleteResumeFixture(t, false)

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
		if groupFixtureBridgeRequestType(call) != "RemoveGroupChildPlacement" {
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
	got := groupGraphFixtureDeleteResumeRelevantOperations(requester.calls[readEnd:])
	if len(got) != len(want) {
		t.Fatalf("resumed Delete operations=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resumed Delete operations=%v want %v", got, want)
		}
	}
}

func TestGroupGraphDeleteResumeStopsOnRequiredFrameBarrierFailure(t *testing.T) {
	ctx := context.Background()
	resourceUnderTest, requester, first := groupGraphFixtureDeleteResumeFixture(t, true)

	read := resource.ReadResponse{State: first.State}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: first.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("partial Delete Read diagnostics: %v", read.Diagnostics)
	}
	readEnd := len(requester.calls)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	second := resource.DeleteResponse{State: read.State}
	resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: read.State}, &second)
	groupFixtureAssertDiagnostic(t, second.Diagnostics, "parent-scene frame barrier failed")
	if requester.removals != 1 {
		t.Fatalf("barrier-failed resumed Delete removals=%d want one", requester.removals)
	}
	want := []string{"GetGroup", "GetSourceScreenshot"}
	got := groupGraphFixtureDeleteResumeRelevantOperations(requester.calls[readEnd:])
	if len(got) != len(want) {
		t.Fatalf("barrier-failed resumed Delete operations=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("barrier-failed resumed Delete operations=%v want %v", got, want)
		}
	}
}

func groupGraphFixtureDeleteResumeMutationCount(calls []groupFixtureCall, typ string) int {
	count := 0
	for _, call := range calls {
		if call.typ == typ || groupFixtureBridgeRequestType(call) == typ {
			count++
		}
	}
	return count
}
