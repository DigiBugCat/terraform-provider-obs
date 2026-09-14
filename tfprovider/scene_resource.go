package tfprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type sceneResource struct{ client *client }

func NewSceneResource() resource.Resource { return &sceneResource{} }
func (r *sceneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scene"
}
func (r *sceneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *sceneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{"id": identityString(), "collection": immutableString(true), "name": schema.StringAttribute{Required: true}, "scene_uuid": identityString()}}
}
func (r *sceneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m sceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString(), "scene:" + m.Name.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		if _, ok, err := sceneByName(ctx, cl, m.Name.ValueString()); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("scene %q already exists; import it instead", m.Name.ValueString())
		}
		x, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateScene", map[string]any{"sceneName": m.Name.ValueString()})
		if err != nil {
			return err
		}
		createdUUID := stringValue(x["sceneUuid"])
		if createdUUID == "" {
			return fmt.Errorf("CreateScene acknowledged without sceneUuid")
		}
		m.SceneUUID = types.StringValue(createdUUID)
		m.ID = types.StringValue(m.Collection.ValueString() + "|" + createdUUID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return waitUntil(ctx, func() (bool, error) {
			s, ok, e := sceneByUUID(ctx, cl, createdUUID)
			return ok && s.Name == m.Name.ValueString(), e
		})
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *sceneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m sceneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withReadRequester(ctx, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		s, ok, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			resp.State.RemoveResource(ctx)
			return nil
		}
		m.Name = types.StringValue(s.Name)
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Read OBS scene failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *sceneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state sceneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{plan.SceneUUID.ValueString(), "scene:" + plan.Name.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, plan.Collection.ValueString()); err != nil {
			return err
		}
		s, ok, err := sceneByUUID(ctx, cl, state.SceneUUID.ValueString())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("scene identity %s disappeared; refresh before apply", state.SceneUUID.ValueString())
		}
		if s.Name != plan.Name.ValueString() {
			if other, exists, err := sceneByName(ctx, cl, plan.Name.ValueString()); err != nil {
				return err
			} else if exists && other.UUID != s.UUID {
				return fmt.Errorf("replacement scene name %q already exists", plan.Name.ValueString())
			}
			if _, err := mutation(ctx, cl, plan.Collection.ValueString(), "SetSceneName", map[string]any{"sceneUuid": s.UUID, "newSceneName": plan.Name.ValueString()}); err != nil {
				return err
			}
		}
		plan.SceneUUID = state.SceneUUID
		plan.ID = state.ID
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS scene failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *sceneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m sceneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.SceneUUID.ValueString(), "scene:" + m.Name.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
			return err
		}
		s, ok, err := sceneByUUID(ctx, cl, m.SceneUUID.ValueString())
		if err != nil || !ok {
			return err
		}
		items, err := sceneItems(ctx, cl, s.Name)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			return fmt.Errorf("refusing to remove scene %q: %d native items remain", s.Name, len(items))
		}
		if _, err := mutation(ctx, cl, m.Collection.ValueString(), "RemoveScene", map[string]any{"sceneUuid": s.UUID}); err != nil {
			return err
		}
		return waitUntil(ctx, func() (bool, error) { _, ok, e := sceneByUUID(ctx, cl, s.UUID); return !ok, e })
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS scene failed", err.Error())
	}
}
func (r *sceneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid scene import ID", "use collection|scene_uuid")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("collection"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scene_uuid"), parts[1])...)
}

var _ resource.ResourceWithConfigure = (*sceneResource)(nil)
var _ resource.ResourceWithImportState = (*sceneResource)(nil)
