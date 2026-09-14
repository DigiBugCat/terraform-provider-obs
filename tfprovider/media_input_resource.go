package tfprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type mediaInputResource struct{ client *client }

func NewMediaInputResource() resource.Resource { return &mediaInputResource{} }
func (r *mediaInputResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_media_input"
}
func (r *mediaInputResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	configureClient(req, &r.client, &resp.Diagnostics)
}
func (r *mediaInputResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state mediaInputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !state.AnchorSceneName.IsNull() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, tfpath.Root("id"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, tfpath.Root("anchor_item_id"), types.Int64Unknown())...)
}
func (r *mediaInputResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id": identityString(), "collection": immutableString(true), "name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}, "input_uuid": identityString(),
		"local_file": schema.StringAttribute{Required: true}, "is_local_file": schema.BoolAttribute{Required: true}, "looping": schema.BoolAttribute{Required: true}, "restart_on_activate": schema.BoolAttribute{Required: true}, "close_when_inactive": schema.BoolAttribute{Required: true},
		"anchor_scene_name": schema.StringAttribute{Required: true}, "anchor_scene_uuid": immutableString(true), "anchor_item_id": identityInt64(), "anchor_enabled": schema.BoolAttribute{Required: true}, "anchor_index": schema.Int64Attribute{Required: true}, "anchor_transform": transformSchema(),
		"observed_media_state": schema.StringAttribute{Computed: true}, "observed_media_cursor": schema.Float64Attribute{Computed: true}, "observed_media_duration": schema.Float64Attribute{Computed: true},
	}}
}
func mediaSettings(m mediaInputModel) map[string]any {
	return map[string]any{"local_file": m.LocalFile.ValueString(), "is_local_file": m.IsLocalFile.ValueBool(), "looping": m.Looping.ValueBool(), "restart_on_activate": m.RestartOnActivate.ValueBool(), "close_when_inactive": m.CloseWhenInactive.ValueBool()}
}
func validateMedia(m mediaInputModel) error {
	p := m.LocalFile.ValueString()
	if !filepath.IsAbs(p) {
		return fmt.Errorf("local_file must be absolute")
	}
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("media asset is not a regular file")
	}
	if !m.IsLocalFile.ValueBool() {
		return fmt.Errorf("obs_media_input requires is_local_file = true")
	}
	return nil
}
func setMediaObserved(ctx context.Context, cl requester, uuid string, m *mediaInputModel) error {
	x, err := request(ctx, cl, "GetMediaInputStatus", map[string]any{"inputUuid": uuid})
	if err != nil {
		return err
	}
	state := stringValue(x["mediaState"])
	cursor, cok := numberValue(x["mediaCursor"])
	duration, dok := numberValue(x["mediaDuration"])
	if state == "" {
		return fmt.Errorf("media status omitted raw state")
	}
	m.ObservedMediaState = types.StringValue(state)
	if cok {
		m.ObservedMediaCursor = types.Float64Value(cursor)
	} else {
		m.ObservedMediaCursor = types.Float64Null()
	}
	if dok {
		m.ObservedMediaDuration = types.Float64Value(duration)
	} else {
		m.ObservedMediaDuration = types.Float64Null()
	}
	return recordTrace(map[string]any{"event": "media-observation", "input_uuid": uuid, "raw_state": state, "cursor": cursor, "cursor_known": cok, "duration": duration, "duration_known": dok})
}
func resolveMediaSettings(live, defaults map[string]any) (map[string]any, map[string]string, error) {
	out := map[string]any{}
	origins := map[string]string{}
	for _, k := range []string{"local_file", "is_local_file", "looping", "restart_on_activate", "close_when_inactive"} {
		v, ok := live[k]
		origin := "live"
		if !ok {
			v, ok = defaults[k]
			origin = "default"
		}
		if !ok {
			return nil, nil, fmt.Errorf("media setting %s absent from live and defaults", k)
		}
		if k == "local_file" {
			if _, ok := v.(string); !ok {
				return nil, nil, fmt.Errorf("media setting %s has type %T, want string", k, v)
			}
		} else if _, ok := v.(bool); !ok {
			return nil, nil, fmt.Errorf("media setting %s has type %T, want bool", k, v)
		}
		out[k] = v
		origins[k] = origin
	}
	return out, origins, nil
}
func observedMediaSettings(ctx context.Context, cl requester, in inputObservation) (map[string]any, error) {
	x, err := request(ctx, cl, "GetInputDefaultSettings", map[string]any{"inputKind": "ffmpeg_source"})
	if err != nil {
		return nil, err
	}
	defaults, ok := x["defaultInputSettings"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GetInputDefaultSettings omitted object")
	}
	values, origins, err := resolveMediaSettings(in.Settings, defaults)
	if err != nil {
		return nil, err
	}
	if err := recordTrace(map[string]any{"event": "media-settings-observation", "input_uuid": in.UUID, "origins": origins}); err != nil {
		return nil, err
	}
	return values, nil
}
func mediaMatches(settings, want map[string]any) string {
	for _, k := range []string{"local_file", "is_local_file", "looping", "restart_on_activate", "close_when_inactive"} {
		if k == "local_file" {
			got, ok := settings[k].(string)
			w, wok := want[k].(string)
			if !ok || !wok || got != w {
				return fmt.Sprintf("%s=%v (%T) want %v", k, settings[k], settings[k], want[k])
			}
		} else {
			got, ok := settings[k].(bool)
			w, wok := want[k].(bool)
			if !ok || !wok || got != w {
				return fmt.Sprintf("%s=%v (%T) want %v", k, settings[k], settings[k], want[k])
			}
		}
	}
	return ""
}
func waitMediaInput(ctx context.Context, cl requester, uuid, name string, want map[string]any) (inputObservation, error) {
	var last string
	for i := 0; i < 40; i++ {
		got, ok, err := inputByUUID(ctx, cl, uuid)
		if err != nil {
			return inputObservation{}, err
		}
		if !ok {
			last = "absent"
		} else if got.Name != name || got.Kind != "ffmpeg_source" {
			last = "identity/kind mismatch"
		} else {
			effective, err := observedMediaSettings(ctx, cl, got)
			if err != nil {
				return inputObservation{}, err
			}
			if mismatch := mediaMatches(effective, want); mismatch != "" {
				last = mismatch
			} else {
				if err := recordTrace(map[string]any{"event": "postcondition", "object": "media-input", "status": "passed", "input_uuid": uuid}); err != nil {
					return inputObservation{}, err
				}
				return got, nil
			}
		}
		select {
		case <-ctx.Done():
			return inputObservation{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return inputObservation{}, fmt.Errorf("media input postcondition did not converge: %s", last)
}

func (r *mediaInputResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m mediaInputModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateMedia(m); err != nil {
		resp.Diagnostics.AddError("Invalid media input", err.Error())
		return
	}
	err := r.client.withKeyed(ctx, []string{m.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		if _, err := ensureCollection(ctx, cl, m.Collection.ValueString()); err != nil {
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
		x, err := mutation(ctx, cl, m.Collection.ValueString(), "CreateInput", map[string]any{"sceneUuid": scene.UUID, "inputName": m.Name.ValueString(), "inputKind": "ffmpeg_source", "inputSettings": mediaSettings(m), "sceneItemEnabled": m.AnchorEnabled.ValueBool()})
		if err != nil {
			return err
		}
		id := int64Value(x["sceneItemId"])
		uuid := stringValue(x["inputUuid"])
		if id <= 0 || uuid == "" {
			return fmt.Errorf("CreateInput omitted identity")
		}
		m.InputUUID = types.StringValue(uuid)
		m.AnchorItemID = types.Int64Value(id)
		m.ID = types.StringValue(inputID(m.Collection.ValueString(), uuid, scene.UUID, id))
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
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
		if _, err := waitMediaInput(ctx, cl, uuid, m.Name.ValueString(), mediaSettings(m)); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, uuid, m.AnchorEnabled.ValueBool(), anchorIndex, *m.AnchorTransform, postconditionAttempts, postconditionDelay); err != nil {
			return err
		}
		return setMediaObserved(ctx, cl, uuid, &m)
	})
	if err != nil {
		resp.Diagnostics.AddError("Create OBS media input failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *mediaInputResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m mediaInputModel
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
		if !m.Name.IsNull() && in.Name != m.Name.ValueString() {
			return fmt.Errorf("input identity rebound")
		}
		m.Name = types.StringValue(in.Name)
		effective, err := observedMediaSettings(ctx, cl, in)
		if err != nil {
			return err
		}
		m.LocalFile = types.StringValue(effective["local_file"].(string))
		m.IsLocalFile = types.BoolValue(effective["is_local_file"].(bool))
		m.Looping = types.BoolValue(effective["looping"].(bool))
		m.RestartOnActivate = types.BoolValue(effective["restart_on_activate"].(bool))
		m.CloseWhenInactive = types.BoolValue(effective["close_when_inactive"].(bool))
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
		return setMediaObserved(ctx, cl, in.UUID, &m)
	})
	if err != nil {
		resp.Diagnostics.AddError("Read OBS media input failed", err.Error())
		return
	}
	if !resp.State.Raw.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *mediaInputResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state mediaInputModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateMedia(plan); err != nil {
		resp.Diagnostics.AddError("Invalid media input", err.Error())
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
			return fmt.Errorf("input %s disappeared; refresh before apply", state.InputUUID.ValueString())
		}
		if in.Name != plan.Name.ValueString() || in.Kind != "ffmpeg_source" {
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
		want := mediaSettings(plan)
		effective, err := observedMediaSettings(ctx, cl, in)
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
		if mismatch := mediaMatches(effective, want); mismatch != "" {
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
			if err != nil {
				return err
			}
			id = int64Value(x["sceneItemId"])
			if id <= 0 {
				return fmt.Errorf("CreateSceneItem acknowledged without item ID")
			}
			plan.InputUUID = state.InputUUID
			plan.AnchorItemID = types.Int64Value(id)
			plan.ID = types.StringValue(inputID(plan.Collection.ValueString(), in.UUID, scene.UUID, id))
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		}
		if exists {
			if err := updateItem(ctx, cl, plan.Collection.ValueString(), scene.Name, it, plan.AnchorEnabled.ValueBool(), anchorIndex, *plan.AnchorTransform); err != nil {
				return err
			}
		} else if err := setItem(ctx, cl, plan.Collection.ValueString(), scene.Name, id, plan.AnchorEnabled.ValueBool(), anchorIndex, *plan.AnchorTransform); err != nil {
			return err
		}
		if _, err := waitMediaInput(ctx, cl, in.UUID, plan.Name.ValueString(), want); err != nil {
			return err
		}
		if _, err := waitItemExpected(ctx, cl, scene.Name, id, in.UUID, plan.AnchorEnabled.ValueBool(), anchorIndex, *plan.AnchorTransform, postconditionAttempts, postconditionDelay); err != nil {
			return err
		}
		plan.InputUUID = state.InputUUID
		plan.AnchorItemID = types.Int64Value(id)
		plan.ID = types.StringValue(inputID(plan.Collection.ValueString(), in.UUID, scene.UUID, id))
		return setMediaObserved(ctx, cl, in.UUID, &plan)
	})
	if err != nil {
		resp.Diagnostics.AddError("Update OBS media input failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *mediaInputResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m mediaInputModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.withKeyed(ctx, []string{m.AnchorSceneUUID.ValueString()}, func(cl requester) error {
		return deleteAnchoredInput(ctx, cl, m.Collection.ValueString(), m.InputUUID.ValueString(), m.AnchorSceneUUID.ValueString(), m.AnchorItemID.ValueInt64())
	})
	if err != nil {
		resp.Diagnostics.AddError("Delete OBS media input failed", err.Error())
	}
}

func (r *mediaInputResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "|")
	if len(parts) != 4 {
		resp.Diagnostics.AddError("Invalid media import ID", "use collection|input_uuid|anchor_scene_uuid|anchor_item_id")
		return
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid media import ID", "anchor_item_id must be positive")
		return
	}
	for k, v := range map[string]any{"id": req.ID, "collection": parts[0], "input_uuid": parts[1], "anchor_scene_uuid": parts[2], "anchor_item_id": id} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, tfpath.Root(k), v)...)
	}
}

var _ resource.ResourceWithConfigure = (*mediaInputResource)(nil)
var _ resource.ResourceWithImportState = (*mediaInputResource)(nil)
var _ resource.ResourceWithModifyPlan = (*mediaInputResource)(nil)
