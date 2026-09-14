package tfprovider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type sceneItemResource struct{ client *client }

func NewSceneItemResource() resource.Resource { return &sceneItemResource{} }
func (r *sceneItemResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scene_item"
}
func (r *sceneItemResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *sceneItemResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true), "scene_name": schema.StringAttribute{Required: true}, "scene_uuid": immutableString(true),
		"source_name": schema.StringAttribute{Required: true}, "source_uuid": immutableString(true), "item_id": identityInt64(),
		"parent_kind":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("scene"), Validators: []validator.String{stringvalidator.OneOf("scene", "group")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Container type. For group, scene_name and scene_uuid identify the existing native group; it must have one direct parent scene placement."},
		"allow_duplicate": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Allow another placement of the same source in this container. Each resource still owns its exact item ID; source data is shared."},
		"enabled":         schema.BoolAttribute{Required: true}, "index": schema.Int64Attribute{Required: true}, "transform": transformSchema(),
	}}
}

func (r *sceneItemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m groupAwareSceneItemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString()}, func(base requester) error {
		normalizeSceneItemDefaults(&m)
		cl := sceneItemContainerRequester{requester: base, group: m.ParentKind.ValueString() == "group"}
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneItemContainer(ctx, base, m.ParentKind.ValueString(), m.SceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok || scene.Name != m.SceneName.ValueString() {
			return fmt.Errorf("scene identity/name mismatch")
		}
		ok, err = existingSourceIdentity(ctx, base, m.SourceName.ValueString(), m.SourceUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("source identity/name mismatch")
		}
		items, err := sceneItems(ctx, cl, scene.Name)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.SourceUUID == m.SourceUUID.ValueString() && !m.AllowDuplicate.ValueBool() {
				return fmt.Errorf("scene already has a placement of source %s; import it instead", m.SourceUUID.ValueString())
			}
		}
		if m.Transform == nil {
			return fmt.Errorf("transform is required")
		}
		if err := validateTransformBounds(*m.Transform); err != nil {
			return err
		}
		id, createErr := createContainerSceneItem(ctx, base, scene, m)
		if id <= 0 {
			if createErr != nil {
				return createErr
			}
			return fmt.Errorf("scene item creation acknowledged without item id")
		}
		m.ItemID = types.Int64Value(id)
		m.ID = types.StringValue(containerSceneItemID(m.Collection.ValueString(), scene.UUID, m.SourceUUID.ValueString(), id, m.ParentKind.ValueString()))
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		if createErr != nil {
			return createErr
		}
		siblings, err := containerItemCount(ctx, cl, scene.Name, false)
		if err != nil {
			return err
		}
		index := clampCreateIndex(m.Index.ValueInt64(), siblings-1)
		if err := setItem(ctx, cl, m.Collection.ValueString(), scene.Name, id, m.Enabled.ValueBool(), index, *m.Transform); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, m.SourceUUID.ValueString(), m.Enabled.ValueBool(), index, *m.Transform, 40, 100*time.Millisecond); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS scene item failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *sceneItemResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m groupAwareSceneItemModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(base requester) error {
		normalizeSceneItemDefaults(&m)
		cl := sceneItemContainerRequester{requester: base, group: m.ParentKind.ValueString() == "group"}
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneItemContainer(ctx, base, m.ParentKind.ValueString(), m.SceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			resp.State.RemoveResource(ctx)
			return nil
		}
		if !m.SceneName.IsNull() && !m.SceneName.IsUnknown() && scene.Name != m.SceneName.ValueString() {
			return fmt.Errorf("scene UUID renamed from %q to %q; refusing silent adoption", m.SceneName.ValueString(), scene.Name)
		}
		m.SceneName = types.StringValue(scene.Name)
		it, ok, err := itemByID(ctx, cl, scene.Name, m.ItemID.ValueInt64())
		if err != nil {
			return err
		}
		if !ok {
			resp.State.RemoveResource(ctx)
			return nil
		}
		if it.SourceUUID != m.SourceUUID.ValueString() || (!m.SourceName.IsNull() && !m.SourceName.IsUnknown() && it.SourceName != m.SourceName.ValueString()) {
			return fmt.Errorf("scene item identity rebound to %s/%s", it.SourceName, it.SourceUUID)
		}
		m.SourceName = types.StringValue(it.SourceName)
		m.Enabled = types.BoolValue(it.Enabled)
		m.Index = types.Int64Value(it.Index)
		m.Transform = retainAuthoredTransform(it.Transform, m.Transform)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Read OBS scene item failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *sceneItemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupAwareSceneItemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.SceneUUID.ValueString()}, func(base requester) error {
		normalizeSceneItemDefaults(&plan)
		cl := sceneItemContainerRequester{requester: base, group: plan.ParentKind.ValueString() == "group"}
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneItemContainer(ctx, base, plan.ParentKind.ValueString(), plan.SceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok || scene.Name != plan.SceneName.ValueString() {
			return fmt.Errorf("scene identity/name mismatch")
		}
		it, ok, err := itemByID(ctx, cl, scene.Name, state.ItemID.ValueInt64())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("scene item disappeared; refresh before apply")
		}
		if it.SourceUUID != plan.SourceUUID.ValueString() || it.SourceName != plan.SourceName.ValueString() {
			return fmt.Errorf("scene item rebound; refusing update")
		}
		if plan.Transform == nil {
			return fmt.Errorf("transform is required")
		}
		if err := updateItem(ctx, cl, plan.Collection.ValueString(), scene.Name, it, plan.Enabled.ValueBool(), plan.Index.ValueInt64(), *plan.Transform); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, it.ItemID, plan.SourceUUID.ValueString(), plan.Enabled.ValueBool(), plan.Index.ValueInt64(), *plan.Transform, 40, 100*time.Millisecond); err != nil {
			return err
		}
		plan.ItemID = state.ItemID
		plan.ID = state.ID
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS scene item failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sceneItemResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m groupAwareSceneItemModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString()}, func(base requester) error {
		normalizeSceneItemDefaults(&m)
		cl := sceneItemContainerRequester{requester: base, group: m.ParentKind.ValueString() == "group"}
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		scene, ok, err := sceneItemContainer(ctx, base, m.ParentKind.ValueString(), m.SceneUUID.ValueString())
		if err != nil || !ok {
			return err
		}
		if !m.SceneName.IsNull() && !m.SceneName.IsUnknown() && scene.Name != m.SceneName.ValueString() {
			return fmt.Errorf("container identity/name mismatch; refusing deletion")
		}
		it, ok, err := itemByID(ctx, cl, scene.Name, m.ItemID.ValueInt64())
		if err != nil || !ok {
			return err
		}
		if it.SourceUUID != m.SourceUUID.ValueString() || (!m.SourceName.IsNull() && !m.SourceName.IsUnknown() && it.SourceName != m.SourceName.ValueString()) {
			return fmt.Errorf("scene item rebound; refusing deletion")
		}
		if err := deleteContainerSceneItem(ctx, base, scene, m, it); err != nil {
			return err
		}
		return waitUntil(ctx, func() (bool, error) { _, exists, e := itemByID(ctx, cl, scene.Name, it.ItemID); return !exists, e })
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS scene item failed", err.Error())
	}
}

func (r *sceneItemResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 4 && !(len(parts) == 5 && parts[4] == "group") {
		resp.Diagnostics.AddError("Invalid scene item import ID", "use collection|scene_uuid|source_uuid|item_id, or append |group for a group container")
		return
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid scene item import ID", "item_id must be positive")
		return
	}
	kind := "scene"
	if len(parts) == 5 {
		kind = "group"
	}
	for k, v := range map[string]any{"parent_kind": kind, "allow_duplicate": false, "id": req.ID, "collection": parts[0], "scene_uuid": parts[1], "source_uuid": parts[2], "item_id": id} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), v)...)
	}
}
func sceneItemID(collection, scene, source string, item int64) string {
	return fmt.Sprintf("%s|%s|%s|%d", collection, scene, source, item)
}

var _ resource.ResourceWithConfigure = (*sceneItemResource)(nil)
var _ resource.ResourceWithImportState = (*sceneItemResource)(nil)
