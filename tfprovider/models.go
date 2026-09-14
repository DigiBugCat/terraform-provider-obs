package tfprovider

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type sceneModel struct {
	ID         types.String `tfsdk:"id"`
	Collection types.String `tfsdk:"collection"`
	Name       types.String `tfsdk:"name"`
	SceneUUID  types.String `tfsdk:"scene_uuid"`
}

type transformModel struct {
	X               types.Float64 `tfsdk:"x"`
	Y               types.Float64 `tfsdk:"y"`
	ScaleX          types.Float64 `tfsdk:"scale_x"`
	ScaleY          types.Float64 `tfsdk:"scale_y"`
	Rotation        types.Float64 `tfsdk:"rotation"`
	CropLeft        types.Int64   `tfsdk:"crop_left"`
	CropTop         types.Int64   `tfsdk:"crop_top"`
	CropRight       types.Int64   `tfsdk:"crop_right"`
	CropBottom      types.Int64   `tfsdk:"crop_bottom"`
	Alignment       types.Int64   `tfsdk:"alignment"`
	BoundsType      types.String  `tfsdk:"bounds_type"`
	BoundsAlignment types.Int64   `tfsdk:"bounds_alignment"`
	BoundsWidth     types.Float64 `tfsdk:"bounds_width"`
	BoundsHeight    types.Float64 `tfsdk:"bounds_height"`
	CropToBounds    types.Bool    `tfsdk:"crop_to_bounds"`
}

func transformFromNative(x map[string]any) transformModel {
	n := func(k string) types.Float64 { v, _ := numberValue(x[k]); return types.Float64Value(v) }
	i := func(k string) types.Int64 { return types.Int64Value(int64Value(x[k])) }
	return transformModel{X: n("positionX"), Y: n("positionY"), ScaleX: n("scaleX"), ScaleY: n("scaleY"), Rotation: n("rotation"), CropLeft: i("cropLeft"), CropTop: i("cropTop"), CropRight: i("cropRight"), CropBottom: i("cropBottom"), Alignment: i("alignment"), BoundsType: types.StringValue(stringValue(x["boundsType"])), BoundsAlignment: i("boundsAlignment"), BoundsWidth: n("boundsWidth"), BoundsHeight: n("boundsHeight"), CropToBounds: types.BoolValue(boolValue(x["cropToBounds"]))}
}

func (t transformModel) native() map[string]any {
	out := map[string]any{"positionX": t.X.ValueFloat64(), "positionY": t.Y.ValueFloat64(), "scaleX": t.ScaleX.ValueFloat64(), "scaleY": t.ScaleY.ValueFloat64(), "rotation": rotationForOBS(t.Rotation.ValueFloat64()), "cropLeft": t.CropLeft.ValueInt64(), "cropTop": t.CropTop.ValueInt64(), "cropRight": t.CropRight.ValueInt64(), "cropBottom": t.CropBottom.ValueInt64(), "alignment": t.Alignment.ValueInt64(), "boundsType": t.BoundsType.ValueString(), "boundsAlignment": t.BoundsAlignment.ValueInt64()}
	// OBS rejects zero bounds dimensions even when bounds are disabled. Null
	// optional fields retain the legacy placement's unmanaged bounds metadata.
	if !t.BoundsWidth.IsNull() && (t.BoundsType.ValueString() != "OBS_BOUNDS_NONE" || t.BoundsWidth.ValueFloat64() > 0) {
		out["boundsWidth"] = t.BoundsWidth.ValueFloat64()
	}
	if !t.BoundsHeight.IsNull() && (t.BoundsType.ValueString() != "OBS_BOUNDS_NONE" || t.BoundsHeight.ValueFloat64() > 0) {
		out["boundsHeight"] = t.BoundsHeight.ValueFloat64()
	}
	if !t.CropToBounds.IsNull() {
		out["cropToBounds"] = t.CropToBounds.ValueBool()
	}
	return out
}

type inputModel struct {
	ID              types.String    `tfsdk:"id"`
	Collection      types.String    `tfsdk:"collection"`
	Name            types.String    `tfsdk:"name"`
	Kind            types.String    `tfsdk:"kind"`
	File            types.String    `tfsdk:"file"`
	Unload          types.Bool      `tfsdk:"unload"`
	InputUUID       types.String    `tfsdk:"input_uuid"`
	AnchorSceneName types.String    `tfsdk:"anchor_scene_name"`
	AnchorSceneUUID types.String    `tfsdk:"anchor_scene_uuid"`
	AnchorItemID    types.Int64     `tfsdk:"anchor_item_id"`
	AnchorEnabled   types.Bool      `tfsdk:"anchor_enabled"`
	AnchorIndex     types.Int64     `tfsdk:"anchor_index"`
	AnchorTransform *transformModel `tfsdk:"anchor_transform"`
}

type sceneItemModel struct {
	ID         types.String    `tfsdk:"id"`
	Collection types.String    `tfsdk:"collection"`
	SceneName  types.String    `tfsdk:"scene_name"`
	SceneUUID  types.String    `tfsdk:"scene_uuid"`
	SourceName types.String    `tfsdk:"source_name"`
	SourceUUID types.String    `tfsdk:"source_uuid"`
	ItemID     types.Int64     `tfsdk:"item_id"`
	Enabled    types.Bool      `tfsdk:"enabled"`
	Index      types.Int64     `tfsdk:"index"`
	Transform  *transformModel `tfsdk:"transform"`
}

type mediaInputModel struct {
	ID                    types.String    `tfsdk:"id"`
	Collection            types.String    `tfsdk:"collection"`
	Name                  types.String    `tfsdk:"name"`
	InputUUID             types.String    `tfsdk:"input_uuid"`
	LocalFile             types.String    `tfsdk:"local_file"`
	IsLocalFile           types.Bool      `tfsdk:"is_local_file"`
	Looping               types.Bool      `tfsdk:"looping"`
	RestartOnActivate     types.Bool      `tfsdk:"restart_on_activate"`
	CloseWhenInactive     types.Bool      `tfsdk:"close_when_inactive"`
	AnchorSceneName       types.String    `tfsdk:"anchor_scene_name"`
	AnchorSceneUUID       types.String    `tfsdk:"anchor_scene_uuid"`
	AnchorItemID          types.Int64     `tfsdk:"anchor_item_id"`
	AnchorEnabled         types.Bool      `tfsdk:"anchor_enabled"`
	AnchorIndex           types.Int64     `tfsdk:"anchor_index"`
	AnchorTransform       *transformModel `tfsdk:"anchor_transform"`
	ObservedMediaState    types.String    `tfsdk:"observed_media_state"`
	ObservedMediaCursor   types.Float64   `tfsdk:"observed_media_cursor"`
	ObservedMediaDuration types.Float64   `tfsdk:"observed_media_duration"`
}

type colorFilterModel struct {
	ID         types.String  `tfsdk:"id"`
	Collection types.String  `tfsdk:"collection"`
	ParentName types.String  `tfsdk:"parent_name"`
	ParentUUID types.String  `tfsdk:"parent_uuid"`
	Name       types.String  `tfsdk:"name"`
	Kind       types.String  `tfsdk:"kind"`
	HueShift   types.Float64 `tfsdk:"hue_shift"`
	Opacity    types.Float64 `tfsdk:"opacity"`
	Enabled    types.Bool    `tfsdk:"enabled"`
	Index      types.Int64   `tfsdk:"index"`
}
