package tfprovider

import (
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// placementFloatTolerance is how far a position, scale or bounds size read back from OBS
// may sit from the authored number and still count as the same value. OBS keeps scene
// item transforms as 32-bit floats relative to the base canvas, so on a canvas whose
// sides do not divide evenly a write of 0.75 can read back as 0.7500000596. That is one
// step of a 32-bit float; the tolerance allows eight (about one part in a million, a few
// thousandths of a pixel on a 4K canvas) and nothing a person or a render could see.
const placementFloatTolerance = 1e-6

func placementFloatMatches(observed, authored float64) bool {
	if observed == authored {
		return true
	}
	if math.IsNaN(observed) || math.IsInf(observed, 0) || math.IsNaN(authored) || math.IsInf(authored, 0) {
		return false
	}
	return math.Abs(observed-authored) <= placementFloatTolerance*math.Max(1, math.Max(math.Abs(observed), math.Abs(authored)))
}

// retainAuthoredFloat keeps the authored number in state when OBS holds the same value
// up to 32-bit rounding, so a refresh does not report drift nobody caused.
func retainAuthoredFloat(observed, authored types.Float64) types.Float64 {
	if authored.IsNull() || authored.IsUnknown() || observed.IsNull() || observed.IsUnknown() {
		return observed
	}
	if placementFloatMatches(observed.ValueFloat64(), authored.ValueFloat64()) {
		return authored
	}
	return observed
}

// Optional bounds fields are unmanaged when omitted, preserving existing
// configurations and native disabled-bounds metadata across refreshes.
func retainAuthoredTransform(observed transformModel, authored *transformModel) *transformModel {
	if authored != nil {
		observed.X, observed.Y = retainAuthoredFloat(observed.X, authored.X), retainAuthoredFloat(observed.Y, authored.Y)
		observed.ScaleX, observed.ScaleY = retainAuthoredFloat(observed.ScaleX, authored.ScaleX), retainAuthoredFloat(observed.ScaleY, authored.ScaleY)
		observed.BoundsWidth, observed.BoundsHeight = retainAuthoredFloat(observed.BoundsWidth, authored.BoundsWidth), retainAuthoredFloat(observed.BoundsHeight, authored.BoundsHeight)
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
		if !placementFloatMatches(field.got.ValueFloat64(), field.want.ValueFloat64()) {
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
