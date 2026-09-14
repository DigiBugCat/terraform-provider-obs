package absoluteshell

import (
	"encoding/json"
	"fmt"
	"math"
)

type PersistedFacts struct {
	Version                       int64 `json:"version"`
	TrueGroupChildCount           int   `json:"true_group_child_count"`
	GroupChildScaleReferenceCount int   `json:"group_child_scale_reference_count"`
	SourceItemScaleReferenceCount int   `json:"source_item_scale_reference_count"`
	OtherScaleReferenceCount      int   `json:"other_scale_reference_count"`
	RelativeTransformFieldCount   int   `json:"relative_transform_field_count"`
}

// ValidatePersisted enforces the source-backed OBS 32.2.2 absolute post-save
// representation. Unlike Validate, this permits exactly one fixed-canvas
// scale_ref on each true group child because OBS writes that field itself.
func ValidatePersisted(raw map[string]any, collection string) (PersistedFacts, error) {
	var facts PersistedFacts
	version, ok := exactInt64(raw["version"])
	if !ok || version != 1 || raw["name"] != collection {
		return facts, fmt.Errorf("persisted absolute collection identity/version mismatch")
	}
	facts.Version = version
	for _, key := range []string{"resolution", "migration_resolution", "AbsoluteCoordinates"} {
		if _, exists := raw[key]; exists {
			return facts, fmt.Errorf("persisted absolute collection forbids top-level %s", key)
		}
	}
	allowed := map[string]bool{}
	groups, ok := raw["groups"].([]any)
	if !ok {
		return facts, fmt.Errorf("persisted absolute collection groups must be an array")
	}
	for i, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			return facts, fmt.Errorf("groups[%d] is not an object", i)
		}
		settings, ok := group["settings"].(map[string]any)
		if !ok {
			return facts, fmt.Errorf("groups[%d].settings is not an object", i)
		}
		items, ok := settings["items"].([]any)
		if !ok {
			return facts, fmt.Errorf("groups[%d].settings.items is not an array", i)
		}
		for j, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return facts, fmt.Errorf("groups[%d].settings.items[%d] is not an object", i, j)
			}
			facts.TrueGroupChildCount++
			path := fmt.Sprintf("$.groups[%d].settings.items[%d].scale_ref", i, j)
			allowed[path] = true
			if err := validateFixedScaleReference(item["scale_ref"], path); err != nil {
				return facts, err
			}
		}
	}
	if err := inspectPersistedFields(raw, "$", allowed, &facts); err != nil {
		return facts, err
	}
	if facts.GroupChildScaleReferenceCount != facts.TrueGroupChildCount || facts.SourceItemScaleReferenceCount != 0 || facts.OtherScaleReferenceCount != 0 || facts.RelativeTransformFieldCount != 0 {
		return facts, fmt.Errorf("persisted absolute field counts are inconsistent: %+v", facts)
	}
	return facts, nil
}

func inspectPersistedFields(value any, path string, allowedScaleRefs map[string]bool, facts *PersistedFacts) error {
	switch x := value.(type) {
	case map[string]any:
		for key, child := range x {
			childPath := path + "." + key
			switch key {
			case "pos_rel", "scale_rel", "bounds_rel":
				facts.RelativeTransformFieldCount++
				return fmt.Errorf("persisted absolute collection contains forbidden field %s", childPath)
			case "AbsoluteCoordinates":
				return fmt.Errorf("persisted absolute collection contains private mode field %s", childPath)
			case "scale_ref":
				if allowedScaleRefs[childPath] {
					facts.GroupChildScaleReferenceCount++
				} else if hasPrefix(path, "$.sources[") {
					facts.SourceItemScaleReferenceCount++
					return fmt.Errorf("persisted absolute source item contains scale_ref at %s", childPath)
				} else {
					facts.OtherScaleReferenceCount++
					return fmt.Errorf("persisted absolute collection contains unclassified scale_ref at %s", childPath)
				}
			}
			if err := inspectPersistedFields(child, childPath, allowedScaleRefs, facts); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range x {
			if err := inspectPersistedFields(child, fmt.Sprintf("%s[%d]", path, i), allowedScaleRefs, facts); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFixedScaleReference(value any, path string) error {
	ref, ok := value.(map[string]any)
	if !ok || len(ref) != 2 {
		return fmt.Errorf("%s must be exactly the fixed canvas object", path)
	}
	x, xok := exactNumber(ref["x"])
	y, yok := exactNumber(ref["y"])
	if !xok || !yok || x != 1920 || y != 1080 {
		return fmt.Errorf("%s must equal {x:1920,y:1080}", path)
	}
	return nil
}

func exactInt64(value any) (int64, bool) {
	n, ok := exactNumber(value)
	if !ok || math.Trunc(n) != n || n < math.MinInt64 || n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func exactNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case json.Number:
		v, err := n.Float64()
		return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
	default:
		return 0, false
	}
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
