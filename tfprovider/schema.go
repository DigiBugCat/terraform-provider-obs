package tfprovider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

func identityString() schema.StringAttribute {
	return schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}

func identityInt64() schema.Int64Attribute {
	return schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}}
}

func immutableString(required bool) schema.StringAttribute {
	return schema.StringAttribute{Required: required, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
}

func immutableInt64() schema.Int64Attribute {
	return schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}}
}

func transformSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Required: true, Attributes: map[string]schema.Attribute{
		"x": schema.Float64Attribute{Required: true}, "y": schema.Float64Attribute{Required: true},
		"scale_x": schema.Float64Attribute{Required: true}, "scale_y": schema.Float64Attribute{Required: true},
		"rotation":  schema.Float64Attribute{Required: true, Description: "Static orientation in degrees. Excess full turns are wrapped for OBS; equivalent native readback retains the authored value."},
		"crop_left": schema.Int64Attribute{Required: true}, "crop_top": schema.Int64Attribute{Required: true},
		"crop_right": schema.Int64Attribute{Required: true}, "crop_bottom": schema.Int64Attribute{Required: true},
		"alignment": schema.Int64Attribute{Required: true}, "bounds_type": schema.StringAttribute{Required: true},
		"bounds_alignment": schema.Int64Attribute{Required: true},
		"bounds_width":     schema.Float64Attribute{Optional: true, Description: "Native bounding box width; required with an active bounds_type. Omit to leave disabled bounds metadata unmanaged."},
		"bounds_height":    schema.Float64Attribute{Optional: true, Description: "Native bounding box height; required with an active bounds_type. Omit to leave disabled bounds metadata unmanaged."},
		"crop_to_bounds":   schema.BoolAttribute{Optional: true, Description: "Crop to the bounding box. Omit to leave the native flag unmanaged."},
	}}
}
