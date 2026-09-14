// obs-add-item: simulate a human editing a managed scene in OBS. Adds (a) a placement of an
// existing source and (b) a brand-new colour source created directly in the scene.
//
//	obs-add-item <scene> <existing-source> <new-input-name>
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: obs-add-item <scene> <existing-source> <new-input-name>")
		os.Exit(2)
	}
	scene, src, newName := os.Args[1], os.Args[2], os.Args[3]
	cfg, _, err := ws.DiscoverConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx := context.Background()
	c, err := ws.Connect(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.Close()
	a, err := c.Req(ctx, "CreateSceneItem", map[string]any{"sceneName": scene, "sourceName": src, "sceneItemEnabled": true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "CreateSceneItem:", err)
		os.Exit(1)
	}
	b, err := c.Req(ctx, "CreateInput", map[string]any{"sceneName": scene, "inputName": newName, "inputKind": "color_source_v3", "inputSettings": map[string]any{"color": 4278255360}, "sceneItemEnabled": true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "CreateInput:", err)
		os.Exit(1)
	}
	fmt.Printf("added: existing %q as item %v; new input %q as item %v in %q\n", src, a["sceneItemId"], newName, b["sceneItemId"], scene)
}
