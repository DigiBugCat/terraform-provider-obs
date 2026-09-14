package tfprovider

import (
	"context"
	"fmt"
)

type nativeRequest = func(context.Context, string, map[string]any) (map[string]any, error)

func validateSourceTarget(ctx context.Context, req nativeRequest, parentName, parentUUID string, settings map[string]any) error {
	kind := "GetSceneItemList"
	groups, err := req(ctx, "GetGroupList", nil)
	if err != nil {
		return err
	}
	list, ok := groups["groups"].([]any)
	if !ok {
		return fmt.Errorf("group inventory unavailable")
	}
	for _, g := range list {
		if g == parentName {
			kind = "GetGroupSceneItemList"
		}
	}
	response, err := req(ctx, kind, map[string]any{"sceneUuid": parentUUID})
	if err != nil {
		return err
	}
	items, ok := response["sceneItems"].([]any)
	if !ok {
		return fmt.Errorf("Move target scene item inventory unavailable")
	}
	target, _ := settings["source"].(string)
	count := 0
	for _, item := range items {
		v, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("malformed Move target item")
		}
		if v["sourceName"] == target {
			count++
		}
	}
	if target == "" || count != 1 {
		return fmt.Errorf("Move target %q must identify exactly one placement in %q; found %d", target, parentName, count)
	}
	return nil
}

func validateValueTarget(ctx context.Context, req nativeRequest, parentName, parentUUID string, settings map[string]any) error {
	name, _ := settings["filter"].(string)
	key, _ := settings["setting_name"].(string)
	var values, defaults map[string]any
	if name == "" {
		response, err := req(ctx, "GetInputSettings", map[string]any{"inputUuid": parentUUID})
		if err != nil {
			return err
		}
		values, _ = response["inputSettings"].(map[string]any)
		kind, _ := response["inputKind"].(string)
		d, err := req(ctx, "GetInputDefaultSettings", map[string]any{"inputKind": kind})
		if err != nil {
			return err
		}
		defaults, _ = d["defaultInputSettings"].(map[string]any)
	} else {
		response, err := req(ctx, "GetSourceFilterList", map[string]any{"sourceUuid": parentUUID})
		if err != nil {
			return err
		}
		filters, ok := response["filters"].([]any)
		if !ok {
			return fmt.Errorf("Move value filter inventory unavailable")
		}
		count := 0
		kind := ""
		for _, filter := range filters {
			v, ok := filter.(map[string]any)
			if !ok {
				return fmt.Errorf("malformed filter inventory")
			}
			if v["filterName"] == name {
				count++
				kind, _ = v["filterKind"].(string)
				values, _ = v["filterSettings"].(map[string]any)
			}
		}
		if count != 1 {
			return fmt.Errorf("Move value target filter %q on %q must exist uniquely", name, parentName)
		}
		d, err := req(ctx, "GetSourceFilterDefaultSettings", map[string]any{"filterKind": kind})
		if err != nil {
			return err
		}
		defaults, _ = d["defaultFilterSettings"].(map[string]any)
	}
	if values == nil || defaults == nil || key == "" {
		return fmt.Errorf("Move value target settings unavailable")
	}
	value, ok := values[key]
	if !ok {
		value, ok = defaults[key]
	}
	if !ok {
		return fmt.Errorf("Move value target setting %q is absent", key)
	}
	switch value.(type) {
	case float64, float32, int, int64:
		return nil
	default:
		return fmt.Errorf("Move value target setting %q is not numeric", key)
	}
}
