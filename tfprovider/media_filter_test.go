package tfprovider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

type mediaSequenceRequester struct{ settingsReads int }

func (m *mediaSequenceRequester) Req(_ context.Context, typ string, _ map[string]any) (map[string]any, error) {
	switch typ {
	case "GetInputList":
		return map[string]any{"inputs": []any{map[string]any{"inputName": "Party Media", "inputUuid": "media-1", "inputKind": "ffmpeg_source"}}}, nil
	case "GetInputSettings":
		m.settingsReads++
		file := "wrong.wav"
		if m.settingsReads > 1 {
			file = "party.wav"
		}
		return map[string]any{"inputKind": "ffmpeg_source", "inputSettings": map[string]any{"local_file": file, "is_local_file": true, "looping": false, "restart_on_activate": true, "close_when_inactive": true}}, nil
	case "GetInputDefaultSettings":
		return map[string]any{"defaultInputSettings": map[string]any{"local_file": "", "is_local_file": false, "looping": false, "restart_on_activate": true, "close_when_inactive": true}}, nil
	}
	return nil, fmt.Errorf("unexpected %s", typ)
}

func TestMediaSettingsComparisonIsTypedAndComplete(t *testing.T) {
	m := mediaInputModel{LocalFile: types.StringValue("/tmp/party.wav"), IsLocalFile: types.BoolValue(true), Looping: types.BoolValue(false), RestartOnActivate: types.BoolValue(true), CloseWhenInactive: types.BoolValue(true)}
	want := mediaSettings(m)
	if got := mediaMatches(want, want); got != "" {
		t.Fatalf("exact mismatch=%s", got)
	}
	for _, key := range []string{"local_file", "is_local_file", "looping", "restart_on_activate", "close_when_inactive"} {
		copy := map[string]any{}
		for k, v := range want {
			copy[k] = v
		}
		copy[key] = "wrong"
		if got := mediaMatches(copy, want); !strings.Contains(got, key) {
			t.Fatalf("key=%s mismatch=%s", key, got)
		}
	}
}

func TestMediaSettingsDefaultsAreExplicitAndMalformedLiveValuesRefuse(t *testing.T) {
	defaults := map[string]any{"local_file": "", "is_local_file": false, "looping": false, "restart_on_activate": true, "close_when_inactive": true}
	values, origins, err := resolveMediaSettings(map[string]any{"local_file": "/tmp/party.wav", "is_local_file": true}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if values["looping"] != false || origins["looping"] != "default" || origins["local_file"] != "live" {
		t.Fatalf("values=%#v origins=%#v", values, origins)
	}
	if _, _, err := resolveMediaSettings(map[string]any{"local_file": "/tmp/party.wav", "is_local_file": "true"}, defaults); err == nil || !strings.Contains(err.Error(), "want bool") {
		t.Fatalf("err=%v", err)
	}
	delete(defaults, "looping")
	if _, _, err := resolveMediaSettings(map[string]any{"local_file": "/tmp/party.wav", "is_local_file": true}, defaults); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("err=%v", err)
	}
}

func TestMediaAnchorReboundRefusesBeforeMutation(t *testing.T) {
	if err := anchorIdentityError(true, itemObservation{SourceUUID: "replacement"}, "expected"); err == nil || !strings.Contains(err.Error(), "refusing all writes") {
		t.Fatalf("err=%v", err)
	}
	if err := anchorIdentityError(false, itemObservation{}, "expected"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitMediaInputAllowsDelayedSettingsConvergence(t *testing.T) {
	f := &mediaSequenceRequester{}
	want := map[string]any{"local_file": "party.wav", "is_local_file": true, "looping": false, "restart_on_activate": true, "close_when_inactive": true}
	if _, err := waitMediaInput(context.Background(), f, "media-1", "Party Media", want); err != nil {
		t.Fatal(err)
	}
	if f.settingsReads != 2 {
		t.Fatalf("settings reads=%d", f.settingsReads)
	}
}

func TestWaitMediaInputHonorsCancellationAfterMismatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &mediaSequenceRequester{}
	want := map[string]any{"local_file": "party.wav", "is_local_file": true, "looping": false, "restart_on_activate": true, "close_when_inactive": true}
	_, err := waitMediaInput(ctx, f, "media-1", "Party Media", want)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
	if f.settingsReads != 1 {
		t.Fatalf("settings reads=%d", f.settingsReads)
	}
}

func TestColorFilterComparisonCoversParentOwnedFields(t *testing.T) {
	m := colorFilterModel{Name: types.StringValue("Color Correction"), Kind: types.StringValue("color_filter_v2"), HueShift: types.Float64Value(180), Opacity: types.Float64Value(0), Enabled: types.BoolValue(true), Index: types.Int64Value(0)}
	base := filterObservation{Name: "Color Correction", Kind: "color_filter_v2", Enabled: true, Index: 0, Settings: map[string]any{"hue_shift": 180.0, "opacity": 0.0}}
	if got := filterMismatch(base, base.Settings, m); got != "" {
		t.Fatalf("exact mismatch=%s", got)
	}
	cases := []struct {
		want string
		edit func(*filterObservation)
	}{{"kind", func(x *filterObservation) { x.Kind = "move_value_filter" }}, {"enabled", func(x *filterObservation) { x.Enabled = false }}, {"index", func(x *filterObservation) { x.Index = 1 }}, {"hue_shift", func(x *filterObservation) { x.Settings = map[string]any{"hue_shift": 181.0, "opacity": 0.0} }}, {"opacity", func(x *filterObservation) { x.Settings = map[string]any{"hue_shift": 180.0, "opacity": 1.0} }}}
	for _, tc := range cases {
		got := base
		tc.edit(&got)
		if mismatch := filterMismatch(got, got.Settings, m); mismatch != tc.want {
			t.Fatalf("got=%s want=%s", mismatch, tc.want)
		}
	}
}

func TestColorDefaultsAreExplicitAndWrongKindDoesNotAlias(t *testing.T) {
	values, origins, err := resolveColorSettings(map[string]any{"hue_shift": 180.0}, map[string]any{"hue_shift": 0.0, "opacity": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if values["opacity"] != 1.0 || origins["opacity"] != "default" {
		t.Fatalf("values=%#v origins=%#v", values, origins)
	}
	if _, _, err := resolveColorSettings(map[string]any{"hue_shift": "180", "opacity": 0.0}, map[string]any{}); err == nil {
		t.Fatal("string-coerced hue accepted")
	}
	if isColorFilterKind("color_filter") || !isColorFilterKind("color_filter_v2") {
		t.Fatal("filter kind pin is not exact")
	}
}

func TestSourceFiltersRejectsMalformedAndDuplicateName(t *testing.T) {
	f := &fakeRequester{responses: map[string]map[string]any{"GetSourceFilterList": {"filters": "bad"}}}
	if _, err := sourceFilters(context.Background(), f, "Party Lights"); err == nil {
		t.Fatal("malformed list accepted")
	}
	dup := filterObservation{Name: "Color Correction"}
	if _, _, err := findFilter([]filterObservation{dup, dup}, dup.Name); err == nil {
		t.Fatal("duplicate filter name accepted")
	}
}
