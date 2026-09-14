package absoluteshell

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildIsDeterministicMinimalAbsoluteShell(t *testing.T) {
	a, err := Bytes("Terraform Final Lab")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bytes("Terraform Final Lab")
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("absolute shell is not deterministic")
	}
	for _, forbidden := range []string{`"resolution"`, `"migration_resolution"`, `"AbsoluteCoordinates"`, `"pos_rel"`, `"scale_rel"`, `"scale_ref"`, `"bounds_rel"`, "Party Lights", "Party Light Left", "Party Light Right"} {
		if strings.Contains(string(a), forbidden) {
			t.Fatalf("shell contains %s", forbidden)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(a, &raw); err != nil {
		t.Fatal(err)
	}
	if err := Validate(raw, "Terraform Final Lab"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsRelativeMigrationOrManagedGraph(t *testing.T) {
	for _, edit := range []func(map[string]any){
		func(x map[string]any) { x["resolution"] = map[string]any{"x": 1920, "y": 1080} },
		func(x map[string]any) { x["migration_resolution"] = map[string]any{} },
		func(x map[string]any) { x["scene_order"] = []any{} },
		func(x map[string]any) { x["transitions"] = []any{map[string]any{"name": "hidden"}} },
		func(x map[string]any) { x["groups"] = []any{map[string]any{"name": "Party Lights"}} },
		func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["settings"].(map[string]any)["items"] = []any{map[string]any{"pos_rel": map[string]any{"x": 0}}}
		},
		func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["filters"] = []any{map[string]any{"name": "hidden"}}
		},
		func(x map[string]any) { x["sources"].([]any)[0].(map[string]any)["uuid"] = "wrong" },
		func(x map[string]any) { x["sources"].([]any)[0].(map[string]any)["versioned_id"] = "wrong" },
		func(x map[string]any) { x["sources"].([]any)[0].(map[string]any)["canvas_uuid"] = "wrong" },
		func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["settings"].(map[string]any)["custom_size"] = true
		},
		func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["settings"].(map[string]any)["id_counter"] = 0
		},
		func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["private_settings"].(map[string]any)["AbsoluteCoordinates"] = true
		},
		func(x map[string]any) {
			x["sources"].([]any)[0].(map[string]any)["private_settings"].(map[string]any)["scale_ref"] = map[string]any{}
		},
	} {
		raw, err := Build("Terraform Test Lab")
		if err != nil {
			t.Fatal(err)
		}
		edit(raw)
		if err := Validate(raw, "Terraform Test Lab"); err == nil {
			t.Fatalf("invalid shell accepted: %#v", raw)
		}
	}
}

func TestCollectionFilenameIsCanonicalAndReversible(t *testing.T) {
	got, err := CollectionFilename("Terraform Final Lab")
	if err != nil || got != "Terraform_Final_Lab.json" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, invalid := range []string{"", " Lab", "Terraform  Lab", "Terraform Lab ", "Terraform X/Y Lab", "Lab	"} {
		if _, err := CollectionFilename(invalid); err == nil {
			t.Fatalf("accepted non-canonical collection %q", invalid)
		}
	}
}
