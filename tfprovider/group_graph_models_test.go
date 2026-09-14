package tfprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func groupGraphTestAsset(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func groupGraphTestPlacement(x float64, index int64) bridgePlacementModel {
	p := groupFixturePlacement()
	p.Transform.X = types.Float64Value(x)
	p.Index = types.Int64Value(index)
	return p
}

func groupGraphTestChild(key, name, file, uuid string, itemID int64, x float64, index int64, present bool) groupGraphChildModel {
	p := groupGraphTestPlacement(x, index)
	return groupGraphChildModel{
		StableKey: types.StringValue(key), Name: types.StringValue(name), Kind: types.StringValue("image_source"),
		File: types.StringValue(file), Unload: types.BoolValue(false), Present: types.BoolValue(present), Placement: &p,
		SourceUUID: types.StringValue(uuid), ItemID: types.Int64Value(itemID),
	}
}

func groupGraphTestModel(leftFile, rightFile string) groupGraphModel {
	parent := groupGraphTestPlacement(0, 0)
	return groupGraphModel{
		ID:         types.StringValue(groupGraphID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID)),
		Collection: types.StringValue(groupFixtureCollection), SceneName: types.StringValue(groupFixtureSceneName), SceneUUID: types.StringValue(groupFixtureSceneUUID),
		Name: types.StringValue(groupFixtureGroupName), ParentPresent: types.BoolValue(true), ParentPlacement: &parent,
		GroupUUID: types.StringValue(groupFixtureGroupUUID), ParentItemID: types.Int64Value(7),
		Children: []groupGraphChildModel{
			groupGraphTestChild("left", "Party Light Left", leftFile, "input-left", 1, 0, 0, true),
			groupGraphTestChild("right", "Party Light Right", rightFile, "input-right", 2, 960, 1, true),
		},
		ObservedComplete: types.BoolValue(true), ObservedGroupWidth: types.Int64Value(1920), ObservedGroupHeight: types.Int64Value(1080), RepairNonce: types.StringValue("settled"),
	}
}

func groupGraphUnknownObservationPlanModel(t *testing.T, m groupGraphModel) groupGraphPlanModel {
	t.Helper()
	root, ok := groupGraphSchema().Type().(types.ObjectType)
	if !ok {
		t.Fatalf("group graph schema type=%T, want types.ObjectType", groupGraphSchema().Type())
	}
	parentType, ok := root.AttrTypes["observed_parent"].(types.ObjectType)
	if !ok {
		t.Fatalf("observed_parent type=%T, want types.ObjectType", root.AttrTypes["observed_parent"])
	}
	childrenType, ok := root.AttrTypes["observed_children"].(types.ListType)
	if !ok {
		t.Fatalf("observed_children type=%T, want types.ListType", root.AttrTypes["observed_children"])
	}
	return groupGraphPlanModel{
		ID: m.ID, Collection: m.Collection, SceneName: m.SceneName, SceneUUID: m.SceneUUID, Name: m.Name,
		ParentPresent: m.ParentPresent, ParentPlacement: m.ParentPlacement, GroupUUID: m.GroupUUID, ParentItemID: m.ParentItemID,
		Children: m.Children, ObservedComplete: m.ObservedComplete, ObservedGroupWidth: m.ObservedGroupWidth,
		ObservedGroupHeight: m.ObservedGroupHeight, ObservedParent: types.ObjectUnknown(parentType.AttrTypes),
		ObservedChildren: types.ListUnknown(childrenType.ElemType), RepairNonce: m.RepairNonce,
	}
}

func TestGroupGraphPlanDecodesWhollyUnknownComputedObservations(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	desired := groupGraphTestModel(left, right)
	desired.ID, desired.GroupUUID, desired.ParentItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	desired.ObservedComplete, desired.ObservedGroupWidth, desired.ObservedGroupHeight, desired.RepairNonce = types.BoolUnknown(), types.Int64Unknown(), types.Int64Unknown(), types.StringUnknown()
	for i := range desired.Children {
		desired.Children[i].SourceUUID, desired.Children[i].ItemID = types.StringUnknown(), types.Int64Unknown()
	}

	plan := groupFixtureSchemaAndPlan(t, ctx, groupGraphSchema(), groupGraphUnknownObservationPlanModel(t, desired))
	var legacy groupGraphModel
	if diags := plan.Get(ctx, &legacy); !diags.HasError() {
		t.Fatal("legacy pointer/slice model unexpectedly decoded wholly unknown computed observations")
	}
	var decoded groupGraphPlanModel
	if diags := plan.Get(ctx, &decoded); diags.HasError() {
		t.Fatalf("unknown-safe plan decode diagnostics: %v", diags)
	}
	if !decoded.ObservedParent.IsUnknown() || !decoded.ObservedChildren.IsUnknown() {
		t.Fatalf("computed observations lost unknown state: parent=%s children=%s", decoded.ObservedParent, decoded.ObservedChildren)
	}
	got := decoded.desiredModel()
	if got.Name.ValueString() != desired.Name.ValueString() || len(got.Children) != 2 || got.Children[0].StableKey.ValueString() != "left" || got.Children[1].StableKey.ValueString() != "right" {
		t.Fatalf("desired graph changed during unknown-safe decode: %#v", got)
	}
}

func TestGroupGraphModifyPlanMarksOnlyRepairIdentityTurnoverUnknown(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	state.ObservedComplete = types.BoolValue(false)
	planModel := groupGraphTestModel(left, right)
	sc := groupGraphSchema()
	plan := groupFixtureSchemaAndPlan(t, ctx, sc, planModel)
	response := resource.ModifyPlanResponse{Plan: plan}
	(&groupGraphResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: groupFixtureSchemaAndState(t, ctx, sc, state), Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("modify plan diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphPlanModel
	if diags := response.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode modified plan: %v", diags)
	}
	if !got.Children[0].SourceUUID.IsUnknown() || !got.Children[0].ItemID.IsUnknown() || !got.RepairNonce.IsUnknown() {
		t.Fatalf("missing child turnover stayed known: %#v", got.Children[0])
	}
	if got.Children[1].SourceUUID.IsUnknown() || got.Children[1].ItemID.IsUnknown() {
		t.Fatalf("surviving child identity became unknown: %#v", got.Children[1])
	}
	if got.GroupUUID.IsUnknown() || got.ParentItemID.IsUnknown() {
		t.Fatalf("stable group/parent identity became unknown: group=%s parent=%s", got.GroupUUID, got.ParentItemID)
	}

	parentAbsent := state
	parentAbsent.ParentPresent = types.BoolValue(false)
	parentResponse := resource.ModifyPlanResponse{Plan: plan}
	(&groupGraphResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: groupFixtureSchemaAndState(t, ctx, sc, parentAbsent), Plan: plan}, &parentResponse)
	if parentResponse.Diagnostics.HasError() {
		t.Fatalf("parent repair modify plan diagnostics: %v", parentResponse.Diagnostics)
	}
	if diags := parentResponse.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode parent repair plan: %v", diags)
	}
	if !got.ParentItemID.IsUnknown() || got.GroupUUID.IsUnknown() || !got.RepairNonce.IsUnknown() {
		t.Fatalf("parent turnover markers: parent=%s group=%s nonce=%s", got.ParentItemID, got.GroupUUID, got.RepairNonce)
	}

	reordered := planModel
	reordered.Children = []groupGraphChildModel{planModel.Children[1], planModel.Children[0]}
	reordered.Children[0].Placement = placementPointer(groupGraphTestPlacement(960, 0))
	reordered.Children[1].Placement = placementPointer(groupGraphTestPlacement(0, 1))
	reorderedPlan := groupFixtureSchemaAndPlan(t, ctx, sc, reordered)
	reorderedResponse := resource.ModifyPlanResponse{Plan: reorderedPlan}
	(&groupGraphResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: groupFixtureSchemaAndState(t, ctx, sc, planModel), Plan: reorderedPlan}, &reorderedResponse)
	if reorderedResponse.Diagnostics.HasError() {
		t.Fatalf("reorder modify plan diagnostics: %v", reorderedResponse.Diagnostics)
	}
	if diags := reorderedResponse.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode reorder plan: %v", diags)
	}
	if !got.RepairNonce.IsUnknown() || got.Children[0].SourceUUID.IsUnknown() || got.Children[0].ItemID.IsUnknown() || got.Children[1].SourceUUID.IsUnknown() || got.Children[1].ItemID.IsUnknown() {
		t.Fatalf("reorder turnover markers: nonce=%s children=%#v", got.RepairNonce, got.Children)
	}
}

func TestGroupGraphConfigAndPlanRejectDestructiveAmbiguity(t *testing.T) {
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	base := groupGraphTestModel(left, right)
	if err := validateGroupGraphConfig(base); err != nil {
		t.Fatalf("valid graph: %v", err)
	}

	tests := []struct {
		name string
		edit func(*groupGraphModel)
		want string
	}{
		{"configured parent absent", func(m *groupGraphModel) { m.ParentPresent = types.BoolValue(false) }, "parent_present"},
		{"configured child absent", func(m *groupGraphModel) { m.Children[0].Present = types.BoolValue(false) }, "present"},
		{"duplicate stable key", func(m *groupGraphModel) { m.Children[1].StableKey = m.Children[0].StableKey }, "duplicated"},
		{"relative asset", func(m *groupGraphModel) { m.Children[0].File = types.StringValue("relative.png") }, "absolute"},
		{"unsupported kind", func(m *groupGraphModel) { m.Children[0].Kind = types.StringValue("ffmpeg_source") }, "image_source"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := base
			got.Children = append([]groupGraphChildModel(nil), base.Children...)
			tc.edit(&got)
			if err := validateGroupGraphConfig(got); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}

	transitions := []struct {
		name string
		edit func(*groupGraphModel)
		want string
	}{
		{"group rename", func(m *groupGraphModel) { m.Name = types.StringValue("Renamed") }, "group name"},
		{"child removal", func(m *groupGraphModel) { m.Children = m.Children[:1] }, "removing"},
		{"key change", func(m *groupGraphModel) { m.Children[0].StableKey = types.StringValue("new-left") }, "stable_key"},
		{"child rename", func(m *groupGraphModel) { m.Children[0].Name = types.StringValue("Renamed") }, ".name"},
		{"kind change", func(m *groupGraphModel) { m.Children[0].Kind = types.StringValue("ffmpeg_source") }, ".kind"},
	}
	for _, tc := range transitions {
		t.Run(tc.name, func(t *testing.T) {
			plan := base
			plan.Children = append([]groupGraphChildModel(nil), base.Children...)
			tc.edit(&plan)
			if err := validateGroupGraphPlanTransition(base, plan); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}
	reordered := base
	reordered.Children = []groupGraphChildModel{base.Children[1], base.Children[0]}
	reordered.Children[0].Placement = placementPointer(groupGraphTestPlacement(960, 0))
	reordered.Children[1].Placement = placementPointer(groupGraphTestPlacement(0, 1))
	if err := validateGroupGraphPlanTransition(base, reordered); err != nil {
		t.Fatalf("stable-key reorder rejected: %v", err)
	}
}

func placementPointer(p bridgePlacementModel) *bridgePlacementModel { return &p }

func TestGroupGraphExactBoundsRequireSupportedZeroAnchor(t *testing.T) {
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	m := groupGraphTestModel(left, right)
	observed := bridgeGroupObservation{
		Width: 1920, Height: 1080,
		Children: []bridgeChildObservation{
			{Name: m.Children[0].Name.ValueString(), UUID: "input-left", Kind: "image_source", ItemID: 1, Width: 960, Height: 1080, Placement: *m.Children[0].Placement},
			{Name: m.Children[1].Name.ValueString(), UUID: "input-right", Kind: "image_source", ItemID: 2, Width: 960, Height: 1080, Placement: *m.Children[1].Placement},
		},
	}
	w, h, anchor, err := proveGroupGraphZeroAnchor(observed, m.Children)
	if err != nil || w != 1920 || h != 1080 || anchor != 0 {
		t.Fatalf("bounds=%dx%d anchor=%d error=%v", w, h, anchor, err)
	}
	m.Children[0].Placement.Transform.Rotation = types.Float64Value(1)
	if _, _, _, err := proveGroupGraphZeroAnchor(observed, m.Children); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("rotated graph error=%v", err)
	}
	m.Children[0].Placement.Transform.Rotation = types.Float64Value(0)
	m.Children[0].Placement.Transform.X = types.Float64Value(10)
	if _, _, _, err := proveGroupGraphZeroAnchor(observed, m.Children); err == nil || !strings.Contains(err.Error(), "exact (0,0)") {
		t.Fatalf("missing zero anchor error=%v", err)
	}
}

func TestGroupGraphImportParsesExactOrderedBindings(t *testing.T) {
	ctx := context.Background()
	sc := groupGraphSchema()
	response := resource.ImportStateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, groupGraphModel{})}
	r := &groupGraphResource{}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "Lab|scene-1|group-1|7|left=input-left:11,right=input-right:12"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.ID.ValueString() != "Lab|scene-1|group-1" || got.ParentItemID.ValueInt64() != 7 || len(got.Children) != 2 || got.Children[0].StableKey.ValueString() != "left" || got.Children[1].SourceUUID.ValueString() != "input-right" {
		t.Fatalf("imported state=%#v", got)
	}
}
