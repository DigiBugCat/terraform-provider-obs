package tfprovider

import (
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Optional bounds fields are unmanaged when omitted, preserving existing
// configurations and native disabled-bounds metadata across refreshes.
func retainAuthoredTransform(observed transformModel, authored *transformModel) *transformModel {
	if authored != nil {
		if !authored.Rotation.IsNull() && !authored.Rotation.IsUnknown() && !observed.Rotation.IsNull() && !observed.Rotation.IsUnknown() && rotationMatches(observed.Rotation.ValueFloat64(), authored.Rotation.ValueFloat64()) {
			observed.Rotation = authored.Rotation
		}
		if authored.BoundsWidth.IsNull() {
			observed.BoundsWidth = types.Float64Null()
		}
		if authored.BoundsHeight.IsNull() {
			observed.BoundsHeight = types.Float64Null()
		}
		if authored.CropToBounds.IsNull() {
			observed.CropToBounds = types.BoolNull()
		}
	}
	return &observed
}

func transformBoundsDifference(observed, desired transformModel) string {
	for _, field := range []struct {
		name      string
		got, want types.Float64
	}{
		{"bounds_width", observed.BoundsWidth, desired.BoundsWidth},
		{"bounds_height", observed.BoundsHeight, desired.BoundsHeight},
	} {
		if field.want.IsNull() {
			continue
		}
		if field.want.IsUnknown() || field.got.IsNull() || field.got.IsUnknown() {
			return field.name + " is unknown or null"
		}
		if field.got.ValueFloat64() != field.want.ValueFloat64() {
			return fmt.Sprintf("%s=%v want %v", field.name, field.got.ValueFloat64(), field.want.ValueFloat64())
		}
	}
	if !desired.CropToBounds.IsNull() {
		if desired.CropToBounds.IsUnknown() || observed.CropToBounds.IsNull() || observed.CropToBounds.IsUnknown() {
			return "crop_to_bounds is unknown or null"
		}
		if observed.CropToBounds.ValueBool() != desired.CropToBounds.ValueBool() {
			return fmt.Sprintf("crop_to_bounds=%t want %t", observed.CropToBounds.ValueBool(), desired.CropToBounds.ValueBool())
		}
	}
	return ""
}

func validateTransformBounds(t transformModel) error {
	if t.Rotation.IsNull() || t.Rotation.IsUnknown() || math.IsNaN(t.Rotation.ValueFloat64()) || math.IsInf(t.Rotation.ValueFloat64(), 0) {
		return fmt.Errorf("rotation must be a known finite number")
	}
	for _, v := range []types.Float64{t.BoundsWidth, t.BoundsHeight} {
		if v.IsUnknown() {
			return fmt.Errorf("bounds dimensions must be known before mutation")
		}
		if !v.IsNull() && (math.IsNaN(v.ValueFloat64()) || math.IsInf(v.ValueFloat64(), 0) || v.ValueFloat64() < 0) {
			return fmt.Errorf("bounds dimensions must be finite and nonnegative")
		}
	}
	if t.BoundsType.ValueString() != "OBS_BOUNDS_NONE" && (t.BoundsWidth.IsNull() || t.BoundsHeight.IsNull() || t.BoundsWidth.ValueFloat64() <= 0 || t.BoundsHeight.ValueFloat64() <= 0) {
		return fmt.Errorf("active bounds_type requires positive bounds_width and bounds_height")
	}
	return nil
}
