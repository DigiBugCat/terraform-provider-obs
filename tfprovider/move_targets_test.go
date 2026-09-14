package tfprovider

import (
	"context"
	"fmt"
	"testing"
)

func TestSourceTargetMustBeOnePlacement(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		req := func(_ context.Context, kind string, _ map[string]any) (map[string]any, error) {
			if kind == "GetGroupList" {
				return map[string]any{"groups": []any{}}, nil
			}
			items := []any{}
			for i := 0; i < count; i++ {
				items = append(items, map[string]any{"sourceName": "Tile"})
			}
			return map[string]any{"sceneItems": items}, nil
		}
		e := validateSourceTarget(context.Background(), req, "Scene", "uuid", map[string]any{"source": "Tile"})
		if (e == nil) != (count == 1) {
			t.Fatalf("count%d err%v", count, e)
		}
	}
}
func TestValueTargetRequiresExistingNumericSetting(t *testing.T) {
	for _, value := range []any{0.5, "not numeric", nil} {
		req := func(_ context.Context, kind string, _ map[string]any) (map[string]any, error) {
			switch kind {
			case "GetSourceFilterList":
				return map[string]any{"filters": []any{map[string]any{"filterName": "Color", "filterKind": "color_filter_v2", "filterSettings": map[string]any{}}}}, nil
			case "GetSourceFilterDefaultSettings":
				m := map[string]any{}
				if value != nil {
					m["opacity"] = value
				}
				return map[string]any{"defaultFilterSettings": m}, nil
			}
			return nil, fmt.Errorf("unexpected %s", kind)
		}
		e := validateValueTarget(context.Background(), req, "Tile", "uuid", map[string]any{"filter": "Color", "setting_name": "opacity"})
		_, valid := value.(float64)
		if (e == nil) != valid {
			t.Fatalf("value%v err%v", value, e)
		}
	}
}
