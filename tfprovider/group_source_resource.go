package tfprovider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type groupSourceResource struct{ client *client }

func NewGroupSourceResource() resource.Resource { return &groupSourceResource{} }
func (r *groupSourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_source"
}
func (r *groupSourceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *groupSourceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state groupSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.GroupName.IsNull() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("item_id"), types.Int64Unknown())...)
}
func (r *groupSourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true),
		"scene_name": schema.StringAttribute{Required: true}, "scene_uuid": immutableString(true),
		"group_name": schema.StringAttribute{Required: true}, "group_uuid": immutableString(true), "group_item_id": schema.Int64Attribute{Required: true},
		"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"kind": immutableString(true), "settings_json": schema.StringAttribute{Required: true, Sensitive: true, Validators: []validator.String{genericSettingsValidator{}}, Description: "JSON object of managed top-level settings. Writes overlay these keys; omitted keys retain OBS values. Nested objects are managed as whole values."},
		"input_uuid": identityString(), "item_id": identityInt64(), "placement": bridgePlacementSchema(),
		"observed_source_width": schema.Int64Attribute{Computed: true}, "observed_source_height": schema.Int64Attribute{Computed: true},
	}}
}

func validateGroupSource(m groupSourceModel) error {
	if m.Name.ValueString() == "" || m.Kind.ValueString() == "" {
		return fmt.Errorf("name and kind must not be empty")
	}
	if m.Placement == nil {
		return fmt.Errorf("placement is required")
	}
	if _, err := m.Placement.request(); err != nil {
		return err
	}
	_, err := parseGenericSettings(m.SettingsJSON.ValueString())
	return err
}

func (r *groupSourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m groupSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGroupSource(m); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group source", err.Error())
		return
	}
	want, _ := parseGenericSettings(m.SettingsJSON.ValueString())
	err := r.client.withKeyed(ctx, []string{m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		if err := requireGenericSourceKind(ctx, cl, m.Kind.ValueString()); err != nil {
			return err
		}
		if _, exists, err := inputByName(ctx, cl, m.Name.ValueString()); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("input exists; import or repair its exact placement")
		}
		children, err := containerItemCount(ctx, cl, m.GroupName.ValueString(), true)
		if err != nil {
			return err
		}
		mm := m
		clamped := *m.Placement
		clamped.Index = types.Int64Value(clampCreateIndex(m.Placement.Index.ValueInt64(), children))
		mm.Placement = &clamped
		placement, _ := mm.Placement.request()
		opID := bridgeOperationID("create-group-source", m.Name.ValueString())
		envelope, callErr := bridgeMutation(ctx, cl, m.Collection.ValueString(), "CreateInputInGroup", opID, map[string]any{
			"scene":     bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0),
			"group":     bridgeIdentity(m.GroupUUID.ValueString(), m.GroupName.ValueString(), m.GroupItemID.ValueInt64()),
			"inputName": m.Name.ValueString(), "inputKind": m.Kind.ValueString(),
			"inputSettings": want, "placement": placement,
		})
		var child bridgeChildObservation
		childErr := fmt.Errorf("bridge result is not trusted because the envelope was invalid")
		if callErr == nil || isAuthoritativeNativeInterruption(callErr) {
			child, childErr = bridgeChildFromResult(envelope.Result)
		}
		if childErr == nil {
			m.InputUUID, m.ItemID = types.StringValue(child.UUID), types.Int64Value(child.ItemID)
			m.ID = types.StringValue(groupSourceID(m.Collection.ValueString(), m.SceneUUID.ValueString(), m.GroupUUID.ValueString(), child.UUID, child.ItemID))
			m.SourceWidth, m.SourceHeight = types.Int64Value(child.Width), types.Int64Value(child.Height)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		}
		if callErr != nil {
			return callErr
		}
		if envelope.Outcome != "completed" {
			return fmt.Errorf("bridge CreateInputInGroup %s (%s): %s; re-observe before retry", envelope.Outcome, envelope.Code, envelope.Message)
		}
		if childErr != nil {
			return childErr
		}
		if child.Name != m.Name.ValueString() || bridgePlacementDifference(child.Placement, *mm.Placement) != "" {
			return fmt.Errorf("bridge group source returned mismatched identity/placement")
		}
		mm, mm.Placement = m, &clamped
		settled, err := waitGroupSourceChild(ctx, cl, mm, 40, 100*time.Millisecond)
		if err != nil {
			return err
		}
		m.SourceWidth, m.SourceHeight = types.Int64Value(settled.Width), types.Int64Value(settled.Height)
		_, err = waitGenericSource(ctx, cl, child.UUID, m.Name.ValueString(), m.Kind.ValueString(), want)
		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS group source failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *groupSourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m groupSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		if _, exists, err := genericSourceByUUID(ctx, cl, m.InputUUID.ValueString()); err != nil {
			return err
		} else if !exists {
			return errGroupSourceGone
		}
		if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", false); err != nil {
			return err
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupSourceGroupRequest(m))
		if err != nil {
			return err
		}
		if envelope.Outcome != "completed" {
			return readGroupSourceWithUnavailableParent(ctx, cl, &m, envelope)
		}
		group, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		child, exists, err := exactBridgeChild(group.Children, m.ItemID.ValueInt64(), m.InputUUID.ValueString())
		if err != nil {
			return err
		}
		if !exists {
			in, inputExists, err := genericSourceByUUID(ctx, cl, m.InputUUID.ValueString())
			if err != nil {
				return err
			}
			if !inputExists {
				return errGroupSourceGone
			}
			if in.Name != m.Name.ValueString() || in.Kind != m.Kind.ValueString() {
				return fmt.Errorf("surviving group source identity rebound")
			}
			m.GroupName = types.StringNull()
			return nil
		}
		if !m.Name.IsNull() && child.Name != m.Name.ValueString() {
			return fmt.Errorf("group child identity/name/kind rebound")
		}
		in, inputExists, err := genericSourceByUUID(ctx, cl, child.UUID)
		if err != nil || !inputExists {
			return fmt.Errorf("group child source unavailable: %v", err)
		}
		m.SceneName, m.GroupName = types.StringValue(group.SceneName), types.StringValue(group.Name)
		// Native bridge inputKind is unversioned. Core OBS WebSocket reports
		// the exact installed kind used in Terraform configuration.
		if in.Name != child.Name || (!m.Kind.IsNull() && in.Kind != m.Kind.ValueString()) {
			return fmt.Errorf("group source identity/name/kind rebound")
		}
		m.Name, m.Kind = types.StringValue(in.Name), types.StringValue(in.Kind)
		effective, err := observedGenericSourceSettings(ctx, cl, in)
		if err != nil {
			return err
		}
		projected, err := projectGenericSourceJSON(m.SettingsJSON, effective)
		if err != nil {
			return err
		}
		m.SettingsJSON = projected
		if m.Placement != nil {
			retained := retainAuthoredBridgePlacement(child.Placement, *m.Placement)
			m.Placement = &retained
		} else {
			m.Placement = &child.Placement
		}
		m.SourceWidth, m.SourceHeight = types.Int64Value(child.Width), types.Int64Value(child.Height)
		return nil
	})
	if err != nil {
		if errors.Is(err, errGroupSourceGone) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read OBS group source failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *groupSourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGroupSource(plan); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group source", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		in, inputExists, err := genericSourceByUUID(ctx, cl, state.InputUUID.ValueString())
		if err != nil || !inputExists {
			return fmt.Errorf("group source disappeared; refresh before apply: %v", err)
		}
		if in.Name != plan.Name.ValueString() || in.Kind != plan.Kind.ValueString() {
			return fmt.Errorf("group source identity/name/kind rebound")
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupSourceGroupRequest(plan))
		if err != nil || envelope.Outcome != "completed" {
			return fmt.Errorf("parent group unavailable: %v outcome=%s", err, envelope.Outcome)
		}
		group, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		child, exists, err := exactBridgeChild(group.Children, state.ItemID.ValueInt64(), in.UUID)
		if err != nil {
			return err
		}
		plan.InputUUID = state.InputUUID
		itemID := state.ItemID.ValueInt64()
		var observedPlacement bridgePlacementModel
		if !exists {
			// A source can intentionally have multiple placements in this group.
			// Restore the missing owned anchor with a new item; never adopt a
			// surviving sibling that happens to reference the same source.
			placement, _ := plan.Placement.request()
			opID := bridgeOperationID("repair-group-source", plan.Name.ValueString())
			envelope, callErr := bridgeMutation(ctx, cl, plan.Collection.ValueString(), "AddSourceToGroup", opID, map[string]any{
				"scene":  bridgeIdentity(plan.SceneUUID.ValueString(), plan.SceneName.ValueString(), 0),
				"group":  bridgeIdentity(plan.GroupUUID.ValueString(), plan.GroupName.ValueString(), plan.GroupItemID.ValueInt64()),
				"source": bridgeIdentity(in.UUID, in.Name, 0), "placement": placement,
			})
			var created bridgeChildObservation
			createdErr := fmt.Errorf("bridge result is not trusted because the envelope was invalid")
			if callErr == nil || isAuthoritativeNativeInterruption(callErr) {
				created, createdErr = bridgeChildFromResult(envelope.Result)
			}
			if createdErr == nil {
				itemID = created.ItemID
				plan.ItemID = types.Int64Value(itemID)
				plan.ID = types.StringValue(groupSourceID(plan.Collection.ValueString(), plan.SceneUUID.ValueString(), plan.GroupUUID.ValueString(), in.UUID, itemID))
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			}
			if callErr != nil {
				return callErr
			}
			if envelope.Outcome != "completed" {
				return fmt.Errorf("bridge AddSourceToGroup %s (%s): %s; re-observe before retry", envelope.Outcome, envelope.Code, envelope.Message)
			}
			if createdErr != nil {
				return createdErr
			}
			observedPlacement = created.Placement
		} else {
			observedPlacement = child.Placement
		}
		if err := updateBridgeItem(ctx, cl, plan.Collection.ValueString(), plan.GroupName.ValueString(), itemID, observedPlacement, plan.Placement); err != nil {
			return err
		}
		want, _ := parseGenericSettings(plan.SettingsJSON.ValueString())
		effective, err := observedGenericSourceSettings(ctx, cl, in)
		if err != nil {
			return err
		}
		if !genericSettingsMatch(effective, want) {
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetInputSettings", map[string]any{"inputUuid": in.UUID, "inputSettings": want, "overlay": true}); err != nil {
				return err
			}
		}
		plan.InputUUID = state.InputUUID
		plan.ItemID = types.Int64Value(itemID)
		plan.ID = types.StringValue(groupSourceID(plan.Collection.ValueString(), plan.SceneUUID.ValueString(), plan.GroupUUID.ValueString(), in.UUID, itemID))
		settled, err := waitGroupSourceChild(ctx, cl, plan, 40, 100*time.Millisecond)
		if err != nil {
			return err
		}
		plan.SourceWidth, plan.SourceHeight = types.Int64Value(settled.Width), types.Int64Value(settled.Height)
		_, err = waitGenericSource(ctx, cl, in.UUID, plan.Name.ValueString(), plan.Kind.ValueString(), want)
		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS group source failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupSourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		in, inputExists, err := genericSourceByUUID(ctx, cl, m.InputUUID.ValueString())
		if err != nil || !inputExists {
			return err
		}
		if in.Name != m.Name.ValueString() || in.Kind != m.Kind.ValueString() {
			return fmt.Errorf("group source identity/name/kind rebound; refusing deletion")
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupSourceGroupRequest(m))
		if err != nil || envelope.Outcome != "completed" {
			return fmt.Errorf("parent group unavailable for exact delete: %v outcome=%s", err, envelope.Outcome)
		}
		group, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		child, exists, err := exactBridgeChild(group.Children, m.ItemID.ValueInt64(), m.InputUUID.ValueString())
		if err != nil {
			return err
		}
		if !exists {
			return waitRemovedSource(ctx, cl, m.Collection.ValueString(), m.InputUUID.ValueString(), m.SceneUUID.ValueString(), m.SceneName.ValueString())
		}
		filters, err := sourceFilters(ctx, cl, child.Name)
		if err != nil {
			return err
		}
		if len(filters) > 0 {
			return fmt.Errorf("refusing group source delete with %d attached source filters", len(filters))
		}
		extras, err := allSourceReferences(ctx, cl, child.UUID, m.GroupName.ValueString(), child.ItemID)
		if err != nil {
			return err
		}
		if len(extras) > 0 {
			return fmt.Errorf("refusing group source delete with additional references %v", extras)
		}
		expected, _ := child.Placement.request()
		opID := bridgeOperationID("delete-group-source", m.Name.ValueString())
		envelope, err = bridgeMutation(ctx, cl, m.Collection.ValueString(), "RemoveGroupChildPlacement", opID, map[string]any{
			"scene": bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0),
			"group": bridgeIdentity(m.GroupUUID.ValueString(), m.GroupName.ValueString(), m.GroupItemID.ValueInt64()),
			"child": bridgeIdentity(child.UUID, child.Name, child.ItemID), "expectedPlacement": expected,
		})
		if err != nil {
			return err
		}
		if envelope.Outcome != "completed" {
			return fmt.Errorf("bridge RemoveGroupChildPlacement %s (%s): %s", envelope.Outcome, envelope.Code, envelope.Message)
		}
		_, ok := envelope.Result["sourceStillExists"].(bool)
		if !ok {
			return fmt.Errorf("bridge removal omitted sourceStillExists")
		}
		return waitRemovedSource(ctx, cl, m.Collection.ValueString(), child.UUID, m.SceneUUID.ValueString(), m.SceneName.ValueString())
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS group source failed", err.Error())
	}
}

func (r *groupSourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 6 {
		resp.Diagnostics.AddError("Invalid group source import ID", "use collection|scene_uuid|group_uuid|group_item_id|input_uuid|item_id")
		return
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[4] == "" {
		resp.Diagnostics.AddError("Invalid group source import ID", "collection and UUIDs must not be empty")
		return
	}
	groupItemID, err1 := strconv.ParseInt(parts[3], 10, 64)
	itemID, err2 := strconv.ParseInt(parts[5], 10, 64)
	if err1 != nil || err2 != nil || groupItemID <= 0 || itemID <= 0 {
		resp.Diagnostics.AddError("Invalid group source import ID", "item IDs must be positive")
		return
	}
	stableID := groupSourceID(parts[0], parts[1], parts[2], parts[4], itemID)
	for key, value := range map[string]any{"id": stableID, "collection": parts[0], "scene_uuid": parts[1], "group_uuid": parts[2], "group_item_id": groupItemID, "input_uuid": parts[4], "item_id": itemID, "settings_json": "{}"} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), value)...)
	}
}

func groupSourceGroupRequest(m groupSourceModel) map[string]any {
	return map[string]any{"scene": bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0), "group": bridgeIdentity(m.GroupUUID.ValueString(), m.GroupName.ValueString(), m.GroupItemID.ValueInt64())}
}

func readGroupSourceWithUnavailableParent(ctx context.Context, cl requester, m *groupSourceModel, envelope bridgeEnvelope) error {
	if envelope.Outcome != "failed" || envelope.MutationAttempted || !envelope.PostconditionVerified {
		return fmt.Errorf("parent group observation %s/%s: %s", envelope.Outcome, envelope.Code, envelope.Message)
	}
	in, inputExists, err := genericSourceByUUID(ctx, cl, m.InputUUID.ValueString())
	if err != nil {
		return err
	}
	if !inputExists {
		return errGroupSourceGone
	}
	if (!m.Name.IsNull() && !m.Name.IsUnknown() && in.Name != m.Name.ValueString()) || (!m.Kind.IsNull() && !m.Kind.IsUnknown() && in.Kind != m.Kind.ValueString()) {
		return fmt.Errorf("surviving group source identity rebound")
	}
	scene, exists, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
	if err != nil {
		return err
	}
	if !exists {
		m.GroupName = types.StringNull()
		return nil
	}
	items, err := sceneItems(ctx, cl, scene.Name)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ItemID == m.GroupItemID.ValueInt64() {
			if item.SourceUUID != m.GroupUUID.ValueString() {
				return fmt.Errorf("parent group item rebound; refusing recovery")
			}
			return fmt.Errorf("parent group observation failed while exact parent remains: %s/%s", envelope.Outcome, envelope.Code)
		}
	}
	// The exact group anchor is absent. Retain input identity and force placement
	// reconciliation once the parent resource has restored its current anchor.
	m.GroupName = types.StringNull()
	return nil
}

func groupSourceID(collection, sceneUUID, groupUUID, inputUUID string, itemID int64) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d", collection, sceneUUID, groupUUID, inputUUID, itemID)
}

func waitGroupSourceChild(ctx context.Context, cl requester, m groupSourceModel, attempts int, delay time.Duration) (bridgeChildObservation, error) {
	// Bridge mutations verify their postcondition before replying, so the first
	// observation normally succeeds; sleeping before it cost ~100ms per group object.
	var last string
	for i := 0; i < attempts; i++ {
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupSourceGroupRequest(m))
		if err != nil {
			return bridgeChildObservation{}, err
		}
		if envelope.Outcome != "completed" {
			last = envelope.Outcome + "/" + envelope.Code
		} else {
			group, err := bridgeGroupFromResult(envelope.Result)
			if err != nil {
				return bridgeChildObservation{}, err
			}
			child, exists, err := exactBridgeChild(group.Children, m.ItemID.ValueInt64(), m.InputUUID.ValueString())
			if err != nil {
				return bridgeChildObservation{}, err
			}
			if !exists {
				last = "child absent"
			} else if child.Name != m.Name.ValueString() {
				last = "child identity mismatch"
			} else if diff := bridgePlacementDifference(child.Placement, *m.Placement); diff != "" {
				last = diff
			} else {
				// The native group bridge reports obs_source_get_unversioned_id;
				// verify versioned kind through core OBS WebSocket instead.
				in, present, err := genericSourceByUUID(ctx, cl, child.UUID)
				if err != nil {
					return bridgeChildObservation{}, err
				}
				if !present || in.Name != m.Name.ValueString() || in.Kind != m.Kind.ValueString() {
					return bridgeChildObservation{}, fmt.Errorf("group source identity/name/kind rebound during placement postcondition")
				}
				if err := recordTrace(map[string]any{"event": "postcondition", "object": "group-source", "status": "passed", "input_uuid": child.UUID, "item_id": child.ItemID, "group_uuid": m.GroupUUID.ValueString()}); err != nil {
					return bridgeChildObservation{}, err
				}
				return child, nil
			}
		}
		select {
		case <-ctx.Done():
			return bridgeChildObservation{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return bridgeChildObservation{}, fmt.Errorf("group source postcondition did not converge: %s", last)
}

var _ resource.ResourceWithConfigure = (*groupSourceResource)(nil)
var _ resource.ResourceWithModifyPlan = (*groupSourceResource)(nil)
var _ resource.ResourceWithImportState = (*groupSourceResource)(nil)

type groupSourceModel struct {
	ID           types.String          `tfsdk:"id"`
	Collection   types.String          `tfsdk:"collection"`
	SceneName    types.String          `tfsdk:"scene_name"`
	SceneUUID    types.String          `tfsdk:"scene_uuid"`
	GroupName    types.String          `tfsdk:"group_name"`
	GroupUUID    types.String          `tfsdk:"group_uuid"`
	GroupItemID  types.Int64           `tfsdk:"group_item_id"`
	Name         types.String          `tfsdk:"name"`
	Kind         types.String          `tfsdk:"kind"`
	SettingsJSON types.String          `tfsdk:"settings_json"`
	InputUUID    types.String          `tfsdk:"input_uuid"`
	ItemID       types.Int64           `tfsdk:"item_id"`
	Placement    *bridgePlacementModel `tfsdk:"placement"`
	SourceWidth  types.Int64           `tfsdk:"observed_source_width"`
	SourceHeight types.Int64           `tfsdk:"observed_source_height"`
}

var errGroupSourceGone = errors.New("managed group source is authoritatively absent")
