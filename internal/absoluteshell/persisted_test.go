package absoluteshell

import (
	"strings"
	"testing"
)

func persistedFixture() map[string]any {
	child := func(name string) map[string]any {
		return map[string]any{"name": name, "pos": map[string]any{"x": 0.0, "y": 0.0}, "scale_ref": map[string]any{"x": 1920.0, "y": 1080.0}}
	}
	return map[string]any{
		"name": "Terraform Final Lab", "version": 1,
		"sources": []any{map[string]any{"name": "Office", "settings": map[string]any{"items": []any{map[string]any{"name": "Party Lights", "pos": map[string]any{"x": 0.0}}}}}},
		"groups":  []any{map[string]any{"name": "Party Lights", "settings": map[string]any{"items": []any{child("Left"), child("Right")}}}},
	}
}

func TestValidatePersistedAcceptsExactOBSAuthoredScaleReferences(t *testing.T) {
	facts, err := ValidatePersisted(persistedFixture(), "Terraform Final Lab")
	if err != nil {
		t.Fatal(err)
	}
	if facts.TrueGroupChildCount != 2 || facts.GroupChildScaleReferenceCount != 2 || facts.SourceItemScaleReferenceCount != 0 {
		t.Fatalf("facts=%+v", facts)
	}
}

func TestValidatePersistedRejectsRelativeOrMisplacedScaleReference(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(map[string]any)
	}{
		{"relative", "pos_rel", func(x map[string]any) { x["sources"].([]any)[0].(map[string]any)["pos_rel"] = map[string]any{} }},
		{"source scale ref", "source item", func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["scale_ref"] = map[string]any{"x": 1920, "y": 1080}
		}},
		{"missing child scale ref", "fixed canvas", func(x map[string]any) {
			delete(x["groups"].([]any)[0].(map[string]any)["settings"].(map[string]any)["items"].([]any)[0].(map[string]any), "scale_ref")
		}},
		{"wrong child scale ref", "1920", func(x map[string]any) {
			x["groups"].([]any)[0].(map[string]any)["settings"].(map[string]any)["items"].([]any)[0].(map[string]any)["scale_ref"].(map[string]any)["x"] = 1280
		}},
		{"private mode", "private mode", func(x map[string]any) { x["groups"].([]any)[0].(map[string]any)["AbsoluteCoordinates"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := persistedFixture()
			tc.edit(raw)
			if _, err := ValidatePersisted(raw, "Terraform Final Lab"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}
