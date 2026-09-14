package tfprovider

import (
	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"

	"context"
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

type inputResource struct{ client *client }

func NewInputResource() resource.Resource { return &inputResource{} }
func (r *inputResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_input"
}
func (r *inputResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *inputResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state inputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.AnchorSceneName.IsNull() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("anchor_item_id"), types.Int64Unknown())...)
}
func (r *inputResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true),
		"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"kind": immutableString(true), "file": schema.StringAttribute{Required: true}, "unload": schema.BoolAttribute{Required: true}, "input_uuid": identityString(),
		"anchor_scene_name": schema.StringAttribute{Required: true}, "anchor_scene_uuid": immutableString(true),
		"anchor_item_id": identityInt64(), "anchor_enabled": schema.BoolAttribute{Required: true}, "anchor_index": schema.Int64Attribute{Required: true},
		"anchor_transform": transformSchema(),
	}}
}

func validateImagePlan(m inputModel) error {
	if m.Kind.ValueString() != "image_source" {
		return fmt.Errorf("pilot supports only image_source, got %q", m.Kind.ValueString())
	}
	p := m.File.ValueString()
	if !filepath.IsAbs(p) {
		return fmt.Errorf("file must be absolute, got %q", p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("image asset: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("image asset %q is not a regular file", p)
	}
	return nil
}

func (r *inputResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m inputModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateImagePlan(m); err != nil {
		resp.Diagnostics.AddError("Invalid image input", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{m.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		if _, ok, err := inputByName(ctx, cl, m.Name.ValueString()); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("input %q already exists; import it instead", m.Name.ValueString())
		}
		scene, ok, err := sceneByUUID(ctx, cl, m.AnchorSceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok || scene.Name != m.AnchorSceneName.ValueString() {
			return fmt.Errorf("anchor scene identity/name mismatch")
		}
		x, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateInput", map[string]any{"sceneUuid": scene.UUID, "inputName": m.Name.ValueString(), "inputKind": m.Kind.ValueString(), "inputSettings": map[string]any{"file": m.File.ValueString(), "unload": m.Unload.ValueBool()}, "sceneItemEnabled": m.AnchorEnabled.ValueBool()})
		if err != nil {
			return err
		}
		id := int64Value(x["sceneItemId"])
		if id <= 0 {
			return fmt.Errorf("CreateInput acknowledged without sceneItemId")
		}
		in, ok, err := inputByName(ctx, cl, m.Name.ValueString())
		if err != nil {
			return err
		}
		if !ok || in.UUID == "" {
			return fmt.Errorf("CreateInput acknowledged but input UUID unavailable")
		}
		if responseUUID := stringValue(x["inputUuid"]); responseUUID != "" && responseUUID != in.UUID {
			return fmt.Errorf("CreateInput response UUID %s differs from observed %s", responseUUID, in.UUID)
		}
		m.InputUUID = types.StringValue(in.UUID)
		m.AnchorItemID = types.Int64Value(id)
		m.ID = types.StringValue(inputID(m.Collection.ValueString(), in.UUID, scene.UUID, id))
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		if m.AnchorTransform == nil {
			return fmt.Errorf("anchor_transform is required")
		}
		siblings, err := containerItemCount(ctx, cl, scene.Name, false)
		if err != nil {
			return err
		}
		anchorIndex := clampCreateIndex(m.AnchorIndex.ValueInt64(), siblings-1)
		if err := setItem(ctx, cl, m.Collection.ValueString(), scene.Name, id, m.AnchorEnabled.ValueBool(), anchorIndex, *m.AnchorTransform); err != nil {
			return err
		}
		if _, err := waitInputExpected(ctx, cl, in.UUID, m.Name.ValueString(), m.Kind.ValueString(), m.File.ValueString(), m.Unload.ValueBool(), 40, 100*time.Millisecond); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, in.UUID, m.AnchorEnabled.ValueBool(), anchorIndex, *m.AnchorTransform, 40, 100*time.Millisecond); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS input failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *inputResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m inputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		in, ok, err := inputByUUID(ctx, cl, m.InputUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			resp.State.RemoveResource(ctx)
			return nil
		}
		if !m.Name.IsNull() && !m.Name.IsUnknown() && in.Name != m.Name.ValueString() {
			return fmt.Errorf("input UUID %s was renamed/rebound from %q to %q; refusing silent adoption", in.UUID, m.Name.ValueString(), in.Name)
		}
		m.Name = types.StringValue(in.Name)
		m.Kind = types.StringValue(in.Kind)
		m.File = types.StringValue(in.File)
		m.Unload = types.BoolValue(in.Unload)
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
		if it.SourceUUID != in.UUID {
			return fmt.Errorf("anchor item %d points to source UUID %s, expected %s", it.ItemID, it.SourceUUID, in.UUID)
		}
		m.AnchorEnabled = types.BoolValue(it.Enabled)
		m.AnchorIndex = types.Int64Value(it.Index)
		m.AnchorTransform = retainAuthoredTransform(it.Transform, m.AnchorTransform)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Read OBS input failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *inputResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inputModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateImagePlan(plan); err != nil {
		resp.Diagnostics.AddError("Invalid image input", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		in, ok, err := inputByUUID(ctx, cl, state.InputUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("input identity disappeared; refresh before apply")
		}
		scene, ok, err := sceneByUUID(ctx, cl, plan.AnchorSceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok || scene.Name != plan.AnchorSceneName.ValueString() {
			return fmt.Errorf("anchor scene identity/name mismatch")
		}
		if in.File != plan.File.ValueString() || in.Unload != plan.Unload.ValueBool() {
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetInputSettings", map[string]any{"inputUuid": in.UUID, "inputSettings": map[string]any{"file": plan.File.ValueString(), "unload": plan.Unload.ValueBool()}, "overlay": true}); err != nil {
				return err
			}
		}
		id := state.AnchorItemID.ValueInt64()
		it, exists, err := itemByID(ctx, cl, scene.Name, id)
		if err != nil {
			return err
		}
		if exists && it.SourceUUID != in.UUID {
			return fmt.Errorf("anchor item identity rebound to another source")
		}
		if !exists {
			x, err := mutation(ctx, cl, plan.Collection.ValueString(), "CreateSceneItem", map[string]any{"sceneUuid": scene.UUID, "sourceUuid": in.UUID, "sceneItemEnabled": plan.AnchorEnabled.ValueBool()})
			if err != nil {
				return err
			}
			id = int64Value(x["sceneItemId"])
			if id <= 0 {
				return fmt.Errorf("CreateSceneItem acknowledged without item id")
			}
			if plan.AnchorTransform == nil {
				return fmt.Errorf("anchor_transform is required")
			}
			if err := setItem(ctx, cl, plan.Collection.ValueString(), scene.Name, id, plan.AnchorEnabled.ValueBool(), plan.AnchorIndex.ValueInt64(), *plan.AnchorTransform); err != nil {
				return err
			}
		} else if plan.AnchorTransform == nil {
			return fmt.Errorf("anchor_transform is required")
		} else if err := updateItem(ctx, cl, plan.Collection.ValueString(), scene.Name, it, plan.AnchorEnabled.ValueBool(), plan.AnchorIndex.ValueInt64(), *plan.AnchorTransform); err != nil {
			return err
		}
		if _, err := waitInputExpected(ctx, cl, in.UUID, plan.Name.ValueString(), plan.Kind.ValueString(), plan.File.ValueString(), plan.Unload.ValueBool(), 40, 100*time.Millisecond); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, in.UUID, plan.AnchorEnabled.ValueBool(), plan.AnchorIndex.ValueInt64(), *plan.AnchorTransform, 40, 100*time.Millisecond); err != nil {
			return err
		}
		plan.InputUUID = state.InputUUID
		plan.AnchorItemID = types.Int64Value(id)
		plan.ID = types.StringValue(inputID(plan.Collection.ValueString(), in.UUID, scene.UUID, id))
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS input failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inputResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m inputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		in, ok, err := inputByUUID(ctx, cl, m.InputUUID.ValueString())
		if err != nil || !ok {
			return err
		}
		allScenes, err := scenes(ctx, cl)
		if err != nil {
			return err
		}
		var extras []string
		for _, s := range allScenes {
			items, err := sceneItems(ctx, cl, s.Name)
			if err != nil {
				return err
			}
			for _, it := range items {
				if it.SourceUUID == in.UUID && !(s.UUID == m.AnchorSceneUUID.ValueString() && it.ItemID == m.AnchorItemID.ValueInt64()) {
					extras = append(extras, fmt.Sprintf("%s/%d", s.Name, it.ItemID))
				}
			}
		}
		if len(extras) > 0 {
			return fmt.Errorf("refusing input destruction before mutation: extra source references %v", extras)
		}
		scene, ok, err := sceneByUUID(ctx, cl, m.AnchorSceneUUID.ValueString())
		if err != nil {
			return err
		}
		if ok {
			if it, exists, err := itemByID(ctx, cl, scene.Name, m.AnchorItemID.ValueInt64()); err != nil {
				return err
			} else if exists {
				if it.SourceUUID != in.UUID {
					return fmt.Errorf("anchor item rebound; refusing deletion")
				}
				if _, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveSceneItem", map[string]any{"sceneUuid": scene.UUID, "sceneItemId": it.ItemID}); err != nil {
					return err
				}
			}
		}
		return waitUntil(ctx, func() (bool, error) { _, exists, e := inputByUUID(ctx, cl, in.UUID); return !exists, e })
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS input failed", err.Error())
	}
}

func (r *inputResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 4 {
		resp.Diagnostics.AddError("Invalid input import ID", "use collection|input_uuid|anchor_scene_uuid|anchor_item_id")
		return
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid input import ID", "anchor_item_id must be positive")
		return
	}
	for k, v := range map[string]any{"id": req.ID, "collection": parts[0], "input_uuid": parts[1], "anchor_scene_uuid": parts[2], "anchor_item_id": id} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), v)...)
	}
}

func inputID(collection, input, scene string, item int64) string {
	return fmt.Sprintf("%s|%s|%s|%d", collection, input, scene, item)
}
func setItem(ctx context.Context, cl requester, collection, scene string, id int64, enabled bool, index int64, tr transformModel) error {
	if err := validateTransformBounds(tr); err != nil {
		return err
	}
	_, err := mutationBatch(ctx, cl, collection, []ws.BatchItem{
		{Type: "SetSceneItemEnabled", Data: map[string]any{"sceneName": scene, "sceneItemId": id, "sceneItemEnabled": enabled}},
		{Type: "SetSceneItemTransform", Data: map[string]any{"sceneName": scene, "sceneItemId": id, "sceneItemTransform": tr.native()}},
		{Type: "SetSceneItemIndex", Data: map[string]any{"sceneName": scene, "sceneItemId": id, "sceneItemIndex": index}},
	})
	return err
}

func updateItem(ctx context.Context, cl requester, collection, scene string, before itemObservation, enabled bool, index int64, tr transformModel) error {
	if err := validateTransformBounds(tr); err != nil {
		return err
	}
	if before.Enabled != enabled {
		if _, err := mutation(ctx, cl, collection, "SetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": before.ItemID, "sceneItemEnabled": enabled}); err != nil {
			return err
		}
	}
	if difference := transformDifference(before.Transform, tr); difference != "" {
		if err := recordTrace(map[string]any{"event": "transform-difference", "scene": scene, "item_id": before.ItemID, "difference": difference}); err != nil {
			return err
		}
		if _, err := mutation(ctx, cl, collection, "SetSceneItemTransform", map[string]any{"sceneName": scene, "sceneItemId": before.ItemID, "sceneItemTransform": tr.native()}); err != nil {
			return err
		}
	}
	if before.Index != index {
		if _, err := mutation(ctx, cl, collection, "SetSceneItemIndex", map[string]any{"sceneName": scene, "sceneItemId": before.ItemID, "sceneItemIndex": index}); err != nil {
			return err
		}
	}
	return nil
}

var _ resource.ResourceWithConfigure = (*inputResource)(nil)
var _ resource.ResourceWithImportState = (*inputResource)(nil)
var _ resource.ResourceWithModifyPlan = (*inputResource)(nil)
