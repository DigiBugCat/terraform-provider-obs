package tfprovider

import (
	"encoding/json"
	"testing"
)

func commonValues() map[string]any {
	return map[string]any{"duration_ms": int64(750), "easing": int64(3), "easing_function": int64(2), "custom_duration": true, "start_trigger": int64(0), "stop_trigger": int64(0), "enabled_match_moving": false}
}
func wireRoundTrip(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]any
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestSourceWireMappingAndUnsupportedMode(t *testing.T) {
	s := sourceSpec()
	m := commonValues()
	m["target_source_name"] = "Tile"
	m["position_x"] = 480.0
	m["position_y"] = 270.0
	m["scale_x"] = 1.0
	m["scale_y"] = 1.0
	m["rotation"] = 45.0
	native, e := s.EncodeSettings(m)
	if e != nil {
		t.Fatal(e)
	}
	if native["source"] != "Tile" || native["rot"] != 45.0 || native["enabled_match_moving"] != false || native["duration"] != int64(750) {
		t.Fatalf("bad native settings: %#v", native)
	}
	p := native["pos"].(map[string]any)
	if p["x"] != 480.0 || p["x_sign"] != " " {
		t.Fatal(p)
	}
	decoded, e := s.DecodeSettings(wireRoundTrip(t, native))
	if e != nil {
		t.Fatal(e)
	}
	if decoded["position_x"] != 480.0 || decoded["duration_ms"] != 750.0 {
		t.Fatal(decoded)
	}
	native["rot_sign"] = "+"
	if _, e = s.DecodeSettings(wireRoundTrip(t, native)); e == nil {
		t.Fatal("unsupported relative mode silently accepted")
	}
}
func TestValueWireMappingAndUnsupportedMode(t *testing.T) {
	s := valueSpec()
	m := commonValues()
	m["target_filter_name"] = "Color"
	m["setting_name"] = "opacity"
	m["value"] = 0.5
	native, e := s.EncodeSettings(m)
	if e != nil {
		t.Fatal(e)
	}
	if native["filter"] != "Color" || native["setting_float"] != 0.5 || native["value_type"] != 2 {
		t.Fatal(native)
	}
	decoded, e := s.DecodeSettings(wireRoundTrip(t, native))
	if e != nil {
		t.Fatal(e)
	}
	if decoded["value"] != 0.5 {
		t.Fatal(decoded)
	}
	native["value_type"] = 1
	if _, e = s.DecodeSettings(wireRoundTrip(t, native)); e == nil {
		t.Fatal("integer mode silently accepted")
	}
}
