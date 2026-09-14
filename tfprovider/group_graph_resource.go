package tfprovider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type groupGraphResource struct{ client *client }

func NewGroupGraphResource() resource.Resource { return &groupGraphResource{} }

func (r *groupGraphResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_graph"
}

func (r *groupGraphResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}

func (r *groupGraphResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = groupGraphSchema()
}

func (r *groupGraphResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if req.Config.Raw.IsNull() {
		return
	}
	var raw groupGraphPlanModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &raw)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m := raw.desiredModel()
	if groupGraphConfigUnknown(m) {
		return
	}
	if err := validateGroupGraphConfig(m); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group graph", err.Error())
	}
}

func groupGraphConfigUnknown(m groupGraphModel) bool {
	if m.Collection.IsUnknown() || m.SceneName.IsUnknown() || m.SceneUUID.IsUnknown() || m.Name.IsUnknown() || m.ParentPresent.IsUnknown() || groupGraphPlacementUnknown(m.ParentPlacement) {
		return true
	}
	for _, child := range m.Children {
		if child.StableKey.IsUnknown() || child.Name.IsUnknown() || child.Kind.IsUnknown() || child.File.IsUnknown() || child.Unload.IsUnknown() || child.Present.IsUnknown() || groupGraphPlacementUnknown(child.Placement) {
			return true
		}
	}
	return false
}

func groupGraphPlacementUnknown(p *bridgePlacementModel) bool {
	if p == nil {
		return false
	}
	if p.Enabled.IsUnknown() || p.Locked.IsUnknown() || p.Index.IsUnknown() || p.Transform == nil {
		return true
	}
	t := p.Transform
	return t.X.IsUnknown() || t.Y.IsUnknown() || t.ScaleX.IsUnknown() || t.ScaleY.IsUnknown() || t.Rotation.IsUnknown() || t.Alignment.IsUnknown() || t.BoundsType.IsUnknown() || t.BoundsAlignment.IsUnknown() || t.BoundsWidth.IsUnknown() || t.BoundsHeight.IsUnknown() || t.CropToBounds.IsUnknown() || t.CropLeft.IsUnknown() || t.CropTop.IsUnknown() || t.CropRight.IsUnknown() || t.CropBottom.IsUnknown()
}

func (r *groupGraphResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state groupGraphModel
	var rawPlan groupGraphPlanModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &rawPlan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan := rawPlan.desiredModel()
	if err := validateGroupGraphPlanTransition(state, plan); err != nil {
		resp.Diagnostics.AddError("Unsupported OBS group graph plan", err.Error())
		return
	}
	if markGroupGraphRepairIdentityUnknowns(state, &rawPlan) {
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &rawPlan)...)
	}
}

// markGroupGraphRepairIdentityUnknowns prevents Terraform from treating a
// retained absent binding as the post-repair identity. Read deliberately keeps
// the old UUID/item tuple while setting Present=false so configuration can plan
// an in-place repair. The repair may create a new source and always creates a
// new placement item, so those computed fields and the observation-derived
// nonce cannot remain known across Apply.
func markGroupGraphRepairIdentityUnknowns(state groupGraphModel, plan *groupGraphPlanModel) bool {
	changed := false
	nonceChanges := len(state.Children) != len(plan.Children)
	stateByKey := groupGraphChildrenByStableKey(state.Children)
	for i := range plan.Children {
		key := plan.Children[i].StableKey
		if key.IsNull() || key.IsUnknown() {
			continue
		}
		before, existed := stateByKey[key.ValueString()]
		if !existed {
			nonceChanges = true
			continue
		}
		if i >= len(state.Children) || state.Children[i].StableKey.IsNull() || state.Children[i].StableKey.IsUnknown() || state.Children[i].StableKey.ValueString() != key.ValueString() {
			nonceChanges = true
		}
		if before.Present.IsNull() || before.Present.IsUnknown() || before.Present.ValueBool() {
			continue
		}
		plan.Children[i].SourceUUID = types.StringUnknown()
		plan.Children[i].ItemID = types.Int64Unknown()
		changed = true
		nonceChanges = true
	}
	if !state.ParentPresent.IsNull() && !state.ParentPresent.IsUnknown() && !state.ParentPresent.ValueBool() {
		plan.ParentItemID = types.Int64Unknown()
		changed = true
		nonceChanges = true
	}
	if nonceChanges {
		plan.RepairNonce = types.StringUnknown()
		changed = true
	}
	return changed
}

func validateGroupGraphPlanTransition(state, plan groupGraphModel) error {
	if knownChanged(state.Name, plan.Name) {
		return fmt.Errorf("group name changes are not supported; remove state and explicitly import the intended native identity")
	}
	stateByKey := groupGraphChildrenByStableKey(state.Children)
	stateIndex := make(map[string]int, len(state.Children))
	for i, child := range state.Children {
		if !child.StableKey.IsNull() && !child.StableKey.IsUnknown() {
			stateIndex[child.StableKey.ValueString()] = i
		}
	}
	planKeys := map[string]bool{}
	for i := range plan.Children {
		after := plan.Children[i]
		if after.StableKey.IsNull() || after.StableKey.IsUnknown() {
			continue
		}
		key := after.StableKey.ValueString()
		planKeys[key] = true
		before, existed := stateByKey[key]
		if !existed {
			continue
		}
		if knownChanged(before.Name, after.Name) {
			return fmt.Errorf("children[%d].name changes are not supported", stateIndex[key])
		}
		if knownChanged(before.Kind, after.Kind) {
			return fmt.Errorf("children[%d].kind changes are not supported", stateIndex[key])
		}
	}
	for key := range stateByKey {
		if !planKeys[key] {
			if len(plan.Children) < len(state.Children) {
				return fmt.Errorf("removing an existing child is not supported in schema version 1 (missing stable key %q)", key)
			}
			return fmt.Errorf("stable_key changes are not supported; existing key %q disappeared; use state removal and explicit reimport", key)
		}
	}
	return nil
}

func groupGraphChildrenByStableKey(children []groupGraphChildModel) map[string]groupGraphChildModel {
	out := make(map[string]groupGraphChildModel, len(children))
	for _, child := range children {
		if child.StableKey.IsNull() || child.StableKey.IsUnknown() {
			continue
		}
		out[child.StableKey.ValueString()] = child
	}
	return out
}

func knownChanged(a, b types.String) bool {
	return !a.IsNull() && !a.IsUnknown() && !b.IsNull() && !b.IsUnknown() && a.ValueString() != b.ValueString()
}

func cloneGroupGraphModel(m groupGraphModel) groupGraphModel {
	out := m
	out.Children = append([]groupGraphChildModel(nil), m.Children...)
	out.ObservedChildren = append([]groupGraphObservedChildModel(nil), m.ObservedChildren...)
	return out
}

func groupGraphChildIndexByStableKey(children []groupGraphChildModel, key string) int {
	for i := range children {
		if !children[i].StableKey.IsNull() && !children[i].StableKey.IsUnknown() && children[i].StableKey.ValueString() == key {
			return i
		}
	}
	return -1
}

// mergeGroupGraphObservation advances only fields actually observed from OBS.
// It intentionally does not copy desired settings from desired into an
// existing actual child; setting callbacks advance File/Unload separately.
func mergeGroupGraphObservation(actual *groupGraphModel, desired groupGraphModel, observed bridgeGroupObservation) error {
	actual.SceneName, actual.Name = types.StringValue(observed.SceneName), types.StringValue(observed.Name)
	actual.GroupUUID, actual.ParentItemID = types.StringValue(observed.UUID), types.Int64Value(observed.ItemID)
	actual.ID = types.StringValue(groupGraphID(actual.Collection.ValueString(), observed.SceneUUID, observed.UUID))
	actual.ParentPresent, actual.ParentPlacement = types.BoolValue(true), placementPointerValue(observed.Placement)
	for i := range desired.Children {
		want := desired.Children[i]
		got, present, err := groupGraphChildByBinding(observed.Children, want.SourceUUID, want.ItemID)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		index := groupGraphChildIndexByStableKey(actual.Children, want.StableKey.ValueString())
		if index < 0 {
			actual.Children = append(actual.Children, want)
			index = len(actual.Children) - 1
		}
		child := &actual.Children[index]
		child.Name, child.Kind = types.StringValue(got.Name), types.StringValue(got.Kind)
		child.SourceUUID, child.ItemID, child.Present = types.StringValue(got.UUID), types.Int64Value(got.ItemID), types.BoolValue(true)
		child.Placement = placementPointerValue(got.Placement)
	}
	setGroupGraphObserved(actual, observed, false)
	return nil
}

func placementPointerValue(p bridgePlacementModel) *bridgePlacementModel { return &p }

func (r *groupGraphResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var rawPlan groupGraphPlanModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &rawPlan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired := rawPlan.desiredModel()
	if err := validateGroupGraphConfig(desired); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group graph", err.Error())
		return
	}
	actual := cloneGroupGraphModel(desired)
	actual.ID, actual.GroupUUID, actual.ParentItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	actual.ParentPresent, actual.ObservedComplete = types.BoolValue(false), types.BoolValue(false)
	for i := range actual.Children {
		actual.Children[i].Present = types.BoolValue(false)
	}
	checkpoint := func() error {
		resp.Diagnostics.Append(resp.State.Set(ctx, &actual)...)
		if resp.Diagnostics.HasError() {
			return fmt.Errorf("partial group graph state checkpoint failed")
		}
		return nil
	}
	err := r.client.withKeyed(ctx, []string{rawPlan.SceneUUID.ValueString(), rawPlan.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, desired.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneByUUID(ctx, cl, desired.SceneUUID.ValueString())
		if err != nil || !ok || scene.Name != desired.SceneName.ValueString() {
			return fmt.Errorf("group parent scene identity mismatch: %v", err)
		}
		if exists, err := groupNameExists(ctx, cl, desired.Name.ValueString()); err != nil || exists {
			return fmt.Errorf("group name must be exactly absent before Create: exists=%t error=%v", exists, err)
		}
		for i := range desired.Children {
			if in, exists, err := inputByName(ctx, cl, desired.Children[i].Name.ValueString()); err != nil || exists {
				return fmt.Errorf("children[%d] source name must be exactly absent before Create: observed=%s/%s exists=%t error=%v", i, in.Name, in.UUID, exists, err)
			}
		}

		siblings, countErr := containerItemCount(ctx, cl, scene.Name, false)
		if countErr != nil {
			return countErr
		}
		parentPlacement := *desired.ParentPlacement
		parentPlacement.Index = types.Int64Value(clampCreateIndex(desired.ParentPlacement.Index.ValueInt64(), siblings))
		placement, _ := parentPlacement.request()
		envelope, callErr := bridgeMutation(ctx, cl, desired.Collection.ValueString(), "CreateGroup", bridgeOperationID("create-group-graph", desired.Name.ValueString()), map[string]any{
			"scene": bridgeIdentity(scene.UUID, scene.Name, 0), "groupName": desired.Name.ValueString(), "placement": placement,
		})
		observed, observedErr := trustedGroupMutationResult(envelope, callErr)
		if observedErr == nil {
			desired.GroupUUID, desired.ParentItemID = types.StringValue(observed.UUID), types.Int64Value(observed.ItemID)
			desired.ID = types.StringValue(groupGraphID(desired.Collection.ValueString(), scene.UUID, observed.UUID))
			if err := mergeGroupGraphObservation(&actual, desired, observed); err != nil {
				return err
			}
			if err := checkpoint(); err != nil {
				return err
			}
		}
		if callErr != nil {
			return callErr
		}
		if envelope.Outcome != "completed" {
			return fmt.Errorf("bridge CreateGroup %s (%s): %s; re-observe before retry", envelope.Outcome, envelope.Code, envelope.Message)
		}
		if observedErr != nil {
			return observedErr
		}
		if observed.SceneUUID != scene.UUID || observed.SceneName != scene.Name || observed.Name != desired.Name.ValueString() || len(observed.Children) != 0 {
			return fmt.Errorf("bridge CreateGroup returned a mismatched or nonempty group")
		}

		observed, err = groupGraphFrameBarrier(ctx, cl, desired)
		if err != nil {
			return err
		}
		if len(observed.Children) != 0 {
			return fmt.Errorf("new group became nonempty before managed child creation")
		}
		if err := mergeGroupGraphObservation(&actual, desired, observed); err != nil {
			return err
		}
		if err := checkpoint(); err != nil {
			return err
		}

		for i := range desired.Children {
			child := &desired.Children[i]
			if _, exists, err := inputByName(ctx, cl, child.Name.ValueString()); err != nil || exists {
				return fmt.Errorf("children[%d] source name is no longer absent before mutation: exists=%t error=%v", i, exists, err)
			}
			requested, _ := child.Placement.request()
			envelope, callErr := bridgeMutation(ctx, cl, desired.Collection.ValueString(), "CreateInputInGroup", bridgeOperationID("create-group-graph-child", child.StableKey.ValueString()), map[string]any{
				"scene":     bridgeIdentity(scene.UUID, scene.Name, 0),
				"group":     bridgeIdentity(desired.GroupUUID.ValueString(), desired.Name.ValueString(), desired.ParentItemID.ValueInt64()),
				"inputName": child.Name.ValueString(), "inputKind": child.Kind.ValueString(),
				"inputSettings": map[string]any{"file": child.File.ValueString(), "unload": child.Unload.ValueBool()}, "placement": requested,
			})
			created, createdErr := trustedChildMutationResult(envelope, callErr)
			if createdErr == nil {
				child.SourceUUID, child.ItemID, child.Present = types.StringValue(created.UUID), types.Int64Value(created.ItemID), types.BoolValue(true)
				actualChild := &actual.Children[i]
				actualChild.SourceUUID, actualChild.ItemID, actualChild.Present = child.SourceUUID, child.ItemID, types.BoolValue(true)
				actualChild.Placement = placementPointerValue(created.Placement)
				if err := checkpoint(); err != nil {
					return err
				}
			}
			if callErr != nil {
				return callErr
			}
			if envelope.Outcome != "completed" {
				return fmt.Errorf("bridge CreateInputInGroup %s (%s): %s; re-observe before retry", envelope.Outcome, envelope.Code, envelope.Message)
			}
			if createdErr != nil {
				return createdErr
			}
			observed, err = groupGraphFrameBarrier(ctx, cl, desired)
			if err != nil {
				return err
			}
			if err := validateGroupGraphRoster(ctx, cl, observed, desired.Children); err != nil {
				return fmt.Errorf("post-create roster: %w", err)
			}
			if err := mergeGroupGraphObservation(&actual, desired, observed); err != nil {
				return err
			}
			if err := checkpoint(); err != nil {
				return err
			}
		}

		observed, err = reconcileGroupGraph(ctx, cl, &desired, &actual, observed, checkpoint)
		if err != nil {
			return err
		}
		setGroupGraphObserved(&desired, observed, true)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS group graph failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &desired)...)
}

func trustedGroupMutationResult(envelope bridgeEnvelope, callErr error) (bridgeGroupObservation, error) {
	if callErr != nil && !isAuthoritativeNativeInterruption(callErr) {
		return bridgeGroupObservation{}, fmt.Errorf("bridge group result is not authoritative: %w", callErr)
	}
	if envelope.Outcome != "completed" {
		return bridgeGroupObservation{}, fmt.Errorf("bridge group result is not authoritative because the mutation outcome is %s", envelope.Outcome)
	}
	return bridgeGroupFromResult(envelope.Result)
}

func trustedChildMutationResult(envelope bridgeEnvelope, callErr error) (bridgeChildObservation, error) {
	if callErr != nil && !isAuthoritativeNativeInterruption(callErr) {
		return bridgeChildObservation{}, fmt.Errorf("bridge child result is not authoritative: %w", callErr)
	}
	if envelope.Outcome != "completed" {
		return bridgeChildObservation{}, fmt.Errorf("bridge child result is not authoritative because the mutation outcome is %s", envelope.Outcome)
	}
	return bridgeChildFromResult(envelope.Result)
}

func (r *groupGraphResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m groupGraphModel
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
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupGraphRequest(m))
		if err != nil {
			return err
		}
		if envelope.Outcome != "completed" {
			probe := groupModel{Collection: m.Collection, SceneName: m.SceneName, SceneUUID: m.SceneUUID, Name: m.Name, GroupUUID: m.GroupUUID, ItemID: m.ParentItemID}
			err := readMissingGroup(ctx, cl, &probe, envelope)
			if errors.Is(err, errGroupGone) {
				return errGroupGraphGone
			}
			if err != nil {
				return err
			}
			m.ParentPresent = types.BoolValue(false)
			m.ObservedComplete = types.BoolValue(false)
			m.ObservedParent = &groupGraphObservedParentModel{Present: types.BoolValue(false), Placement: m.ParentPlacement}
			m.RepairNonce = types.StringValue(groupGraphRepairNonce(m))
			return nil
		}
		observed, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		if err := validateGroupGraphAnchor(observed, m); err != nil {
			return err
		}
		complete, err := refreshGroupGraphChildren(ctx, cl, &m, observed)
		if err != nil {
			return err
		}
		m.ParentPresent = types.BoolValue(true)
		if m.ParentPlacement != nil {
			retained := retainAuthoredBridgePlacement(observed.Placement, *m.ParentPlacement)
			m.ParentPlacement = &retained
		} else {
			m.ParentPlacement = &observed.Placement
		}
		setGroupGraphObserved(&m, observed, complete)
		return nil
	})
	if err != nil {
		if errors.Is(err, errGroupGraphGone) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read OBS group graph failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *groupGraphResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var state groupGraphModel
	var rawPlan groupGraphPlanModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &rawPlan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan := rawPlan.desiredModel()
	if err := validateGroupGraphConfig(plan); err != nil {
		resp.Diagnostics.AddError("Invalid OBS group graph", err.Error())
		return
	}
	if err := validateGroupGraphPlanTransition(state, plan); err != nil {
		resp.Diagnostics.AddError("Unsupported OBS group graph plan", err.Error())
		return
	}
	actual := cloneGroupGraphModel(state)
	checkpoint := func() error {
		resp.Diagnostics.Append(resp.State.Set(ctx, &actual)...)
		if resp.Diagnostics.HasError() {
			return fmt.Errorf("partial group graph state checkpoint failed")
		}
		return nil
	}
	err := r.client.withKeyed(ctx, []string{state.SceneUUID.ValueString(), state.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneByUUID(ctx, cl, plan.SceneUUID.ValueString())
		if err != nil || !ok || scene.Name != plan.SceneName.ValueString() {
			return fmt.Errorf("group parent scene identity mismatch: %v", err)
		}
		plan.GroupUUID, plan.ParentItemID = state.GroupUUID, state.ParentItemID
		plan.ID = types.StringValue(groupGraphID(plan.Collection.ValueString(), plan.SceneUUID.ValueString(), plan.GroupUUID.ValueString()))
		stateByKey := groupGraphChildrenByStableKey(state.Children)
		for i := range plan.Children {
			if before, existed := stateByKey[plan.Children[i].StableKey.ValueString()]; existed {
				plan.Children[i].SourceUUID, plan.Children[i].ItemID = before.SourceUUID, before.ItemID
			}
		}

		var observed bridgeGroupObservation
		if !state.ParentPresent.ValueBool() {
			if exists, err := groupNameExists(ctx, cl, plan.Name.ValueString()); err != nil || !exists {
				return fmt.Errorf("missing parent anchor cannot be repaired because exact group source survival is unproven: exists=%t error=%v", exists, err)
			}
			items, err := sceneItems(ctx, cl, scene.Name)
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ItemID == state.ParentItemID.ValueInt64() || item.SourceUUID == state.GroupUUID.ValueString() {
					return fmt.Errorf("parent anchor absence proof contradicted by item %d/%s", item.ItemID, item.SourceUUID)
				}
			}
			if _, err := bridgeCapabilities(ctx, cl, plan.Collection.ValueString(), "GetGroup", true); err != nil {
				return err
			}
			x, callErr := mutation(ctx, cl, plan.Collection.ValueString(), "CreateSceneItem", map[string]any{"sceneUuid": scene.UUID, "sourceUuid": state.GroupUUID.ValueString(), "sceneItemEnabled": plan.ParentPlacement.Enabled.ValueBool()})
			itemID := int64Value(x["sceneItemId"])
			if itemID <= 0 {
				return fmt.Errorf("CreateSceneItem omitted positive parent item ID: %v", callErr)
			}
			plan.ParentItemID, plan.ParentPresent = types.Int64Value(itemID), types.BoolValue(true)
			actual.ParentItemID, actual.ParentPresent = plan.ParentItemID, types.BoolValue(true)
			actual.ID = types.StringValue(groupGraphID(actual.Collection.ValueString(), actual.SceneUUID.ValueString(), actual.GroupUUID.ValueString()))
			if err := checkpoint(); err != nil {
				return err
			}
			if callErr != nil {
				return callErr
			}
			observed, err = groupGraphFrameBarrier(ctx, cl, plan)
			if err != nil {
				return err
			}
			if err := mergeGroupGraphObservation(&actual, plan, observed); err != nil {
				return err
			}
			if err := checkpoint(); err != nil {
				return err
			}
		} else {
			envelope, err := bridgeRead(ctx, cl, "GetGroup", groupGraphRequest(plan))
			if err != nil || envelope.Outcome != "completed" {
				return fmt.Errorf("group unavailable before aggregate update: error=%v outcome=%s", err, envelope.Outcome)
			}
			observed, err = bridgeGroupFromResult(envelope.Result)
			if err != nil {
				return err
			}
		}
		if err := validateGroupGraphAnchor(observed, plan); err != nil {
			return err
		}
		if err := validateGroupGraphRoster(ctx, cl, observed, state.Children); err != nil {
			return fmt.Errorf("pre-membership roster: %w", err)
		}
		if err := validateGroupGraphPreMutationSnapshot(observed, state, plan); err != nil {
			return err
		}

		for i := range plan.Children {
			child := &plan.Children[i]
			_, existedInState := stateByKey[child.StableKey.ValueString()]
			current, present, err := groupGraphChildByBinding(observed.Children, child.SourceUUID, child.ItemID)
			if err != nil {
				return err
			}
			if present {
				if current.Name != child.Name.ValueString() || current.Kind != child.Kind.ValueString() {
					return fmt.Errorf("children[%d] identity/name/kind rebound", i)
				}
				child.Present = types.BoolValue(true)
				continue
			}

			requested, _ := child.Placement.request()
			var envelope bridgeEnvelope
			var callErr error
			createdSource := false
			in, sourceExists, sourceErr := inputByUUID(ctx, cl, child.SourceUUID.ValueString())
			if sourceErr != nil {
				return sourceErr
			}
			if !existedInState || child.SourceUUID.IsNull() || child.SourceUUID.IsUnknown() || child.SourceUUID.ValueString() == "" || !sourceExists {
				createdSource = true
				if collision, exists, err := inputByName(ctx, cl, child.Name.ValueString()); err != nil || exists {
					return fmt.Errorf("replacement child name is not exactly absent: observed=%s/%s exists=%t error=%v", collision.Name, collision.UUID, exists, err)
				}
				envelope, callErr = bridgeMutation(ctx, cl, plan.Collection.ValueString(), "CreateInputInGroup", bridgeOperationID("repair-group-graph-child", child.StableKey.ValueString()), map[string]any{
					"scene":     bridgeIdentity(plan.SceneUUID.ValueString(), plan.SceneName.ValueString(), 0),
					"group":     bridgeIdentity(plan.GroupUUID.ValueString(), plan.Name.ValueString(), plan.ParentItemID.ValueInt64()),
					"inputName": child.Name.ValueString(), "inputKind": child.Kind.ValueString(),
					"inputSettings": map[string]any{"file": child.File.ValueString(), "unload": child.Unload.ValueBool()}, "placement": requested,
				})
			} else {
				if in.Name != child.Name.ValueString() || in.Kind != child.Kind.ValueString() {
					return fmt.Errorf("surviving child source identity/name/kind rebound")
				}
				envelope, callErr = bridgeMutation(ctx, cl, plan.Collection.ValueString(), "AddSourceToGroup", bridgeOperationID("reattach-group-graph-child", child.StableKey.ValueString()), map[string]any{
					"scene":  bridgeIdentity(plan.SceneUUID.ValueString(), plan.SceneName.ValueString(), 0),
					"group":  bridgeIdentity(plan.GroupUUID.ValueString(), plan.Name.ValueString(), plan.ParentItemID.ValueInt64()),
					"source": bridgeIdentity(in.UUID, in.Name, 0), "placement": requested,
				})
			}
			created, createdErr := trustedChildMutationResult(envelope, callErr)
			if createdErr == nil {
				child.SourceUUID, child.ItemID, child.Present = types.StringValue(created.UUID), types.Int64Value(created.ItemID), types.BoolValue(true)
				actualIndex := groupGraphChildIndexByStableKey(actual.Children, child.StableKey.ValueString())
				if actualIndex < 0 {
					actual.Children = append(actual.Children, *child)
					actualIndex = len(actual.Children) - 1
				}
				actualChild := &actual.Children[actualIndex]
				actualChild.Name, actualChild.Kind = child.Name, child.Kind
				actualChild.SourceUUID, actualChild.ItemID, actualChild.Present = child.SourceUUID, child.ItemID, types.BoolValue(true)
				actualChild.Placement = placementPointerValue(created.Placement)
				if createdSource {
					actualChild.File, actualChild.Unload = child.File, child.Unload
				}
				if err := checkpoint(); err != nil {
					return err
				}
			}
			if callErr != nil {
				return callErr
			}
			if envelope.Outcome != "completed" {
				return fmt.Errorf("bridge child repair %s (%s): %s", envelope.Outcome, envelope.Code, envelope.Message)
			}
			if createdErr != nil {
				return createdErr
			}
			observed, err = groupGraphFrameBarrier(ctx, cl, plan)
			if err != nil {
				return err
			}
			if err := validateGroupGraphRoster(ctx, cl, observed, plan.Children); err != nil {
				return fmt.Errorf("post-membership roster: %w", err)
			}
			if err := mergeGroupGraphObservation(&actual, plan, observed); err != nil {
				return err
			}
			if err := checkpoint(); err != nil {
				return err
			}
		}

		observed, err = reconcileGroupGraph(ctx, cl, &plan, &actual, observed, checkpoint)
		if err != nil {
			return err
		}
		setGroupGraphObserved(&plan, observed, true)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS group graph failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupGraphResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupGraphModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	checkpoint := func() error {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		if resp.Diagnostics.HasError() {
			return fmt.Errorf("partial group graph delete state checkpoint failed")
		}
		return nil
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString(), m.GroupUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		envelope, err := bridgeRead(ctx, cl, "GetGroup", groupGraphRequest(m))
		if err != nil || envelope.Outcome != "completed" {
			return fmt.Errorf("group graph delete cannot prove exact current group: error=%v outcome=%s", err, envelope.Outcome)
		}
		observed, err := bridgeGroupFromResult(envelope.Result)
		if err != nil {
			return err
		}
		if err := validateGroupGraphAnchor(observed, m); err != nil {
			return err
		}
		if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
			return fmt.Errorf("pre-delete roster: %w", err)
		}
		for len(m.Children) > 0 {
			childState := m.Children[0]
			child, exists, err := groupGraphChildByBinding(observed.Children, childState.SourceUUID, childState.ItemID)
			if err != nil {
				return fmt.Errorf("delete child binding unavailable: %v", err)
			}
			if !exists && !childState.Present.IsNull() && !childState.Present.IsUnknown() && !childState.Present.ValueBool() {
				if err := proveRemovedGroupGraphChildAbsent(ctx, cl, childState); err != nil {
					return err
				}
				observed, err = groupGraphFrameBarrier(ctx, cl, m)
				if err != nil {
					return fmt.Errorf("resumed-delete barrier after completed child removal: %w", err)
				}
				if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
					return fmt.Errorf("resumed-delete post-barrier roster: %w", err)
				}
				if err := proveRemovedGroupGraphChildAbsent(ctx, cl, childState); err != nil {
					return err
				}
				m.Children = m.Children[1:]
				if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
					return fmt.Errorf("resumed-delete roster: %w", err)
				}
				if err := mergeGroupGraphObservation(&m, m, observed); err != nil {
					return err
				}
				if err := checkpoint(); err != nil {
					return err
				}
				continue
			}
			if !exists {
				return fmt.Errorf("delete child binding disappeared without a prior authoritative absence checkpoint")
			}
			filters, err := sourceFilters(ctx, cl, child.Name)
			if err != nil {
				return err
			}
			if len(filters) > 0 {
				return fmt.Errorf("refusing child delete with %d attached filters", len(filters))
			}
			extras, err := allSourceReferences(ctx, cl, child.UUID, observed.Name, child.ItemID)
			if err != nil {
				return err
			}
			if len(extras) > 0 {
				return fmt.Errorf("refusing child delete with outside references %v", extras)
			}
			expected, _ := child.Placement.request()
			envelope, callErr := bridgeMutation(ctx, cl, m.Collection.ValueString(), "RemoveGroupChildPlacement", bridgeOperationID("delete-group-graph-child", childState.StableKey.ValueString()), map[string]any{
				"scene": bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0),
				"group": bridgeIdentity(m.GroupUUID.ValueString(), m.Name.ValueString(), m.ParentItemID.ValueInt64()),
				"child": bridgeIdentity(child.UUID, child.Name, child.ItemID), "expectedPlacement": expected,
			})
			if callErr != nil && !isAuthoritativeNativeInterruption(callErr) {
				return callErr
			}
			if envelope.Outcome != "completed" {
				return fmt.Errorf("bridge RemoveGroupChildPlacement %s (%s): %s", envelope.Outcome, envelope.Code, envelope.Message)
			}
			survives, ok := envelope.Result["sourceStillExists"].(bool)
			if !ok || survives {
				return fmt.Errorf("child removal source lifetime is unknown or unexpectedly surviving")
			}
			m.Children[0].Present = types.BoolValue(false)
			m.ObservedComplete = types.BoolValue(false)
			if err := checkpoint(); err != nil {
				return err
			}
			if callErr != nil {
				return callErr
			}
			observed, err = groupGraphFrameBarrier(ctx, cl, m)
			if err != nil {
				return err
			}
			if _, exists, err := inputByUUID(ctx, cl, child.UUID); err != nil || exists {
				return fmt.Errorf("removed child source absence was not proven: exists=%t error=%v", exists, err)
			}
			m.Children = m.Children[1:]
			if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
				return fmt.Errorf("post-delete roster: %w", err)
			}
			if err := mergeGroupGraphObservation(&m, m, observed); err != nil {
				return err
			}
			if err := checkpoint(); err != nil {
				return err
			}
		}
		filters, err := sourceFilters(ctx, cl, observed.Name)
		if err != nil {
			return err
		}
		if len(filters) > 0 {
			return fmt.Errorf("refusing group delete with %d attached filters", len(filters))
		}
		extras, err := sourceReferences(ctx, cl, observed.UUID, m.SceneUUID.ValueString(), m.ParentItemID.ValueInt64())
		if err != nil {
			return err
		}
		if len(extras) > 0 {
			return fmt.Errorf("refusing group delete with outside references %v", extras)
		}
		if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", true); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveSceneItem", map[string]any{"sceneUuid": m.SceneUUID.ValueString(), "sceneItemId": m.ParentItemID.ValueInt64()}); err != nil {
			return err
		}
		items, err := sceneItems(ctx, cl, m.SceneName.ValueString())
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ItemID == m.ParentItemID.ValueInt64() || item.SourceUUID == m.GroupUUID.ValueString() {
				return fmt.Errorf("group parent/source still present after delete")
			}
		}
		if exists, err := groupNameExists(ctx, cl, m.Name.ValueString()); err != nil || exists {
			return fmt.Errorf("group source absence not proven after delete: exists=%t error=%v", exists, err)
		}
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS group graph failed", err.Error())
	}
}

func (r *groupGraphResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 5 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid group graph import ID", "use collection|scene_uuid|group_uuid|parent_item_id|stable_key=source_uuid:item_id,...")
		return
	}
	parentID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || parentID <= 0 {
		resp.Diagnostics.AddError("Invalid group graph import ID", "parent_item_id must be positive")
		return
	}
	var children []groupGraphChildModel
	seen := map[string]bool{}
	if parts[4] == "" {
		resp.Diagnostics.AddError("Invalid group graph import ID", "at least one child binding is required")
		return
	}
	for _, binding := range strings.Split(parts[4], ",") {
		pair := strings.SplitN(binding, "=", 2)
		if len(pair) != 2 || !groupGraphStableKeyRE.MatchString(pair[0]) || seen[pair[0]] {
			resp.Diagnostics.AddError("Invalid group graph import ID", fmt.Sprintf("invalid or duplicate stable key binding %q", binding))
			return
		}
		native := strings.SplitN(pair[1], ":", 2)
		itemID, parseErr := int64(0), error(nil)
		if len(native) == 2 {
			itemID, parseErr = strconv.ParseInt(native[1], 10, 64)
		}
		if len(native) != 2 || native[0] == "" || parseErr != nil || itemID <= 0 {
			resp.Diagnostics.AddError("Invalid group graph import ID", fmt.Sprintf("invalid native child binding %q", binding))
			return
		}
		seen[pair[0]] = true
		children = append(children, groupGraphChildModel{
			StableKey: types.StringValue(pair[0]), Name: types.StringNull(), Kind: types.StringNull(), File: types.StringNull(),
			Unload: types.BoolNull(), Present: types.BoolValue(true), Placement: nil,
			SourceUUID: types.StringValue(native[0]), ItemID: types.Int64Value(itemID),
		})
	}
	for key, value := range map[string]any{
		"id": groupGraphID(parts[0], parts[1], parts[2]), "collection": parts[0], "scene_uuid": parts[1],
		"group_uuid": parts[2], "parent_item_id": parentID, "parent_present": true, "children": children,
	} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), value)...)
	}
}

func proveRemovedGroupGraphChildAbsent(ctx context.Context, cl requester, child groupGraphChildModel) error {
	if !child.SourceUUID.IsNull() && !child.SourceUUID.IsUnknown() && child.SourceUUID.ValueString() != "" {
		if _, sourceExists, err := inputByUUID(ctx, cl, child.SourceUUID.ValueString()); err != nil || sourceExists {
			return fmt.Errorf("previously removed child source absence is not proven: exists=%t error=%v", sourceExists, err)
		}
	}
	if collision, nameExists, err := inputByName(ctx, cl, child.Name.ValueString()); err != nil || nameExists {
		return fmt.Errorf("previously removed child name absence is not proven: observed=%s/%s exists=%t error=%v", collision.Name, collision.UUID, nameExists, err)
	}
	return nil
}

func groupGraphRequest(m groupGraphModel) map[string]any {
	return map[string]any{
		"scene": bridgeIdentity(m.SceneUUID.ValueString(), m.SceneName.ValueString(), 0),
		"group": bridgeIdentity(m.GroupUUID.ValueString(), m.Name.ValueString(), m.ParentItemID.ValueInt64()),
	}
}

func groupGraphFrameBarrier(ctx context.Context, cl requester, m groupGraphModel) (bridgeGroupObservation, error) {
	scene, ok, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
	if err != nil || !ok || (!m.SceneName.IsNull() && !m.SceneName.IsUnknown() && scene.Name != m.SceneName.ValueString()) {
		return bridgeGroupObservation{}, fmt.Errorf("frame barrier parent scene identity mismatch: %v", err)
	}
	if scene.UUID == m.GroupUUID.ValueString() {
		return bridgeGroupObservation{}, fmt.Errorf("frame barrier refuses group-source screenshot")
	}
	x, err := request(ctx, cl, "GetSourceScreenshot", map[string]any{"sourceUuid": scene.UUID, "imageFormat": "png", "imageWidth": 64, "imageHeight": 36})
	if err != nil {
		return bridgeGroupObservation{}, fmt.Errorf("parent-scene frame barrier failed: %w", err)
	}
	imageData, ok := x["imageData"].(string)
	if !ok || imageData == "" {
		return bridgeGroupObservation{}, fmt.Errorf("parent-scene frame barrier returned empty imageData")
	}
	payload := imageData
	if comma := strings.IndexByte(payload, ','); comma >= 0 {
		payload = payload[comma+1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(decoded) == 0 {
		return bridgeGroupObservation{}, fmt.Errorf("parent-scene frame barrier returned invalid image data")
	}
	hash := sha256.Sum256(decoded)
	if err := recordTrace(map[string]any{"event": "group-frame-barrier", "source_uuid": scene.UUID, "source_name": scene.Name, "image_sha256": hex.EncodeToString(hash[:]), "image_bytes": len(decoded)}); err != nil {
		return bridgeGroupObservation{}, err
	}
	envelope, err := bridgeRead(ctx, cl, "GetGroup", groupGraphRequest(m))
	if err != nil || envelope.Outcome != "completed" {
		return bridgeGroupObservation{}, fmt.Errorf("fresh GetGroup after frame barrier failed: error=%v outcome=%s", err, envelope.Outcome)
	}
	observed, err := bridgeGroupFromResult(envelope.Result)
	if err != nil {
		return bridgeGroupObservation{}, err
	}
	if err := validateGroupGraphAnchor(observed, m); err != nil {
		return bridgeGroupObservation{}, err
	}
	return observed, nil
}

func validateGroupGraphAnchor(observed bridgeGroupObservation, m groupGraphModel) error {
	if observed.SceneUUID != m.SceneUUID.ValueString() || observed.UUID != m.GroupUUID.ValueString() || observed.ItemID != m.ParentItemID.ValueInt64() {
		return fmt.Errorf("group parent/group identity rebound: observed scene=%s group=%s item=%d", observed.SceneUUID, observed.UUID, observed.ItemID)
	}
	if !m.SceneName.IsNull() && !m.SceneName.IsUnknown() && observed.SceneName != m.SceneName.ValueString() {
		return fmt.Errorf("group parent scene renamed/rebound")
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() && observed.Name != m.Name.ValueString() {
		return fmt.Errorf("group source renamed/rebound")
	}
	return nil
}

func groupGraphChildByBinding(observed []bridgeChildObservation, uuid types.String, itemID types.Int64) (bridgeChildObservation, bool, error) {
	if uuid.IsNull() || uuid.IsUnknown() || uuid.ValueString() == "" || itemID.IsNull() || itemID.IsUnknown() || itemID.ValueInt64() <= 0 {
		return bridgeChildObservation{}, false, nil
	}
	return exactBridgeChild(observed, itemID.ValueInt64(), uuid.ValueString())
}

func validateGroupGraphRoster(ctx context.Context, cl requester, observed bridgeGroupObservation, expected []groupGraphChildModel) error {
	seenUUID, seenName := map[string]bool{}, map[string]bool{}
	for _, child := range observed.Children {
		if seenUUID[child.UUID] || seenName[child.Name] {
			return fmt.Errorf("duplicate direct child UUID or name: %s/%s/%d", child.Name, child.UUID, child.ItemID)
		}
		seenUUID[child.UUID], seenName[child.Name] = true, true
	}
	matched := map[int64]bool{}
	for i, child := range expected {
		got, present, err := groupGraphChildByBinding(observed.Children, child.SourceUUID, child.ItemID)
		if err != nil {
			return fmt.Errorf("children[%d]: %w", i, err)
		}
		if !present {
			if child.SourceUUID.IsNull() || child.SourceUUID.IsUnknown() || child.SourceUUID.ValueString() == "" {
				continue
			}
			if !child.Present.IsNull() && !child.Present.IsUnknown() && child.Present.ValueBool() {
				return fmt.Errorf("children[%d] disappeared after refresh; refusing unplanned membership repair", i)
			}
			in, exists, err := inputByUUID(ctx, cl, child.SourceUUID.ValueString())
			if err != nil {
				return err
			}
			if exists && ((!child.Name.IsNull() && !child.Name.IsUnknown() && in.Name != child.Name.ValueString()) || (!child.Kind.IsNull() && !child.Kind.IsUnknown() && in.Kind != child.Kind.ValueString())) {
				return fmt.Errorf("children[%d] surviving source identity/name/kind rebound", i)
			}
			continue
		}
		if (!child.Name.IsNull() && !child.Name.IsUnknown() && got.Name != child.Name.ValueString()) || (!child.Kind.IsNull() && !child.Kind.IsUnknown() && got.Kind != child.Kind.ValueString()) {
			return fmt.Errorf("children[%d] identity/name/kind rebound", i)
		}
		if !child.Present.IsNull() && !child.Present.IsUnknown() && !child.Present.ValueBool() {
			return fmt.Errorf("children[%d] reappeared after an authoritative absence refresh; refusing concurrent adoption", i)
		}
		if matched[got.ItemID] {
			return fmt.Errorf("children[%d] duplicates item ID %d", i, got.ItemID)
		}
		matched[got.ItemID] = true
	}
	if len(matched) != len(observed.Children) {
		var extras []string
		for _, child := range observed.Children {
			if !matched[child.ItemID] {
				extras = append(extras, fmt.Sprintf("%s/%s/%d", child.Name, child.UUID, child.ItemID))
			}
		}
		return fmt.Errorf("unexpected unmanaged or duplicate direct children %v", extras)
	}
	return nil
}

func validateGroupGraphPreMutationSnapshot(observed bridgeGroupObservation, state groupGraphModel, plans ...groupGraphModel) error {
	if state.ParentPresent.ValueBool() && state.ParentPlacement != nil {
		if diff := bridgePlacementDifference(observed.Placement, *state.ParentPlacement); diff != "" {
			// Another declared sibling can shift this index while being repaired.
			// Accept only an already-converged known index, with every other
			// parent field still equal to the refreshed snapshot.
			converged := false
			if len(plans) == 1 && plans[0].ParentPlacement != nil {
				index := plans[0].ParentPlacement.Index
				before := state.ParentPlacement.Index
				actual := observed.Placement.Index
				if !index.IsNull() && !index.IsUnknown() && !before.IsNull() && !before.IsUnknown() && !actual.IsNull() && !actual.IsUnknown() && actual.Equal(index) {
					expected := *state.ParentPlacement
					expected.Index = index
					converged = bridgePlacementDifference(observed.Placement, expected) == ""
				}
			}
			if !converged {
				return fmt.Errorf("parent placement changed concurrently after refresh: %s", diff)
			}
		}
	}
	for i := range state.Children {
		if state.Children[i].Present.IsNull() || state.Children[i].Present.IsUnknown() || !state.Children[i].Present.ValueBool() || state.Children[i].Placement == nil {
			continue
		}
		got, present, err := groupGraphChildByBinding(observed.Children, state.Children[i].SourceUUID, state.Children[i].ItemID)
		if err != nil || !present {
			return fmt.Errorf("children[%d] changed concurrently after refresh: %v", i, err)
		}
		if diff := bridgePlacementDifference(got.Placement, *state.Children[i].Placement); diff != "" {
			return fmt.Errorf("children[%d] placement changed concurrently after refresh: %s", i, diff)
		}
	}
	return nil
}

func refreshGroupGraphChildren(ctx context.Context, cl requester, m *groupGraphModel, observed bridgeGroupObservation) (bool, error) {
	matched := map[int64]bool{}
	complete := true
	var observedChildren []groupGraphObservedChildModel
	for i := range m.Children {
		child := &m.Children[i]
		got, present, err := groupGraphChildByBinding(observed.Children, child.SourceUUID, child.ItemID)
		if err != nil {
			return false, err
		}
		if !present {
			complete = false
			child.Present = types.BoolValue(false)
			if !child.SourceUUID.IsNull() && !child.SourceUUID.IsUnknown() && child.SourceUUID.ValueString() != "" {
				in, exists, err := inputByUUID(ctx, cl, child.SourceUUID.ValueString())
				if err != nil {
					return false, err
				}
				if exists && ((!child.Name.IsNull() && !child.Name.IsUnknown() && in.Name != child.Name.ValueString()) || (!child.Kind.IsNull() && !child.Kind.IsUnknown() && in.Kind != child.Kind.ValueString())) {
					return false, fmt.Errorf("children[%d] surviving source identity/name/kind rebound", i)
				}
				if exists {
					child.Name, child.Kind = types.StringValue(in.Name), types.StringValue(in.Kind)
					child.File, child.Unload = types.StringValue(in.File), types.BoolValue(in.Unload)
				}
			}
			observedChildren = append(observedChildren, groupGraphObservedChildModel{StableKey: child.StableKey, Name: child.Name, Kind: child.Kind, SourceUUID: child.SourceUUID, ItemID: child.ItemID, Present: types.BoolValue(false), Width: types.Int64Value(0), Height: types.Int64Value(0), Placement: child.Placement})
			continue
		}
		if matched[got.ItemID] {
			return false, fmt.Errorf("duplicate managed child item ID %d", got.ItemID)
		}
		matched[got.ItemID] = true
		in, exists, err := inputByUUID(ctx, cl, got.UUID)
		if err != nil || !exists {
			return false, fmt.Errorf("present group child source settings unavailable: %v", err)
		}
		if (!child.Name.IsNull() && !child.Name.IsUnknown() && got.Name != child.Name.ValueString()) || (!child.Kind.IsNull() && !child.Kind.IsUnknown() && got.Kind != child.Kind.ValueString()) {
			return false, fmt.Errorf("children[%d] identity/name/kind rebound", i)
		}
		child.Name, child.Kind, child.File, child.Unload = types.StringValue(got.Name), types.StringValue(got.Kind), types.StringValue(in.File), types.BoolValue(in.Unload)
		child.Present = types.BoolValue(true)
		if child.Placement != nil {
			retained := retainAuthoredBridgePlacement(got.Placement, *child.Placement)
			child.Placement = &retained
		} else {
			child.Placement = &got.Placement
		}
		observedChildren = append(observedChildren, groupGraphObservedChildModel{StableKey: child.StableKey, Name: types.StringValue(got.Name), Kind: types.StringValue(got.Kind), SourceUUID: types.StringValue(got.UUID), ItemID: types.Int64Value(got.ItemID), Present: types.BoolValue(true), Width: types.Int64Value(got.Width), Height: types.Int64Value(got.Height), Placement: &got.Placement})
	}
	if len(matched) != len(observed.Children) {
		return false, fmt.Errorf("unexpected unmanaged direct child present")
	}
	m.ObservedChildren = observedChildren
	return complete, nil
}

func reconcileGroupGraph(ctx context.Context, cl requester, m *groupGraphModel, actual *groupGraphModel, observed bridgeGroupObservation, checkpoint func() error) (bridgeGroupObservation, error) {
	if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
		return observed, err
	}
	wantWidth, wantHeight, anchor, err := proveGroupGraphZeroAnchor(observed, m.Children)
	if err != nil {
		return observed, err
	}

	settingsChanged := false
	for i := range m.Children {
		got, present, err := groupGraphChildByBinding(observed.Children, m.Children[i].SourceUUID, m.Children[i].ItemID)
		if err != nil || !present {
			return observed, fmt.Errorf("children[%d] unavailable before settings phase: %v", i, err)
		}
		in, exists, err := inputByUUID(ctx, cl, got.UUID)
		if err != nil || !exists {
			return observed, fmt.Errorf("children[%d] source settings unavailable: %v", i, err)
		}
		if in.File != m.Children[i].File.ValueString() || in.Unload != m.Children[i].Unload.ValueBool() {
			if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", true); err != nil {
				return observed, err
			}
			_, callErr := mutation(ctx, cl, m.Collection.ValueString(), "SetInputSettings", map[string]any{"inputUuid": in.UUID, "inputSettings": map[string]any{"file": m.Children[i].File.ValueString(), "unload": m.Children[i].Unload.ValueBool()}, "overlay": true})
			if callErr == nil || isAuthoritativeNativeInterruption(callErr) {
				actualIndex := groupGraphChildIndexByStableKey(actual.Children, m.Children[i].StableKey.ValueString())
				if actualIndex < 0 {
					return observed, fmt.Errorf("actual child state %q is unavailable after settings mutation", m.Children[i].StableKey.ValueString())
				}
				actual.Children[actualIndex].File, actual.Children[actualIndex].Unload = m.Children[i].File, m.Children[i].Unload
				setGroupGraphObserved(actual, observed, false)
				if err := checkpoint(); err != nil {
					return observed, err
				}
			}
			if callErr != nil {
				return observed, callErr
			}
			settingsChanged = true
		}
	}
	if settingsChanged {
		observed, err = groupGraphFrameBarrier(ctx, cl, *m)
		if err != nil {
			return observed, err
		}
		if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
			return observed, err
		}
		for i := range m.Children {
			if _, err := waitInputExpected(ctx, cl, m.Children[i].SourceUUID.ValueString(), m.Children[i].Name.ValueString(), m.Children[i].Kind.ValueString(), m.Children[i].File.ValueString(), m.Children[i].Unload.ValueBool(), 1, 0); err != nil {
				return observed, fmt.Errorf("children[%d] settings postcondition: %w", i, err)
			}
		}
		if err := mergeGroupGraphObservation(actual, *m, observed); err != nil {
			return observed, err
		}
		if err := checkpoint(); err != nil {
			return observed, err
		}
		wantWidth, wantHeight, anchor, err = proveGroupGraphZeroAnchor(observed, m.Children)
		if err != nil {
			return observed, err
		}
	}

	childWrites := false
	for i := range m.Children {
		got, present, err := groupGraphChildByBinding(observed.Children, m.Children[i].SourceUUID, m.Children[i].ItemID)
		if err != nil || !present {
			return observed, fmt.Errorf("children[%d] unavailable before placement phase: %v", i, err)
		}
		if diff := bridgePlacementDifference(got.Placement, *m.Children[i].Placement); diff != "" {
			if i == anchor {
				return observed, fmt.Errorf("managed zero anchor drifted before setters: %s", diff)
			}
			if !childWrites {
				if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", true); err != nil {
					return observed, err
				}
			}
			observedIndex := groupGraphObservedChildIndex(observed.Children, got.ItemID)
			if observedIndex < 0 {
				return observed, fmt.Errorf("child item %d vanished from the group between observation and reorder", got.ItemID)
			}
			knownIndex := observed.Children[observedIndex].Placement.Index.ValueInt64()
			var checkpointErr error
			updated, err := updateGroupGraphItem(ctx, cl, m.Collection.ValueString(), observed.Name, got.ItemID, got.Placement, m.Children[i].Placement, func(current bridgePlacementModel) {
				if current.Index.ValueInt64() != knownIndex {
					applyObservedGroupIndexMove(observed.Children, observedIndex, knownIndex, current.Index.ValueInt64())
					knownIndex = current.Index.ValueInt64()
				}
				observed.Children[observedIndex].Placement = current
				if mergeErr := mergeGroupGraphObservation(actual, *m, observed); mergeErr != nil {
					checkpointErr = mergeErr
					return
				}
				checkpointErr = checkpoint()
			})
			if checkpointErr != nil {
				return observed, checkpointErr
			}
			if err != nil {
				return observed, err
			}
			observed.Children[observedIndex].Placement = updated
			childWrites = true
		}
	}
	if childWrites {
		observed, err = groupGraphFrameBarrier(ctx, cl, *m)
		if err != nil {
			return observed, err
		}
		if err := mergeGroupGraphObservation(actual, *m, observed); err != nil {
			return observed, err
		}
		if err := checkpoint(); err != nil {
			return observed, err
		}
	}
	if err := requireExactGroupGraphChildren(observed, m.Children, wantWidth, wantHeight); err != nil {
		return observed, err
	}
	if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
		return observed, fmt.Errorf("pre-parent roster: %w", err)
	}

	siblings, countErr := containerItemCount(ctx, cl, m.SceneName.ValueString(), false)
	if countErr != nil {
		return observed, countErr
	}
	// parentWant is the declared parent placement clamped to the scene's current size;
	// state keeps the declared index (see clampCreateIndex).
	parentWant := *m.ParentPlacement
	parentWant.Index = types.Int64Value(clampCreateIndex(m.ParentPlacement.Index.ValueInt64(), siblings-1))
	if diff := bridgePlacementDifference(observed.Placement, parentWant); diff != "" {
		if _, err := bridgeCapabilities(ctx, cl, m.Collection.ValueString(), "GetGroup", true); err != nil {
			return observed, err
		}
		var checkpointErr error
		updated, err := updateGroupGraphItem(ctx, cl, m.Collection.ValueString(), m.SceneName.ValueString(), observed.ItemID, observed.Placement, &parentWant, func(current bridgePlacementModel) {
			observed.Placement = current
			if mergeErr := mergeGroupGraphObservation(actual, *m, observed); mergeErr != nil {
				checkpointErr = mergeErr
				return
			}
			checkpointErr = checkpoint()
		})
		if checkpointErr != nil {
			return observed, checkpointErr
		}
		if err != nil {
			return observed, err
		}
		observed.Placement = updated
		observed, err = groupGraphFrameBarrier(ctx, cl, *m)
		if err != nil {
			return observed, err
		}
		if err := mergeGroupGraphObservation(actual, *m, observed); err != nil {
			return observed, err
		}
		if err := checkpoint(); err != nil {
			return observed, err
		}
	}
	if diff := bridgePlacementDifference(observed.Placement, parentWant); diff != "" {
		return observed, fmt.Errorf("final parent placement mismatch: %s", diff)
	}
	if err := requireExactGroupGraphChildren(observed, m.Children, wantWidth, wantHeight); err != nil {
		return observed, err
	}
	if err := validateGroupGraphRoster(ctx, cl, observed, m.Children); err != nil {
		return observed, err
	}
	return observed, nil
}

func groupGraphObservedChildIndex(children []bridgeChildObservation, itemID int64) int {
	for i := range children {
		if children[i].ItemID == itemID {
			return i
		}
	}
	return -1
}

func applyObservedGroupIndexMove(children []bridgeChildObservation, moved int, oldIndex, newIndex int64) {
	for i := range children {
		if i == moved {
			continue
		}
		index := children[i].Placement.Index.ValueInt64()
		if oldIndex > newIndex && index >= newIndex && index < oldIndex {
			children[i].Placement.Index = types.Int64Value(index + 1)
		}
		if oldIndex < newIndex && index > oldIndex && index <= newIndex {
			children[i].Placement.Index = types.Int64Value(index - 1)
		}
	}
}

func updateGroupGraphItem(ctx context.Context, cl requester, collection, sceneName string, itemID int64, observed bridgePlacementModel, want *bridgePlacementModel, checkpoint func(bridgePlacementModel)) (bridgePlacementModel, error) {
	transformRequest, err := want.coreWebSocketTransformRequest()
	if err != nil {
		return observed, err
	}
	target := nativeBridgePlacement(*want)
	type setter struct {
		needed bool
		typ    string
		data   map[string]any
		apply  func()
	}
	transformOnly := observed
	transformOnly.Enabled, transformOnly.Locked, transformOnly.Index = want.Enabled, want.Locked, want.Index
	setters := []setter{
		{observed.Enabled.ValueBool() != want.Enabled.ValueBool(), "SetSceneItemEnabled", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemEnabled": want.Enabled.ValueBool()}, func() { observed.Enabled = target.Enabled }},
		{observed.Locked.ValueBool() != want.Locked.ValueBool(), "SetSceneItemLocked", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemLocked": want.Locked.ValueBool()}, func() { observed.Locked = target.Locked }},
		{bridgePlacementDifference(transformOnly, *want) != "", "SetSceneItemTransform", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemTransform": transformRequest}, func() { observed.Transform = target.Transform }},
		{observed.Index.ValueInt64() != want.Index.ValueInt64(), "SetSceneItemIndex", map[string]any{"sceneName": sceneName, "sceneItemId": itemID, "sceneItemIndex": want.Index.ValueInt64()}, func() { observed.Index = target.Index }},
	}
	for _, setter := range setters {
		if !setter.needed {
			continue
		}
		if _, err := bridgeCapabilities(ctx, cl, collection, "GetGroup", true); err != nil {
			return observed, err
		}
		_, callErr := mutation(ctx, cl, collection, setter.typ, setter.data)
		if callErr == nil || isAuthoritativeNativeInterruption(callErr) {
			setter.apply()
			checkpoint(observed)
		}
		if callErr != nil {
			return observed, callErr
		}
	}
	return observed, nil
}

func proveGroupGraphZeroAnchor(observed bridgeGroupObservation, children []groupGraphChildModel) (int64, int64, int, error) {
	if len(children) == 0 || len(observed.Children) != len(children) {
		return 0, 0, -1, fmt.Errorf("exact complete roster is required for transformed bounds")
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	anchor := -1
	for i := range children {
		got, present, err := groupGraphChildByBinding(observed.Children, children[i].SourceUUID, children[i].ItemID)
		if err != nil || !present || children[i].Placement == nil || children[i].Placement.Transform == nil {
			return 0, 0, -1, fmt.Errorf("children[%d] transformed bounds are unavailable", i)
		}
		t := children[i].Placement.Transform
		if t.Rotation.ValueFloat64() != 0 || t.Alignment.ValueInt64() != 5 || t.BoundsType.ValueString() != "OBS_BOUNDS_NONE" || t.CropToBounds.ValueBool() || t.CropLeft.ValueInt64() != 0 || t.CropTop.ValueInt64() != 0 || t.CropRight.ValueInt64() != 0 || t.CropBottom.ValueInt64() != 0 || t.ScaleX.ValueFloat64() <= 0 || t.ScaleY.ValueFloat64() <= 0 {
			return 0, 0, -1, fmt.Errorf("children[%d] uses unsupported rotation/crop/bounds/alignment for exact transformed bounds", i)
		}
		x, y := float64(float32(t.X.ValueFloat64())), float64(float32(t.Y.ValueFloat64()))
		w := float64(got.Width) * float64(float32(t.ScaleX.ValueFloat64()))
		h := float64(got.Height) * float64(float32(t.ScaleY.ValueFloat64()))
		minX, minY, maxX, maxY = math.Min(minX, x), math.Min(minY, y), math.Max(maxX, x+w), math.Max(maxY, y+h)
		geometryOnly := got.Placement
		geometryOnly.Enabled, geometryOnly.Locked, geometryOnly.Index = children[i].Placement.Enabled, children[i].Placement.Locked, children[i].Placement.Index
		if x == 0 && y == 0 && bridgePlacementDifference(geometryOnly, *children[i].Placement) == "" {
			anchor = i
		}
	}
	if minX != 0 || minY != 0 || anchor < 0 {
		return 0, 0, -1, fmt.Errorf("managed transformed minimum must be exact (0,0) with an already-correct managed zero anchor; got (%v,%v)", minX, minY)
	}
	width, height := maxX-minX, maxY-minY
	if math.Trunc(width) != width || math.Trunc(height) != height || width < 0 || height < 0 || width > math.MaxInt64 || height > math.MaxInt64 {
		return 0, 0, -1, fmt.Errorf("group dimensions are not exactly representable integers: %vx%v", width, height)
	}
	return int64(width), int64(height), anchor, nil
}

func requireExactGroupGraphChildren(observed bridgeGroupObservation, children []groupGraphChildModel, width, height int64) error {
	if observed.Width != width || observed.Height != height {
		return fmt.Errorf("group dimensions=%dx%d want %dx%d", observed.Width, observed.Height, width, height)
	}
	if len(observed.Children) != len(children) {
		return fmt.Errorf("group roster length=%d want %d", len(observed.Children), len(children))
	}
	for i := range children {
		if observed.Children[i].UUID != children[i].SourceUUID.ValueString() || observed.Children[i].ItemID != children[i].ItemID.ValueInt64() {
			return fmt.Errorf("children[%d] observed array order is %s/%d, want stable key %q binding %s/%d", i, observed.Children[i].UUID, observed.Children[i].ItemID, children[i].StableKey.ValueString(), children[i].SourceUUID.ValueString(), children[i].ItemID.ValueInt64())
		}
		got, present, err := groupGraphChildByBinding(observed.Children, children[i].SourceUUID, children[i].ItemID)
		if err != nil || !present {
			return fmt.Errorf("children[%d] exact binding unavailable: %v", i, err)
		}
		if diff := bridgePlacementDifference(got.Placement, *children[i].Placement); diff != "" {
			return fmt.Errorf("children[%d] placement mismatch: %s", i, diff)
		}
	}
	return nil
}

func setGroupGraphObserved(m *groupGraphModel, observed bridgeGroupObservation, complete bool) {
	m.SceneName, m.Name = types.StringValue(observed.SceneName), types.StringValue(observed.Name)
	m.GroupUUID, m.ParentItemID = types.StringValue(observed.UUID), types.Int64Value(observed.ItemID)
	m.ID = types.StringValue(groupGraphID(m.Collection.ValueString(), observed.SceneUUID, observed.UUID))
	m.ParentPresent = types.BoolValue(true)
	m.ObservedComplete = types.BoolValue(complete)
	m.ObservedGroupWidth, m.ObservedGroupHeight = types.Int64Value(observed.Width), types.Int64Value(observed.Height)
	m.ObservedParent = &groupGraphObservedParentModel{Present: types.BoolValue(true), Placement: &observed.Placement}
	byID := map[int64]groupGraphChildModel{}
	for _, child := range m.Children {
		if !child.ItemID.IsNull() && !child.ItemID.IsUnknown() {
			byID[child.ItemID.ValueInt64()] = child
		}
	}
	m.ObservedChildren = nil
	for _, got := range observed.Children {
		state := byID[got.ItemID]
		m.ObservedChildren = append(m.ObservedChildren, groupGraphObservedChildModel{StableKey: state.StableKey, Name: types.StringValue(got.Name), Kind: types.StringValue(got.Kind), SourceUUID: types.StringValue(got.UUID), ItemID: types.Int64Value(got.ItemID), Present: types.BoolValue(true), Width: types.Int64Value(got.Width), Height: types.Int64Value(got.Height), Placement: &got.Placement})
	}
	m.RepairNonce = types.StringValue(groupGraphRepairNonce(*m))
}

func groupGraphRepairNonce(m groupGraphModel) string {
	type binding struct {
		Key, UUID string
		Item      int64
		Present   bool
	}
	v := struct {
		Parent   int64
		Present  bool
		Children []binding
	}{Parent: m.ParentItemID.ValueInt64(), Present: m.ParentPresent.ValueBool()}
	for _, child := range m.Children {
		v.Children = append(v.Children, binding{Key: child.StableKey.ValueString(), UUID: child.SourceUUID.ValueString(), Item: child.ItemID.ValueInt64(), Present: child.Present.ValueBool()})
	}
	raw, _ := json.Marshal(v)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

var errGroupGraphGone = errors.New("managed group graph is authoritatively absent")

var _ resource.ResourceWithConfigure = (*groupGraphResource)(nil)
var _ resource.ResourceWithValidateConfig = (*groupGraphResource)(nil)
var _ resource.ResourceWithModifyPlan = (*groupGraphResource)(nil)
var _ resource.ResourceWithImportState = (*groupGraphResource)(nil)
