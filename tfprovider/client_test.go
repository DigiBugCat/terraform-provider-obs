package tfprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

type fakeRequester struct {
	responses map[string]map[string]any
	calls     []string
}

type sequenceRequester struct {
	responses []map[string]any
	calls     int
}

func (s *sequenceRequester) Req(_ context.Context, typ string, _ map[string]any) (map[string]any, error) {
	if typ != "GetSceneItemList" {
		return nil, fmt.Errorf("unexpected %s", typ)
	}
	i := s.calls
	if i >= len(s.responses) {
		i = len(s.responses) - 1
	}
	s.calls++
	return s.responses[i], nil
}

func nativeItem(x float64) map[string]any {
	return map[string]any{"sceneItems": []any{map[string]any{
		"sceneItemId": 1.0, "sceneItemIndex": 0.0, "sourceName": "Mini Camera", "sourceUuid": "source-1", "sceneItemEnabled": true,
		"sceneItemTransform": map[string]any{"positionX": x, "positionY": 40.0, "scaleX": 1.0, "scaleY": 1.0, "rotation": 0.0, "cropLeft": 0.0, "cropTop": 0.0, "cropRight": 0.0, "cropBottom": 0.0, "alignment": 5.0, "boundsType": "OBS_BOUNDS_NONE", "boundsAlignment": 0.0},
	}}}
}

func (f *fakeRequester) Req(_ context.Context, typ string, _ map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, typ)
	x, ok := f.responses[typ]
	if !ok {
		return nil, fmt.Errorf("unexpected %s", typ)
	}
	return x, nil
}

func TestMalformedListIsUnknownNotAbsence(t *testing.T) {
	f := &fakeRequester{responses: map[string]map[string]any{"GetInputList": {"inputs": "not-an-array"}}}
	_, err := inputs(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "want array") {
		t.Fatalf("err=%v", err)
	}
}

func TestMutationPreflightRefusesUnknownCanvasBeforeWrite(t *testing.T) {
	f := &fakeRequester{responses: map[string]map[string]any{
		"GetVersion":             {"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"SetSceneItemTransform"}},
		"GetSceneCollectionList": {"currentSceneCollectionName": "Reference Terraform Lab"},
		"GetVideoSettings":       {"baseWidth": 1920.0},
	}}
	_, err := mutationPreflight(context.Background(), f, "Reference Terraform Lab", "SetSceneItemTransform")
	if err == nil || !strings.Contains(err.Error(), "numeric base canvas") {
		t.Fatalf("err=%v", err)
	}
	for _, c := range f.calls {
		if c == "SetSceneItemTransform" {
			t.Fatal("mutation was sent")
		}
	}
}

func TestMutationPreflightRefusesCapabilityCollectionAndActiveOutput(t *testing.T) {
	cases := []struct {
		name, want string
		responses  map[string]map[string]any
	}{
		{"missing capability", "does not advertise", map[string]map[string]any{"GetVersion": {"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{}}}},
		{"wrong collection", "current collection", map[string]map[string]any{"GetVersion": {"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"SetSceneItemTransform"}}, "GetSceneCollectionList": {"currentSceneCollectionName": "MAIN"}}},
		{"active output", "reports active output", map[string]map[string]any{"GetVersion": {"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"SetSceneItemTransform", "GetStreamStatus"}}, "GetSceneCollectionList": {"currentSceneCollectionName": "Reference Terraform Lab"}, "GetVideoSettings": {"baseWidth": 1920.0, "baseHeight": 1080.0}, "GetStreamStatus": {"outputActive": true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRequester{responses: tc.responses}
			_, err := mutation(context.Background(), f, "Reference Terraform Lab", "SetSceneItemTransform", map[string]any{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
			for _, call := range f.calls {
				if call == "SetSceneItemTransform" {
					t.Fatal("unsafe mutation was sent")
				}
			}
		})
	}
}

func expectedTransform() transformModel {
	return transformFromNative(map[string]any{"positionX": 120.0, "positionY": 40.0, "scaleX": 1.0, "scaleY": 1.0, "rotation": 0.0, "cropLeft": 0.0, "cropTop": 0.0, "cropRight": 0.0, "cropBottom": 0.0, "alignment": 5.0, "boundsType": "OBS_BOUNDS_NONE", "boundsAlignment": 0.0})
}

func TestWaitItemExpectedAcceptsExactAndDelayedConvergence(t *testing.T) {
	f := &sequenceRequester{responses: []map[string]any{nativeItem(200), nativeItem(120)}}
	if _, err := waitItemExpected(context.Background(), f, "Office", 1, "source-1", true, 0, expectedTransform(), 2, 0); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatalf("calls=%d", f.calls)
	}
}

func TestWaitItemExpectedRejectsIgnoredMutation(t *testing.T) {
	f := &sequenceRequester{responses: []map[string]any{nativeItem(200)}}
	_, err := waitItemExpected(context.Background(), f, "Office", 1, "source-1", true, 0, expectedTransform(), 2, 0)
	if err == nil || !strings.Contains(err.Error(), "did not converge") {
		t.Fatalf("err=%v", err)
	}
}

// OBS 32 relative-coordinate scenes snap top-level item positions to half
// pixels. Report the real disagreement, rather than framework pointer internals;
// do not weaken exact postconditions to hide an unrepresentable configuration.
func TestOfficeHalfPixelPostconditionDiagnostic(t *testing.T) {
	want := expectedTransform()
	want.X = types.Float64Value(396.75006103515625)
	got := itemObservation{SourceUUID: "source-1", Enabled: true, Index: 0, Transform: want}
	got.Transform.X = types.Float64Value(397)
	if mismatch := itemMismatch(got, "source-1", true, 0, want); mismatch != "transform: x=397 want 396.75006103515625" {
		t.Fatalf("unhelpful mismatch: %s", mismatch)
	}
	want.X = types.Float64Value(397)
	if mismatch := itemMismatch(got, "source-1", true, 0, want); mismatch != "" {
		t.Fatalf("representable position rejected: %s", mismatch)
	}
	got.Transform.X = types.Float64Value(397.5)
	if mismatch := itemMismatch(got, "source-1", true, 0, want); mismatch == "" {
		t.Fatal("real half-pixel drift was ignored")
	}
}

func TestItemMismatchCoversEveryNonTransformPostcondition(t *testing.T) {
	base := itemObservation{SourceUUID: "source-1", Enabled: true, Index: 0, Transform: expectedTransform()}
	cases := []struct {
		name string
		edit func(*itemObservation)
		want string
	}{
		{"source rebound", func(x *itemObservation) { x.SourceUUID = "source-2" }, "source UUID"},
		{"enabled ignored", func(x *itemObservation) { x.Enabled = false }, "enabled"},
		{"index ignored", func(x *itemObservation) { x.Index = 2 }, "index"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := base
			tc.edit(&got)
			if mismatch := itemMismatch(got, "source-1", true, 0, expectedTransform()); !strings.Contains(mismatch, tc.want) {
				t.Fatalf("mismatch=%q", mismatch)
			}
		})
	}
}

func TestWaitInputExpectedRejectsSettingsMismatch(t *testing.T) {
	f := &fakeRequester{responses: map[string]map[string]any{
		"GetInputList":            {"inputs": []any{map[string]any{"inputName": "Mini Camera", "inputUuid": "source-1", "inputKind": "image_source"}}},
		"GetInputSettings":        {"inputKind": "image_source", "inputSettings": map[string]any{"file": "wrong.png", "unload": false}},
		"GetInputDefaultSettings": {"defaultInputSettings": map[string]any{"file": "", "unload": false}},
	}}
	_, err := waitInputExpected(context.Background(), f, "source-1", "Mini Camera", "image_source", "camera.png", false, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "file=") {
		t.Fatalf("err=%v", err)
	}
}

func TestObservedImageSettingsUsesTypedLiveOrDefaultValues(t *testing.T) {
	f := &fakeRequester{responses: map[string]map[string]any{"GetInputDefaultSettings": {"defaultInputSettings": map[string]any{"file": "default.png", "unload": false}}}}
	file, unload, err := observedImageSettings(context.Background(), f, inputObservation{UUID: "image-1", Settings: map[string]any{"file": "live.png"}})
	if err != nil || file != "live.png" || unload {
		t.Fatalf("file=%q unload=%t err=%v", file, unload, err)
	}
	for _, bad := range []map[string]any{{"file": "live.png", "unload": "false"}, {"file": 3.0, "unload": false}} {
		if _, _, err := observedImageSettings(context.Background(), f, inputObservation{UUID: "image-1", Settings: bad}); err == nil {
			t.Fatalf("malformed settings accepted: %#v", bad)
		}
	}
}

func TestInputByUUIDRejectsMissingOrConflictingSettingsKind(t *testing.T) {
	for _, settingsKind := range []any{nil, "ffmpeg_source"} {
		f := &fakeRequester{responses: map[string]map[string]any{
			"GetInputList":     {"inputs": []any{map[string]any{"inputName": "Mini Camera", "inputUuid": "source-1", "inputKind": "image_source"}}},
			"GetInputSettings": {"inputKind": settingsKind, "inputSettings": map[string]any{"file": "camera.png", "unload": false}},
		}}
		if _, _, err := inputByUUID(context.Background(), f, "source-1"); err == nil || (!strings.Contains(err.Error(), "omitted") && !strings.Contains(err.Error(), "disagrees")) {
			t.Fatalf("settingsKind=%#v err=%v", settingsKind, err)
		}
		for _, call := range f.calls {
			if call == "GetInputDefaultSettings" {
				t.Fatal("malformed kind bypassed directly into defaults")
			}
		}
	}
}

func TestWaitItemExpectedHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &sequenceRequester{responses: []map[string]any{nativeItem(200)}}
	_, err := waitItemExpected(ctx, f, "Office", 1, "source-1", true, 0, expectedTransform(), 2, time.Second)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
}

func TestTraceFailuresAndPostconditionReadErrorsAreNotDiscarded(t *testing.T) {
	t.Run("refusal receipt write", func(t *testing.T) {
		t.Setenv("OBSTF_TF_TRACE_PATH", filepath.Join(t.TempDir(), "missing", "trace.jsonl"))
		f := &fakeRequester{responses: map[string]map[string]any{"GetVersion": {"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{}}}}
		_, err := mutation(context.Background(), f, "Reference Terraform Lab", "SetSceneItemTransform", map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "receipt write also failed") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("read error emits uncertain event", func(t *testing.T) {
		trace := filepath.Join(t.TempDir(), "trace.jsonl")
		t.Setenv("OBSTF_TF_TRACE_PATH", trace)
		f := &fakeRequester{responses: map[string]map[string]any{}}
		_, err := waitItemExpected(context.Background(), f, "Office", 1, "source-1", true, 0, expectedTransform(), 1, 0)
		if err == nil {
			t.Fatal("expected read error")
		}
		b, readErr := os.ReadFile(trace)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(b), `"status":"failed-or-uncertain"`) {
			t.Fatalf("trace=%s", b)
		}
	})
}
