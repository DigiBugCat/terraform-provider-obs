// obs-purge-prefix removes every scene, group and input in the current OBS collection
// whose name starts with the given prefix. Destructive by design: it is the lab recovery
// step after a crashed run leaves objects that Terraform state no longer tracks. The
// prefix is a required argument and must not be empty.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: obs-purge-prefix <name-prefix>")
		os.Exit(2)
	}
	prefix := os.Args[1]
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
	removed := map[string]int{}
	list := func(typ, list, key string) []string {
		x, err := c.Req(ctx, typ, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, typ, err)
			os.Exit(1)
		}
		var out []string
		for _, n := range ws.ListNames(x, list, key) {
			if strings.HasPrefix(n, prefix) {
				out = append(out, n)
			}
		}
		return out
	}
	// Inputs first (removing an input removes its placements), then groups, then scenes.
	for _, n := range list("GetInputList", "inputs", "inputName") {
		if _, err := c.Req(ctx, "RemoveInput", map[string]any{"inputName": n}); err == nil {
			removed["inputs"]++
		}
	}
	for _, n := range list("GetGroupList", "groups", "") {
		if _, err := c.Req(ctx, "RemoveScene", map[string]any{"sceneName": n}); err == nil {
			removed["groups"]++
		}
	}
	for _, n := range list("GetSceneList", "scenes", "sceneName") {
		if _, err := c.Req(ctx, "RemoveScene", map[string]any{"sceneName": n}); err == nil {
			removed["scenes"]++
		}
	}
	fmt.Printf("purged prefix %q: %v\n", prefix, removed)
}
