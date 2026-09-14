package tfprovider

import (
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework-validators/boolvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Settings keys and constants are from exeldro/obs-move-transition tag 3.2.1.
// This slice deliberately configures manual triggering; it never presses Start.
func commonSpec(suffix, kind string) FilterSpec {
	return FilterSpec{TypeSuffix: suffix, Kind: kind, Settings: map[string]FilterSettingType{
		"duration_ms": FilterInt64, "easing": FilterInt64, "easing_function": FilterInt64,
		"custom_duration": FilterBool, "start_trigger": FilterInt64, "stop_trigger": FilterInt64, "enabled_match_moving": FilterBool,
	}, Attributes: map[string]schema.Attribute{
		"duration_ms":          schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(300), Validators: []validator.Int64{int64validator.Between(0, 3600000)}, Description: "Custom animation duration in milliseconds."},
		"easing":               schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(3), Validators: []validator.Int64{int64validator.Between(0, 3)}, Description: "0 none, 1 in, 2 out, 3 in-out."},
		"easing_function":      schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(2), Validators: []validator.Int64{int64validator.Between(1, 10)}, Description: "1 quadratic, 2 cubic, 3 quartic, 4 quintic, 5 sine, 6 circular, 7 exponential, 8 elastic, 9 bounce, 10 back."},
		"custom_duration":      schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Use duration_ms instead of current OBS transition duration."},
		"start_trigger":        schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.OneOf(0)}, Description: "Manual profile: must remain 0 (none)."},
		"stop_trigger":         schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.OneOf(0)}, Description: "Manual profile: must remain 0 (none)."},
		"enabled_match_moving": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Validators: []validator.Bool{boolvalidator.Equals(false)}, Description: "Keep false to avoid automatically starting on enable. This slice rejects true."},
	}}
}
func remap(m map[string]any, from, to string) { m[to] = m[from]; delete(m, from) }
func encodeCommon(m map[string]any) (map[string]any, error) {

	remap(m, "duration_ms", "duration")
	remap(m, "easing", "easing_match")
	remap(m, "easing_function", "easing_function_match")
	return m, nil
}
func decodeCommon(m map[string]any) {
	remap(m, "duration", "duration_ms")
	remap(m, "easing_match", "easing")
	remap(m, "easing_function_match", "easing_function")
}
func sourceSpec() FilterSpec {
	s := commonSpec("_move_source_filter", "move_source_filter")
	s.ValidateNative = validateSourceTarget
	s.Settings["target_source_name"] = FilterString
	s.Attributes["target_source_name"] = schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, Description: "Unique source name of the scene item in the parent scene/group."}
	for _, k := range []string{"position_x", "position_y", "scale_x", "scale_y", "rotation"} {
		s.Settings[k] = FilterFloat64
		d := float64(0)
		if k == "scale_x" || k == "scale_y" {
			d = 1
		}
		s.Attributes[k] = schema.Float64Attribute{Optional: true, Computed: true, Default: float64default.StaticFloat64(d)}
	}
	s.EncodeSettings = func(m map[string]any) (map[string]any, error) {
		m, e := encodeCommon(m)
		if e != nil {
			return nil, e
		}
		remap(m, "target_source_name", "source")
		remap(m, "rotation", "rot")
		m["transform"] = true
		m["rot_sign"] = " "
		for _, v := range []struct{ key, prefix string }{{"pos", "position"}, {"scale", "scale"}} {
			m[v.key] = map[string]any{"x": m[v.prefix+"_x"], "y": m[v.prefix+"_y"], "x_sign": " ", "y_sign": " "}
			delete(m, v.prefix+"_x")
			delete(m, v.prefix+"_y")
		}
		// Relative zero leaves unsupported bounds and crop unchanged when triggered.
		m["bounds"] = map[string]any{"x": 0, "y": 0, "x_sign": "+", "y_sign": "+"}
		m["crop"] = map[string]any{"left": 0, "top": 0, "right": 0, "bottom": 0, "left_sign": "+", "top_sign": "+", "right_sign": "+", "bottom_sign": "+"}
		return m, nil
	}
	s.DecodeSettings = func(m map[string]any) (map[string]any, error) {
		if err := requireFixed(m, map[string]any{"bounds": map[string]any{"x": 0, "y": 0, "x_sign": "+", "y_sign": "+"}, "crop": map[string]any{"left": 0, "top": 0, "right": 0, "bottom": 0, "left_sign": "+", "top_sign": "+", "right_sign": "+", "bottom_sign": "+"}}); err != nil {
			return nil, err
		}
		decodeCommon(m)
		remap(m, "source", "target_source_name")
		remap(m, "rot", "rotation")
		if m["transform"] != true || m["rot_sign"] != " " {
			return nil, fmt.Errorf("Move source transform mode drifted outside supported absolute slice")
		}
		for _, v := range []struct{ key, prefix string }{{"pos", "position"}, {"scale", "scale"}} {
			p, ok := m[v.key].(map[string]any)
			if !ok || p["x_sign"] != " " || p["y_sign"] != " " {
				return nil, fmt.Errorf("unsupported Move %s relative transform", v.key)
			}
			m[v.prefix+"_x"] = p["x"]
			m[v.prefix+"_y"] = p["y"]
		}
		return m, nil
	}
	return s
}
func valueSpec() FilterSpec {
	s := commonSpec("_move_value_filter", "move_value_filter")
	s.ValidateNative = validateValueTarget
	s.Settings["target_filter_name"] = FilterString
	s.Settings["setting_name"] = FilterString
	s.Settings["value"] = FilterFloat64
	s.Attributes["target_filter_name"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Filter on the parent to animate; empty animates a setting on the parent itself."}
	s.Attributes["setting_name"] = schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, Description: "Native floating-point property key, e.g. opacity on color_filter_v2."}
	s.Attributes["value"] = schema.Float64Attribute{Required: true, Description: "Absolute floating-point target value. Integer/color/text settings are outside this slice."}
	s.EncodeSettings = func(m map[string]any) (map[string]any, error) {
		m, e := encodeCommon(m)
		if e != nil {
			return nil, e
		}
		remap(m, "target_filter_name", "filter")
		remap(m, "value", "setting_float")
		m["move_value_type"] = 0
		m["value_type"] = 2
		return m, nil
	}
	s.DecodeSettings = func(m map[string]any) (map[string]any, error) {
		if err := requireFixed(m, map[string]any{"move_value_type": 0, "value_type": 2}); err != nil {
			return nil, err
		}
		decodeCommon(m)
		remap(m, "filter", "target_filter_name")
		remap(m, "setting_float", "value")
		return m, nil
	}
	return s
}
func NewSourceFilterResource() resource.Resource {
	return NewTypedFilterResource(sourceSpec())
}
func NewValueFilterResource() resource.Resource {
	return NewTypedFilterResource(valueSpec())
}

// Fail closed on native modes outside this deliberately bounded typed slice.
func requireFixed(got, want map[string]any) error {
	for k, v := range want {
		a, _ := json.Marshal(got[k])
		b, _ := json.Marshal(v)
		if string(a) != string(b) {
			return fmt.Errorf("Move %s drifted outside supported typed slice; restore native mode before refresh", k)
		}
	}
	return nil
}
