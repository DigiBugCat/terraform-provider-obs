package tfprovider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type filterRecoveryFake struct {
	base          genericFilterFake
	breakReadback bool
	wrote         bool
	normalize     bool
}

func (f *filterRecoveryFake) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if typ == "GetSourceFilterDefaultSettings" {
		return map[string]any{"defaultFilterSettings": map[string]any{}}, nil
	}
	if typ == "GetSourceFilterList" && f.breakReadback && f.wrote {
		return nil, fmt.Errorf("readback unavailable")
	}
	x, err := f.base.Req(ctx, typ, data)
	if err == nil && (typ == "CreateSourceFilter" || typ == "SetSourceFilterSettings") {
		f.wrote = true
		if f.normalize {
			f.base.filter.Settings["gain"] = float64(4)
		}
	}
	return x, err
}
func TestGenericFilterNewManagedKeyOverlay(t *testing.T) {
	ctx := context.Background()
	f := &filterRecoveryFake{base: genericFilterFake{filter: &filterObservation{Name: "fx", Kind: "plugin_filter", Enabled: false, Settings: map[string]any{"private": "keep"}}}}
	r := &genericFilterResource{client: &client{testRequester: f}}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	old := genericTestModel()
	old.SettingsJSON = types.StringValue("{}")
	plan := old
	plan.SettingsJSON = types.StringValue(`{"new_plugin_key":{"value":9}}`)
	state := groupFixtureSchemaAndState(t, ctx, sc.Schema, &old)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{State: state, Plan: groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &plan)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if f.base.sent["overlay"] != true || f.base.filter.Settings["private"] != "keep" {
		t.Fatal("overlay did not preserve unowned values")
	}
	if !genericJSONEqual(mapValue(f.base.filter.Settings["new_plugin_key"]), map[string]any{"value": float64(9)}) {
		t.Fatal("new key not added")
	}
}
func TestGenericFilterCreateFailureRecordsObservedPrefix(t *testing.T) {
	for _, fail := range []string{"SetSourceFilterEnabled", "SetSourceFilterIndex"} {
		t.Run(fail, func(t *testing.T) {
			ctx := context.Background()
			f := &filterRecoveryFake{base: genericFilterFake{fail: fail}, normalize: true}
			r := &genericFilterResource{client: &client{testRequester: f}}
			var sc resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sc)
			plan := genericTestModel()
			plan.ID = types.StringUnknown()
			resp := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, sc.Schema, &plan)}
			r.Create(ctx, resource.CreateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &plan)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected mutation error")
			}
			var got genericFilterModel
			groupFixtureRequireState(t, ctx, resp.State, &got)
			if got.ID.ValueString() != "lab|p|fx" || got.SettingsJSON.ValueString() != `{"gain":4}` {
				t.Fatal("state claims intended rather than observed settings", got)
			}
			if got.Enabled.ValueBool() != f.base.filter.Enabled || got.Index.ValueInt64() != f.base.filter.Index {
				t.Fatal("state did not retain observed prefix")
			}
		})
	}
}
func TestGenericFilterUpdateFailureRecordsSettingsBeforeEnableFailure(t *testing.T) {
	ctx := context.Background()
	f := &filterRecoveryFake{base: genericFilterFake{fail: "SetSourceFilterEnabled", filter: &filterObservation{Name: "fx", Kind: "plugin_filter", Enabled: true, Settings: map[string]any{"gain": float64(1)}}}, normalize: true}
	r := &genericFilterResource{client: &client{testRequester: f}}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	old := genericTestModel()
	old.Enabled = types.BoolValue(true)
	old.SettingsJSON = types.StringValue(`{"gain":1}`)
	plan := genericTestModel()
	state := groupFixtureSchemaAndState(t, ctx, sc.Schema, &old)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{State: state, Plan: groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected failure")
	}
	var got genericFilterModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.SettingsJSON.ValueString() != `{"gain":4}` || !got.Enabled.ValueBool() || got.ID != old.ID {
		t.Fatal("completed prefix lost", got)
	}
	if !strings.Contains(fmt.Sprint(resp.Diagnostics), "SetSourceFilterEnabled") {
		t.Fatal("primary error hidden")
	}
}
func TestGenericFilterFailedReadbackDoesNotPromotePlan(t *testing.T) {
	ctx := context.Background()
	f := &filterRecoveryFake{base: genericFilterFake{fail: "SetSourceFilterEnabled"}, breakReadback: true}
	r := &genericFilterResource{client: &client{testRequester: f}}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	plan := genericTestModel()
	plan.ID = types.StringUnknown()
	resp := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, sc.Schema, &plan)}
	r.Create(ctx, resource.CreateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected failure")
	}
	var got genericFilterModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.ID.ValueString() != "lab|p|fx" || !got.SettingsJSON.IsNull() || !got.Enabled.IsNull() || !got.Index.IsNull() {
		t.Fatal("unobserved desired values persisted", got)
	}
	if !strings.Contains(fmt.Sprint(resp.Diagnostics), "readback unavailable") || !strings.Contains(fmt.Sprint(resp.Diagnostics), "SetSourceFilterEnabled") {
		t.Fatal("primary/recovery diagnostics missing")
	}
	f.breakReadback = false
	read := resource.ReadResponse{State: resp.State}
	r.Read(ctx, resource.ReadRequest{State: resp.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal("partial state could not refresh", read.Diagnostics)
	}
	groupFixtureRequireState(t, ctx, read.State, &got)
	if got.SettingsJSON.ValueString() != "{}" || !got.Enabled.ValueBool() {
		t.Fatal("recovery refresh must retain native placement without adopting settings", got)
	}

}
func TestGenericFilterAcknowledgedCreateInterruptionRecordsNativeEnabled(t *testing.T) {
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "CreateSourceFilter")
	ctx := context.Background()
	f := &filterRecoveryFake{}
	r := &genericFilterResource{client: &client{testRequester: f}}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	plan := genericTestModel()
	plan.ID = types.StringUnknown()
	resp := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, sc.Schema, &plan)}
	r.Create(ctx, resource.CreateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &plan)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected failure")
	}
	var got genericFilterModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.ID.ValueString() != "lab|p|fx" || !got.Enabled.ValueBool() {
		t.Fatal("create default enabled state not observed")
	}
	for _, c := range f.base.calls {
		if strings.HasPrefix(c, "SetSource") {
			t.Fatal("write continued after interruption")
		}
	}
}
