package tfprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type groupInputResource struct{ client *client }

func NewGroupInputResource() resource.Resource { return &groupInputResource{} }
func (r *groupInputResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_input"
}
func (r *groupInputResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *groupInputResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state groupInputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.GroupName.IsNull() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("item_id"), types.Int64Unknown())...)
}
func (r *groupInputResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true),
		"scene_name": schema.StringAttribute{Required: true}, "scene_uuid": immutableString(true),
		"group_name": schema.StringAttribute{Required: true}, "group_uuid": immutableString(true), "group_item_id": schema.Int64Attribute{Required: true},
		"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"kind": immutableString(true), "file": schema.StringAttribute{Required: true}, "unload": schema.BoolAttribute{Required: true},
		"input_uuid": identityString(), "item_id": identityInt64(), "placement": bridgePlacementSchema(),
		"observed_source_width": schema.Int64Attribute{Computed: true}, "observed_source_height": schema.Int64Attribute{Computed: true},
	}}
}

func validateGroupImageInput(m groupInputModel) error {
	if m.Kind.ValueString() != "image_source" {
		return fmt.Errorf("group input currently supports only image_source")
	}
	if !filepath.IsAbs(m.File.ValueString()) {
		return fmt.Errorf("file must be absolute")
	}
	info, err := os.Stat(m.File.ValueString())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file is not a regular asset")
	}
	if m.Placement == nil {
		return fmt.Errorf("placement is required")
	}
	_, err = m.Placement.request()
	return err
}

func (r *groupInputResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m groupInputModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGroupImageInput(m); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group input", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
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
		opID := bridgeOperationID("create-group-input", m.Name.ValueString())
		envelope, callErr := bridgeMutation(ctx, cl, m.Collection.ValueString(), "CreateInputInGroup", opID, map[string]any{
			"scene":     bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0),
			"group":     bridgeIdentity(m.GroupUUID.ValueString(), m.GroupName.ValueString(), m.GroupItemID.ValueInt64()),
			"inputName": m.Name.ValueString(), "inputKind": m.Kind.ValueString(),
			"inputSettings": map[string]any{"file": m.File.ValueString(), "unload": m.Unload.ValueBool()}, "placement": placement,
		})
		var child bridgeChildObservation
		childErr := fmt.Errorf("bridge result is not trusted because the envelope was invalid")
		if callErr == nil || isAuthoritativeNativeInterruption(callErr) {
			child, childErr = bridgeChildFromResult(envelope.Result)
		}
		if childErr == nil {
			m.InputUUID, m.ItemID = types.StringValue(child.UUID), types.Int64Value(child.ItemID)
			m.ID = types.StringValue(groupInputID(m.Collection.ValueString(), m.SceneUUID.ValueString(), m.GroupUUID.ValueString(), child.UUID, child.ItemID))
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
		if child.Name != m.Name.ValueString() || child.Kind != m.Kind.ValueString() || bridgePlacementDifference(child.Placement, *mm.Placement) != "" {
			return fmt.Errorf("bridge group input returned mismatched identity/placement")
		}
		settled, err := waitBridgeChild(ctx, cl, m, 40, 100*time.Millisecond)
		if err != nil {
			return err
		}
		m.SourceWidth, m.SourceHeight = types.Int64Value(settled.Width), types.Int64Value(settled.Height)
		_, err = waitInputExpected(ctx, cl, child.UUID, m.Name.ValueString(), m.Kind.ValueString(), m.File.ValueString(), m.Unload.ValueBool(), 40, 100*time.Millisecond)
		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS group input failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *groupInputResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m groupInputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", false); err != nil {
			return err
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupInputGroupRequest(m))
		if err != nil {
			return err
		}
		if envelope.Outcome != "completed" {
			return readGroupInputWithUnavailableParent(ctx, cl, &m, envelope)
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
			in, inputExists, err := inputByUUID(ctx, cl, m.InputUUID.ValueString())
			if err != nil {
				return err
			}
			if !inputExists {
				return errGroupInputGone
			}
			if in.Name != m.Name.ValueString() || in.Kind != m.Kind.ValueString() {
				return fmt.Errorf("surviving group input identity rebound")
			}
			m.GroupName = types.StringNull()
			return nil
		}
		if child.Name != m.Name.ValueString() || child.Kind != m.Kind.ValueString() {
			return fmt.Errorf("group child identity/name/kind rebound")
		}
		in, inputExists, err := inputByUUID(ctx, cl, child.UUID)
		if err != nil || !inputExists {
			return fmt.Errorf("group child source unavailable: %v", err)
		}
		m.SceneName, m.GroupName = types.StringValue(group.SceneName), types.StringValue(group.Name)
		m.Name, m.Kind = types.StringValue(child.Name), types.StringValue(child.Kind)
		m.File, m.Unload = types.StringValue(in.File), types.BoolValue(in.Unload)
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
		if errors.Is(err, errGroupInputGone) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read OBS group input failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *groupInputResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupInputModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGroupImageInput(plan); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group input", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		in, inputExists, err := inputByUUID(ctx, cl, state.InputUUID.ValueString())
		if err != nil || !inputExists {
			return fmt.Errorf("group input disappeared; refresh before apply: %v", err)
		}
		if in.Name != plan.Name.ValueString() || in.Kind != plan.Kind.ValueString() {
			return fmt.Errorf("group input identity/name/kind rebound")
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupInputGroupRequest(plan))
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
		itemID := state.ItemID.ValueInt64()
		var observedPlacement bridgePlacementModel
		if !exists {
			for _, other := range group.Children {
				if other.UUID == in.UUID {
					return fmt.Errorf("same source already has different group item %d; refusing logical adoption", other.ItemID)
				}
			}
			placement, _ := plan.Placement.request()
			opID := bridgeOperationID("repair-group-input", plan.Name.ValueString())
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
				plan.ID = types.StringValue(groupInputID(plan.Collection.ValueString(), plan.SceneUUID.ValueString(), plan.GroupUUID.ValueString(), in.UUID, itemID))
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
		if in.File != plan.File.ValueString() || in.Unload != plan.Unload.ValueBool() {
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetInputSettings", map[string]any{"inputUuid": in.UUID, "inputSettings": map[string]any{"file": plan.File.ValueString(), "unload": plan.Unload.ValueBool()}, "overlay": true}); err != nil {
				return err
			}
		}
		plan.InputUUID = state.InputUUID
		plan.ItemID = types.Int64Value(itemID)
		plan.ID = types.StringValue(groupInputID(plan.Collection.ValueString(), plan.SceneUUID.ValueString(), plan.GroupUUID.ValueString(), in.UUID, itemID))
		settled, err := waitBridgeChild(ctx, cl, plan, 40, 100*time.Millisecond)
		if err != nil {
			return err
		}
		plan.SourceWidth, plan.SourceHeight = types.Int64Value(settled.Width), types.Int64Value(settled.Height)
		_, err = waitInputExpected(ctx, cl, in.UUID, plan.Name.ValueString(), plan.Kind.ValueString(), plan.File.ValueString(), plan.Unload.ValueBool(), 40, 100*time.Millisecond)
		return err
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS group input failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupInputResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupInputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupInputGroupRequest(m))
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
			_, inputExists, err := inputByUUID(ctx, cl, m.InputUUID.ValueString())
			if err != nil || inputExists {
				return fmt.Errorf("child absent but source still exists; refusing delete: %v", err)
			}
			return nil
		}
		filters, err := sourceFilters(ctx, cl, child.Name)
		if err != nil {
			return err
		}
		if len(filters) > 0 {
			return fmt.Errorf("refusing group input delete with %d attached source filters", len(filters))
		}
		extras, err := allSourceReferences(ctx, cl, child.UUID, m.GroupName.ValueString(), child.ItemID)
		if err != nil {
			return err
		}
		if len(extras) > 0 {
			return fmt.Errorf("refusing group input delete with additional references %v", extras)
		}
		expected, _ := child.Placement.request()
		opID := bridgeOperationID("delete-group-input", m.Name.ValueString())
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
		resp.Diagnostics.AddError("Delete OBS group input failed", err.Error())
	}
}

func (r *groupInputResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 6 {
		resp.Diagnostics.AddError("Invalid group input import ID", "use collection|scene_uuid|group_uuid|group_item_id|input_uuid|item_id")
		return
	}
	groupItemID, err1 := strconv.ParseInt(parts[3], 10, 64)
	itemID, err2 := strconv.ParseInt(parts[5], 10, 64)
	if err1 != nil || err2 != nil || groupItemID <= 0 || itemID <= 0 {
		resp.Diagnostics.AddError("Invalid group input import ID", "item IDs must be positive")
		return
	}
	stableID := groupInputID(parts[0], parts[1], parts[2], parts[4], itemID)
	for key, value := range map[string]any{"id": stableID, "collection": parts[0], "scene_uuid": parts[1], "group_uuid": parts[2], "group_item_id": groupItemID, "input_uuid": parts[4], "item_id": itemID} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), value)...)
	}
}

func groupInputGroupRequest(m groupInputModel) map[string]any {
	return map[string]any{"scene": bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0), "group": bridgeIdentity(m.GroupUUID.ValueString(), m.GroupName.ValueString(), m.GroupItemID.ValueInt64())}
}

func readGroupInputWithUnavailableParent(ctx context.Context, cl requester, m *groupInputModel, envelope bridgeEnvelope) error {
	if envelope.Outcome != "failed" || envelope.MutationAttempted || !envelope.PostconditionVerified {
		return fmt.Errorf("parent group observation %s/%s: %s", envelope.Outcome, envelope.Code, envelope.Message)
	}
	in, inputExists, err := inputByUUID(ctx, cl, m.InputUUID.ValueString())
	if err != nil {
		return err
	}
	if !inputExists {
		return errGroupInputGone
	}
	if (!m.Name.IsNull() && !m.Name.IsUnknown() && in.Name != m.Name.ValueString()) || (!m.Kind.IsNull() && !m.Kind.IsUnknown() && in.Kind != m.Kind.ValueString()) {
		return fmt.Errorf("surviving group input identity rebound")
	}
	if envelope.Code != "ambiguous_group_item_id" {
		return fmt.Errorf("parent group observation failed with non-anchor code %s", envelope.Code)
	}
	scene, exists, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
	if err != nil || !exists {
		return fmt.Errorf("parent scene unavailable during group-anchor deferral: %v", err)
	}
	items, err := sceneItems(ctx, cl, scene.Name)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ItemID == m.GroupItemID.ValueInt64() {
			return fmt.Errorf("stale group tuple item ID still exists on %s; refusing deferral", item.SourceUUID)
		}
	}
	if m.GroupName.IsNull() || m.GroupName.IsUnknown() || m.GroupName.ValueString() == "" {
		return fmt.Errorf("group name unavailable for parent-anchor deferral")
	}
	groupExists, err := groupNameExists(ctx, cl, m.GroupName.ValueString())
	if err != nil {
		return err
	}
	if !groupExists {
		return fmt.Errorf("parent group and anchor are absent while child input %s survives", in.UUID)
	}
	return recordTrace(map[string]any{"event": "observation-deferred", "object": "group-input", "reason": "parent-group-anchor-absent", "group_uuid": m.GroupUUID.ValueString(), "old_group_item_id": m.GroupItemID.ValueInt64(), "input_uuid": in.UUID})
}

func groupInputID(collection, sceneUUID, groupUUID, inputUUID string, itemID int64) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d", collection, sceneUUID, groupUUID, inputUUID, itemID)
}

func exactBridgeChild(children []bridgeChildObservation, itemID int64, inputUUID string) (bridgeChildObservation, bool, error) {
	matches := 0
	var found bridgeChildObservation
	for _, child := range children {
		if child.ItemID == itemID {
			matches++
			found = child
		}
	}
	if matches > 1 {
		return found, false, fmt.Errorf("group child item ID %d is ambiguous", itemID)
	}
	if matches == 0 {
		return found, false, nil
	}
	if found.UUID != inputUUID {
		return found, false, fmt.Errorf("group child item %d rebound to %s", itemID, found.UUID)
	}
	return found, true, nil
}

func waitBridgeChild(ctx context.Context, cl requester, m groupInputModel, attempts int, delay time.Duration) (bridgeChildObservation, error) {
	// Bridge mutations verify their postcondition before replying, so the first
	// observation normally succeeds; sleeping before it cost ~100ms per group object.
	var last string
	for i := 0; i < attempts; i++ {
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupInputGroupRequest(m))
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
			} else if child.Name != m.Name.ValueString() || child.Kind != m.Kind.ValueString() {
				last = "child identity mismatch"
			} else if child.Width <= 0 || child.Height <= 0 {
				last = fmt.Sprintf("source dimensions=%dx%d", child.Width, child.Height)
			} else if diff := bridgePlacementDifference(child.Placement, *m.Placement); diff != "" {
				last = diff
			} else {
				if err := recordTrace(map[string]any{"event": "postcondition", "object": "group-input", "status": "passed", "input_uuid": child.UUID, "item_id": child.ItemID, "group_uuid": m.GroupUUID.ValueString()}); err != nil {
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
	return bridgeChildObservation{}, fmt.Errorf("group input postcondition did not converge: %s", last)
}

func allSourceReferences(ctx context.Context, cl requester, sourceUUID, exceptGroupName string, exceptItemID int64) ([]string, error) {
	extras, err := sourceReferences(ctx, cl, sourceUUID, "", -1)
	if err != nil {
		return nil, err
	}
	x, err := request(ctx, cl, "GetGroupList", nil)
	if err != nil {
		return nil, err
	}
	groups, err := requiredSlice(x, "groups")
	if err != nil {
		return nil, err
	}
	for _, raw := range groups {
		name := stringValue(raw)
		response, err := request(ctx, cl, "GetGroupSceneItemList", map[string]any{"sceneName": name})
		if err != nil {
			return nil, err
		}
		items, err := requiredSlice(response, "sceneItems")
		if err != nil {
			return nil, err
		}
		for _, rawItem := range items {
			item := mapValue(rawItem)
			itemID := int64Value(item["sceneItemId"])
			if stringValue(item["sourceUuid"]) == sourceUUID && !(name == exceptGroupName && itemID == exceptItemID) {
				extras = append(extras, fmt.Sprintf("group:%s/%d", name, itemID))
			}
		}
	}
	return extras, nil
}

var errGroupInputGone = errors.New("managed group input is authoritatively absent")

var _ resource.ResourceWithConfigure = (*groupInputResource)(nil)
var _ resource.ResourceWithModifyPlan = (*groupInputResource)(nil)
var _ resource.ResourceWithImportState = (*groupInputResource)(nil)
