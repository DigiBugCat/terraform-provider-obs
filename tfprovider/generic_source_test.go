package tfprovider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// An explicit protocol fake, not an OBS/native plugin qualification.
type genericSourceFake struct {
	exists, anchor, extraScene, extraGroup, rebound, active bool
	name, kind                                              string
	settings, defaults                                      map[string]any
	itemID                                                  int64
	enabled                                                 bool
	index                                                   int64
	transform                                               transformModel
	available                                               []any
	calls                                                   []groupFixtureCall
	fail                                                    string
}

func newGenericSourceFake() *genericSourceFake {
	return &genericSourceFake{name: "Plugin", kind: "plugin_input_v2", settings: map[string]any{"private": "untouched"}, defaults: map[string]any{"gain": float64(1)}, itemID: 7, enabled: true, transform: transformFromNative(map[string]any{"scaleX": 1., "scaleY": 1., "alignment": 5., "boundsType": "OBS_BOUNDS_NONE"}), available: []any{"plugin_input_v2"}}
}
func (f *genericSourceFake) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, groupFixtureCall{typ: typ, data: data})
	if f.fail == typ {
		return nil, fmt.Errorf("injected %s failure", typ)
	}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": "Lab"}, nil
	case "GetVersion":
		return map[string]any{"obsVersion": "32.1.0", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"CreateInput", "SetInputSettings", "CreateSceneItem", "SetSceneItemEnabled", "SetSceneItemTransform", "SetSceneItemIndex", "RemoveSceneItem", "GetStreamStatus"}}, nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920., "baseHeight": 1080.}, nil
	case "GetStreamStatus":
		return map[string]any{"outputActive": f.active}, nil
	case "GetInputKindList":
		if data["unversioned"] != false {
			return nil, fmt.Errorf("exact versioned kinds required")
		}
		return map[string]any{"inputKinds": f.available}, nil
	case "GetInputList":
		inputs := []any{}
		if f.exists {
			inputs = append(inputs, map[string]any{"inputUuid": "input-1", "inputName": f.name, "inputKind": f.kind})
		}
		return map[string]any{"inputs": inputs}, nil
	case "GetInputSettings":
		return map[string]any{"inputKind": f.kind, "inputSettings": f.settings}, nil
	case "GetInputDefaultSettings":
		return map[string]any{"defaultInputSettings": f.defaults}, nil
	case "GetSceneList":
		return map[string]any{"scenes": []any{map[string]any{"sceneName": "Scene", "sceneUuid": "scene-1"}}}, nil
	case "GetSceneItemList":
		items := []any{}
		uuid := "input-1"
		if f.rebound {
			uuid = "other-input"
		}
		if f.anchor {
			items = append(items, map[string]any{"sourceUuid": uuid, "sourceName": f.name, "sceneItemId": f.itemID, "sceneItemIndex": f.index, "sceneItemEnabled": f.enabled, "sceneItemTransform": f.transform.native()})
		}
		if f.extraScene {
			items = append(items, map[string]any{"sourceUuid": "input-1", "sourceName": f.name, "sceneItemId": int64(99), "sceneItemTransform": f.transform.native()})
		}
		return map[string]any{"sceneItems": items}, nil
	case "GetGroupList":
		groups := []any{}
		if f.extraGroup {
			groups = append(groups, "Group")
		}
		return map[string]any{"groups": groups}, nil
	case "GetGroupSceneItemList":
		return map[string]any{"sceneItems": []any{map[string]any{"sourceUuid": "input-1", "sceneItemId": int64(100)}}}, nil
	case "CreateInput":
		f.exists = true
		f.anchor = true
		f.name = stringValue(data["inputName"])
		f.kind = stringValue(data["inputKind"])
		f.enabled = boolValue(data["sceneItemEnabled"])
		for k, v := range mapValue(data["inputSettings"]) {
			f.settings[k] = v
		}
		return map[string]any{"inputUuid": "input-1", "sceneItemId": f.itemID}, nil
	case "SetInputSettings":
		if data["overlay"] != true || data["inputUuid"] != "input-1" {
			return nil, fmt.Errorf("expected UUID overlay")
		}
		for k, v := range mapValue(data["inputSettings"]) {
			f.settings[k] = v
		}
		return map[string]any{}, nil
	case "CreateSceneItem":
		f.itemID++
		f.anchor = true
		f.enabled = boolValue(data["sceneItemEnabled"])
		return map[string]any{"sceneItemId": f.itemID}, nil
	case "SetSceneItemEnabled":
		f.enabled = boolValue(data["sceneItemEnabled"])
		return map[string]any{}, nil
	case "SetSceneItemIndex":
		f.index = int64Value(data["sceneItemIndex"])
		return map[string]any{}, nil
	case "SetSceneItemTransform":
		f.transform = transformFromNative(mapValue(data["sceneItemTransform"]))
		return map[string]any{}, nil
	case "RemoveSceneItem":
		f.anchor = false
		f.exists = false
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("unexpected request %s", typ)
}
func (f *genericSourceFake) writes() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c.typ, "Set") || strings.HasPrefix(c.typ, "Create") || strings.HasPrefix(c.typ, "Remove") {
			out = append(out, c.typ)
		}
	}
	return out
}
func genericSourceTestModel(f *genericSourceFake) genericSourceModel {
	return genericSourceModel{ID: types.StringValue("Lab|input-1|scene-1|7"), Collection: types.StringValue("Lab"), Name: types.StringValue("Plugin"), Kind: types.StringValue("plugin_input_v2"), SettingsJSON: types.StringValue("{ \"gain\": 1 }"), InputUUID: types.StringValue("input-1"), AnchorSceneName: types.StringValue("Scene"), AnchorSceneUUID: types.StringValue("scene-1"), AnchorItemID: types.Int64Value(7), AnchorEnabled: types.BoolValue(true), AnchorIndex: types.Int64Value(0), AnchorTransform: &f.transform}
}
func genericSourceTestSchema() schema.Schema {
	var resp resource.SchemaResponse
	(&genericSourceResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}
func genericSourceTestState(t *testing.T, m genericSourceModel) tfsdk.State {
	t.Helper()
	return groupFixtureSchemaAndState(t, context.Background(), genericSourceTestSchema(), m)
}
func genericSourceTestPlan(t *testing.T, m genericSourceModel) tfsdk.Plan {
	t.Helper()
	return groupFixtureSchemaAndPlan(t, context.Background(), genericSourceTestSchema(), m)
}

func TestGenericSourceCRUDOverlayAndProjection(t *testing.T) {
	ctx := context.Background()
	f := newGenericSourceFake()
	r := &genericSourceResource{client: &client{testRequester: f}}
	m := genericSourceTestModel(f)
	cr := resource.CreateResponse{State: genericSourceTestState(t, m)}
	r.Create(ctx, resource.CreateRequest{Plan: genericSourceTestPlan(t, m)}, &cr)
	if cr.Diagnostics.HasError() {
		t.Fatal(cr.Diagnostics)
	}
	if f.settings["private"] != "untouched" {
		t.Fatal("unmanaged setting overwritten")
	}
	var created genericSourceModel
	groupFixtureRequireState(t, ctx, cr.State, &created)
	f.calls = nil
	rr := resource.ReadResponse{State: cr.State}
	r.Read(ctx, resource.ReadRequest{State: cr.State}, &rr)
	if rr.Diagnostics.HasError() {
		t.Fatal(rr.Diagnostics)
	}
	var read genericSourceModel
	groupFixtureRequireState(t, ctx, rr.State, &read)
	if read.SettingsJSON != m.SettingsJSON {
		t.Fatal("read canonicalized unchanged JSON or adopted unknown fields")
	}
	if len(f.writes()) != 0 {
		t.Fatal(f.writes())
	}
	// Drift projects only managed keys and is repaired by a UUID overlay.
	f.settings["gain"] = float64(3)
	r.Read(ctx, resource.ReadRequest{State: rr.State}, &rr)
	if rr.Diagnostics.HasError() {
		t.Fatal(rr.Diagnostics)
	}
	groupFixtureRequireState(t, ctx, rr.State, &read)
	if read.SettingsJSON.ValueString() != "{\"gain\":3}" {
		t.Fatal(read.SettingsJSON)
	}
	up := resource.UpdateResponse{State: rr.State}
	r.Update(ctx, resource.UpdateRequest{State: rr.State, Plan: genericSourceTestPlan(t, m)}, &up)
	if up.Diagnostics.HasError() {
		t.Fatal(up.Diagnostics)
	}
	if f.settings["gain"] != float64(1) || f.settings["private"] != "untouched" {
		t.Fatal(f.settings)
	}
	// Relinquishing ownership writes nothing and keeps native settings.
	f.calls = nil
	m.SettingsJSON = types.StringValue("{}")
	up2 := resource.UpdateResponse{State: up.State}
	r.Update(ctx, resource.UpdateRequest{State: up.State, Plan: genericSourceTestPlan(t, m)}, &up2)
	if up2.Diagnostics.HasError() {
		t.Fatal(up2.Diagnostics)
	}
	if len(f.writes()) != 0 {
		t.Fatal(f.writes())
	}
	dr := resource.DeleteResponse{State: up2.State}
	r.Delete(ctx, resource.DeleteRequest{State: up2.State}, &dr)
	if dr.Diagnostics.HasError() {
		t.Fatal(dr.Diagnostics)
	}
	if f.exists {
		t.Fatal("input not released with anchor")
	}
}

func TestGenericSourceSafetyRefusals(t *testing.T) {
	for _, scenario := range []string{"kind unavailable", "active output", "anchor rebound", "extra scene reference", "extra group reference", "renamed", "kind rebound"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			f := newGenericSourceFake()
			m := genericSourceTestModel(f)
			r := &genericSourceResource{client: &client{testRequester: f}}
			if scenario == "kind unavailable" || scenario == "active output" {
				if scenario == "kind unavailable" {
					f.available = []any{"plugin_input"}
				} else {
					f.active = true
				}
				resp := resource.CreateResponse{State: genericSourceTestState(t, m)}
				r.Create(ctx, resource.CreateRequest{Plan: genericSourceTestPlan(t, m)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("expected refusal")
				}
			} else {
				f.exists = true
				f.anchor = true
				switch scenario {
				case "anchor rebound":
					f.rebound = true
				case "extra scene reference":
					f.extraScene = true
				case "extra group reference":
					f.extraGroup = true
				case "renamed":
					f.name = "Renamed"
				case "kind rebound":
					f.kind = "other"
				}
				if scenario == "anchor rebound" {
					m.SettingsJSON = types.StringValue("{\"gain\":2}")
					resp := resource.UpdateResponse{State: genericSourceTestState(t, m)}
					r.Update(ctx, resource.UpdateRequest{State: resp.State, Plan: genericSourceTestPlan(t, m)}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("expected refusal")
					}
				} else {
					resp := resource.DeleteResponse{State: genericSourceTestState(t, m)}
					r.Delete(ctx, resource.DeleteRequest{State: resp.State}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("expected refusal")
					}
				}
			}
			if len(f.writes()) != 0 {
				t.Fatalf("writes before refusal: %v", f.writes())
			}
		})
	}
}

func TestGenericSourceCreateRetainsAcknowledgedIdentity(t *testing.T) {
	for _, failure := range []string{"CreateInput", "SetSceneItemTransform"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			f := newGenericSourceFake()
			m := genericSourceTestModel(f)
			m.ID = types.StringUnknown()
			m.InputUUID = types.StringUnknown()
			m.AnchorItemID = types.Int64Unknown()
			if failure == "CreateInput" {
				t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", failure)
			} else {
				f.fail = failure
			}
			r := &genericSourceResource{client: &client{testRequester: f}}
			resp := resource.CreateResponse{State: genericSourceTestState(t, m)}
			r.Create(ctx, resource.CreateRequest{Plan: genericSourceTestPlan(t, m)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected failure")
			}
			var got genericSourceModel
			groupFixtureRequireState(t, ctx, resp.State, &got)
			if got.InputUUID.ValueString() != "input-1" || got.AnchorItemID.ValueInt64() != 7 || got.ID.IsUnknown() {
				t.Fatal("lost acknowledged identity")
			}
			if !f.exists {
				t.Fatal("fake did not execute source creation")
			}
		})
	}
}

func TestGenericSourceMissingAnchorRepairRetainsNewIdentity(t *testing.T) {
	ctx := context.Background()
	f := newGenericSourceFake()
	f.exists = true
	m := genericSourceTestModel(f)
	r := &genericSourceResource{client: &client{testRequester: f}}
	rr := resource.ReadResponse{State: genericSourceTestState(t, m)}
	r.Read(ctx, resource.ReadRequest{State: rr.State}, &rr)
	if rr.Diagnostics.HasError() {
		t.Fatal(rr.Diagnostics)
	}
	var read genericSourceModel
	groupFixtureRequireState(t, ctx, rr.State, &read)
	if !read.AnchorSceneName.IsNull() {
		t.Fatal("missing anchor not represented")
	}
	// An acknowledged repair interrupted immediately must bind the new ID.
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "CreateSceneItem")
	resp := resource.UpdateResponse{State: rr.State}
	r.Update(ctx, resource.UpdateRequest{State: rr.State, Plan: genericSourceTestPlan(t, m)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected interruption")
	}
	var got genericSourceModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.AnchorItemID.ValueInt64() != 8 || !strings.HasSuffix(got.ID.ValueString(), "|8") {
		t.Fatal("repair identity lost")
	}
	if len(f.writes()) != 1 || f.writes()[0] != "CreateSceneItem" {
		t.Fatal(f.writes())
	}
}

func TestGenericSourceImportOwnsNoSettings(t *testing.T) {
	ctx := context.Background()
	sc := genericSourceTestSchema()
	r := &genericSourceResource{}
	for _, id := range []string{"Lab|input-1|scene-1|7", "|input-1|scene-1|7", "Lab|input-1|scene-1|0", "bad"} {
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: sc, Raw: tftypes.NewValue(sc.Type().TerraformType(ctx), nil)}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &resp)
		if id != "Lab|input-1|scene-1|7" {
			if !resp.Diagnostics.HasError() {
				t.Fatal("accepted invalid import")
			}
			continue
		}
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		var got genericSourceModel
		groupFixtureRequireState(t, ctx, resp.State, &got)
		if got.SettingsJSON.ValueString() != "{}" {
			t.Fatal("import must not adopt all plugin settings")
		}
	}
	attr := sc.Attributes["settings_json"].(schema.StringAttribute)
	if !attr.Sensitive || !attr.Required || len(attr.Validators) == 0 {
		t.Fatal("settings schema lacks sensitive required validation")
	}
}

func TestTerraformCLIGenericSourceLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI regression")
	}
	root, tf := pinnedTerraform(t)
	f := newGenericSourceFake()
	server := newFakeOBSWebsocketServer(t, f)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	providers := builtProvider(t, root)
	rc := writeDevOverrides(t, work, providers)
	config := fmt.Sprintf(`terraform {
 required_providers { obs = { source = "digibugcat/obs" } }
}
provider "obs" {
 host = "127.0.0.1"
 port = %s
 password = "fake"
}
resource "obs_source" "test" {
 collection = "Lab"
 name = "Plugin"
 kind = "plugin_input_v2"
 settings_json = "{ \"gain\": 1.0 }"
 anchor_scene_name = "Scene"
 anchor_scene_uuid = "scene-1"
 anchor_enabled = true
 anchor_index = 0
 anchor_transform = {
  x=0
  y=0
  scale_x=1
  scale_y=1
  rotation=0
  crop_left=0
  crop_top=0
  crop_right=0
  crop_bottom=0
  alignment=5
  bounds_type="OBS_BOUNDS_NONE"
  bounds_alignment=0
 }
}
`, u.Port())
	if err := os.WriteFile(filepath.Join(work, "main.tf"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command(tf, args...)
		c.Dir = work
		c.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	f.settings["gain"] = float64(9)
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	f.anchor = false
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	if !f.anchor || f.itemID != 8 {
		t.Fatal("missing anchor was not repaired with new identity")
	}
	if f.settings["private"] != "untouched" {
		t.Fatal("unmanaged settings lost")
	}
	run("destroy", "-auto-approve", "-input=false", "-no-color")
	if f.exists {
		t.Fatal("source lifetime not released")
	}
}

func TestGenericSourceMissingReadAndNewManagedKey(t *testing.T) {
	ctx := context.Background()
	f := newGenericSourceFake()
	m := genericSourceTestModel(f)
	r := &genericSourceResource{client: &client{testRequester: f}}
	rr := resource.ReadResponse{State: genericSourceTestState(t, m)}
	r.Read(ctx, resource.ReadRequest{State: rr.State}, &rr)
	if rr.Diagnostics.HasError() || !rr.State.Raw.IsNull() {
		t.Fatal("absent UUID must remove resource", rr.Diagnostics)
	}
	f.exists = true
	f.anchor = true
	plan := m
	plan.SettingsJSON = types.StringValue(`{"new_setting":{"whole":"object"}}`)
	up := resource.UpdateResponse{State: genericSourceTestState(t, m)}
	r.Update(ctx, resource.UpdateRequest{State: up.State, Plan: genericSourceTestPlan(t, plan)}, &up)
	if up.Diagnostics.HasError() {
		t.Fatal(up.Diagnostics)
	}
	if !genericJSONEqual(mapValue(f.settings["new_setting"]), map[string]any{"whole": "object"}) {
		t.Fatal("new key missing")
	}
	if f.settings["private"] != "untouched" {
		t.Fatal("unknown settings lost")
	}
}
