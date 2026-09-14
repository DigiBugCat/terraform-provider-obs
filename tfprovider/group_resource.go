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
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type groupResource struct{ client *client }

func NewGroupResource() resource.Resource { return &groupResource{} }
func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}
func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *groupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.SceneName.IsNull() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("item_id"), types.Int64Unknown())...)
}
func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true),
		"scene_name": schema.StringAttribute{Required: true}, "scene_uuid": immutableString(true),
		"name":       schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"group_uuid": identityString(), "item_id": identityInt64(), "placement": bridgePlacementSchema(),
		"observed_group_width": schema.Int64Attribute{Computed: true}, "observed_group_height": schema.Int64Attribute{Computed: true},
	}}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.Placement == nil {
		resp.Diagnostics.AddError("Invalid OBS group", "placement is required")
		return
	}
	if _, err := m.Placement.request(); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString(), m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
		if err != nil || !ok || scene.Name != m.SceneName.ValueString() {
			return fmt.Errorf("group parent scene identity mismatch: %v", err)
		}
		siblings, err := containerItemCount(ctx, cl, scene.Name, false)
		if err != nil {
			return err
		}
		mm := m
		clamped := *m.Placement
		clamped.Index = types.Int64Value(clampCreateIndex(m.Placement.Index.ValueInt64(), siblings))
		mm.Placement = &clamped
		placement, _ := mm.Placement.request()
		opID := bridgeOperationID("create-group", m.Name.ValueString())
		envelope, callErr := bridgeMutation(ctx, cl, m.Collection.ValueString(), "CreateGroup", opID, map[string]any{
			"scene": bridgeIdentity(scene.UUID, scene.Name, 0), "groupName": m.Name.ValueString(), "placement": placement,
		})
		observed, trusted, err := bridgeOutcome("CreateGroup", envelope, callErr, bridgeGroupFromResult)
		if trusted {
			m.GroupUUID = types.StringValue(observed.UUID)
			m.ItemID = types.Int64Value(observed.ItemID)
			m.ID = types.StringValue(groupID(m.Collection.ValueString(), scene.UUID, observed.UUID, observed.ItemID))
			m.GroupWidth = types.Int64Value(observed.Width)
			m.GroupHeight = types.Int64Value(observed.Height)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		}
		if err != nil {
			return err
		}
		if observed.SceneUUID != scene.UUID || observed.SceneName != scene.Name || observed.Name != m.Name.ValueString() {
			return fmt.Errorf("bridge CreateGroup returned mismatched identity")
		}
		if diff := bridgePlacementDifference(observed.Placement, *mm.Placement); diff != "" {
			return fmt.Errorf("bridge CreateGroup placement mismatch: %s", diff)
		}
		mm, mm.Placement = m, &clamped
		settled, err := waitBridgeGroup(ctx, cl, mm, postconditionAttempts, postconditionDelay)
		if err != nil {
			return err
		}
		m.GroupWidth = types.Int64Value(settled.Width)
		m.GroupHeight = types.Int64Value(settled.Height)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS group failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m groupModel
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
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupRequest(m))
		if err != nil {
			return err
		}
		if envelope.Outcome != "completed" {
			return readMissingGroup(ctx, cl, &m, envelope)
		}
		observed, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		if observed.SceneUUID != m.SceneUUID.ValueString() || observed.UUID != m.GroupUUID.ValueString() || observed.ItemID != m.ItemID.ValueInt64() {
			return fmt.Errorf("group identity rebound")
		}
		if !m.SceneName.IsNull() && !m.SceneName.IsUnknown() && observed.SceneName != m.SceneName.ValueString() {
			return fmt.Errorf("group parent scene renamed/rebound")
		}
		if !m.Name.IsNull() && !m.Name.IsUnknown() && observed.Name != m.Name.ValueString() {
			return fmt.Errorf("group renamed/rebound")
		}
		m.SceneName, m.Name = types.StringValue(observed.SceneName), types.StringValue(observed.Name)
		if m.Placement != nil {
			retained := retainAuthoredBridgePlacement(observed.Placement, *m.Placement)
			m.Placement = &retained
		} else {
			m.Placement = &observed.Placement
		}
		m.GroupWidth, m.GroupHeight = types.Int64Value(observed.Width), types.Int64Value(observed.Height)
		return nil
	})
	if err != nil {
		if errors.Is(err, errGroupGone) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read OBS group failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Placement == nil {
		resp.Diagnostics.AddError("Invalid OBS group", "placement is required")
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.SceneUUID.ValueString(), plan.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneByUUID(ctx, cl, plan.SceneUUID.ValueString())
		if err != nil || !ok || scene.Name != plan.SceneName.ValueString() {
			return fmt.Errorf("group parent scene identity mismatch: %v", err)
		}
		itemID := state.ItemID.ValueInt64()
		var observedPlacement *bridgePlacementModel
		// target is the placement OBS can honour now; state keeps the declared index.
		target := *plan.Placement
		clampTarget := func() error {
			siblings, err := containerItemCount(ctx, cl, scene.Name, false)
			if err != nil {
				return err
			}
			target.Index = types.Int64Value(clampCreateIndex(plan.Placement.Index.ValueInt64(), siblings-1))
			return nil
		}
		if state.SceneName.IsNull() {
			if exists, err := groupNameExists(ctx, cl, plan.Name.ValueString()); err != nil || !exists {
				return fmt.Errorf("missing group source cannot be proven available: %v", err)
			}
			if _, err := bridgeCapabilities(ctx, cl, plan.Collection.ValueString(), "GetGroup", true); err != nil {
				return err
			}
			x, callErr := mutation(ctx, cl, plan.Collection.ValueString(), "CreateSceneItem", map[string]any{"sceneUuid": scene.UUID, "sourceUuid": state.GroupUUID.ValueString(), "sceneItemEnabled": plan.Placement.Enabled.ValueBool()})
			// A receipt-write failure still returns the created item id; record it before
			// surfacing the error so the placement is never orphaned.
			if callErr != nil && !isAuthoritativeNativeInterruption(callErr) && int64Value(x["sceneItemId"]) <= 0 {
				return callErr
			}
			itemID = int64Value(x["sceneItemId"])
			if itemID <= 0 {
				if callErr != nil {
					return callErr
				}
				return fmt.Errorf("CreateSceneItem omitted group item ID")
			}
			plan.GroupUUID = state.GroupUUID
			plan.ItemID = types.Int64Value(itemID)
			plan.ID = types.StringValue(groupID(plan.Collection.ValueString(), scene.UUID, state.GroupUUID.ValueString(), itemID))
			plan.GroupWidth, plan.GroupHeight = state.GroupWidth, state.GroupHeight
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			if callErr != nil {
				return callErr
			}
			if err := clampTarget(); err != nil {
				return err
			}
			if err := setBridgeItem(ctx, cl, plan.Collection.ValueString(), scene.Name, itemID, &target); err != nil {
				return err
			}
		} else {
			envelope, err := bridgeRead(ctx, cl, "GetGroup", groupRequest(state))
			if err != nil || envelope.Outcome != "completed" {
				return fmt.Errorf("group unavailable before update: %v outcome=%s", err, envelope.Outcome)
			}
			observed, err := bridgeGroupFromResult(envelope.Result)
			if err != nil {
				return err
			}
			if observed.UUID != state.GroupUUID.ValueString() || observed.ItemID != itemID {
				return fmt.Errorf("group identity rebound before update")
			}
			observedPlacement = &observed.Placement
		}
		if observedPlacement != nil {
			if err := clampTarget(); err != nil {
				return err
			}
			if err := updateBridgeItem(ctx, cl, plan.Collection.ValueString(), scene.Name, itemID, *observedPlacement, &target); err != nil {
				return err
			}
		}
		plan.GroupUUID = state.GroupUUID
		plan.ItemID = types.Int64Value(itemID)
		plan.ID = types.StringValue(groupID(plan.Collection.ValueString(), scene.UUID, state.GroupUUID.ValueString(), itemID))
		verify := plan
		verify.Placement = &target
		settled, err := waitBridgeGroup(ctx, cl, verify, postconditionAttempts, postconditionDelay)
		if err != nil {
			return err
		}
		plan.GroupWidth, plan.GroupHeight = types.Int64Value(settled.Width), types.Int64Value(settled.Height)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS group failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString(), m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupRequest(m))
		if err != nil {
			return err
		}
		if envelope.Outcome != "completed" {
			if missingErr := readMissingGroup(ctx, cl, &m, envelope); errors.Is(missingErr, errGroupGone) {
				return nil
			} else if missingErr != nil {
				return missingErr
			}
			return fmt.Errorf("group delete cannot prove exact current identity: %s/%s", envelope.Outcome, envelope.Code)
		}
		observed, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		if len(observed.Children) != 0 {
			return fmt.Errorf("refusing group delete with %d direct children", len(observed.Children))
		}
		filters, err := sourceFilters(ctx, cl, observed.Name)
		if err != nil {
			return err
		}
		if len(filters) > 0 {
			return fmt.Errorf("refusing group delete with %d attached source filters", len(filters))
		}
		extras, err := sourceReferences(ctx, cl, observed.UUID, m.SceneUUID.ValueString(), m.ItemID.ValueInt64())
		if err != nil {
			return err
		}
		if len(extras) > 0 {
			return fmt.Errorf("refusing group delete with additional references %v", extras)
		}
		if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveSceneItem", map[string]any{"sceneUuid": m.SceneUUID.ValueString(), "sceneItemId": m.ItemID.ValueInt64()}); err != nil {
			return err
		}
		return waitUntil(ctx, func() (bool, error) { exists, e := groupNameExists(ctx, cl, m.Name.ValueString()); return !exists, e })
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS group failed", err.Error())
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 4 {
		resp.Diagnostics.AddError("Invalid group import ID", "use collection|scene_uuid|group_uuid|item_id")
		return
	}
	itemID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || itemID <= 0 {
		resp.Diagnostics.AddError("Invalid group import ID", "item_id must be positive")
		return
	}
	for key, value := range map[string]any{"id": req.ID, "collection": parts[0], "scene_uuid": parts[1], "group_uuid": parts[2], "item_id": itemID} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), value)...)
	}
}

func bridgeIdentity(uuid, name string, itemID int64) map[string]any {
	out := map[string]any{"sourceUuid": uuid}
	if name != "" {
		out["sourceName"] = name
	}
	if itemID > 0 {
		out["sceneItemId"] = itemID
	}
	return out
}

func groupRequest(m groupModel) map[string]any {
	return map[string]any{"scene": bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0), "group": bridgeIdentity(m.GroupUUID.ValueString(), m.Name.ValueString(), m.ItemID.ValueInt64())}
}

func bridgeOperationID(kind, name string) string {
	return fmt.Sprintf("terraform/%d/%s/%s", time.Now().UTC().UnixNano(), kind, name)
}

func groupID(collection, sceneUUID, groupUUID string, itemID int64) string {
	return fmt.Sprintf("%s|%s|%s|%d", collection, sceneUUID, groupUUID, itemID)
}

func groupNameExists(ctx context.Context, cl requester, name string) (bool, error) {
	x, err := request(ctx, cl, "GetGroupList", nil)
	if err != nil {
		return false, err
	}
	groups, err := requiredSlice(x, "groups")
	if err != nil {
		return false, err
	}
	for _, raw := range groups {
		groupName, ok := raw.(string)
		if !ok || groupName == "" {
			return false, fmt.Errorf("GetGroupList returned invalid group name")
		}
		if groupName == name {
			return true, nil
		}
	}
	return false, nil
}

func readMissingGroup(ctx context.Context, cl requester, m *groupModel, envelope bridgeEnvelope) error {
	if envelope.Outcome != "failed" || envelope.MutationAttempted || !envelope.PostconditionVerified {
		return fmt.Errorf("group observation %s/%s: %s", envelope.Outcome, envelope.Code, envelope.Message)
	}
	scene, ok, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
	if err != nil {
		return err
	}
	if !ok {
		// Terraform cannot order its way out of this refresh error: deleting a
		// scene may also delete the group's last reference. Establish absence
		// independently, rather than requiring the parent state to refresh first.
		if m.Name.IsNull() || m.Name.IsUnknown() || m.Name.ValueString() == "" {
			return fmt.Errorf("group parent scene is absent and group name is unavailable for survival check")
		}
		_, survives, err := sceneItemContainer(ctx, cl, "group", m.GroupUUID.ValueString())
		if err != nil {
			return err
		}
		named, err := groupNameExists(ctx, cl, m.Name.ValueString())
		if err != nil {
			return err
		}
		if survives || named {
			return fmt.Errorf("group parent scene is absent but its group UUID or name still exists; refusing to discard a surviving or conflicting group")
		}
		return errGroupGone
	}
	items, err := sceneItems(ctx, cl, scene.Name)
	if err != nil {
		return err
	}
	count := 0
	for _, item := range items {
		if item.ItemID == m.ItemID.ValueInt64() {
			count++
			if item.SourceUUID != m.GroupUUID.ValueString() {
				return fmt.Errorf("group item ID rebound to %s", item.SourceUUID)
			}
		}
	}
	if count > 1 {
		return fmt.Errorf("group parent item ID is duplicated")
	}
	if count == 1 {
		return fmt.Errorf("bridge failed to resolve an otherwise present exact group")
	}
	if m.Name.IsNull() || m.Name.IsUnknown() || m.Name.ValueString() == "" {
		return fmt.Errorf("group anchor is absent and group name is unavailable for survival check")
	}
	exists, err := groupNameExists(ctx, cl, m.Name.ValueString())
	if err != nil {
		return err
	}
	if exists {
		m.SceneName = types.StringNull()
		return nil
	}
	return errGroupGone
}

var errGroupGone = errors.New("managed group is authoritatively absent")

func waitBridgeGroup(ctx context.Context, cl requester, m groupModel, attempts int, delay time.Duration) (bridgeGroupObservation, error) {
	// Bridge mutations verify their postcondition before replying, so the first
	// observation normally succeeds; sleeping before it cost ~100ms per group object.
	var last string
	for i := 0; i < attempts; i++ {
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupRequest(m))
		if err != nil {
			return bridgeGroupObservation{}, err
		}
		if envelope.Outcome != "completed" {
			last = envelope.Outcome + "/" + envelope.Code
		} else {
			observed, err := bridgeGroupFromResult(envelope.Result)
			if err != nil {
				return bridgeGroupObservation{}, err
			}
			if observed.UUID != m.GroupUUID.ValueString() || observed.ItemID != m.ItemID.ValueInt64() {
				last = "identity mismatch"
			} else if diff := bridgePlacementDifference(observed.Placement, *m.Placement); diff != "" {
				last = diff
			} else {
				if err := recordTrace(map[string]any{"event": "postcondition", "object": "group", "status": "passed", "group_uuid": observed.UUID, "item_id": observed.ItemID}); err != nil {
					return bridgeGroupObservation{}, err
				}
				return observed, nil
			}
		}
		select {
		case <-ctx.Done():
			return bridgeGroupObservation{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return bridgeGroupObservation{}, fmt.Errorf("group postcondition did not converge: %s", last)
}

func updateBridgeItem(ctx context.Context, cl requester, collection, sceneName string, itemID int64, observed bridgePlacementModel, want *bridgePlacementModel) error {
	transformRequest, err := want.coreWebSocketTransformRequest()
	if err != nil {
		return err
	}
	if observed.Enabled.ValueBool() != want.Enabled.ValueBool() {
		if _, err := bridgeCapabilities(ctx, cl, collection, "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, collection, "SetSceneItemEnabled", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemEnabled": want.Enabled.ValueBool()}); err != nil {
			return err
		}
	}
	if observed.Locked.ValueBool() != want.Locked.ValueBool() {
		if _, err := bridgeCapabilities(ctx, cl, collection, "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, collection, "SetSceneItemLocked", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemLocked": want.Locked.ValueBool()}); err != nil {
			return err
		}
	}
	transformOnly := observed
	transformOnly.Enabled, transformOnly.Locked, transformOnly.Index = want.Enabled, want.Locked, want.Index
	if diff := bridgePlacementDifference(transformOnly, *want); diff != "" {
		if _, err := bridgeCapabilities(ctx, cl, collection, "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, collection, "SetSceneItemTransform", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemTransform": transformRequest}); err != nil {
			return err
		}
	}
	if observed.Index.ValueInt64() != want.Index.ValueInt64() {
		if _, err := bridgeCapabilities(ctx, cl, collection, "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, collection, "SetSceneItemIndex", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemIndex": want.Index.ValueInt64()}); err != nil {
			return err
		}
	}
	return nil
}

func setBridgeItem(ctx context.Context, cl requester, collection, sceneName string, itemID int64, want *bridgePlacementModel) error {
	transformRequest, err := want.coreWebSocketTransformRequest()
	if err != nil {
		return err
	}
	requests := []struct {
		typ  string
		data map[string]any
	}{
		{"SetSceneItemEnabled", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemEnabled": want.Enabled.ValueBool()}},
		{"SetSceneItemLocked", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemLocked": want.Locked.ValueBool()}},
		{"SetSceneItemTransform", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemTransform": transformRequest}},
		{"SetSceneItemIndex", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemIndex": want.Index.ValueInt64()}},
	}
	for _, request := range requests {
		if _, err := bridgeCapabilities(ctx, cl, collection, "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, collection, request.typ, request.data); err != nil {
			return err
		}
	}
	return nil
}

func sourceReferences(ctx context.Context, cl requester, sourceUUID, exceptSceneUUID string, exceptItemID int64) ([]string, error) {
	allScenes, err := scenes(ctx, cl)
	if err != nil {
		return nil, err
	}
	var extras []string
	for _, scene := range allScenes {
		items, err := sceneItems(ctx, cl, scene.Name)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.SourceUUID == sourceUUID && !(scene.UUID == exceptSceneUUID && item.ItemID == exceptItemID) {
				extras = append(extras, fmt.Sprintf("%s/%d", scene.Name, item.ItemID))
			}
		}
	}
	return extras, nil
}

var _ resource.ResourceWithConfigure = (*groupResource)(nil)
var _ resource.ResourceWithModifyPlan = (*groupResource)(nil)
var _ resource.ResourceWithImportState = (*groupResource)(nil)
