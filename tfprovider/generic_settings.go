package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Settings ownership is at top-level keys. A nested value is owned in full.
func parseGenericSettings(raw string) (map[string]any, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v == nil {
		return nil, fmt.Errorf("settings_json must be a JSON object")
	}
	for _, value := range v {
		if value == nil {
			return nil, fmt.Errorf("top-level null settings are unsupported; omit the key to relinquish ownership")
		}
	}
	return v, nil
}
func mustGenericSettings(raw string) map[string]any { v, _ := parseGenericSettings(raw); return v }
func genericJSONEqual(a, b map[string]any) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && string(x) == string(y)
}
func projectGenericSettings(live, defaults, managed map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k := range managed {
		v, ok := live[k]
		if !ok {
			v, ok = defaults[k]
		}
		if !ok {
			return nil, fmt.Errorf("managed setting %q absent from live settings and kind defaults", k)
		}
		out[k] = v
	}
	return out, nil
}

type genericSettingsValidator struct{}

func (genericSettingsValidator) Description(context.Context) string {
	return "A JSON object owning its top-level keys; null top-level values are unsupported."
}
func (v genericSettingsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (genericSettingsValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}
	if _, err := parseGenericSettings(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid settings JSON", err.Error())
	}
}
func genericFilterKindAvailable(ctx context.Context, cl requester, kind string) error {
	x, err := request(ctx, cl, "GetSourceFilterKindList", nil)
	if err != nil {
		return err
	}
	kinds, err := requiredSlice(x, "sourceFilterKinds")
	if err != nil {
		return err
	}
	for _, k := range kinds {
		if stringValue(k) == kind {
			return nil
		}
	}
	return fmt.Errorf("filter kind %q is not installed; install and qualify it explicitly", kind)
}
func observedGenericSettings(ctx context.Context, cl requester, g filterObservation, raw string) (map[string]any, error) {
	managed, err := parseGenericSettings(raw)
	if err != nil {
		return nil, err
	}
	defaults := map[string]any{}
	for k := range managed {
		if _, ok := g.Settings[k]; !ok {
			x, e := request(ctx, cl, "GetSourceFilterDefaultSettings", map[string]any{"filterKind": g.Kind})
			if e != nil {
				return nil, e
			}
			var good bool
			defaults, good = x["defaultFilterSettings"].(map[string]any)
			if !good {
				return nil, fmt.Errorf("GetSourceFilterDefaultSettings omitted object")
			}
			break
		}
	}
	return projectGenericSettings(g.Settings, defaults, managed)
}
func waitGenericFilterExpected(ctx context.Context, cl requester, parent string, m genericFilterModel) error {
	err := waitUntil(ctx, func() (bool, error) {
		xs, e := sourceFilters(ctx, cl, parent)
		if e != nil {
			return false, e
		}
		g, ok, e := findFilter(xs, m.Name.ValueString())
		if e != nil || !ok {
			return false, e
		}
		if g.Kind != m.Kind.ValueString() {
			return false, fmt.Errorf("same-name filter kind rebound")
		}
		settings, e := observedGenericSettings(ctx, cl, g, m.SettingsJSON.ValueString())
		if e != nil {
			return false, e
		}
		return g.Enabled == m.Enabled.ValueBool() && g.Index == m.Index.ValueInt64() && genericJSONEqual(settings, mustGenericSettings(m.SettingsJSON.ValueString())), nil
	})
	if err != nil {
		return err
	}
	return recordTrace(map[string]any{"event": "postcondition", "object": "generic-filter", "status": "passed", "parent_uuid": m.ParentUUID.ValueString(), "filter_name": m.Name.ValueString()})
}
