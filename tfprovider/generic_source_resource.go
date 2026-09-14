package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type genericSourceResource struct{ client *client }

func NewGenericSourceResource() resource.Resource { return &genericSourceResource{} }
func (r *genericSourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_source"
}
func (r *genericSourceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *genericSourceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state genericSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.AnchorSceneName.IsNull() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, tfpath.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, tfpath.Root("anchor_item_id"), types.Int64Unknown())...)
}
func (r *genericSourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true), "name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}, "input_uuid": identityString(),
		"kind": immutableString(true), "settings_json": schema.StringAttribute{Required: true, Sensitive: true, Validators: []validator.String{genericSettingsValidator{}}, Description: "JSON object of managed top-level settings. Writes overlay these keys; omitted keys retain OBS values. Nested objects are managed as whole values."},
		"anchor_scene_name": schema.StringAttribute{Required: true}, "anchor_scene_uuid": immutableString(true), "anchor_item_id": identityInt64(), "anchor_enabled": schema.BoolAttribute{Required: true}, "anchor_index": schema.Int64Attribute{Required: true}, "anchor_transform": transformSchema(),
	}}
}
func (r *genericSourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m genericSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGenericSource(m); err != nil {
		resp.Diagnostics.AddError("Invalid source", err.Error())
		return
	}
	want, _ := parseGenericSettings(m.SettingsJSON.ValueString())
	err := r.client.withKeyed(ctx, []string{m.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		if err := requireGenericSourceKind(ctx, cl, m.Kind.ValueString()); err != nil {
			return err
		}
		if _, ok, err := inputByName(ctx, cl, m.Name.ValueString()); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("input exists; import it")
		}
		scene, ok, err := sceneByUUID(ctx, cl, m.AnchorSceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok || scene.Name != m.AnchorSceneName.ValueString() {
			return fmt.Errorf("anchor scene mismatch")
		}
		x, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateInput", map[string]any{"sceneUuid": scene.UUID, "inputName": m.Name.ValueString(), "inputKind": m.Kind.ValueString(), "inputSettings": want, "sceneItemEnabled": m.AnchorEnabled.ValueBool()})
		writeErr := err
		id := int64Value(x["sceneItemId"])
		uuid := stringValue(x["inputUuid"])
		if id <= 0 || uuid == "" {
			if writeErr != nil {
				return writeErr
			}
			return fmt.Errorf("CreateInput omitted identity; inspect OBS before retrying")
		}
		m.InputUUID = types.StringValue(uuid)
		m.AnchorItemID = types.Int64Value(id)
		m.ID = types.StringValue(inputID(m.Collection.ValueString(), uuid, scene.UUID, id))
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		if writeErr != nil {
			return writeErr
		}
		if m.AnchorTransform == nil {
			return fmt.Errorf("anchor_transform required")
		}
		siblings, err := containerItemCount(ctx, cl, scene.Name, false)
		if err != nil {
			return err
		}
		anchorIndex := clampCreateIndex(m.AnchorIndex.ValueInt64(), siblings-1)
		if err := setItem(ctx, cl, m.Collection.ValueString(), scene.Name, id, m.AnchorEnabled.ValueBool(), anchorIndex, *m.AnchorTransform); err != nil {
			return err
		}
		if _, err := waitGenericSource(ctx, cl, uuid, m.Name.ValueString(), m.Kind.ValueString(), want); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, uuid, m.AnchorEnabled.ValueBool(), anchorIndex, *m.AnchorTransform, postconditionAttempts, postconditionDelay); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS source failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *genericSourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m genericSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		in, ok, err := genericSourceByUUID(ctx, cl, m.InputUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			resp.State.RemoveResource(ctx)
			return nil
		}
		if (!m.Name.IsNull() && in.Name != m.Name.ValueString()) || (!m.Kind.IsNull() && in.Kind != m.Kind.ValueString()) {
			return fmt.Errorf("input identity rebound")
		}
		m.Name = types.StringValue(in.Name)
		effective, err := observedGenericSourceSettings(ctx, cl, in)
		if err != nil {
			return err
		}
		m.Kind = types.StringValue(in.Kind)
		projected, err := projectGenericSourceJSON(m.SettingsJSON, effective)
		if err != nil {
			return err
		}
		m.SettingsJSON = projected
		scene, ok, err := sceneByUUID(ctx, cl, m.AnchorSceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			m.AnchorSceneName = types.StringNull()
			return nil
		}
		m.AnchorSceneName = types.StringValue(scene.Name)
		it, ok, err := itemByID(ctx, cl, scene.Name, m.AnchorItemID.ValueInt64())
		if err != nil {
			return err
		}
		if !ok {
			m.AnchorSceneName = types.StringNull()
			return nil
		}
		if err := anchorIdentityError(true, it, in.UUID); err != nil {
			return err
		}
		m.AnchorEnabled = types.BoolValue(it.Enabled)
		m.AnchorIndex = types.Int64Value(it.Index)
		m.AnchorTransform = retainAuthoredTransform(it.Transform, m.AnchorTransform)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Read OBS source failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *genericSourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state genericSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGenericSource(plan); err != nil {
		resp.Diagnostics.AddError("Invalid source", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		if err := requireGenericSourceKind(ctx, cl, plan.Kind.ValueString()); err != nil {
			return err
		}
		in, ok, err := genericSourceByUUID(ctx, cl, state.InputUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("input %s disappeared; refresh before apply", state.InputUUID.ValueString())
		}
		if in.Name != plan.Name.ValueString() || in.Kind != plan.Kind.ValueString() {
			return fmt.Errorf("input identity/name/kind rebound")
		}
		scene, ok, err := sceneByUUID(ctx, cl, plan.AnchorSceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok || scene.Name != plan.AnchorSceneName.ValueString() {
			return fmt.Errorf("anchor scene %s is missing or renamed; refresh before apply", plan.AnchorSceneUUID.ValueString())
		}
		if plan.AnchorTransform == nil {
			return fmt.Errorf("anchor_transform required")
		}
		want, _ := parseGenericSettings(plan.SettingsJSON.ValueString())
		effective, err := observedGenericSourceSettings(ctx, cl, in)
		if err != nil {
			return err
		}
		id := state.AnchorItemID.ValueInt64()
		it, exists, err := itemByID(ctx, cl, scene.Name, id)
		if err != nil {
			return err
		}
		if err := anchorIdentityError(exists, it, in.UUID); err != nil {
			return err
		}
		if mismatch := genericSettingsMatch(effective, want); !mismatch {
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetInputSettings", map[string]any{"inputUuid": in.UUID, "inputSettings": want, "overlay": true}); err != nil {
				return err
			}
		}
		siblings, err := containerItemCount(ctx, cl, scene.Name, false)
		if err != nil {
			return err
		}
		// A re-created anchor lands at the end of the list; an existing one moves within it.
		// State keeps the declared index (see clampCreateIndex).
		anchorIndex := clampCreateIndex(plan.AnchorIndex.ValueInt64(), siblings-1)
		if !exists {
			anchorIndex = clampCreateIndex(plan.AnchorIndex.ValueInt64(), siblings)
		}
		if !exists {
			x, err := mutation(ctx, cl, plan.Collection.ValueString(), "CreateSceneItem", map[string]any{"sceneUuid": scene.UUID, "sourceUuid": in.UUID, "sceneItemEnabled": plan.AnchorEnabled.ValueBool()})
			writeErr := err
			id = int64Value(x["sceneItemId"])
			if id <= 0 {
				if writeErr != nil {
					return writeErr
				}
				return fmt.Errorf("CreateSceneItem acknowledged without item ID")
			}
			plan.InputUUID = state.InputUUID
			plan.AnchorItemID = types.Int64Value(id)
			plan.ID = types.StringValue(inputID(plan.Collection.ValueString(), in.UUID, scene.UUID, id))
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			if writeErr != nil {
				return writeErr
			}
		}
		if exists {
			if err := updateItem(ctx, cl, plan.Collection.ValueString(), scene.Name, it, plan.AnchorEnabled.ValueBool(), anchorIndex, *plan.AnchorTransform); err != nil {
				return err
			}
		} else if err := setItem(ctx, cl, plan.Collection.ValueString(), scene.Name, id, plan.AnchorEnabled.ValueBool(), anchorIndex, *plan.AnchorTransform); err != nil {
			return err
		}
		if _, err := waitGenericSource(ctx, cl, in.UUID, plan.Name.ValueString(), plan.Kind.ValueString(), want); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, in.UUID, plan.AnchorEnabled.ValueBool(), anchorIndex, *plan.AnchorTransform, postconditionAttempts, postconditionDelay); err != nil {
			return err
		}
		plan.InputUUID = state.InputUUID
		plan.AnchorItemID = types.Int64Value(id)
		plan.ID = types.StringValue(inputID(plan.Collection.ValueString(), in.UUID, scene.UUID, id))
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS source failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *genericSourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m genericSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		return deleteGenericSource(ctx, cl, m)
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS source failed", err.Error())
	}
}

func (r *genericSourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 4 {
		resp.Diagnostics.AddError("Invalid source import ID", "use collection|input_uuid|anchor_scene_uuid|anchor_item_id")
		return
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid source import ID", "collection and UUIDs must not be empty")
		return
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid source import ID", "anchor_item_id must be positive")
		return
	}
	for k, v := range map[string]any{"id": req.ID, "collection": parts[0], "input_uuid": parts[1], "anchor_scene_uuid": parts[2], "anchor_item_id": id, "settings_json": "{}"} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, tfpath.Root(k), v)...)
	}
}

var _ resource.ResourceWithConfigure = (*genericSourceResource)(nil)
var _ resource.ResourceWithImportState = (*genericSourceResource)(nil)
var _ resource.ResourceWithModifyPlan = (*genericSourceResource)(nil)

type genericSourceModel struct {
	ID              types.String    `tfsdk:"id"`
	Collection      types.String    `tfsdk:"collection"`
	Name            types.String    `tfsdk:"name"`
	Kind            types.String    `tfsdk:"kind"`
	SettingsJSON    types.String    `tfsdk:"settings_json"`
	InputUUID       types.String    `tfsdk:"input_uuid"`
	AnchorSceneName types.String    `tfsdk:"anchor_scene_name"`
	AnchorSceneUUID types.String    `tfsdk:"anchor_scene_uuid"`
	AnchorItemID    types.Int64     `tfsdk:"anchor_item_id"`
	AnchorEnabled   types.Bool      `tfsdk:"anchor_enabled"`
	AnchorIndex     types.Int64     `tfsdk:"anchor_index"`
	AnchorTransform *transformModel `tfsdk:"anchor_transform"`
}

func validateGenericSource(m genericSourceModel) error {
	if m.Name.ValueString() == "" || m.Kind.ValueString() == "" {
		return fmt.Errorf("name and kind must not be empty")
	}
	if m.AnchorTransform == nil {
		return fmt.Errorf("anchor_transform required")
	}
	if err := validateTransformBounds(*m.AnchorTransform); err != nil {
		return err
	}
	_, err := parseGenericSettings(m.SettingsJSON.ValueString())
	return err
}

// Generic inputs must not inherit typed image/media setting requirements.
func genericSourceByUUID(ctx context.Context, cl requester, uuid string) (inputObservation, bool, error) {
	all, err := inputs(ctx, cl)
	if err != nil {
		return inputObservation{}, false, err
	}
	for _, in := range all {
		if in.UUID != uuid {
			continue
		}
		x, err := request(ctx, cl, "GetInputSettings", map[string]any{"inputUuid": uuid})
		if err != nil {
			if inputSettingsNotFound(err) {
				return inputObservation{}, false, nil
			}
			return inputObservation{}, false, err
		}
		settings, ok := x["inputSettings"].(map[string]any)
		if !ok || stringValue(x["inputKind"]) != in.Kind {
			return inputObservation{}, false, fmt.Errorf("GetInputSettings omitted settings or disagrees with input kind")
		}
		in.Settings = settings
		return in, true, nil
	}
	return inputObservation{}, false, nil
}

func requireGenericSourceKind(ctx context.Context, cl requester, kind string) error {
	x, err := request(ctx, cl, "GetInputKindList", map[string]any{"unversioned": false})
	if err != nil {
		return err
	}
	kinds, err := requiredSlice(x, "inputKinds")
	if err != nil {
		return err
	}
	for _, v := range kinds {
		if stringValue(v) == kind {
			return nil
		}
	}
	return fmt.Errorf("input kind %q is not installed; use an exact advertised kind and install plugins explicitly", kind)
}

func observedGenericSourceSettings(ctx context.Context, cl requester, in inputObservation) (map[string]any, error) {
	x, err := request(ctx, cl, "GetInputDefaultSettings", map[string]any{"inputKind": in.Kind})
	if err != nil {
		return nil, err
	}
	defaults, ok := x["defaultInputSettings"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GetInputDefaultSettings omitted object")
	}
	effective := make(map[string]any, len(defaults)+len(in.Settings))
	for k, v := range defaults {
		effective[k] = v
	}
	for k, v := range in.Settings {
		effective[k] = v
	}
	return effective, nil
}

func genericSettingsMatch(effective, want map[string]any) bool {
	got, err := projectGenericSettings(effective, nil, want)
	return err == nil && genericJSONEqual(got, want)
}

func projectGenericSourceJSON(raw types.String, effective map[string]any) (types.String, error) {
	managed, err := parseGenericSettings(raw.ValueString())
	if err != nil {
		return types.StringNull(), err
	}
	got, err := projectGenericSettings(effective, nil, managed)
	if err != nil {
		return types.StringNull(), err
	}
	if genericJSONEqual(got, managed) {
		return raw, nil
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		return types.StringNull(), fmt.Errorf("cannot encode observed managed settings")
	}
	return types.StringValue(string(encoded)), nil
}

func waitGenericSource(ctx context.Context, cl requester, uuid, name, kind string, want map[string]any) (inputObservation, error) {
	var result inputObservation
	err := waitUntil(ctx, func() (bool, error) {
		in, ok, err := genericSourceByUUID(ctx, cl, uuid)
		if err != nil || !ok {
			return false, err
		}
		if in.Name != name || in.Kind != kind {
			return false, fmt.Errorf("source identity/name/kind rebound")
		}
		effective, err := observedGenericSourceSettings(ctx, cl, in)
		if err != nil {
			return false, err
		}
		result = in
		return genericSettingsMatch(effective, want), nil
	})
	if err != nil {
		return result, err
	}
	return result, recordTrace(map[string]any{"event": "postcondition", "object": "generic-source", "status": "passed", "input_uuid": uuid})
}

func deleteGenericSource(ctx context.Context, cl requester, m genericSourceModel) error {
	if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
		return err
	}
	in, exists, err := genericSourceByUUID(ctx, cl, m.InputUUID.ValueString())
	if err != nil || !exists {
		return err
	}
	if in.Name != m.Name.ValueString() || in.Kind != m.Kind.ValueString() {
		return fmt.Errorf("source identity/name/kind rebound; refusing deletion")
	}
	scene, sceneExists, err := sceneByUUID(ctx, cl, m.AnchorSceneUUID.ValueString())
	if err != nil {
		return err
	}
	var anchor itemObservation
	anchorExists := false
	if sceneExists {
		anchor, anchorExists, err = itemByID(ctx, cl, scene.Name, m.AnchorItemID.ValueInt64())
		if err != nil {
			return err
		}
		if err := anchorIdentityError(anchorExists, anchor, in.UUID); err != nil {
			return err
		}
	}
	extras, err := sourceReferences(ctx, cl, in.UUID, m.AnchorSceneUUID.ValueString(), m.AnchorItemID.ValueInt64())
	if err != nil {
		return err
	}
	// Scene items inside groups are not included in GetSceneItemList.
	x, err := request(ctx, cl, "GetGroupList", nil)
	if err != nil {
		return err
	}
	groups, err := requiredSlice(x, "groups")
	if err != nil {
		return err
	}
	for _, raw := range groups {
		name, ok := raw.(string)
		if !ok || name == "" {
			return fmt.Errorf("GetGroupList returned invalid group name")
		}
		x, err := request(ctx, cl, "GetGroupSceneItemList", map[string]any{"sceneName": name})
		if err != nil {
			return err
		}
		items, err := requiredSlice(x, "sceneItems")
		if err != nil {
			return err
		}
		for _, raw := range items {
			item := mapValue(raw)
			uuid, id := stringValue(item["sourceUuid"]), int64Value(item["sceneItemId"])
			if uuid == "" || id <= 0 {
				return fmt.Errorf("group item omitted identity; refusing deletion")
			}
			if uuid == in.UUID {
				extras = append(extras, fmt.Sprintf("group:%s/%d", name, id))
			}
		}
	}
	if len(extras) > 0 {
		return fmt.Errorf("refusing source destruction before mutation: extra source references %v", extras)
	}
	if !anchorExists {
		return waitRemovedSource(ctx, cl, m.Collection.ValueString(), in.UUID, m.AnchorSceneUUID.ValueString(), m.AnchorSceneName.ValueString())
	}
	if _, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveSceneItem", map[string]any{"sceneUuid": scene.UUID, "sceneItemId": anchor.ItemID}); err != nil {
		return err
	}
	return waitRemovedSource(ctx, cl, m.Collection.ValueString(), in.UUID, scene.UUID, scene.Name)
}
