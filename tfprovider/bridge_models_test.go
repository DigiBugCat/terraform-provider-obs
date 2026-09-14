package tfprovider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func testBridgePlacement() bridgePlacementModel {
	return bridgePlacementModel{Enabled: types.BoolValue(true), Locked: types.BoolValue(false), Index: types.Int64Value(2), Transform: &bridgeTransformModel{
		X: types.Float64Value(10), Y: types.Float64Value(20), ScaleX: types.Float64Value(1), ScaleY: types.Float64Value(0.5), Rotation: types.Float64Value(0),
		Alignment: types.Int64Value(5), BoundsType: types.StringValue("OBS_BOUNDS_NONE"), BoundsAlignment: types.Int64Value(0), BoundsWidth: types.Float64Value(0), BoundsHeight: types.Float64Value(0), CropToBounds: types.BoolValue(false),
		CropLeft: types.Int64Value(0), CropTop: types.Int64Value(1), CropRight: types.Int64Value(2), CropBottom: types.Int64Value(3),
	}}
}

func TestBridgePlacementRequestRoundTrip(t *testing.T) {
	want := testBridgePlacement()
	native, err := want.request()
	if err != nil {
		t.Fatal(err)
	}
	got, err := bridgePlacementFromNative(native)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled.ValueBool() != want.Enabled.ValueBool() || got.Locked.ValueBool() != want.Locked.ValueBool() || got.Index.ValueInt64() != want.Index.ValueInt64() || got.Transform.X.ValueFloat64() != want.Transform.X.ValueFloat64() || got.Transform.CropBottom.ValueInt64() != want.Transform.CropBottom.ValueInt64() {
		t.Fatalf("round trip got=%#v want=%#v", got, want)
	}
}

func TestCoreWebSocketTransformOmitsIgnoredNoneBoundsWithoutChangingRawModel(t *testing.T) {
	want := testBridgePlacement()
	raw, err := want.request()
	if err != nil {
		t.Fatal(err)
	}
	rawTransform := mapValue(raw["sceneItemTransform"])
	if rawTransform["boundsWidth"] != float64(0) || rawTransform["boundsHeight"] != float64(0) {
		t.Fatalf("complete bridge/raw placement lost zero bounds: %#v", rawTransform)
	}

	setter, err := want.coreWebSocketTransformRequest()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := setter["boundsWidth"]; present {
		t.Fatalf("OBS_BOUNDS_NONE setter included schema-invalid boundsWidth: %#v", setter)
	}
	if _, present := setter["boundsHeight"]; present {
		t.Fatalf("OBS_BOUNDS_NONE setter included schema-invalid boundsHeight: %#v", setter)
	}
	if want.Transform.BoundsWidth.ValueFloat64() != 0 || want.Transform.BoundsHeight.ValueFloat64() != 0 {
		t.Fatalf("setter encoding changed authored raw bounds: %#v", want.Transform)
	}
	observed := nativeBridgePlacement(want)
	if diff := bridgePlacementDifference(observed, want); diff != "" {
		t.Fatalf("omitted setter fields changed raw/readback comparison: %s", diff)
	}
	positiveNone := testBridgePlacement()
	positiveNone.Transform.BoundsWidth = types.Float64Value(640)
	setter, err = positiveNone.coreWebSocketTransformRequest()
	if err != nil {
		t.Fatal(err)
	}
	if setter["boundsWidth"] != float64(640) {
		t.Fatalf("positive OBS_BOUNDS_NONE width was silently omitted: %#v", setter)
	}
	if _, present := setter["boundsHeight"]; present {
		t.Fatalf("zero OBS_BOUNDS_NONE height was not independently omitted: %#v", setter)
	}

	bounded := testBridgePlacement()
	bounded.Transform.BoundsType = types.StringValue("OBS_BOUNDS_STRETCH")
	bounded.Transform.BoundsWidth = types.Float64Value(640)
	bounded.Transform.BoundsHeight = types.Float64Value(360)
	setter, err = bounded.coreWebSocketTransformRequest()
	if err != nil {
		t.Fatal(err)
	}
	if setter["boundsWidth"] != float64(640) || setter["boundsHeight"] != float64(360) {
		t.Fatalf("bounded setter omitted meaningful dimensions: %#v", setter)
	}
	bounded.Transform.BoundsWidth = types.Float64Value(0)
	if _, err := bounded.coreWebSocketTransformRequest(); err == nil {
		t.Fatal("bounded transform with schema-invalid zero width was accepted")
	}
}

func TestBridgePlacementRejectsMalformedNativeIntegers(t *testing.T) {
	native, err := testBridgePlacement().request()
	if err != nil {
		t.Fatal(err)
	}
	native["sceneItemIndex"] = 1.5
	if _, err := bridgePlacementFromNative(native); err == nil {
		t.Fatal("fractional native index accepted")
	}
}

func TestBridgePlacementValidatesCompleteTransform(t *testing.T) {
	for _, edit := range []func(*bridgePlacementModel){
		func(p *bridgePlacementModel) { p.Index = types.Int64Value(-1) },
		func(p *bridgePlacementModel) { p.Transform.BoundsType = types.StringValue("bogus") },
		func(p *bridgePlacementModel) { p.Transform.CropLeft = types.Int64Value(-1) },
		func(p *bridgePlacementModel) { p.Transform.ScaleX = types.Float64Unknown() },
	} {
		p := testBridgePlacement()
		edit(&p)
		if _, err := p.request(); err == nil {
			t.Fatalf("invalid placement accepted: %#v", p)
		}
	}
}

func TestNativeGroupPositionIsRawFloat32ExactNotEpsilon(t *testing.T) {
	authored := testBridgePlacement()
	authored.Transform.X = types.Float64Value(0)
	authored.Transform.Y = types.Float64Value(0)
	native := nativeBridgePlacement(authored)
	if got := native.Transform.X.ValueFloat64(); got != 0 {
		t.Fatalf("native raw x=%v", got)
	}
	if diff := bridgePlacementDifference(native, authored); diff != "" {
		t.Fatalf("raw placement mismatch=%s", diff)
	}
	drift := native
	driftTransform := *native.Transform
	driftTransform.X = types.Float64Value(native.Transform.X.ValueFloat64() + 0.000001)
	drift.Transform = &driftTransform
	if diff := bridgePlacementDifference(drift, authored); diff == "" {
		t.Fatal("nearby raw drift was hidden")
	}
}

func TestBridgeGroupResultRequiresUniqueExactChildren(t *testing.T) {
	placement, err := testBridgePlacement().request()
	if err != nil {
		t.Fatal(err)
	}
	child := func(name, uuid string, itemID int64) map[string]any {
		return map[string]any{"sourceName": name, "sourceUuid": uuid, "inputKind": "image_source", "sceneItemId": float64(itemID), "sourceWidth": 960.0, "sourceHeight": 1080.0, "placement": placement}
	}
	result := map[string]any{
		"scene":    map[string]any{"sourceName": "Office", "sourceUuid": "scene-1"},
		"group":    map[string]any{"sourceName": "Party Lights", "sourceUuid": "group-1", "sceneItemId": 7.0, "groupWidth": 1920.0, "groupHeight": 1080.0, "placement": placement},
		"children": []any{child("Left", "left-1", 1), child("Right", "right-1", 2)},
	}
	got, err := bridgeGroupFromResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if got.UUID != "group-1" || got.ItemID != 7 || len(got.Children) != 2 || got.Children[1].UUID != "right-1" {
		t.Fatalf("group=%#v", got)
	}
	result["children"] = []any{child("Left", "left-1", 1), child("Right", "right-1", 1)}
	if _, err := bridgeGroupFromResult(result); err == nil {
		t.Fatal("duplicate child numeric ID accepted")
	}
}
