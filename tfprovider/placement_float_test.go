package tfprovider

import (
	"math"
	"math/rand"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPlacementFloatMatchesAbsorbsOnly32BitRounding(t *testing.T) {
	same := [][2]float64{{0.7500000596046448, 0.75}, {0.9999999403953552, 1}, {0.4999999701976776, 0.5}, {0, 0}, {1920, 1920}, {-250.5, -250.5}, {float64(float32(0.333)), 0.333}, {float64(float32(1234.56)), 1234.56}}
	for _, c := range same {
		if !placementFloatMatches(c[0], c[1]) || !placementFloatMatches(c[1], c[0]) {
			t.Errorf("%v and %v should match", c[0], c[1])
		}
	}
	different := [][2]float64{{0.75, 0.7501}, {1, 1.00001}, {100, 100.01}, {0, 0.00001}, {1920, 1921}, {3000, 3000.05}, {-1, 1}, {math.NaN(), 1}, {math.Inf(1), 1e300}, {1, math.NaN()}}
	for _, c := range different {
		if placementFloatMatches(c[0], c[1]) {
			t.Errorf("%v and %v should differ", c[0], c[1])
		}
	}
}

// Whatever number is authored, a value that only went through OBS's 32-bit, canvas-relative
// storage must compare equal, and anything a person could have moved must not.
func TestRandomPlacementValuesSurviveCanvasRelativeRounding(t *testing.T) {
	rng := rand.New(rand.NewSource(20261115))
	for i := 0; i < 200000; i++ {
		canvas := float32(2 * (160 + rng.Intn(8000)))
		var v float64
		switch rng.Intn(4) {
		case 0:
			v = float64(rng.Intn(8000)) / 4
		case 1:
			v = math.Round(rng.Float64()*8000*1000) / 1000
		case 2:
			v = math.Round((rng.Float64()*8-4)*10000) / 10000
		default:
			v = rng.Float64()*16000 - 8000
		}
		// Divide by the canvas and multiply back, each step in 32-bit, as OBS stores it.
		stored := float64(float32(v) / canvas * canvas)
		if !placementFloatMatches(stored, v) {
			t.Fatalf("case %d: %v through canvas %v came back %v and was called different", i, v, canvas, stored)
		}
		nudged := v + (0.01+rng.Float64())*(1-2*float64(rng.Intn(2)))
		if placementFloatMatches(nudged, v) {
			t.Fatalf("case %d: %v and %v were called the same", i, v, nudged)
		}
	}
}

func TestRetainAuthoredTransformKeepsAuthoredNumbersAcrossRounding(t *testing.T) {
	f := types.Float64Value
	authored := &transformModel{X: f(100), Y: f(200), ScaleX: f(0.75), ScaleY: f(1), Rotation: f(0), BoundsWidth: f(640), BoundsHeight: types.Float64Null()}
	observed := transformModel{X: f(100.00000762939453), Y: f(200), ScaleX: f(0.7500000596046448), ScaleY: f(0.9999999403953552), Rotation: f(0), BoundsWidth: f(640.0000610351562), BoundsHeight: f(0)}
	got := retainAuthoredTransform(observed, authored)
	if got.X != authored.X || got.ScaleX != authored.ScaleX || got.ScaleY != authored.ScaleY || got.BoundsWidth != authored.BoundsWidth || !got.BoundsHeight.IsNull() {
		t.Fatalf("rounding leaked into state: %+v", got)
	}
	moved := observed
	moved.X, moved.ScaleX = f(137), f(0.8)
	got = retainAuthoredTransform(moved, authored)
	if got.X.ValueFloat64() != 137 || got.ScaleX.ValueFloat64() != 0.8 {
		t.Fatalf("real drift was hidden: %+v", got)
	}
}
