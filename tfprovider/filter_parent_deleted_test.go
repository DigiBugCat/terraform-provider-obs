package tfprovider

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Protocol fake: no live OBS operations. Unexpected filter requests or writes
// fail, so parent absence cannot accidentally target a replacement source.
type deletedFilterParentFake struct {
	scenario string
	calls    []string
}

func (f *deletedFilterParentFake) Req(_ context.Context, typ string, _ map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, typ)
	if f.scenario == "failed-input-read" && typ == "GetInputList" {
		return nil, fmt.Errorf("connection lost")
	}
	switch typ {
	case "GetSceneCollectionList":
		name := "lab"
		if f.scenario == "wrong-collection" {
			name = "other"
		}
		return map[string]any{"currentSceneCollectionName": name}, nil
	case "GetInputList":
		inputs := []any{}
		if f.scenario == "replacement-input" {
			inputs = append(inputs, map[string]any{"inputName": "parent", "inputUuid": "replacement", "inputKind": "color_source_v3"})
		}
		if f.scenario == "renamed-input" {
			inputs = append(inputs, map[string]any{"inputName": "renamed", "inputUuid": "p", "inputKind": "color_source_v3"})
		}
		return map[string]any{"inputs": inputs}, nil
	case "GetSceneList":
		if f.scenario == "malformed-scene-read" {
			return map[string]any{}, nil
		}
		scenes := []any{map[string]any{"sceneName": "unrelated", "sceneUuid": "unrelated"}}
		if f.scenario == "replacement-scene" {
			scenes = append(scenes, map[string]any{"sceneName": "parent", "sceneUuid": "replacement"})
		}
		return map[string]any{"scenes": scenes}, nil
	case "GetSceneItemList":
		return map[string]any{"sceneItems": []any{}}, nil
	}
	return nil, fmt.Errorf("unexpected request after parent loss: %s", typ)
}

func TestFilterParentDeletionRefreshAndDelete(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"generic", "typed", "color"} {
		for _, scenario := range []string{"absent", "replacement-input", "replacement-scene", "renamed-input", "failed-input-read", "malformed-scene-read", "wrong-collection"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				f := &deletedFilterParentFake{scenario: scenario}
				var r resource.Resource
				g := &genericFilterResource{client: &client{testRequester: f}}
				switch kind {
				case "typed":
					// Typed filter resources (move_filters.go, obs_color_filter) share this lifecycle.
					g.spec = &FilterSpec{TypeSuffix: "_typed", Kind: "plugin_filter", Settings: map[string]FilterSettingType{"gain": FilterFloat64}}
				case "color":
					spec := colorFilterSpec()
					g.spec = &spec
				}
				r = g
				var sc resource.SchemaResponse
				r.Schema(ctx, resource.SchemaRequest{}, &sc)
				state := tfsdk.State{Schema: sc.Schema, Raw: tftypes.NewValue(sc.Schema.Type().TerraformType(ctx), nil)}
				m := genericTestModel()
				if kind == "color" {
					m.SettingsJSON = types.StringValue(`{"hue_shift":0,"opacity":1}`)
				}
				if g, ok := r.(*genericFilterResource); ok {
					if d := g.writeModel(ctx, &state, &m); d.HasError() {
						t.Fatal(d)
					}
				}
				read := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &read)
				wantError := scenario != "absent"
				if read.Diagnostics.HasError() != wantError || read.State.Raw.IsNull() == wantError {
					t.Fatalf("read null=%v diagnostics=%v", read.State.Raw.IsNull(), read.Diagnostics)
				}
				if wantError && !read.State.Raw.Equal(state.Raw) {
					t.Fatal("uncertain or conflicting read changed state")
				}
				delete := resource.DeleteResponse{State: state}
				r.Delete(ctx, resource.DeleteRequest{State: state}, &delete)
				if delete.Diagnostics.HasError() != wantError {
					t.Fatalf("delete diagnostics=%v", delete.Diagnostics)
				}
				for _, call := range f.calls {
					if !strings.HasPrefix(call, "Get") || call == "GetSourceFilterList" {
						t.Fatalf("accessed filter after parent loss: %s", call)
					}
				}
			})
		}
	}
}
