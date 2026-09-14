package tfprovider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/boolvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var groupGraphStableKeyRE = regexp.MustCompile(`^[a-z0-9_-]+$`)

// groupGraphChildModel deliberately keeps the desired presence bit and the
// authoritative native binding together. Read can therefore retain a missing
// child's UUID/item tuple while recording Present=false, which produces an
// ordinary repair diff against configuration's required true value.
type groupGraphChildModel struct {
	StableKey  types.String          `tfsdk:"stable_key"`
	Name       types.String          `tfsdk:"name"`
	Kind       types.String          `tfsdk:"kind"`
	File       types.String          `tfsdk:"file"`
	Unload     types.Bool            `tfsdk:"unload"`
	Present    types.Bool            `tfsdk:"present"`
	Placement  *bridgePlacementModel `tfsdk:"placement"`
	SourceUUID types.String          `tfsdk:"source_uuid"`
	ItemID     types.Int64           `tfsdk:"item_id"`
}

type groupGraphObservedParentModel struct {
	Present   types.Bool            `tfsdk:"present"`
	Placement *bridgePlacementModel `tfsdk:"placement"`
}

type groupGraphObservedChildModel struct {
	StableKey  types.String          `tfsdk:"stable_key"`
	Name       types.String          `tfsdk:"name"`
	Kind       types.String          `tfsdk:"kind"`
	SourceUUID types.String          `tfsdk:"source_uuid"`
	ItemID     types.Int64           `tfsdk:"item_id"`
	Present    types.Bool            `tfsdk:"present"`
	Width      types.Int64           `tfsdk:"width"`
	Height     types.Int64           `tfsdk:"height"`
	Placement  *bridgePlacementModel `tfsdk:"placement"`
}

type groupGraphModel struct {
	ID                  types.String                   `tfsdk:"id"`
	Collection          types.String                   `tfsdk:"collection"`
	SceneName           types.String                   `tfsdk:"scene_name"`
	SceneUUID           types.String                   `tfsdk:"scene_uuid"`
	Name                types.String                   `tfsdk:"name"`
	ParentPresent       types.Bool                     `tfsdk:"parent_present"`
	ParentPlacement     *bridgePlacementModel          `tfsdk:"parent_placement"`
	GroupUUID           types.String                   `tfsdk:"group_uuid"`
	ParentItemID        types.Int64                    `tfsdk:"parent_item_id"`
	Children            []groupGraphChildModel         `tfsdk:"children"`
	ObservedComplete    types.Bool                     `tfsdk:"observed_complete"`
	ObservedGroupWidth  types.Int64                    `tfsdk:"observed_group_width"`
	ObservedGroupHeight types.Int64                    `tfsdk:"observed_group_height"`
	ObservedParent      *groupGraphObservedParentModel `tfsdk:"observed_parent"`
	ObservedChildren    []groupGraphObservedChildModel `tfsdk:"observed_children"`
	RepairNonce         types.String                   `tfsdk:"repair_nonce"`
}

// groupGraphPlanModel uses framework-native collection/object values for
// computed nested observations. Terraform represents these as wholly unknown
// during an initial plan; Go pointers/slices cannot decode that state. The
// resource callbacks convert only desired/configuration and identity fields to
// groupGraphModel, while Read/partial checkpoints continue using the strongly
// typed state model once observations are known or null.
type groupGraphPlanModel struct {
	ID                  types.String           `tfsdk:"id"`
	Collection          types.String           `tfsdk:"collection"`
	SceneName           types.String           `tfsdk:"scene_name"`
	SceneUUID           types.String           `tfsdk:"scene_uuid"`
	Name                types.String           `tfsdk:"name"`
	ParentPresent       types.Bool             `tfsdk:"parent_present"`
	ParentPlacement     *bridgePlacementModel  `tfsdk:"parent_placement"`
	GroupUUID           types.String           `tfsdk:"group_uuid"`
	ParentItemID        types.Int64            `tfsdk:"parent_item_id"`
	Children            []groupGraphChildModel `tfsdk:"children"`
	ObservedComplete    types.Bool             `tfsdk:"observed_complete"`
	ObservedGroupWidth  types.Int64            `tfsdk:"observed_group_width"`
	ObservedGroupHeight types.Int64            `tfsdk:"observed_group_height"`
	ObservedParent      types.Object           `tfsdk:"observed_parent"`
	ObservedChildren    types.List             `tfsdk:"observed_children"`
	RepairNonce         types.String           `tfsdk:"repair_nonce"`
}

func (m groupGraphPlanModel) desiredModel() groupGraphModel {
	return groupGraphModel{
		ID: m.ID, Collection: m.Collection, SceneName: m.SceneName, SceneUUID: m.SceneUUID, Name: m.Name,
		ParentPresent: m.ParentPresent, ParentPlacement: m.ParentPlacement, GroupUUID: m.GroupUUID, ParentItemID: m.ParentItemID,
		Children: m.Children, ObservedComplete: m.ObservedComplete, ObservedGroupWidth: m.ObservedGroupWidth,
		ObservedGroupHeight: m.ObservedGroupHeight, RepairNonce: m.RepairNonce,
	}
}

func computedBridgeTransformSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Computed: true, Attributes: map[string]schema.Attribute{
		"x": schema.Float64Attribute{Computed: true}, "y": schema.Float64Attribute{Computed: true},
		"scale_x": schema.Float64Attribute{Computed: true}, "scale_y": schema.Float64Attribute{Computed: true},
		"rotation": schema.Float64Attribute{Computed: true}, "alignment": schema.Int64Attribute{Computed: true},
		"bounds_type": schema.StringAttribute{Computed: true}, "bounds_alignment": schema.Int64Attribute{Computed: true},
		"bounds_width": schema.Float64Attribute{Computed: true}, "bounds_height": schema.Float64Attribute{Computed: true},
		"crop_to_bounds": schema.BoolAttribute{Computed: true}, "crop_left": schema.Int64Attribute{Computed: true},
		"crop_top": schema.Int64Attribute{Computed: true}, "crop_right": schema.Int64Attribute{Computed: true},
		"crop_bottom": schema.Int64Attribute{Computed: true},
	}}
}

func computedBridgePlacementSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Computed: true, Attributes: map[string]schema.Attribute{
		"enabled": schema.BoolAttribute{Computed: true}, "locked": schema.BoolAttribute{Computed: true},
		"index": schema.Int64Attribute{Computed: true}, "transform": computedBridgeTransformSchema(),
	}}
}

func groupGraphSchema() schema.Schema {
	return schema.Schema{Version: 1, Attributes: map[string]schema.Attribute{
		"id":               identityString(),
		"collection":       immutableString(true),
		"scene_name":       schema.StringAttribute{Required: true},
		"scene_uuid":       immutableString(true),
		"name":             schema.StringAttribute{Required: true},
		"parent_present":   schema.BoolAttribute{Required: true, Validators: []validator.Bool{boolvalidator.Equals(true)}},
		"parent_placement": bridgePlacementSchema(),
		"group_uuid":       identityString(),
		"parent_item_id":   identityInt64(),
		"children": schema.ListNestedAttribute{
			Required: true,
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"stable_key":  schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(groupGraphStableKeyRE, "must match [a-z0-9_-]+")}},
				"name":        schema.StringAttribute{Required: true},
				"kind":        schema.StringAttribute{Required: true},
				"file":        schema.StringAttribute{Required: true},
				"unload":      schema.BoolAttribute{Required: true},
				"present":     schema.BoolAttribute{Required: true, Validators: []validator.Bool{boolvalidator.Equals(true)}},
				"placement":   bridgePlacementSchema(),
				"source_uuid": identityString(),
				"item_id":     identityInt64(),
			}},
		},
		"observed_complete":     schema.BoolAttribute{Computed: true},
		"observed_group_width":  schema.Int64Attribute{Computed: true},
		"observed_group_height": schema.Int64Attribute{Computed: true},
		"observed_parent": schema.SingleNestedAttribute{Computed: true, Attributes: map[string]schema.Attribute{
			"present":   schema.BoolAttribute{Computed: true},
			"placement": computedBridgePlacementSchema(),
		}},
		"observed_children": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"stable_key": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
			"kind": schema.StringAttribute{Computed: true}, "source_uuid": schema.StringAttribute{Computed: true},
			"item_id": schema.Int64Attribute{Computed: true}, "present": schema.BoolAttribute{Computed: true},
			"width": schema.Int64Attribute{Computed: true}, "height": schema.Int64Attribute{Computed: true},
			"placement": computedBridgePlacementSchema(),
		}}},
		"repair_nonce": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
	}}
}

func validateGroupGraphConfig(m groupGraphModel) error {
	if m.ParentPlacement == nil {
		return fmt.Errorf("parent_placement is required")
	}
	if _, err := m.ParentPlacement.request(); err != nil {
		return fmt.Errorf("parent_placement: %w", err)
	}
	if !m.ParentPresent.IsNull() && !m.ParentPresent.IsUnknown() && !m.ParentPresent.ValueBool() {
		return fmt.Errorf("parent_present must be true in schema version 1")
	}
	if len(m.Children) == 0 {
		return fmt.Errorf("children must contain at least one managed child")
	}
	keys, names := map[string]bool{}, map[string]bool{}
	for i, child := range m.Children {
		key, name := child.StableKey.ValueString(), child.Name.ValueString()
		if !groupGraphStableKeyRE.MatchString(key) {
			return fmt.Errorf("children[%d].stable_key %q must match [a-z0-9_-]+", i, key)
		}
		if keys[key] {
			return fmt.Errorf("children[%d].stable_key %q is duplicated", i, key)
		}
		if name == "" || names[name] {
			return fmt.Errorf("children[%d].name %q is empty or duplicated", i, name)
		}
		keys[key], names[name] = true, true
		if child.Kind.ValueString() != "image_source" {
			return fmt.Errorf("children[%d].kind currently supports only image_source", i)
		}
		file := child.File.ValueString()
		if !filepath.IsAbs(file) {
			return fmt.Errorf("children[%d].file must be absolute", i)
		}
		info, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("children[%d].file: %w", i, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("children[%d].file is not a regular asset", i)
		}
		if !child.Present.IsNull() && !child.Present.IsUnknown() && !child.Present.ValueBool() {
			return fmt.Errorf("children[%d].present must be true in schema version 1", i)
		}
		if child.Placement == nil {
			return fmt.Errorf("children[%d].placement is required", i)
		}
		if _, err := child.Placement.request(); err != nil {
			return fmt.Errorf("children[%d].placement: %w", i, err)
		}
		if child.Placement.Index.ValueInt64() != int64(i) {
			return fmt.Errorf("children[%d].placement.index must equal its ordered list position %d", i, i)
		}
	}
	return nil
}

func groupGraphID(collection, sceneUUID, groupUUID string) string {
	return fmt.Sprintf("%s|%s|%s", collection, sceneUUID, groupUUID)
}
