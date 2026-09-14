package tfprovider

import (
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type bridgeTransformModel struct {
	X               types.Float64 `tfsdk:"x"`
	Y               types.Float64 `tfsdk:"y"`
	ScaleX          types.Float64 `tfsdk:"scale_x"`
	ScaleY          types.Float64 `tfsdk:"scale_y"`
	Rotation        types.Float64 `tfsdk:"rotation"`
	Alignment       types.Int64   `tfsdk:"alignment"`
	BoundsType      types.String  `tfsdk:"bounds_type"`
	BoundsAlignment types.Int64   `tfsdk:"bounds_alignment"`
	BoundsWidth     types.Float64 `tfsdk:"bounds_width"`
	BoundsHeight    types.Float64 `tfsdk:"bounds_height"`
	CropToBounds    types.Bool    `tfsdk:"crop_to_bounds"`
	CropLeft        types.Int64   `tfsdk:"crop_left"`
	CropTop         types.Int64   `tfsdk:"crop_top"`
	CropRight       types.Int64   `tfsdk:"crop_right"`
	CropBottom      types.Int64   `tfsdk:"crop_bottom"`
}

type bridgePlacementModel struct {
	Enabled   types.Bool            `tfsdk:"enabled"`
	Locked    types.Bool            `tfsdk:"locked"`
	Index     types.Int64           `tfsdk:"index"`
	Transform *bridgeTransformModel `tfsdk:"transform"`
}

type groupModel struct {
	ID          types.String          `tfsdk:"id"`
	Collection  types.String          `tfsdk:"collection"`
	SceneName   types.String          `tfsdk:"scene_name"`
	SceneUUID   types.String          `tfsdk:"scene_uuid"`
	Name        types.String          `tfsdk:"name"`
	GroupUUID   types.String          `tfsdk:"group_uuid"`
	ItemID      types.Int64           `tfsdk:"item_id"`
	Placement   *bridgePlacementModel `tfsdk:"placement"`
	GroupWidth  types.Int64           `tfsdk:"observed_group_width"`
	GroupHeight types.Int64           `tfsdk:"observed_group_height"`
}

type groupInputModel struct {
	ID           types.String          `tfsdk:"id"`
	Collection   types.String          `tfsdk:"collection"`
	SceneName    types.String          `tfsdk:"scene_name"`
	SceneUUID    types.String          `tfsdk:"scene_uuid"`
	GroupName    types.String          `tfsdk:"group_name"`
	GroupUUID    types.String          `tfsdk:"group_uuid"`
	GroupItemID  types.Int64           `tfsdk:"group_item_id"`
	Name         types.String          `tfsdk:"name"`
	Kind         types.String          `tfsdk:"kind"`
	File         types.String          `tfsdk:"file"`
	Unload       types.Bool            `tfsdk:"unload"`
	InputUUID    types.String          `tfsdk:"input_uuid"`
	ItemID       types.Int64           `tfsdk:"item_id"`
	Placement    *bridgePlacementModel `tfsdk:"placement"`
	SourceWidth  types.Int64           `tfsdk:"observed_source_width"`
	SourceHeight types.Int64           `tfsdk:"observed_source_height"`
}

func bridgeTransformSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Required: true, Attributes: map[string]schema.Attribute{
		"x": schema.Float64Attribute{Required: true}, "y": schema.Float64Attribute{Required: true},
		"scale_x": schema.Float64Attribute{Required: true}, "scale_y": schema.Float64Attribute{Required: true},
		"rotation": schema.Float64Attribute{Required: true, Description: "Static orientation in degrees; WebSocket setters wrap excess full turns while state retains equivalent authored angles."}, "alignment": schema.Int64Attribute{Required: true},
		"bounds_type": schema.StringAttribute{Required: true}, "bounds_alignment": schema.Int64Attribute{Required: true},
		"bounds_width": schema.Float64Attribute{Required: true}, "bounds_height": schema.Float64Attribute{Required: true},
		"crop_to_bounds": schema.BoolAttribute{Required: true}, "crop_left": schema.Int64Attribute{Required: true},
		"crop_top": schema.Int64Attribute{Required: true}, "crop_right": schema.Int64Attribute{Required: true},
		"crop_bottom": schema.Int64Attribute{Required: true},
	}}
}

func bridgePlacementSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{Required: true, Attributes: map[string]schema.Attribute{
		"enabled": schema.BoolAttribute{Required: true}, "locked": schema.BoolAttribute{Required: true},
		"index": schema.Int64Attribute{Required: true}, "transform": bridgeTransformSchema(),
	}}
}

func (m bridgePlacementModel) request() (map[string]any, error) {
	if m.Transform == nil {
		return nil, fmt.Errorf("placement transform is required")
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	t := m.Transform
	return map[string]any{
		"sceneItemEnabled": m.Enabled.ValueBool(), "sceneItemLocked": m.Locked.ValueBool(), "sceneItemIndex": m.Index.ValueInt64(),
		"sceneItemTransform": map[string]any{
			"positionX": t.X.ValueFloat64(), "positionY": t.Y.ValueFloat64(), "rotation": t.Rotation.ValueFloat64(),
			"scaleX": t.ScaleX.ValueFloat64(), "scaleY": t.ScaleY.ValueFloat64(), "alignment": t.Alignment.ValueInt64(),
			"boundsType": t.BoundsType.ValueString(), "boundsAlignment": t.BoundsAlignment.ValueInt64(),
			"boundsWidth": t.BoundsWidth.ValueFloat64(), "boundsHeight": t.BoundsHeight.ValueFloat64(), "cropToBounds": t.CropToBounds.ValueBool(),
			"cropLeft": t.CropLeft.ValueInt64(), "cropTop": t.CropTop.ValueInt64(), "cropRight": t.CropRight.ValueInt64(), "cropBottom": t.CropBottom.ValueInt64(),
		},
	}, nil
}

// coreWebSocketTransformRequest encodes the partial transform object accepted
// by obs-websocket SetSceneItemTransform. GetSceneItemTransform and the native
// bridge correctly report raw zero bounds for OBS_BOUNDS_NONE, while
// obs-websocket 5.7.4 rejects an explicitly supplied boundsWidth/boundsHeight
// below 1 before considering boundsType. Omitting only those ignored fields
// preserves the authored/raw model and lets OBS retain the existing zero
// values. Native bridge requests continue to use request(), which deliberately
// carries the complete raw placement contract.
func (m bridgePlacementModel) coreWebSocketTransformRequest() (map[string]any, error) {
	native, err := m.request()
	if err != nil {
		return nil, err
	}
	full := mapValue(native["sceneItemTransform"])
	transform := make(map[string]any, len(full))
	for key, value := range full {
		transform[key] = value
	}
	// request() is also used for raw optimistic preconditions and must preserve
	// their observed values. Normalize only the WebSocket setter payload here.
	transform["rotation"] = rotationForOBS(m.Transform.Rotation.ValueFloat64())
	if m.Transform.BoundsType.ValueString() == "OBS_BOUNDS_NONE" {
		if m.Transform.BoundsWidth.ValueFloat64() == 0 {
			delete(transform, "boundsWidth")
		}
		if m.Transform.BoundsHeight.ValueFloat64() == 0 {
			delete(transform, "boundsHeight")
		}
	}
	return transform, nil
}

func (m bridgePlacementModel) validate() error {
	if m.Transform == nil || m.Index.IsNull() || m.Index.IsUnknown() || m.Index.ValueInt64() < 0 {
		return fmt.Errorf("placement and non-negative index are required")
	}
	if m.Enabled.IsNull() || m.Enabled.IsUnknown() || m.Locked.IsNull() || m.Locked.IsUnknown() {
		return fmt.Errorf("placement enabled/locked must be known")
	}
	t := m.Transform
	for name, v := range map[string]types.Float64{"x": t.X, "y": t.Y, "scale_x": t.ScaleX, "scale_y": t.ScaleY, "rotation": t.Rotation, "bounds_width": t.BoundsWidth, "bounds_height": t.BoundsHeight} {
		if v.IsNull() || v.IsUnknown() || math.IsNaN(v.ValueFloat64()) || math.IsInf(v.ValueFloat64(), 0) || math.Abs(v.ValueFloat64()) > math.MaxFloat32 {
			return fmt.Errorf("placement %s must be a finite float32", name)
		}
	}
	if t.Alignment.IsNull() || t.Alignment.IsUnknown() || t.BoundsAlignment.IsNull() || t.BoundsAlignment.IsUnknown() || t.BoundsType.IsNull() || t.BoundsType.IsUnknown() || t.CropToBounds.IsNull() || t.CropToBounds.IsUnknown() {
		return fmt.Errorf("placement alignment/bounds fields must be known")
	}
	if t.Alignment.ValueInt64() < 0 || t.Alignment.ValueInt64() > 10 || t.BoundsAlignment.ValueInt64() < 0 || t.BoundsAlignment.ValueInt64() > 10 {
		return fmt.Errorf("placement alignment fields are out of range")
	}
	allowedBounds := map[string]bool{"OBS_BOUNDS_NONE": true, "OBS_BOUNDS_STRETCH": true, "OBS_BOUNDS_SCALE_INNER": true, "OBS_BOUNDS_SCALE_OUTER": true, "OBS_BOUNDS_SCALE_TO_WIDTH": true, "OBS_BOUNDS_SCALE_TO_HEIGHT": true, "OBS_BOUNDS_MAX_ONLY": true}
	if !allowedBounds[t.BoundsType.ValueString()] {
		return fmt.Errorf("unsupported bounds_type %q", t.BoundsType.ValueString())
	}
	if t.BoundsWidth.ValueFloat64() < 0 || t.BoundsHeight.ValueFloat64() < 0 || (t.BoundsType.ValueString() != "OBS_BOUNDS_NONE" && (t.BoundsWidth.ValueFloat64() < 1 || t.BoundsHeight.ValueFloat64() < 1)) {
		return fmt.Errorf("placement bounds dimensions are invalid for bounds_type %q", t.BoundsType.ValueString())
	}
	for name, v := range map[string]types.Int64{"crop_left": t.CropLeft, "crop_top": t.CropTop, "crop_right": t.CropRight, "crop_bottom": t.CropBottom} {
		if v.IsNull() || v.IsUnknown() || v.ValueInt64() < 0 || v.ValueInt64() > math.MaxInt32 {
			return fmt.Errorf("placement %s is out of range", name)
		}
	}
	return nil
}

func bridgePlacementFromNative(v any) (bridgePlacementModel, error) {
	o := mapValue(v)
	tr := mapValue(o["sceneItemTransform"])
	if o == nil || tr == nil {
		return bridgePlacementModel{}, fmt.Errorf("bridge placement object is incomplete")
	}
	b, bok := o["sceneItemEnabled"].(bool)
	l, lok := o["sceneItemLocked"].(bool)
	index, iok := integerNumber(o["sceneItemIndex"])
	if !bok || !lok || !iok {
		return bridgePlacementModel{}, fmt.Errorf("bridge placement state fields are malformed")
	}
	f := func(key string) (types.Float64, bool) {
		v, ok := numberValue(tr[key])
		return types.Float64Value(v), ok
	}
	i := func(key string) (types.Int64, bool) {
		v, ok := integerNumber(tr[key])
		return types.Int64Value(v), ok
	}
	x, xok := f("positionX")
	y, yok := f("positionY")
	sx, sxok := f("scaleX")
	sy, syok := f("scaleY")
	rotation, rok := f("rotation")
	bw, bwok := f("boundsWidth")
	bh, bhok := f("boundsHeight")
	alignment, aok := i("alignment")
	ba, baok := i("boundsAlignment")
	cl, clok := i("cropLeft")
	ct, ctok := i("cropTop")
	cr, crok := i("cropRight")
	cb, cbok := i("cropBottom")
	cropToBounds, ctbok := tr["cropToBounds"].(bool)
	boundsType := stringValue(tr["boundsType"])
	if !(xok && yok && sxok && syok && rok && bwok && bhok && aok && baok && clok && ctok && crok && cbok && ctbok) || boundsType == "" {
		return bridgePlacementModel{}, fmt.Errorf("bridge transform fields are malformed")
	}
	return bridgePlacementModel{Enabled: types.BoolValue(b), Locked: types.BoolValue(l), Index: types.Int64Value(index), Transform: &bridgeTransformModel{X: x, Y: y, ScaleX: sx, ScaleY: sy, Rotation: rotation, Alignment: alignment, BoundsType: types.StringValue(boundsType), BoundsAlignment: ba, BoundsWidth: bw, BoundsHeight: bh, CropToBounds: types.BoolValue(cropToBounds), CropLeft: cl, CropTop: ct, CropRight: cr, CropBottom: cb}}, nil
}

func integerNumber(v any) (int64, bool) {
	n, ok := numberValue(v)
	if !ok || math.Trunc(n) != n || n < math.MinInt64 || n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func nativeBridgePlacement(want bridgePlacementModel) bridgePlacementModel {
	out := want
	if want.Transform == nil {
		return out
	}
	t := *want.Transform
	f32 := func(v types.Float64) types.Float64 { return types.Float64Value(float64(float32(v.ValueFloat64()))) }
	t.X, t.Y, t.ScaleX, t.ScaleY, t.Rotation = f32(t.X), f32(t.Y), f32(t.ScaleX), f32(t.ScaleY), f32(t.Rotation)
	t.BoundsWidth, t.BoundsHeight = f32(t.BoundsWidth), f32(t.BoundsHeight)
	out.Transform = &t
	return out
}

func bridgePlacementDifference(got, authored bridgePlacementModel) string {
	want := nativeBridgePlacement(authored)
	if got.Enabled.ValueBool() != want.Enabled.ValueBool() {
		return fmt.Sprintf("enabled=%t want %t", got.Enabled.ValueBool(), want.Enabled.ValueBool())
	}
	if got.Locked.ValueBool() != want.Locked.ValueBool() {
		return fmt.Sprintf("locked=%t want %t", got.Locked.ValueBool(), want.Locked.ValueBool())
	}
	if got.Index.ValueInt64() != want.Index.ValueInt64() {
		return fmt.Sprintf("index=%d want %d", got.Index.ValueInt64(), want.Index.ValueInt64())
	}
	if got.Transform == nil || want.Transform == nil {
		return "transform absent"
	}
	floatFields := []struct {
		name string
		got  types.Float64
		want types.Float64
	}{{"x", got.Transform.X, want.Transform.X}, {"y", got.Transform.Y, want.Transform.Y}, {"scale_x", got.Transform.ScaleX, want.Transform.ScaleX}, {"scale_y", got.Transform.ScaleY, want.Transform.ScaleY}, {"rotation", got.Transform.Rotation, want.Transform.Rotation}, {"bounds_width", got.Transform.BoundsWidth, want.Transform.BoundsWidth}, {"bounds_height", got.Transform.BoundsHeight, want.Transform.BoundsHeight}}
	for _, field := range floatFields {
		if field.name == "rotation" && rotationMatches(field.got.ValueFloat64(), authored.Transform.Rotation.ValueFloat64()) {
			continue
		}
		if field.got.ValueFloat64() != field.want.ValueFloat64() {
			return fmt.Sprintf("%s=%v want native %v", field.name, field.got.ValueFloat64(), field.want.ValueFloat64())
		}
	}
	intFields := []struct {
		name string
		got  types.Int64
		want types.Int64
	}{{"alignment", got.Transform.Alignment, want.Transform.Alignment}, {"bounds_alignment", got.Transform.BoundsAlignment, want.Transform.BoundsAlignment}, {"crop_left", got.Transform.CropLeft, want.Transform.CropLeft}, {"crop_top", got.Transform.CropTop, want.Transform.CropTop}, {"crop_right", got.Transform.CropRight, want.Transform.CropRight}, {"crop_bottom", got.Transform.CropBottom, want.Transform.CropBottom}}
	for _, field := range intFields {
		if field.got.ValueInt64() != field.want.ValueInt64() {
			return fmt.Sprintf("%s=%d want %d", field.name, field.got.ValueInt64(), field.want.ValueInt64())
		}
	}
	if got.Transform.BoundsType.ValueString() != want.Transform.BoundsType.ValueString() {
		return fmt.Sprintf("bounds_type=%s want %s", got.Transform.BoundsType.ValueString(), want.Transform.BoundsType.ValueString())
	}
	if got.Transform.CropToBounds.ValueBool() != want.Transform.CropToBounds.ValueBool() {
		return fmt.Sprintf("crop_to_bounds=%t want %t", got.Transform.CropToBounds.ValueBool(), want.Transform.CropToBounds.ValueBool())
	}
	return ""
}

func retainAuthoredBridgePlacement(observed, prior bridgePlacementModel) bridgePlacementModel {
	if bridgePlacementDifference(observed, prior) == "" {
		return prior
	}
	if observed.Transform != nil && prior.Transform != nil && !prior.Transform.Rotation.IsNull() && !prior.Transform.Rotation.IsUnknown() && rotationMatches(observed.Transform.Rotation.ValueFloat64(), prior.Transform.Rotation.ValueFloat64()) {
		tr := *observed.Transform
		tr.Rotation = prior.Transform.Rotation
		observed.Transform = &tr
	}
	return observed
}

type bridgeGroupObservation struct {
	SceneName, SceneUUID string
	Name, UUID           string
	ItemID               int64
	Width, Height        int64
	Placement            bridgePlacementModel
	Children             []bridgeChildObservation
}

type bridgeChildObservation struct {
	Name, UUID    string
	Kind          string
	ItemID        int64
	Width, Height int64
	Placement     bridgePlacementModel
}

func bridgeGroupFromResult(result map[string]any) (bridgeGroupObservation, error) {
	scene := mapValue(result["scene"])
	group := mapValue(result["group"])
	if scene == nil || group == nil {
		return bridgeGroupObservation{}, fmt.Errorf("bridge group result omitted scene/group")
	}
	itemID, iok := integerNumber(group["sceneItemId"])
	width, wok := integerNumber(group["groupWidth"])
	height, hok := integerNumber(group["groupHeight"])
	placement, err := bridgePlacementFromNative(group["placement"])
	if err != nil {
		return bridgeGroupObservation{}, err
	}
	out := bridgeGroupObservation{SceneName: stringValue(scene["sourceName"]), SceneUUID: stringValue(scene["sourceUuid"]), Name: stringValue(group["sourceName"]), UUID: stringValue(group["sourceUuid"]), ItemID: itemID, Width: width, Height: height, Placement: placement}
	if out.SceneName == "" || out.SceneUUID == "" || out.Name == "" || out.UUID == "" || !iok || !wok || !hok || itemID <= 0 || width < 0 || height < 0 {
		return bridgeGroupObservation{}, fmt.Errorf("bridge group identity/dimensions are malformed")
	}
	children, ok := result["children"].([]any)
	if !ok {
		return bridgeGroupObservation{}, fmt.Errorf("bridge group result omitted children array")
	}
	seenIDs := map[int64]bool{}
	for i, raw := range children {
		child, err := bridgeChildFromResult(mapValue(raw))
		if err != nil {
			return bridgeGroupObservation{}, fmt.Errorf("child %d: %w", i, err)
		}
		if seenIDs[child.ItemID] {
			return bridgeGroupObservation{}, fmt.Errorf("duplicate child item ID %d", child.ItemID)
		}
		seenIDs[child.ItemID] = true
		out.Children = append(out.Children, child)
	}
	return out, nil
}

func bridgeChildFromResult(child map[string]any) (bridgeChildObservation, error) {
	if child == nil {
		return bridgeChildObservation{}, fmt.Errorf("child is not an object")
	}
	itemID, iok := integerNumber(child["sceneItemId"])
	width, wok := integerNumber(child["sourceWidth"])
	height, hok := integerNumber(child["sourceHeight"])
	placement, err := bridgePlacementFromNative(child["placement"])
	if err != nil {
		return bridgeChildObservation{}, err
	}
	out := bridgeChildObservation{Name: stringValue(child["sourceName"]), UUID: stringValue(child["sourceUuid"]), Kind: stringValue(child["inputKind"]), ItemID: itemID, Width: width, Height: height, Placement: placement}
	if out.Name == "" || out.UUID == "" || out.Kind == "" || !iok || !wok || !hok || itemID <= 0 || width < 0 || height < 0 {
		return bridgeChildObservation{}, fmt.Errorf("child identity/dimensions are malformed")
	}
	return out, nil
}
