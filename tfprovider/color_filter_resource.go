// Filters shared helpers plus obs_color_filter, a typed color_filter_v2 resource built on
// the generic filter lifecycle (see generic_typed_filter.go). It predates the typed
// mechanism and keeps its original import ID form for existing workspaces.
package tfprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func colorFilterSpec() FilterSpec {
	return FilterSpec{
		TypeSuffix: "_color_filter", Kind: "color_filter_v2",
		Settings: map[string]FilterSettingType{"hue_shift": FilterFloat64, "opacity": FilterFloat64},
		ImportID: func(id string) (collection, parentUUID, parentName, name string, ok bool) {
			parts := strings.Split(id, "|")
			if len(parts) != 4 {
				return "", "", "", "", false
			}
			return parts[0], parts[1], parts[2], parts[3], true
		},
	}
}

func NewColorFilterResource() resource.Resource { return NewTypedFilterResource(colorFilterSpec()) }

type filterObservation struct {
	Name, Kind string
	Enabled    bool
	Index      int64
	Settings   map[string]any
}

func sourceFilters(ctx context.Context, cl requester, parent string) ([]filterObservation, error) {
	x, err := request(ctx, cl, "GetSourceFilterList", map[string]any{"sourceName": parent})
	if err != nil {
		return nil, err
	}
	xs, err := requiredSlice(x, "filters")
	if err != nil {
		return nil, err
	}
	out := make([]filterObservation, 0, len(xs))
	for i, v := range xs {
		o, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("filter %d malformed", i)
		}
		name, kind := stringValue(o["filterName"]), stringValue(o["filterKind"])
		settings, ok := o["filterSettings"].(map[string]any)
		enabled, eok := o["filterEnabled"].(bool)
		idx, iok := numberValue(o["filterIndex"])
		if name == "" || kind == "" || !ok || !eok || !iok {
			return nil, fmt.Errorf("filter %d response incomplete", i)
		}
		out = append(out, filterObservation{name, kind, enabled, int64(idx), settings})
	}
	return out, nil
}

func findFilter(xs []filterObservation, name string) (filterObservation, bool, error) {
	matches := 0
	var out filterObservation
	for _, f := range xs {
		if f.Name == name {
			matches++
			out = f
		}
	}
	if matches > 1 {
		return out, false, fmt.Errorf("filter %q is ambiguous", name)
	}
	return out, matches == 1, nil
}
