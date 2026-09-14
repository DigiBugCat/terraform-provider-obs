// obs-inventory: read-only count of scenes, groups, inputs and filters in the current OBS
// collection whose name starts with a prefix (default "[TF] "). Used to verify
// that terraform destroy leaves nothing of a managed run behind.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

func main() {
	prefix := "[TF] "
	if len(os.Args) > 1 {
		prefix = os.Args[1]
	}
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
	req := func(t string, d map[string]any) map[string]any {
		x, e := c.Req(ctx, t, d)
		if e != nil {
			fmt.Fprintln(os.Stderr, t, e)
			os.Exit(1)
		}
		return x
	}
	names := func(x map[string]any, list, key string) (all, hit []string) {
		arr, _ := x[list].([]any)
		for _, v := range arr {
			var n string
			switch t := v.(type) {
			case map[string]any:
				n, _ = t[key].(string)
			case string:
				n = t
			}
			all = append(all, n)
			if strings.HasPrefix(n, prefix) {
				hit = append(hit, n)
			}
		}
		return
	}
	allScenes, scenes := names(req("GetSceneList", nil), "scenes", "sceneName")
	_, groups := names(req("GetGroupList", nil), "groups", "")
	allInputs, inputs := names(req("GetInputList", nil), "inputs", "inputName")
	filters := 0
	for _, src := range append(append([]string{}, allScenes...), allInputs...) {
		x, e := c.Req(ctx, "GetSourceFilterList", map[string]any{"sourceName": src})
		if e != nil {
			continue
		}
		if fl, _ := x["filters"].([]any); len(fl) > 0 && strings.HasPrefix(src, prefix) {
			filters += len(fl)
		}
	}
	fmt.Printf("prefix %q: scenes=%d groups=%d inputs=%d filters-on-managed-sources=%d\n", prefix, len(scenes), len(groups), len(inputs), filters)
	for _, n := range scenes {
		fmt.Println("  scene ", n)
	}
	for _, n := range groups {
		fmt.Println("  group ", n)
	}
	if len(inputs) > 0 {
		fmt.Printf("  inputs: %v\n", inputs[:min(len(inputs), 8)])
	}
	if len(scenes)+len(groups)+len(inputs)+filters > 0 {
		os.Exit(2)
	}
}
