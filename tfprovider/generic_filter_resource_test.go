package tfprovider

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"testing"
)

type genericFilterFake struct {
	filter *filterObservation
	fail   string
	calls  []string
	sent   map[string]any
}

func (f *genericFilterFake) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, typ)
	if f.fail == typ {
		return nil, fmt.Errorf("injected %s", typ)
	}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": "lab"}, nil
	case "GetInputList":
		return map[string]any{"inputs": []any{map[string]any{"inputName": "parent", "inputUuid": "p", "inputKind": "color_source_v3"}}}, nil
	case "GetSourceFilterKindList":
		return map[string]any{"sourceFilterKinds": []any{"plugin_filter"}}, nil
	case "GetVersion":
		return map[string]any{"obsVersion": "32", "obsWebSocketVersion": "5.6", "availableRequests": []any{"CreateSourceFilter", "SetSourceFilterSettings", "SetSourceFilterEnabled", "SetSourceFilterIndex", "RemoveSourceFilter"}}, nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920., "baseHeight": 1080.}, nil
	case "GetSourceFilterList":
		xs := []any{}
		if f.filter != nil {
			g := f.filter
			xs = append(xs, map[string]any{"filterName": g.Name, "filterKind": g.Kind, "filterEnabled": g.Enabled, "filterIndex": float64(g.Index), "filterSettings": g.Settings})
		}
		return map[string]any{"filters": xs}, nil
	case "CreateSourceFilter":
		f.filter = &filterObservation{Name: "fx", Kind: "plugin_filter", Enabled: true, Settings: data["filterSettings"].(map[string]any)}
		return map[string]any{}, nil
	case "SetSourceFilterSettings":
		f.sent = data
		for k, v := range data["filterSettings"].(map[string]any) {
			f.filter.Settings[k] = v
		}
		return map[string]any{}, nil
	case "SetSourceFilterEnabled":
		f.filter.Enabled = data["filterEnabled"].(bool)
		return map[string]any{}, nil
	case "SetSourceFilterIndex":
		n, _ := numberValue(data["filterIndex"])
		f.filter.Index = int64(n)
		return map[string]any{}, nil
	case "RemoveSourceFilter":
		f.filter = nil
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("unexpected %s", typ)
}
func genericTestModel() genericFilterModel {
	return genericFilterModel{ID: types.StringValue("lab|p|fx"), Collection: types.StringValue("lab"), ParentName: types.StringValue("parent"), ParentUUID: types.StringValue("p"), Name: types.StringValue("fx"), Kind: types.StringValue("plugin_filter"), SettingsJSON: types.StringValue(`{ "gain": 2.0 }`), Enabled: types.BoolValue(false), Index: types.Int64Value(0)}
}
func TestGenericFilterPartialCreateRetainsIdentity(t *testing.T) {
	for _, fail := range []string{"SetSourceFilterEnabled", "SetSourceFilterIndex", ""} {
		t.Run(fail, func(t *testing.T) {
			ctx := context.Background()
			f := &genericFilterFake{fail: fail}
			r := &genericFilterResource{client: &client{testRequester: f}}
			var sc resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sc)
			m := genericTestModel()
			m.ID = types.StringUnknown()
			plan := lunaLifecycleSchemaAndPlan(t, ctx, sc.Schema, &m)
			resp := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc.Schema, &m)}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
			if resp.Diagnostics.HasError() != (fail != "") {
				t.Fatal(resp.Diagnostics)
			}
			var got genericFilterModel
			lunaLifecycleRequireState(t, ctx, resp.State, &got)
			if got.ID.ValueString() != "lab|p|fx" {
				t.Fatal("identity lost", got.ID)
			}
		})
	}
}
func TestGenericFilterOverlayAndSemanticRead(t *testing.T) {
	ctx := context.Background()
	f := &genericFilterFake{filter: &filterObservation{Name: "fx", Kind: "plugin_filter", Enabled: false, Settings: map[string]any{"gain": 1., "private": "preserved"}}}
	r := &genericFilterResource{client: &client{testRequester: f}}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	m := genericTestModel()
	state := lunaLifecycleSchemaAndState(t, ctx, sc.Schema, &m)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc.Schema, &m), State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if f.sent["overlay"] != true || f.filter.Settings["private"] != "preserved" {
		t.Fatal("unmanaged overwritten")
	}
	read := resource.ReadResponse{State: resp.State}
	r.Read(ctx, resource.ReadRequest{State: resp.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var got genericFilterModel
	lunaLifecycleRequireState(t, ctx, read.State, &got)
	if got.SettingsJSON.ValueString() != m.SettingsJSON.ValueString() {
		t.Fatal("semantic equal JSON rewritten")
	}
}
func TestGenericSettingsDefaultsMissingAndOwnership(t *testing.T) {
	managed, _ := parseGenericSettings(`{"gain":2,"nested":{"a":true}}`)
	live := map[string]any{"nested": map[string]any{"a": true}, "unmanaged": "secret"}
	got, e := projectGenericSettings(live, map[string]any{"gain": 2.}, managed)
	if e != nil || !genericJSONEqual(got, managed) {
		t.Fatal(got, e)
	}
	if _, e := projectGenericSettings(live, nil, managed); e == nil {
		t.Fatal("missing managed value accepted")
	}
	for _, s := range []string{"[]", "null", `{"a":null}`, "broken"} {
		if _, e := parseGenericSettings(s); e == nil {
			t.Fatal(s)
		}
	}
}

func TestGenericFilterKindAndWriteFailureRefuse(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"unavailable", "rebound", "write-failed", "authoritative-create"} {
		t.Run(scenario, func(t *testing.T) {
			f := &genericFilterFake{}
			r := &genericFilterResource{client: &client{testRequester: f}}
			var sc resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sc)
			m := genericTestModel()
			if scenario == "unavailable" {
				m.Kind = types.StringValue("missing_plugin")
				resp := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc.Schema, &m)}
				r.Create(ctx, resource.CreateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc.Schema, &m)}, &resp)
				if !resp.Diagnostics.HasError() || f.filter != nil {
					t.Fatal("unavailable kind mutated")
				}
				return
			}
			if scenario == "authoritative-create" {
				t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "CreateSourceFilter")
				m.ID = types.StringUnknown()
				resp := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc.Schema, &m)}
				r.Create(ctx, resource.CreateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc.Schema, &m)}, &resp)
				var got genericFilterModel
				lunaLifecycleRequireState(t, ctx, resp.State, &got)
				if !resp.Diagnostics.HasError() || got.ID.IsUnknown() {
					t.Fatal("authoritative identity lost")
				}
				return
			}
			f.filter = &filterObservation{Name: "fx", Kind: "plugin_filter", Enabled: true, Settings: map[string]any{"gain": 1.}}
			if scenario == "rebound" {
				f.filter.Kind = "different"
			} else {
				f.fail = "SetSourceFilterSettings"
			}
			state := lunaLifecycleSchemaAndState(t, ctx, sc.Schema, &m)
			resp := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc.Schema, &m), State: state}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("failure accepted")
			}
			for _, c := range f.calls {
				if c == "SetSourceFilterEnabled" {
					t.Fatal("continued after failure")
				}
			}
		})
	}
}
