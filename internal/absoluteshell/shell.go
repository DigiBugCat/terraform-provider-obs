// Package absoluteshell builds the minimal version-1 OBS collection seed used
// before Terraform and the native bridge create any managed graph objects.
package absoluteshell

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const BootstrapName = "__obstf_bootstrap__"

func Build(collection string) (map[string]any, error) {
	if _, err := CollectionFilename(collection); err != nil {
		return nil, err
	}
	out := buildUnchecked(collection)
	if err := Validate(out, collection); err != nil {
		return nil, err
	}
	return out, nil
}

// CollectionFilename is the single reversible mapping from a collection name to its OBS
// scene-collection filename: the name must be non-empty, trimmed, single-spaced and
// made of ASCII letters, digits and spaces, and the file is the name with spaces as
// underscores. Names in that form round-trip exactly through OBS's own file naming.
func CollectionFilename(collection string) (string, error) {
	if collection == "" || strings.TrimSpace(collection) != collection ||
		strings.Join(strings.Fields(collection), " ") != collection {
		return "", fmt.Errorf("collection name must be non-empty, trimmed and single-spaced")
	}
	for _, r := range collection {
		if r != ' ' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", fmt.Errorf("collection contains non-canonical character %q", r)
		}
	}
	return strings.ReplaceAll(collection, " ", "_") + ".json", nil
}

func buildUnchecked(collection string) map[string]any {
	bootstrapUUID := deterministicUUID("obstf:absolute-shell:" + collection + ":bootstrap")
	scene := map[string]any{
		"prev_ver": 537001986, "name": BootstrapName, "uuid": bootstrapUUID,
		"id": "scene", "versioned_id": "scene", "settings": map[string]any{"id_counter": 1, "custom_size": false, "items": []any{}},
		"mixers": 0, "sync": 0, "flags": 0, "volume": 1.0, "balance": 0.5,
		"enabled": true, "muted": false, "push-to-mute": false, "push-to-mute-delay": 0,
		"push-to-talk": false, "push-to-talk-delay": 0, "hotkeys": map[string]any{},
		"deinterlace_mode": 0, "deinterlace_field_order": 0, "monitoring_type": 0,
		"canvas_uuid": "6c69626f-6273-4c00-9d88-c5136d61696e", "private_settings": map[string]any{}, "filters": []any{},
	}
	out := map[string]any{
		"name": collection, "version": 1,
		"current_scene": BootstrapName, "current_program_scene": BootstrapName,
		"scene_order": []any{map[string]any{"name": BootstrapName}},
		"sources":     []any{scene}, "groups": []any{}, "transitions": []any{}, "quick_transitions": []any{},
		"current_transition": "Fade", "transition_duration": 300, "preview_locked": false,
		"saved_projectors": []any{}, "scaling_enabled": false, "scaling_level": 0,
		"scaling_off_x": 0, "scaling_off_y": 0, "virtual-camera": map[string]any{"type2": 3},
		"modules": map[string]any{}, "canvases": []any{},
	}
	return out
}

func Bytes(collection string) ([]byte, error) {
	raw, err := Build(collection)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func Validate(raw map[string]any, collection string) error {
	if err := rejectRelativeFields(raw, "$"); err != nil {
		return err
	}
	if _, err := CollectionFilename(collection); err != nil {
		return err
	}
	got, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode absolute shell: %w", err)
	}
	want, err := json.Marshal(buildUnchecked(collection))
	if err != nil {
		return fmt.Errorf("encode canonical absolute shell: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("absolute shell differs from exact canonical empty-shell shape")
	}
	return nil
}

func rejectRelativeFields(value any, path string) error {
	switch x := value.(type) {
	case map[string]any:
		for key, child := range x {
			switch key {
			case "pos_rel", "scale_rel", "scale_ref", "bounds_rel", "resolution", "migration_resolution", "AbsoluteCoordinates":
				return fmt.Errorf("absolute shell contains forbidden field %s.%s", path, key)
			}
			if err := rejectRelativeFields(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range x {
			if err := rejectRelativeFields(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func deterministicUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	b := append([]byte(nil), sum[:16]...)
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[0:8], s[8:12], s[12:16], s[16:20], s[20:32])
}
