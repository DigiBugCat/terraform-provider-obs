package tfprovider

import (
	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"

	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type genericFilterModel struct {
	ID           types.String `tfsdk:"id"`
	Collection   types.String `tfsdk:"collection"`
	ParentName   types.String `tfsdk:"parent_name"`
	ParentUUID   types.String `tfsdk:"parent_uuid"`
	Name         types.String `tfsdk:"name"`
	Kind         types.String `tfsdk:"kind"`
	SettingsJSON types.String `tfsdk:"settings_json"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	Index        types.Int64  `tfsdk:"index"`
}
type genericFilterResource struct {
	client *client
	spec   *FilterSpec
}

func NewGenericFilterResource() resource.Resource { return &genericFilterResource{} }
func (r *genericFilterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_filter"
	if r.spec != nil {
		resp.TypeName = req.ProviderTypeName + r.spec.TypeSuffix
	}
}
func (r *genericFilterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *genericFilterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{"id": identityString(), "collection": immutableString(true), "parent_name": schema.StringAttribute{Required: true}, "parent_uuid": immutableString(true), "name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}, "kind": immutableString(true), "settings_json": schema.StringAttribute{Required: true, Sensitive: true, Validators: []validator.String{genericSettingsValidator{}}}, "enabled": schema.BoolAttribute{Required: true}, "index": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.AtLeast(0)}}}}
	r.typedSchema(&resp.Schema)
}
func (r *genericFilterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m genericFilterModel
	resp.Diagnostics.Append(r.readModel(ctx, req.Plan, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.withKeyed(ctx, []string{m.ParentUUID.ValueString()}, func(cl requester) (resultErr error) {
		acknowledged := false
		defer func() {
			if resultErr != nil && acknowledged {
				resp.Diagnostics.Append(r.recoverFilterState(ctx, cl, m, &resp.State)...)
			}
		}()
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
		if err := genericFilterKindAvailable(ctx, cl, m.Kind.ValueString()); err != nil {
			return err
		}
		if err := r.validateTypedNative(ctx, cl, m); err != nil {
			return err
		}
		reply, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateSourceFilter", map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString(), "filterKind": m.Kind.ValueString(), "filterSettings": mustGenericSettings(m.SettingsJSON.ValueString())})
		acknowledged = err == nil || reply != nil || isAuthoritativeNativeInterruption(err)
		if acknowledged {
			m.ID = types.StringValue(fmt.Sprintf("%s|%s|%s", m.Collection.ValueString(), m.ParentUUID.ValueString(), m.Name.ValueString()))
		}
		if err != nil {
			return err
		}

		if _, err := mutationBatch(ctx, cl, m.Collection.ValueString(), []ws.BatchItem{
			{Type: "SetSourceFilterEnabled", Data: map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString(), "filterEnabled": m.Enabled.ValueBool()}},
			{Type: "SetSourceFilterIndex", Data: map[string]any{"sourceUuid": m.ParentUUID.ValueString(), "filterName": m.Name.ValueString(), "filterIndex": mm.Index.ValueInt64()}},
		}); err != nil {
			return err
		}
		return waitGenericFilterExpected(ctx, cl, m.ParentName.ValueString(), mm)
	})
	if err != nil {
		resp.Diagnostics.AddError("Create generic filter failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.writeModel(ctx, &resp.State, &m)...)
}
func (r *genericFilterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m genericFilterModel
	resp.Diagnostics.Append(r.readModel(ctx, req.State, &m)...)
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
		if !(got.Kind == m.Kind.ValueString()) {
			return fmt.Errorf("same-name filter kind rebound")
		}

		// Failed readback may have left settings explicitly unknown. Configuration
		// acquires its managed keys again on apply; do not adopt all live settings.
		if m.SettingsJSON.IsNull() || m.SettingsJSON.IsUnknown() {
			m.SettingsJSON = types.StringValue("{}")
		}
		settings, err := observedGenericSettings(ctx, cl, got, m.SettingsJSON.ValueString())
		if err != nil {
			return err
		}
		if !genericJSONEqual(settings, mustGenericSettings(m.SettingsJSON.ValueString())) {
			b, _ := json.Marshal(settings)
			m.SettingsJSON = types.StringValue(string(b))
		}
		m.Enabled = types.BoolValue(got.Enabled)
		m.Index = types.Int64Value(got.Index)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Read generic filter failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(r.writeModel(ctx, &resp.State, &m)...)
	}
}
func (r *genericFilterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state genericFilterModel
	resp.Diagnostics.Append(r.readModel(ctx, req.Plan, &plan)...)
	resp.Diagnostics.Append(r.readModel(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.withKeyed(ctx, []string{plan.ParentUUID.ValueString()}, func(cl requester) (resultErr error) {
		attempted := false
		defer func() {
			if resultErr != nil && attempted {
				plan.ID = state.ID
				resp.Diagnostics.Append(r.recoverFilterState(ctx, cl, plan, &resp.State)...)
			}
		}()
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		ok, err := existingSourceIdentity(ctx, cl, plan.ParentName.ValueString(), plan.ParentUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("parent %q is missing or rebound; refresh before apply", plan.ParentName.ValueString())
		}
		xs, err := sourceFilters(ctx, cl, plan.ParentName.ValueString())
		if err != nil {
			return err
		}
		got, ok, err := findFilter(xs, plan.Name.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("filter %q is missing on %q; refresh before apply", plan.Name.ValueString(), plan.ParentName.ValueString())
		}
		if !(got.Kind == plan.Kind.ValueString()) {
			return fmt.Errorf("same-name filter kind rebound; refusing all writes")
		}
		if plan.Index.ValueInt64() < 0 || plan.Index.ValueInt64() >= int64(len(xs)) {
			return fmt.Errorf("filter index outside live chain")
		}
		if err := r.validateTypedNative(ctx, cl, plan); err != nil {
			return err
		}
		if got.Index != plan.Index.ValueInt64() {
			for _, f := range xs {
				if f.Name != got.Name && f.Index == plan.Index.ValueInt64() {
					return fmt.Errorf("refusing to displace filter %q at absolute index %d", f.Name, f.Index)
				}
			}
			attempted = true
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSourceFilterIndex", map[string]any{"sourceUuid": plan.ParentUUID.ValueString(), "filterName": plan.Name.ValueString(), "filterIndex": plan.Index.ValueInt64()}); err != nil {
				return err
			}
		}
		desired := mustGenericSettings(plan.SettingsJSON.ValueString())
		settings, err := genericFilterSettingsForUpdate(ctx, cl, got, desired)
		if err != nil {
			return err
		}

		if !genericJSONEqual(settings, desired) {
			attempted = true
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSourceFilterSettings", map[string]any{"sourceUuid": plan.ParentUUID.ValueString(), "filterName": plan.Name.ValueString(), "filterSettings": desired, "overlay": true}); err != nil {
				return err
			}
		}
		if got.Enabled != plan.Enabled.ValueBool() {
			attempted = true
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSourceFilterEnabled", map[string]any{"sourceUuid": plan.ParentUUID.ValueString(), "filterName": plan.Name.ValueString(), "filterEnabled": plan.Enabled.ValueBool()}); err != nil {
				return err
			}
		}
		plan.ID = state.ID
		return waitGenericFilterExpected(ctx, cl, plan.ParentName.ValueString(), plan)
	})
	if err != nil {
		resp.Diagnostics.AddError("Update generic filter failed", err.Error())
		return
	}
	resp.Diagnostics.Append(r.writeModel(ctx, &resp.State, &plan)...)
}
func (r *genericFilterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m genericFilterModel
	resp.Diagnostics.Append(r.readModel(ctx, req.State, &m)...)
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
		if !(got.Kind == m.Kind.ValueString()) {
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
		resp.Diagnostics.AddError("Delete generic filter failed", err.Error())
	}
}

func (r *genericFilterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var id struct {
		Collection string          `json:"collection"`
		ParentUUID string          `json:"parent_uuid"`
		ParentName string          `json:"parent_name"`
		Name       string          `json:"name"`
		Kind       string          `json:"kind"`
		Settings   json.RawMessage `json:"settings"`
	}
	parsed := false
	if r.spec != nil && r.spec.ImportID != nil {
		if c, pu, pn, n, ok := r.spec.ImportID(req.ID); ok {
			id.Collection, id.ParentUUID, id.ParentName, id.Name, parsed = c, pu, pn, n, true
		}
	}
	if !parsed {
		if err := json.Unmarshal([]byte(req.ID), &id); err != nil {
			id = struct {
				Collection string          `json:"collection"`
				ParentUUID string          `json:"parent_uuid"`
				ParentName string          `json:"parent_name"`
				Name       string          `json:"name"`
				Kind       string          `json:"kind"`
				Settings   json.RawMessage `json:"settings"`
			}{}
		}
	}
	if id.Collection == "" || id.ParentUUID == "" || id.ParentName == "" || id.Name == "" || (id.Kind == "" && r.spec == nil) {
		resp.Diagnostics.AddError("Invalid generic filter import ID", "Use JSON with collection,parent_uuid,parent_name,name,kind,settings (object of explicitly managed keys).")
		return
	}
	if len(id.Settings) == 0 {
		id.Settings = json.RawMessage("{}")
	}
	if r.spec != nil {
		id.Kind = r.spec.Kind
	}
	if _, err := parseGenericSettings(string(id.Settings)); err != nil {
		resp.Diagnostics.AddError("Invalid import settings", err.Error())
		return
	}
	attrs := map[string]any{"id": fmt.Sprintf("%s|%s|%s", id.Collection, id.ParentUUID, id.Name), "collection": id.Collection, "parent_uuid": id.ParentUUID, "parent_name": id.ParentName, "name": id.Name, "kind": id.Kind, "settings_json": string(id.Settings)}
	if r.spec != nil {
		delete(attrs, "settings_json")
		for k, t := range r.spec.Settings {
			switch t {
			case FilterString:
				attrs[k] = ""
			case FilterBool:
				attrs[k] = false
			case FilterFloat64:
				attrs[k] = float64(0)
			case FilterInt64:
				attrs[k] = int64(0)
			}
		}
	}
	for k, v := range attrs {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), v)...)
	}
}

var _ resource.ResourceWithConfigure = (*genericFilterResource)(nil)
var _ resource.ResourceWithImportState = (*genericFilterResource)(nil)

// Recovery records observed mutable fields only. If readback fails, null values
// explicitly preserve uncertainty rather than claiming the plan was executed.
func (r *genericFilterResource) recoverFilterState(ctx context.Context, cl requester, identity genericFilterModel, dst *tfsdk.State) diag.Diagnostics {
	var ds diag.Diagnostics
	for k, v := range map[string]any{"id": identity.ID, "collection": identity.Collection, "parent_name": identity.ParentName, "parent_uuid": identity.ParentUUID, "name": identity.Name, "kind": identity.Kind, "enabled": types.BoolNull(), "index": types.Int64Null()} {
		ds.Append(dst.SetAttribute(ctx, path.Root(k), v)...)
	}
	if r.spec == nil {
		ds.Append(dst.SetAttribute(ctx, path.Root("settings_json"), types.StringNull())...)
	} else {
		for k, t := range r.spec.Settings {
			var v any
			switch t {
			case FilterString:
				v = types.StringNull()
			case FilterBool:
				v = types.BoolNull()
			case FilterInt64:
				v = types.Int64Null()
			case FilterFloat64:
				v = types.Float64Null()
			}
			ds.Append(dst.SetAttribute(ctx, path.Root(k), v)...)
		}
	}
	fail := func(err error) diag.Diagnostics {
		ds.AddWarning("Filter partial state could not be fully observed", err.Error())
		return ds
	}
	if _, err := ensureCollection(ctx, cl, identity.Collection.ValueString()); err != nil {
		return fail(err)
	}
	ok, err := existingSourceIdentity(ctx, cl, identity.ParentName.ValueString(), identity.ParentUUID.ValueString())
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("parent identity unavailable during recovery"))
	}
	xs, err := sourceFilters(ctx, cl, identity.ParentName.ValueString())
	if err != nil {
		return fail(err)
	}
	got, ok, err := findFilter(xs, identity.Name.ValueString())
	if err != nil {
		return fail(err)
	}
	if !ok || got.Kind != identity.Kind.ValueString() {
		return fail(fmt.Errorf("filter missing or kind rebound during recovery"))
	}
	identity.Enabled = types.BoolValue(got.Enabled)
	identity.Index = types.Int64Value(got.Index)
	ds.Append(dst.SetAttribute(ctx, path.Root("enabled"), identity.Enabled)...)
	ds.Append(dst.SetAttribute(ctx, path.Root("index"), identity.Index)...)
	values, err := observedGenericSettings(ctx, cl, got, identity.SettingsJSON.ValueString())
	if err != nil {
		return fail(err)
	}
	if !genericJSONEqual(values, mustGenericSettings(identity.SettingsJSON.ValueString())) {
		b, err := json.Marshal(values)
		if err != nil {
			return fail(err)
		}
		identity.SettingsJSON = types.StringValue(string(b))
	}
	ds.Append(r.writeModel(ctx, dst, &identity)...)
	return ds
}

// genericFilterSettingsForUpdate resolves the current value of every managed key from
// the live filter, then kind defaults. A key OBS reports in neither is a new plugin
// setting the update introduces, so it is simply written rather than refused.
func genericFilterSettingsForUpdate(ctx context.Context, cl requester, got filterObservation, desired map[string]any) (map[string]any, error) {
	defaults := map[string]any{}
	for k := range desired {
		if _, ok := got.Settings[k]; !ok {
			x, err := request(ctx, cl, "GetSourceFilterDefaultSettings", map[string]any{"filterKind": got.Kind})
			if err != nil {
				return nil, err
			}
			var ok bool
			defaults, ok = x["defaultFilterSettings"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("GetSourceFilterDefaultSettings omitted object")
			}
			break
		}
	}
	values := map[string]any{}
	for k := range desired {
		if v, ok := got.Settings[k]; ok {
			values[k] = v
		} else if v, ok := defaults[k]; ok {
			values[k] = v
		}
	}
	return values, nil
}
