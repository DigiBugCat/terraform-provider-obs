package tfprovider

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Randomised sweep over base canvases: for any live canvas and any configured canvas,
// the write goes through exactly when the two are equal, and a refused write never
// reaches OBS. The seed is fixed so a failure reproduces; the case is in the message.
func TestRandomCanvasesAreAcceptedOnlyWhenTheyMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(20261115))
	common := [][2]int{{1920, 1080}, {1280, 720}, {2560, 1440}, {3840, 2160}, {1080, 1920}, {720, 1280}, {3440, 1440}, {5120, 1440}, {1920, 1200}, {1, 1}, {maxCanvasSide, maxCanvasSide}}
	pick := func() [2]int {
		if rng.Intn(3) == 0 {
			return common[rng.Intn(len(common))]
		}
		return [2]int{1 + rng.Intn(maxCanvasSide), 1 + rng.Intn(maxCanvasSide)}
	}
	accepted, refused := 0, 0
	for i := 0; i < 5000; i++ {
		live := pick()
		var cfg [2]int
		switch rng.Intn(5) {
		case 0: // provider block left at its default
		case 1: // configured for exactly what OBS has
			cfg = live
		case 2: // sides swapped
			cfg = [2]int{live[1], live[0]}
		case 3: // off by one pixel on one side
			cfg = live
			side := rng.Intn(2)
			if cfg[side] == 1 || (cfg[side] < maxCanvasSide && rng.Intn(2) == 0) {
				cfg[side]++
			} else {
				cfg[side]--
			}
		default:
			cfg = pick()
		}
		// Through the same path the provider block takes.
		var w, h types.Int64 = types.Int64Null(), types.Int64Null()
		if cfg != ([2]int{}) {
			w, h = types.Int64Value(int64(cfg[0])), types.Int64Value(int64(cfg[1]))
		}
		got, err := canvasFromConfig(w, h)
		if err != nil || got != cfg {
			t.Fatalf("case %d: canvasFromConfig(%v)=%v err=%v", i, cfg, got, err)
		}
		expect := cfg
		if expect == ([2]int{}) {
			expect = [2]int{requiredCanvasWidth, requiredCanvasHeight}
		}
		f := &fakeRequester{responses: map[string]map[string]any{
			"GetVersion":             {"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"SetSceneItemTransform"}},
			"GetSceneCollectionList": {"currentSceneCollectionName": "Reference Terraform Lab"},
			"GetVideoSettings":       {"baseWidth": float64(live[0]), "baseHeight": float64(live[1])},
			"SetSceneItemTransform":  {},
		}}
		_, err = mutation(context.Background(), exclusiveRequester{f, &preflightScope{canvas: got}}, "Reference Terraform Lab", "SetSceneItemTransform", map[string]any{})
		sent := false
		for _, c := range f.calls {
			sent = sent || c == "SetSceneItemTransform"
		}
		if live == expect {
			accepted++
			if err != nil || !sent {
				t.Fatalf("case %d: live %v configured %v should write: err=%v sent=%v", i, live, cfg, err, sent)
			}
			continue
		}
		refused++
		if err == nil || sent || !strings.Contains(err.Error(), "unexpected live canvas") {
			t.Fatalf("case %d: live %v configured %v should refuse before writing: err=%v sent=%v", i, live, cfg, err, sent)
		}
	}
	if accepted < 500 || refused < 500 {
		t.Fatalf("sweep is lopsided: accepted=%d refused=%d", accepted, refused)
	}
	t.Logf("accepted=%d refused=%d", accepted, refused)
}
