package tfprovider

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// sceneContentsResource exclusively manages placements, never source lifetimes.
// Read writes the real roster into item_ids so normal Terraform planning exposes
// added placements and ordering drift. Delete merely releases that contract.
type sceneContentsResource struct{ client *client }
type sceneContentsModel struct {
	ID          types.String `tfsdk:"id"`
	Collection  types.String `tfsdk:"collection"`
	SceneName   types.String `tfsdk:"scene_name"`
	SceneUUID   types.String `tfsdk:"scene_uuid"`
	ParentKind  types.String `tfsdk:"parent_kind"`
	ItemIDs     types.List   `tfsdk:"item_ids"`
	ItemSources types.Map    `tfsdk:"item_sources"`
}

func NewSceneContentsResource() resource.Resource { return &sceneContentsResource{} }
func (r *sceneContentsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scene_contents"
}
func (r *sceneContentsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *sceneContentsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Exclusive ordered placement membership for one Terraform-owned scene or group. Apply removes undeclared placements; shared sources in other containers survive. Use exactly one contents resource per container. Destroy releases the contract without deleting placements.", Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true), "scene_uuid": immutableString(true), "scene_name": immutableString(true),
		"parent_kind":  schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("scene"), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{stringvalidator.OneOf("scene", "group")}},
		"item_ids":     schema.ListAttribute{Required: true, ElementType: types.Int64Type, Description: "Complete desired list of native placement IDs, in ascending OBS sceneItemIndex order (bottom to top). Reference every owned placement, including group and nested-scene placements. An empty list removes all placements in this container."},
		"item_sources": schema.MapAttribute{Computed: true, ElementType: types.StringType, Description: "Observed placement ID to source UUID identities, used to reject identity rebinding."},
	}}
}
func (r *sceneContentsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m sceneContentsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString()}, func(cl requester) error { return reconcileSceneContents(ctx, cl, &m, nil) })
	if err != nil {
		resp.Diagnostics.AddError("Reconcile OBS scene contents failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneContentsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state sceneContentsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	old, err := sceneContentsSources(ctx, state.ItemSources)
	if err == nil {
		err = r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString()}, func(cl requester) error { return reconcileSceneContents(ctx, cl, &m, old) })
	}
	if err != nil {
		resp.Diagnostics.AddError("Reconcile OBS scene contents failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneContentsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m sceneContentsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	old, err := sceneContentsSources(ctx, m.ItemSources)
	absent := false
	if err == nil {
		err = r.client.withReadRequester(ctx, func(cl requester) error {
			_, items, exists, err := observeSceneContents(ctx, cl, m)
			if err != nil {
				return err
			}
			if !exists {
				absent = true
				return nil
			}
			if err := sceneContentsCheckSources(items, old); err != nil {
				return err
			}
			return recordSceneContents(ctx, &m, items)
		})
	}
	if err != nil {
		resp.Diagnostics.AddError("Read OBS scene contents failed", err.Error())
		return
	}
	if absent {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneContentsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Releasing exclusivity cannot delete resources independently owned by another
	// Terraform resource, nor convert a refreshed roster into deletion authority.
}
func sceneContentsSources(ctx context.Context, value types.Map) (map[string]string, error) {
	out := map[string]string{}
	if value.IsNull() || value.IsUnknown() {
		return out, nil
	}
	if diags := value.ElementsAs(ctx, &out, false); diags.HasError() {
		return nil, fmt.Errorf("invalid recorded item source identities: %s", diags)
	}
	return out, nil
}
func sceneContentsIDs(ctx context.Context, value types.List) ([]int64, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, fmt.Errorf("item_ids must be fully known before reconciling contents")
	}
	var ids []int64
	if diags := value.ElementsAs(ctx, &ids, false); diags.HasError() {
		return nil, fmt.Errorf("item_ids must contain known integer IDs: %s", diags)
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, fmt.Errorf("item_ids contains invalid or duplicate ID %d", id)
		}
		seen[id] = true
	}
	return ids, nil
}
func observeSceneContents(ctx context.Context, base requester, m sceneContentsModel) (sceneItemContainerObservation, []itemObservation, bool, error) {
	var empty sceneItemContainerObservation
	if m.Collection.IsUnknown() || m.Collection.IsNull() || m.SceneName.IsUnknown() || m.SceneName.IsNull() || m.SceneUUID.IsUnknown() || m.SceneUUID.IsNull() {
		return empty, nil, false, fmt.Errorf("collection and exact container name/UUID must be known")
	}
	if _, err := ensureCollection(ctx, base, m.Collection.ValueString()); err != nil {
		return empty, nil, false, err
	}
	kind := m.ParentKind.ValueString()
	if kind == "" {
		kind = "scene"
	}
	container, exists, err := sceneItemContainer(ctx, base, kind, m.SceneUUID.ValueString())
	if err != nil || !exists {
		return container, nil, exists, err
	}
	if container.Name != m.SceneName.ValueString() {
		return container, nil, false, fmt.Errorf("contents container renamed/rebound from %q to %q", m.SceneName.ValueString(), container.Name)
	}
	cl := sceneItemContainerRequester{requester: base, group: kind == "group"}
	// Validate raw identity/index fields before the shared parser's scalar defaults.
	raw, err := request(ctx, cl, "GetSceneItemList", map[string]any{"sceneName": container.Name})
	if err != nil {
		return container, nil, false, err
	}
	values, err := requiredSlice(raw, "sceneItems")
	if err != nil {
		return container, nil, false, err
	}
	items := make([]itemObservation, 0, len(values))
	ids := map[int64]bool{}
	indexes := map[int64]bool{}
	for _, value := range values {
		v := mapValue(value)
		id := int64Value(v["sceneItemId"])
		index := int64Value(v["sceneItemIndex"])
		rawID, idOK := numberValue(v["sceneItemId"])
		rawIndex, indexOK := numberValue(v["sceneItemIndex"])
		if !idOK || !indexOK || math.IsNaN(rawID) || math.IsInf(rawID, 0) || math.IsNaN(rawIndex) || math.IsInf(rawIndex, 0) || rawID != float64(id) || rawIndex != float64(index) || id <= 0 || ids[id] || index < 0 || index >= int64(len(values)) || indexes[index] || stringValue(v["sourceUuid"]) == "" || stringValue(v["sourceName"]) == "" || mapValue(v["sceneItemTransform"]) == nil {
			return container, nil, false, fmt.Errorf("container roster has incomplete or duplicate placement identity/index")
		}
		ids[id] = true
		indexes[index] = true
		items = append(items, itemObservation{SceneName: container.Name, SceneUUID: container.UUID, ItemID: id, Index: index, SourceUUID: stringValue(v["sourceUuid"]), SourceName: stringValue(v["sourceName"]), Transform: transformFromNative(mapValue(v["sceneItemTransform"])), Enabled: boolValue(v["sceneItemEnabled"])})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Index < items[j].Index })
	return container, items, true, nil
}
func sceneContentsCheckSources(items []itemObservation, old map[string]string) error {
	for _, item := range items {
		if uuid, ok := old[strconv.FormatInt(item.ItemID, 10)]; ok && uuid != item.SourceUUID {
			return fmt.Errorf("placement %d rebound from source %s to %s", item.ItemID, uuid, item.SourceUUID)
		}
	}
	return nil
}
func recordSceneContents(ctx context.Context, m *sceneContentsModel, items []itemObservation) error {
	ids := make([]int64, 0, len(items))
	sources := map[string]string{}
	for _, item := range items {
		ids = append(ids, item.ItemID)
		sources[strconv.FormatInt(item.ItemID, 10)] = item.SourceUUID
	}
	value, diags := types.ListValueFrom(ctx, types.Int64Type, ids)
	if diags.HasError() {
		return fmt.Errorf("record contents: %s", diags)
	}
	m.ItemIDs = value
	src, diags := types.MapValueFrom(ctx, types.StringType, sources)
	if diags.HasError() {
		return fmt.Errorf("record contents sources: %s", diags)
	}
	m.ItemSources = src
	m.ID = types.StringValue(m.Collection.ValueString() + "|" + m.SceneUUID.ValueString() + "|contents")
	return nil
}
func reconcileSceneContents(ctx context.Context, cl requester, m *sceneContentsModel, prior map[string]string) error {
	desired, err := sceneContentsIDs(ctx, m.ItemIDs)
	if err != nil {
		return err
	}
	if m.ParentKind.IsNull() {
		m.ParentKind = types.StringValue("scene")
	}
	container, items, exists, err := observeSceneContents(ctx, cl, *m)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("contents container is absent; create its scene/group before reconciliation")
	}
	if err = sceneContentsCheckSources(items, prior); err != nil {
		return err
	}
	expected := map[string]string{}
	present := map[int64]bool{}
	wanted := map[int64]bool{}
	for _, it := range items {
		expected[strconv.FormatInt(it.ItemID, 10)] = it.SourceUUID
		present[it.ItemID] = true
	}
	for _, id := range desired {
		if !present[id] {
			return fmt.Errorf("desired placement %d is absent; refusing partial prune", id)
		}
		wanted[id] = true
	}
	// Every write gets a new exact-container/roster observation. Fail closed if a
	// concurrent edit changes membership or identities while the prune is running.
	refresh := func() error {
		c, now, ok, e := observeSceneContents(ctx, cl, *m)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("contents container disappeared during reconciliation")
		}
		if c.UUID != container.UUID || c.Parent.UUID != container.Parent.UUID || c.ParentItemID != container.ParentItemID {
			return fmt.Errorf("contents container placement rebound")
		}
		actual := map[string]string{}
		for _, it := range now {
			actual[strconv.FormatInt(it.ItemID, 10)] = it.SourceUUID
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("container membership changed during reconciliation; refresh and retry")
		}
		items = now
		return nil
	}
	// One fresh observation before the prune, one before the reorder and one after it.
	// Between them the exclusive client lock keeps this provider's own writes out, and
	// the final observation still fails closed on any external edit: membership is
	// compared exactly and the order must match the declaration item for item. The
	// previous per-write re-observation pulled the whole container list before every
	// SetSceneItemIndex, which is quadratic in container size once siblings are created
	// in arbitrary order.
	if err = refresh(); err != nil {
		return err
	}
	for _, candidate := range append([]itemObservation(nil), items...) {
		if wanted[candidate.ItemID] {
			continue
		}
		model := groupAwareSceneItemModel{Collection: m.Collection, ParentKind: m.ParentKind, SourceUUID: types.StringValue(candidate.SourceUUID)}
		if err = deleteContainerSceneItem(ctx, cl, container, model, candidate); err != nil {
			return err
		}
		delete(expected, strconv.FormatInt(candidate.ItemID, 10))
	}
	if err = refresh(); err != nil {
		return err
	}
	// SetSceneItemIndex moves one item to a position; simulate each move on the local
	// roster so later targets account for earlier shifts without re-reading OBS.
	order := make([]int64, 0, len(items))
	for _, it := range items {
		order = append(order, it.ItemID)
	}
	for index, id := range desired {
		if index < len(order) && order[index] == id {
			continue
		}
		if _, err = mutation(ctx, cl, m.Collection.ValueString(), "SetSceneItemIndex", map[string]any{"sceneUuid": container.UUID, "sceneItemId": id, "sceneItemIndex": int64(index)}); err != nil {
			return err
		}
		from := -1
		for i, x := range order {
			if x == id {
				from = i
				break
			}
		}
		if from >= 0 {
			order = append(order[:from], order[from+1:]...)
		}
		if index > len(order) {
			index = len(order)
		}
		order = append(order[:index], append([]int64{id}, order[index:]...)...)
	}
	if err = refresh(); err != nil {
		return err
	}
	for i, id := range desired {
		if items[i].ItemID != id {
			return fmt.Errorf("contents order did not converge at index %d", i)
		}
	}
	return recordSceneContents(ctx, m, items)
}
