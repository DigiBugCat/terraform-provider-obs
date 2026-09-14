package tfprovider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"testing"
)

func TestFilterRefreshRecoversUnknownPartialSettings(t *testing.T) {
	ctx := context.Background()
	for _, typed := range []bool{false, true} {
		f := &filterRecoveryFake{base: genericFilterFake{filter: &filterObservation{Name: "fx", Kind: "plugin_filter", Enabled: true, Index: 0, Settings: map[string]any{"gain": float64(7), "private": "never adopt"}}}}
		r := &genericFilterResource{client: &client{testRequester: f}}
		if typed {
			r.spec = &FilterSpec{TypeSuffix: "_typed", Kind: "plugin_filter", Settings: map[string]FilterSettingType{"gain": FilterFloat64}}
		}
		var sc resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sc)
		m := genericTestModel()
		state := tfsdk.State{Raw: tftypes.NewValue(sc.Schema.Type().TerraformType(ctx), nil), Schema: sc.Schema}
		if d := r.writeModel(ctx, &state, &m); d.HasError() {
			t.Fatal(d)
		}
		key := "settings_json"
		var v any = types.StringNull()
		if typed {
			key = "gain"
			v = types.Float64Null()
		}
		if d := state.SetAttribute(ctx, path.Root(key), v); d.HasError() {
			t.Fatal(d)
		}
		response := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("typed=%v: %v", typed, response.Diagnostics)
		}
		if typed {
			var gain types.Float64
			response.State.GetAttribute(ctx, path.Root("gain"), &gain)
			if gain.ValueFloat64() != 7 {
				t.Fatal("typed read did not restore actual")
			}
		} else {
			var raw types.String
			response.State.GetAttribute(ctx, path.Root("settings_json"), &raw)
			if raw.ValueString() != "{}" {
				t.Fatal("unknown generic ownership adopted live settings")
			}
		}
	}
}
