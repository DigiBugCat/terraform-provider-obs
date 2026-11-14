package main

import (
	"strings"
	"testing"
)

// The rendered documentation is exactly what the canvases say, and it is complete. A hand
// edit to docs/, README.md or a generated example fails here.
func TestDocsMatchTheirCanvases(t *testing.T) {
	root := repoRoot()
	p, err := loadPalette(root)
	if err != nil {
		t.Fatal(err)
	}
	if n := render(root, p, false); n > 0 {
		t.Fatalf("%d rendered file(s) differ from their canvas; run `make docs`, and never edit them by hand", n)
	}
	if n := critique(root, p, "", true); n > 0 {
		t.Fatalf("%d documentation issue(s)", n)
	}
}

// Every resource the provider registers is importable or not exactly as its page says,
// and no attribute is left without a description.
func TestPaletteCoversTheProvider(t *testing.T) {
	p, err := loadPalette(repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Resources) == 0 || p.Resources["obs_scene"] == nil || !p.Resources["obs_scene"].Importable {
		t.Fatalf("palette did not load the provider: %d resources", len(p.Resources))
	}
	if p.Resources["obs_scene_item"].find("transform.x") == nil {
		t.Fatal("nested attributes are not on the palette")
	}
}

func TestSettableFollowsTheSchema(t *testing.T) {
	p, err := loadPalette(repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	item, graph := p.Resources["obs_scene_item"], p.Resources["obs_group_graph"]
	for path, ok := range map[string]bool{"transform.x": true, "enabled": true, "item_id": false, "transform.nope": false, "nope": false} {
		if err := settable(item, path); (err == nil) != ok {
			t.Errorf("obs_scene_item %s: settable=%v, want %v (%v)", path, err == nil, ok, err)
		}
	}
	for path, ok := range map[string]bool{"children.0.name": true, "children.name": false, "children.0.item_id": false, "children.0": false} {
		if err := settable(graph, path); (err == nil) != ok {
			t.Errorf("obs_group_graph %s: settable=%v, want %v (%v)", path, err == nil, ok, err)
		}
	}
}

func TestExampleRendersInTerraformLayout(t *testing.T) {
	e := &example{Blocks: []exBlockT{{Kind: "resource", Labels: []string{"obs_scene", "main"}, Remark: []string{"A scene."},
		Attrs: []exAttr{{"collection", `"Show"`}, {"name", `"Main"`}, {"t.x", "0"}, {"t.scale_x", "1"}, {"list.0.a", "1"}, {"list.1.a", "2"}}}}}
	want := strings.Join([]string{
		`# A scene.`,
		`resource "obs_scene" "main" {`,
		`  collection = "Show"`,
		`  name       = "Main"`,
		`  t = {`,
		`    x       = 0`,
		`    scale_x = 1`,
		`  }`,
		`  list = [`,
		`    {`,
		`      a = 1`,
		`    },`,
		`    {`,
		`      a = 2`,
		`    }`,
		`  ]`,
		`}`, ``}, "\n")
	if got := e.hcl(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMixPrefersTheLongestKey(t *testing.T) {
	mixes := map[string]string{"index": "filter", "placement.index": "layer"}
	if mixFor(mixes, "placement.index") != "layer" || mixFor(mixes, "index") != "filter" || mixFor(mixes, "parent_placement.index") != "filter" {
		t.Fatal("longest matching suffix must win, on whole path segments")
	}
}
