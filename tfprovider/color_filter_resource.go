package tfprovider

import (
	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"

	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type filterObservation struct {
	Name, Kind string
	Enabled    bool
	Index      int64
	Settings   map[string]any
}

func isColorFilterKind(kind string) bool { return kind == "color_filter_v2" }

func resolveColorSettings(live, defaults map[string]any) (map[string]any, map[string]string, error) {
	out := map[string]any{}
	origins := map[string]string{}
	for _, k := range []string{"hue_shift", "opacity"} {
		v, ok := live[k]
		origin := "live"
		if !ok {
			v, ok = defaults[k]
			origin = "default"
		}
		if !ok {
			return nil, nil, fmt.Errorf("color setting %s absent from live and defaults", k)
		}
		n, nok := numberValue(v)
		if !nok {
			return nil, nil, fmt.Errorf("color setting %s has type %T, want number", k, v)
		}
		out[k] = n
		origins[k] = origin
	}
	return out, origins, nil
}
func observedColorSettings(ctx context.Context, cl requester, parentUUID string, g filterObservation) (map[string]any, error) {
	x, err := request(ctx, cl, "GetSourceFilterDefaultSettings", map[string]any{"filterKind": "color_filter_v2"})
	if err != nil {
		return nil, err
	}
	defaults, ok := x["defaultFilterSettings"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GetSourceFilterDefaultSettings omitted object")
	}
	values, origins, err := resolveColorSettings(g.Settings, defaults)
	if err != nil {
		return nil, err
	}
	if err := recordTrace(map[string]any{"event": "color-settings-observation", "parent_uuid": parentUUID, "filter_name": g.Name, "origins": origins}); err != nil {
		return nil, err
	}
	return values, nil
}

func sourceFilters(ctx context.Context, cl requester, parent string) ([]filterObservation, error) {
	x, err := request(ctx, cl, "GetSourceFilterList", map[string]any{"sourceName": parent})
	if err != nil {
		return nil, err
	}
	xs, err := requiredSlice(x, "filters")
	if err != nil {
		return nil, err
	}
	out := make([]filterObservation, 0, len(xs))
	for i, v := range xs {
		o, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("filter %d malformed", i)
		}
		name, kind := stringValue(o["filterName"]), stringValue(o["filterKind"])
		settings, ok := o["filterSettings"].(map[string]any)
		enabled, eok := o["filterEnabled"].(bool)
		idx, iok := numberValue(o["filterIndex"])
		if name == "" || kind == "" || !ok || !eok || !iok {
			return nil, fmt.Errorf("filter %d response incomplete", i)
		}
		out = append(out, filterObservation{name, kind, enabled, int64(idx), settings})
	}
	return out, nil
}
func findFilter(xs []filterObservation, name string) (filterObservation, bool, error) {
	matches := 0
	var out filterObservation
	for _, f := range xs {
		if f.Name == name {
			matches++
			out = f
		}
	}
	if matches > 1 {
		return out, false, fmt.Errorf("filter %q is ambiguous", name)
	}
	return out, matches == 1, nil
}
func filterMismatch(g filterObservation, settings map[string]any, m colorFilterModel) string {
	if !isColorFilterKind(g.Kind) {
		return "kind"
	}
	if g.Enabled != m.Enabled.ValueBool() {
		return "enabled"
	}
	if g.Index != m.Index.ValueInt64() {
		return "index"
	}
	h, hok := numberValue(settings["hue_shift"])
	o, ook := numberValue(settings["opacity"])
	if !hok || h != m.HueShift.ValueFloat64() {
		return "hue_shift"
	}
	if !ook || o != m.Opacity.ValueFloat64() {
		return "opacity"
	}
	return ""
}
func waitFilterExpected(ctx context.Context, cl requester, parent string, m colorFilterModel) error {
	last := ""
	for i := 0; i < 40; i++ {
		xs, err := sourceFilters(ctx, cl, parent)
		if err != nil {
			return err
		}
		got, ok, err := findFilter(xs, m.Name.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			last = "absent"
		} else if settings, settingsErr := observedColorSettings(ctx, cl, m.ParentUUID.ValueString(), got); settingsErr != nil {
			return settingsErr
		} else if mismatch := filterMismatch(got, settings, m); mismatch != "" {
			last = mismatch
		} else {
			return recordTrace(map[string]any{"event": "postcondition", "object": "color-filter", "status": "passed", "parent_uuid": m.ParentUUID.ValueString(), "filter_name": m.Name.ValueString()})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("filter postcondition did not converge: %s", last)
}

type colorFilterResource struct{ client *client }

func NewColorFilterResource() resource.Resource { return &colorFilterResource{} }
func (r *colorFilterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_color_filter"
}
func (r *colorFilterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *colorFilterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{"id": identityString(), "collection": immutableString(true), "parent_name": schema.StringAttribute{Required: true}, "parent_uuid": immutableString(true), "name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}, "kind": schema.StringAttribute{Computed: true}, "hue_shift": schema.Float64Attribute{Required: true}, "opacity": schema.Float64Attribute{Required: true}, "enabled": schema.BoolAttribute{Required: true}, "index": schema.Int64Attribute{Required: true}}}
}
func (r *colorFilterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m colorFilterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.Kind = types.StringValue("color_filter_v2")
	err := r.client.withKeyed(ctx, []string{m.ParentUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		ok, err := existingSourceIdentity(ctx, cl, m.ParentName.ValueString(), m.ParentUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("parent identity unavailable")
		}
		xs, err := sourceFilters(ctx, cl, m.ParentName.ValueString())
		if err != nil {
			return err
		}
		if _, exists, err := findFilter(xs, m.Name.ValueString()); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("filter exists; import it")
		}
		mm := m
		mm.Index = types.Int64Value(clampCreateIndex(m.Index.ValueInt64(), int64(len(xs))))
		if _, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateSourceFilter", map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString(), "filterKind": "color_filter_v2", "filterSettings": map[string]any{"hue_shift": m.HueShift.ValueFloat64(), "opacity": m.Opacity.ValueFloat64()}}); err != nil {
			return err
		}
		m.ID = types.StringValue(fmt.Sprintf("%s|%s|%s", m.Collection.ValueString(), m.ParentUUID.ValueString(), m.Name.ValueString()))
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		if _, err := mutationBatch(ctx, cl, m.Collection.ValueString(), []ws.BatchItem{
			{Type: "SetSourceFilterEnabled", Data: map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString(), "filterEnabled": m.Enabled.ValueBool()}},
			{Type: "SetSourceFilterIndex", Data: map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString(), "filterIndex": mm.Index.ValueInt64()}},
		}); err != nil {
			return err
		}
		return waitFilterExpected(ctx, cl, m.ParentName.ValueString(), mm)
	})
	if err != nil {
		resp.Diagnostics.AddError("Create color filter failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *colorFilterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m colorFilterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		ok, err := existingSourceIdentity(ctx, cl, m.ParentName.ValueString(), m.ParentUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			// Absence was proven by successful identity reads. A filter cannot
			// survive its source; let Terraform recreate the dependency chain.
			resp.State.RemoveResource(ctx)
			return nil
		}
		xs, err := sourceFilters(ctx, cl, m.ParentName.ValueString())
		if err != nil {
			return err
		}
		got, ok, err := findFilter(xs, m.Name.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			resp.State.RemoveResource(ctx)
			return nil
		}
		if !isColorFilterKind(got.Kind) {
			return fmt.Errorf("same-name filter kind rebound")
		}
		m.Kind = types.StringValue("color_filter_v2")
		settings, err := observedColorSettings(ctx, cl, m.ParentUUID.ValueString(), got)
		if err != nil {
			return err
		}
		h, hok := numberValue(settings["hue_shift"])
		o, ook := numberValue(settings["opacity"])
		if !hok || !ook {
			return fmt.Errorf("filter settings incomplete")
		}
		m.HueShift = types.Float64Value(h)
		m.Opacity = types.Float64Value(o)
		m.Enabled = types.BoolValue(got.Enabled)
		m.Index = types.Int64Value(got.Index)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Read color filter failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *colorFilterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state colorFilterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Kind = types.StringValue("color_filter_v2")
	err := r.client.withKeyed(ctx, []string{plan.ParentUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		ok, err := existingSourceIdentity(ctx, cl, plan.ParentName.ValueString(), plan.ParentUUID.ValueString())
		if err != nil || !ok {
			return fmt.Errorf("parent unavailable: %v", err)
		}
		xs, err := sourceFilters(ctx, cl, plan.ParentName.ValueString())
		if err != nil {
			return err
		}
		got, ok, err := findFilter(xs, plan.Name.ValueString())
		if err != nil || !ok {
			return fmt.Errorf("filter unavailable: %v", err)
		}
		if !isColorFilterKind(got.Kind) {
			return fmt.Errorf("same-name filter kind rebound; refusing all writes")
		}
		if got.Index != plan.Index.ValueInt64() {
			for _, f := range xs {
				if f.Name != got.Name && f.Index == plan.Index.ValueInt64() {
					return fmt.Errorf("refusing to displace filter %q at absolute index %d", f.Name, f.Index)
				}
			}
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSourceFilterIndex", map[string]any{"sourceUuid": plan.ParentUUID.ValueString(), "filterName": plan.Name.ValueString(), "filterIndex": plan.Index.ValueInt64()}); err != nil {
				return err
			}
		}
		settings, err := observedColorSettings(ctx, cl, plan.ParentUUID.ValueString(), got)
		if err != nil {
			return err
		}
		h, _ := numberValue(settings["hue_shift"])
		o, _ := numberValue(settings["opacity"])
		if h != plan.HueShift.ValueFloat64() || o != plan.Opacity.ValueFloat64() {
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSourceFilterSettings", map[string]any{"sourceUuid": plan.ParentUUID.ValueString(), "filterName": plan.Name.ValueString(), "filterSettings": map[string]any{"hue_shift": plan.HueShift.ValueFloat64(), "opacity": plan.Opacity.ValueFloat64()}, "overlay": true}); err != nil {
				return err
			}
		}
		if got.Enabled != plan.Enabled.ValueBool() {
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSourceFilterEnabled", map[string]any{"sourceUuid": plan.ParentUUID.ValueString(), "filterName": plan.Name.ValueString(), "filterEnabled": plan.Enabled.ValueBool()}); err != nil {
				return err
			}
		}
		plan.ID = state.ID
		return waitFilterExpected(ctx, cl, plan.ParentName.ValueString(), plan)
	})
	if err != nil {
		resp.Diagnostics.AddError("Update color filter failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *colorFilterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m colorFilterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.ParentUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		parentOK, err := existingSourceIdentity(ctx, cl, m.ParentName.ValueString(), m.ParentUUID.ValueString())
		if err != nil {
			return err
		}
		if !parentOK {
			// The parent and therefore this filter have already been deleted.
			return nil
		}
		xs, err := sourceFilters(ctx, cl, m.ParentName.ValueString())
		if err != nil {
			return err
		}
		got, ok, err := findFilter(xs, m.Name.ValueString())
		if err != nil || !ok {
			return err
		}
		if !isColorFilterKind(got.Kind) {
			return fmt.Errorf("same-name filter kind rebound; refusing delete")
		}
		if _, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveSourceFilter", map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString()}); err != nil {
			return err
		}
		return waitUntil(ctx, func() (bool, error) {
			xs, e := sourceFilters(ctx, cl, m.ParentName.ValueString())
			if e != nil {
				return false, e
			}
			_, ok, e = findFilter(xs, m.Name.ValueString())
			return !ok, e
		})
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete color filter failed", err.Error())
	}
}

func (r *colorFilterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 4 {
		resp.Diagnostics.AddError("Invalid color filter import ID", "use collection|parent_uuid|parent_name|filter_name")
		return
	}
	for k, v := range map[string]any{"id": fmt.Sprintf("%s|%s|%s", parts[0], parts[1], parts[3]), "collection": parts[0], "parent_uuid": parts[1], "parent_name": parts[2], "name": parts[3]} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), v)...)
	}
}

var _ resource.ResourceWithConfigure = (*colorFilterResource)(nil)
var _ resource.ResourceWithImportState = (*colorFilterResource)(nil)
