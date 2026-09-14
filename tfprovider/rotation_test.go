package tfprovider

import (
	"math"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRotationTransportAndExactEquivalence(t *testing.T) {
	for _, tc := range []struct{ authored, wire float64 }{
		{12524.91015625, 284.91015625}, {-12524.91015625, -284.91015625},
		{360, 360}, {-360, -360}, {720, 0}, {-720, 0}, {721.1, float64(float32(1.1))}, {-721.1, float64(float32(-1.1))},
		{0.1, float64(float32(0.1))}, {-0.1, float64(float32(-0.1))},
	} {
		if got := rotationForOBS(tc.authored); got != tc.wire {
			t.Fatalf("rotation %v encoded %v want %v", tc.authored, got, tc.wire)
		}
		if !rotationMatches(tc.wire, tc.authored) {
			t.Fatalf("own readback rejected for %v", tc.authored)
		}
		if rotationMatches(tc.wire+0.5, tc.authored) {
			t.Fatalf("angular drift hidden for %v", tc.authored)
		}
	}
	if !rotationMatches(0, 360) || !rotationMatches(-360, 360) {
		t.Fatal("full turns not equivalent")
	}
	if rotationMatches(0, -1e-7) {
		t.Fatal("small real angular drift hidden")
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if rotationMatches(bad, 0) || rotationMatches(0, bad) {
			t.Fatal("nonfinite rotation accepted")
		}
	}
}

func TestRotationRefreshRetainsAuthoredAndReportsRealDrift(t *testing.T) {
	want := expectedTransform()
	want.Rotation = types.Float64Value(12524.91015625)
	got := want
	got.Rotation = types.Float64Value(284.91015625)
	if !transformEqual(got, want) || transformDifference(got, want) != "" {
		t.Fatal("equivalent angle failed postcondition")
	}
	got.X = types.Float64Value(999)
	kept := retainAuthoredTransform(got, &want)
	if kept.Rotation != want.Rotation || kept.X.ValueFloat64() != 999 {
		t.Fatal("rotation preservation hid unrelated drift")
	}
	got.Rotation = types.Float64Value(285)
	kept = retainAuthoredTransform(got, &want)
	if kept.Rotation != got.Rotation || transformDifference(got, want) == "" {
		t.Fatal("real rotation drift hidden")
	}
}

func TestBridgeRotationSetterNormalizesButRawPreconditionsRemainExact(t *testing.T) {
	// Use the same complete native placement parser as actual bridge reads.
	tr := expectedTransform().native()
	tr["rotation"] = 12524.91015625
	tr["boundsWidth"], tr["boundsHeight"], tr["cropToBounds"] = float64(0), float64(0), false
	want, err := bridgePlacementFromNative(map[string]any{"sceneItemEnabled": true, "sceneItemLocked": false, "sceneItemIndex": float64(0), "sceneItemTransform": tr})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := want.coreWebSocketTransformRequest()
	if err != nil {
		t.Fatal(err)
	}
	if wire["rotation"] != 284.91015625 {
		t.Fatal("group setter failed to normalize")
	}
	raw, err := want.request()
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(raw["sceneItemTransform"])["rotation"] != 12524.91015625 {
		t.Fatal("raw optimistic precondition changed")
	}
	observed := nativeBridgePlacement(want)
	observed.Transform.Rotation = types.Float64Value(284.91015625)
	if bridgePlacementDifference(observed, want) != "" {
		t.Fatal("normalized bridge readback rejected")
	}
	observed.Enabled = types.BoolValue(false)
	kept := retainAuthoredBridgePlacement(observed, want)
	if kept.Enabled.ValueBool() || kept.Transform.Rotation != want.Transform.Rotation {
		t.Fatal("bridge refresh lost authored rotation or hid visibility drift")
	}
}
