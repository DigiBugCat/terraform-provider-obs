package tfprovider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOptionalTransformBoundsPreserveUnmanagedValues(t *testing.T) {
	want := expectedTransform()
	want.BoundsWidth, want.BoundsHeight, want.CropToBounds = types.Float64Null(), types.Float64Null(), types.BoolNull()
	got := want
	got.BoundsWidth, got.BoundsHeight, got.CropToBounds = types.Float64Value(640), types.Float64Value(480), types.BoolValue(true)
	if !transformEqual(got, want) {
		t.Fatal("omitted native metadata became managed")
	}
	retained := retainAuthoredTransform(got, &want)
	if !retained.BoundsWidth.IsNull() || !retained.BoundsHeight.IsNull() || !retained.CropToBounds.IsNull() {
		t.Fatal("refresh adopted omitted metadata")
	}
	for _, key := range []string{"boundsWidth", "boundsHeight", "cropToBounds"} {
		if _, ok := want.native()[key]; ok {
			t.Fatalf("omitted field was sent: %s", key)
		}
	}
}

func TestActiveTransformBoundsAreSentAndChecked(t *testing.T) {
	want := expectedTransform()
	want.BoundsType = types.StringValue("OBS_BOUNDS_SCALE_INNER")
	want.BoundsWidth, want.BoundsHeight, want.CropToBounds = types.Float64Value(640), types.Float64Value(480), types.BoolValue(true)
	if err := validateTransformBounds(want); err != nil {
		t.Fatal(err)
	}
	if want.native()["boundsWidth"] != float64(640) || want.native()["boundsHeight"] != float64(480) || want.native()["cropToBounds"] != true {
		t.Fatal("bounded wire transform incomplete")
	}
	for _, field := range []string{"width", "height", "crop"} {
		got := want
		switch field {
		case "width":
			got.BoundsWidth = types.Float64Value(641)
		case "height":
			got.BoundsHeight = types.Float64Value(481)
		case "crop":
			got.CropToBounds = types.BoolValue(false)
		}
		if transformEqual(got, want) || transformDifference(got, want) == "" {
			t.Fatalf("ignored %s drift", field)
		}
	}
	want.BoundsWidth = types.Float64Null()
	if validateTransformBounds(want) == nil {
		t.Fatal("active bounds accepted without width")
	}
	want.BoundsWidth = types.Float64Value(0)
	if validateTransformBounds(want) == nil {
		t.Fatal("active bounds accepted zero width")
	}
}
