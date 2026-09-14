package tfprovider

import (
	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"

	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// sceneItemModel is obs_scene_item: a placement of an existing source in a scene or,
// with parent_kind = "group", directly in a group.
type sceneItemModel struct {
	ID             types.String    `tfsdk:"id"`
	Collection     types.String    `tfsdk:"collection"`
	SceneName      types.String    `tfsdk:"scene_name"`
	SceneUUID      types.String    `tfsdk:"scene_uuid"`
	SourceName     types.String    `tfsdk:"source_name"`
	SourceUUID     types.String    `tfsdk:"source_uuid"`
	ItemID         types.Int64     `tfsdk:"item_id"`
	Enabled        types.Bool      `tfsdk:"enabled"`
	Index          types.Int64     `tfsdk:"index"`
	Transform      *transformModel `tfsdk:"transform"`
	ParentKind     types.String    `tfsdk:"parent_kind"`
	AllowDuplicate types.Bool      `tfsdk:"allow_duplicate"`
}

func normalizeSceneItemDefaults(m *sceneItemModel) {
	if m.ParentKind.IsNull() {
		m.ParentKind = types.StringValue("scene")
	}
	if m.AllowDuplicate.IsNull() {
		m.AllowDuplicate = types.BoolValue(false)
	}
}

// The core SetSceneItem handlers explicitly allow groups; GetSceneItemList,
// CreateSceneItem and RemoveSceneItem exclude them. Lists use the group endpoint,
// and creation/removal go through the existing guarded native bridge.
type sceneItemContainerRequester struct {
	requester
	group bool
}

func (q sceneItemContainerRequester) preflightScope() *preflightScope {
	if p, ok := q.requester.(preflightScoper); ok {
		return p.preflightScope()
	}
	return nil
}

func (r sceneItemContainerRequester) Batch(ctx context.Context, items []ws.BatchItem, chunk int, halt bool) ([]ws.BatchResult, error) {
	b, ok := r.requester.(batchRequester)
	if !ok {
		return nil, errNoBatch
	}
	if r.group {
		rewritten := make([]ws.BatchItem, len(items))
		for i, it := range items {
			if it.Type == "GetSceneItemList" {
				it.Type = "GetGroupSceneItemList"
			}
			rewritten[i] = it
		}
		items = rewritten
	}
	return b.Batch(ctx, items, chunk, halt)
}

func (r sceneItemContainerRequester) Req(ctx context.Context, kind string, data map[string]any) (map[string]any, error) {
	if r.group && kind == "GetSceneItemList" {
		kind = "GetGroupSceneItemList"
	}
	return r.requester.Req(ctx, kind, data)
}

type sceneItemContainerObservation struct {
	sceneObservation
	Parent       sceneObservation
	ParentItemID int64
}

func sceneItemContainer(ctx context.Context, cl requester, kind, uuid string) (sceneItemContainerObservation, bool, error) {
	if kind == "scene" {
		s, ok, err := sceneByUUID(ctx, cl, uuid)
		return sceneItemContainerObservation{sceneObservation: s}, ok, err
	}
	if kind != "group" {
		return sceneItemContainerObservation{}, false, fmt.Errorf("parent_kind must be scene or group")
	}
	// GetGroupList identifies native group names, while scene rosters supply UUIDs
	// and the exact parent placement required by the bridge. A UUID match is never
	// enough on its own to reinterpret a scene or ordinary input as a group.
	raw, err := request(ctx, cl, "GetGroupList", nil)
	if err != nil {
		return sceneItemContainerObservation{}, false, err
	}
	names, err := requiredSlice(raw, "groups")
	if err != nil {
		return sceneItemContainerObservation{}, false, err
	}
	groups := map[string]bool{}
	for _, value := range names {
		name, ok := value.(string)
		if !ok || name == "" {
			return sceneItemContainerObservation{}, false, fmt.Errorf("GetGroupList returned invalid group name")
		}
		groups[name] = true
	}
	all, err := scenes(ctx, cl)
	if err != nil {
		return sceneItemContainerObservation{}, false, err
	}
	var found sceneItemContainerObservation
	count := 0
	for _, parent := range all {
		items, err := sceneItems(ctx, cl, parent.Name)
		if err != nil {
			return found, false, err
		}
		for _, item := range items {
			if item.SourceUUID != uuid {
				continue
			}
			if !groups[item.SourceName] {
				return found, false, fmt.Errorf("parent UUID %s is not a native group", uuid)
			}
			count++
			found = sceneItemContainerObservation{sceneObservation: sceneObservation{Name: item.SourceName, UUID: uuid}, Parent: parent, ParentItemID: item.ItemID}
		}
	}
	if count > 1 {
		return found, false, fmt.Errorf("group UUID %s has multiple parent placements; exact single parent is required", uuid)
	}
	// Nested groups cannot use the bridge's top-level-parent contract. Detect them
	// instead of treating an existing nested group as a deleted resource.
	for name := range groups {
		items, err := sceneItems(ctx, sceneItemContainerRequester{requester: cl, group: true}, name)
		if err != nil {
			return found, false, err
		}
		for _, item := range items {
			if item.SourceUUID == uuid {
				return found, false, fmt.Errorf("group UUID %s is nested inside group %q; nested group containers are unsupported", uuid, name)
			}
		}
	}
	return found, count == 1, nil
}

func createContainerSceneItem(ctx context.Context, cl requester, container sceneItemContainerObservation, m sceneItemModel) (int64, error) {
	if m.ParentKind.ValueString() == "scene" {
		x, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateSceneItem", map[string]any{"sceneUuid": container.UUID, "sourceUuid": m.SourceUUID.ValueString(), "sceneItemEnabled": m.Enabled.ValueBool()})
		if err != nil && !isAuthoritativeNativeInterruption(err) {
			return 0, err
		}
		return int64Value(x["sceneItemId"]), err
	}
	// AddSourceToGroup currently accepts native inputs only. Refuse scene/group
	// sources before any mutation rather than falling back to CreateInput.
	knownInputs, err := inputs(ctx, cl)
	if err != nil {
		return 0, err
	}
	exists := false
	for _, source := range knownInputs {
		if source.UUID == m.SourceUUID.ValueString() && source.Name == m.SourceName.ValueString() {
			exists = true
		}
	}
	if !exists {
		return 0, fmt.Errorf("group placements require an existing input with exact name/UUID")
	}
	children, err := containerItemCount(ctx, cl, container.Name, true)
	if err != nil {
		return 0, err
	}
	mm := m
	mm.Index = types.Int64Value(clampCreateIndex(m.Index.ValueInt64(), children))
	placement, err := sceneItemBridgePlacement(mm)
	if err != nil {
		return 0, err
	}
	envelope, callErr := bridgeMutation(ctx, cl, m.Collection.ValueString(), "AddSourceToGroup", bridgeOperationID("create-group-scene-item", m.SourceName.ValueString()), map[string]any{
		"scene":     bridgeIdentity(container.Parent.UUID, container.Parent.Name, 0),
		"group":     bridgeIdentity(container.UUID, container.Name, container.ParentItemID),
		"source":    bridgeIdentity(m.SourceUUID.ValueString(), m.SourceName.ValueString(), 0),
		"placement": placement,
	})
	child, resultErr := trustedChildMutationResult(envelope, callErr)
	if resultErr != nil {
		if callErr != nil {
			return 0, callErr
		}
		return 0, resultErr
	}
	if child.UUID != m.SourceUUID.ValueString() || child.Name != m.SourceName.ValueString() {
		return 0, fmt.Errorf("native group placement returned rebound source identity")
	}
	if callErr != nil {
		return child.ItemID, callErr
	}
	if envelope.Outcome != "completed" {
		return child.ItemID, fmt.Errorf("bridge AddSourceToGroup %s (%s): %s", envelope.Outcome, envelope.Code, envelope.Message)
	}
	return child.ItemID, nil
}

func sceneItemBridgePlacement(m sceneItemModel) (map[string]any, error) {
	if m.Transform == nil {
		return nil, fmt.Errorf("transform is required")
	}
	transform := m.Transform.native()
	// Core WebSocket omits unused zero bounds; the bridge requires all raw fields.
	if _, ok := transform["boundsWidth"]; !ok {
		transform["boundsWidth"] = float64(0)
	}
	if _, ok := transform["boundsHeight"]; !ok {
		transform["boundsHeight"] = float64(0)
	}
	if _, ok := transform["cropToBounds"]; !ok {
		transform["cropToBounds"] = false
	}
	placement, err := bridgePlacementFromNative(map[string]any{"sceneItemTransform": transform, "sceneItemEnabled": m.Enabled.ValueBool(), "sceneItemLocked": false, "sceneItemIndex": m.Index.ValueInt64()})
	if err != nil {
		return nil, err
	}
	return placement.request()
}

func containerSceneItemID(collection, container, source string, item int64, kind string) string {
	id := sceneItemID(collection, container, source, item)
	if kind == "group" {
		id += "|group"
	}
	return id
}

func deleteContainerSceneItem(ctx context.Context, cl requester, container sceneItemContainerObservation, m sceneItemModel, it itemObservation) error {
	if m.ParentKind.ValueString() == "scene" {
		_, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveSceneItem", map[string]any{"sceneUuid": container.UUID, "sceneItemId": it.ItemID})
		return err
	}
	data := map[string]any{
		"scene": bridgeIdentity(container.Parent.UUID, container.Parent.Name, 0),
		"group": bridgeIdentity(container.UUID, container.Name, container.ParentItemID),
	}
	envelope, err := bridgeRead(ctx, cl, "GetGroup", data)
	if err != nil {
		return err
	}
	if envelope.Outcome != "completed" {
		return fmt.Errorf("group unavailable for exact placement removal: %s/%s", envelope.Outcome, envelope.Code)
	}
	group, err := bridgeGroupFromResult(envelope.Result)
	if err != nil {
		return err
	}
	if group.UUID != container.UUID || group.Name != container.Name || group.SceneUUID != container.Parent.UUID || group.SceneName != container.Parent.Name || group.ItemID != container.ParentItemID {
		return fmt.Errorf("group identity rebound before placement removal")
	}
	child, exists, err := exactBridgeChild(group.Children, it.ItemID, m.SourceUUID.ValueString())
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if child.Name != it.SourceName {
		return fmt.Errorf("source renamed before placement removal")
	}
	expected, err := child.Placement.request()
	if err != nil {
		return err
	}
	data["child"] = bridgeIdentity(child.UUID, child.Name, child.ItemID)
	data["expectedPlacement"] = expected
	envelope, err = bridgeMutation(ctx, cl, m.Collection.ValueString(), "RemoveGroupChildPlacement", bridgeOperationID("remove-group-scene-item", it.SourceName), data)
	if err != nil {
		return err
	}
	if envelope.Outcome != "completed" {
		return fmt.Errorf("bridge RemoveGroupChildPlacement %s (%s): %s", envelope.Outcome, envelope.Code, envelope.Message)
	}
	return nil
}
