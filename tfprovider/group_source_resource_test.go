package tfprovider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// In-process OBS-shaped fixture exercises generic settings and group lifetime
// without opening a real OBS connection.
type groupSourceFixture struct {
	input, child, parent              bool
	extraReference, rebound, relative bool
	settings                          map[string]any
	kind, bridgeKind                  string
	calls                             []groupFixtureCall
}

func (s *groupSourceFixture) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	s.calls = append(s.calls, groupFixtureCall{typ: typ, data: data})
	kind := s.kind
	if kind == "" {
		kind = "browser_source"
	}
	bridgeKind := s.bridgeKind
	if bridgeKind == "" {
		bridgeKind = kind
	}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": groupFixtureCollection}, nil
	case "GetVersion":
		return groupFixtureVersion("CallVendorRequest", "SetInputSettings"), nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}, nil
	case "GetInputKindList":
		return map[string]any{"inputKinds": []any{kind}}, nil
	case "GetInputList":
		list := []any{}
		if s.input {
			list = append(list, map[string]any{"inputName": groupFixtureInputName, "inputUuid": groupFixtureInputUUID, "inputKind": kind})
		}
		return map[string]any{"inputs": list}, nil
	case "GetInputSettings":
		return map[string]any{"inputKind": kind, "inputSettings": s.settings}, nil
	case "GetInputDefaultSettings":
		return map[string]any{"defaultInputSettings": map[string]any{"width": 800.0, "height": 600.0}}, nil
	case "SetInputSettings":
		if data["overlay"] != true {
			return nil, fmt.Errorf("expected managed overlay")
		}
		for k, v := range mapValue(data["inputSettings"]) {
			s.settings[k] = v
		}
		return map[string]any{}, nil
	case "GetSceneList":
		if !s.parent {
			return map[string]any{"scenes": []any{}}, nil
		}
		return groupFixtureSceneList(), nil
	case "GetSceneItemList":
		return map[string]any{"sceneItems": []any{}}, nil
	case "GetGroupList":
		return map[string]any{"groups": []any{groupFixtureGroupName}}, nil
	case "GetGroupSceneItemList":
		items := []any{}
		if s.child {
			items = append(items, map[string]any{"sourceUuid": groupFixtureInputUUID, "sceneItemId": 12.0})
		}
		if s.extraReference {
			items = append(items, map[string]any{"sourceUuid": groupFixtureInputUUID, "sceneItemId": 13.0})
		}
		return map[string]any{"sceneItems": items}, nil
	case "GetSourceFilterList":
		return map[string]any{"filters": []any{}}, nil
	case "CallVendorRequest":
		rt := stringValue(data["requestType"])
		rd := mapValue(data["requestData"])
		reply := groupFixtureBridgeReply{outcome: "completed", postconditionVerified: true}
		child := groupFixtureChildResult(12, groupFixtureInputUUID, groupFixtureInputName, groupFixturePlacement())
		child["inputKind"] = bridgeKind
		child["sourceWidth"], child["sourceHeight"] = 0.0, 0.0
		if s.rebound {
			child["sourceUuid"] = "other-input"
		}
		switch rt {
		case "GetCapabilities":
			reply.result = groupFixtureCapabilities([]string{"CreateInputInGroup", "GetGroup", "AddSourceToGroup", "RemoveGroupChildPlacement"})
			if s.relative {
				reply.result["absoluteCoordinates"] = false
				g := mapValue(reply.result["absoluteRuntimeGuard"])
				g["absoluteCoordinates"] = false
				g["supported"] = false
				g["status"] = "unsupported"
			}
		case "CreateInputInGroup":
			if rd["inputKind"] != kind {
				return nil, fmt.Errorf("kind not passed")
			}
			s.input, s.child = true, true
			s.settings = mapValue(rd["inputSettings"])
			reply.mutationAttempted = true
			reply.result = child
		case "AddSourceToGroup":
			s.child = true
			reply.mutationAttempted = true
			reply.result = child
		case "GetGroup":
			if !s.parent {
				reply.outcome = "failed"
				reply.code = "scene_not_found"
				break
			}
			children := []any{}
			if s.child {
				children = append(children, child)
			}
			if s.extraReference {
				sibling := groupFixtureChildResult(13, groupFixtureInputUUID, groupFixtureInputName, groupFixturePlacement())
				sibling["inputKind"] = bridgeKind
				children = append(children, sibling)
			}
			reply.result = groupFixtureGroupResult(7, groupFixtureGroupUUID, groupFixtureGroupName, groupFixturePlacement(), children)
		case "RemoveGroupChildPlacement":
			s.input, s.child = false, false
			reply.mutationAttempted = true
			reply.result = map[string]any{"sourceStillExists": false}
		default:
			return nil, fmt.Errorf("unexpected bridge request %s", rt)
		}
		return groupFixtureBridgeEnvelope(rt, stringValue(rd["clientOperationId"]), reply), nil
	}
	return nil, fmt.Errorf("unexpected request %s", typ)
}
func groupSourceTestModel() groupSourceModel {
	p := groupFixturePlacement()
	return groupSourceModel{ID: types.StringValue(groupSourceID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, groupFixtureInputUUID, 12)), Collection: types.StringValue(groupFixtureCollection), SceneName: types.StringValue(groupFixtureSceneName), SceneUUID: types.StringValue(groupFixtureSceneUUID), GroupName: types.StringValue(groupFixtureGroupName), GroupUUID: types.StringValue(groupFixtureGroupUUID), GroupItemID: types.Int64Value(7), Name: types.StringValue(groupFixtureInputName), Kind: types.StringValue("browser_source"), SettingsJSON: types.StringValue(`{"url":"https://example.invalid/overlay"}`), InputUUID: types.StringValue(groupFixtureInputUUID), ItemID: types.Int64Value(12), Placement: &p, SourceWidth: types.Int64Value(0), SourceHeight: types.Int64Value(0)}
}
func groupSourceTestSchema(ctx context.Context) resourceschema.Schema {
	var resp resource.SchemaResponse
	(&groupSourceResource{}).Schema(ctx, resource.SchemaRequest{}, &resp)
	return resp.Schema
}
func TestGroupSourceCreateGenericAndPartialIdentity(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupt), func(t *testing.T) {
			t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
			if interrupt {
				t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateInputInGroup")
			}
			ctx := context.Background()
			sc := groupSourceTestSchema(ctx)
			m := groupSourceTestModel()
			m.ID, m.InputUUID, m.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
			f := &groupSourceFixture{parent: true}
			resp := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, m)}
			(&groupSourceResource{client: &client{testRequester: f}}).Create(ctx, resource.CreateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &resp)
			if resp.Diagnostics.HasError() != interrupt {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			var got groupSourceModel
			groupFixtureRequireState(t, ctx, resp.State, &got)
			if got.InputUUID.ValueString() != groupFixtureInputUUID || got.ItemID.ValueInt64() != 12 {
				t.Fatalf("lost created identity: %#v", got)
			}
			if !interrupt && (got.SourceWidth.ValueInt64() != 0 || got.SourceHeight.ValueInt64() != 0) {
				t.Fatal("generic dimensions were not read back")
			}
		})
	}
}
func TestGroupSourceReadMissingSourceAndParentRecovery(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			ctx := context.Background()
			sc := groupSourceTestSchema(ctx)
			m := groupSourceTestModel()
			f := &groupSourceFixture{input: present, settings: map[string]any{"url": "https://example.invalid/overlay"}}
			state := groupFixtureSchemaAndState(t, ctx, sc, m)
			resp := resource.ReadResponse{State: state}
			(&groupSourceResource{client: &client{testRequester: f}}).Read(ctx, resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if !present {
				if !resp.State.Raw.IsNull() {
					t.Fatal("absent source retained")
				}
				return
			}
			var got groupSourceModel
			groupFixtureRequireState(t, ctx, resp.State, &got)
			if !got.GroupName.IsNull() || got.InputUUID.ValueString() != groupFixtureInputUUID {
				t.Fatal("surviving source identity not retained for recovery")
			}
		})
	}
}
func TestGroupSourceReadProjectsOnlyManagedSettings(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	m := groupSourceTestModel()
	f := &groupSourceFixture{input: true, child: true, parent: true, settings: map[string]any{"url": "https://example.invalid/drift", "unmanaged": "keep"}}
	state := groupFixtureSchemaAndState(t, ctx, sc, m)
	resp := resource.ReadResponse{State: state}
	(&groupSourceResource{client: &client{testRequester: f}}).Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	var got groupSourceModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.SettingsJSON.ValueString() != `{"url":"https://example.invalid/drift"}` {
		t.Fatal(got.SettingsJSON.ValueString())
	}
}
func TestGroupSourceUpdateRepairsPlacementAndPreservesUnmanagedSettings(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	m := groupSourceTestModel()
	old := m
	old.GroupName = types.StringNull()
	old.ItemID = types.Int64Value(9)
	f := &groupSourceFixture{input: true, parent: true, settings: map[string]any{"url": "old", "unmanaged": "keep"}}
	resp := resource.UpdateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, old)}
	(&groupSourceResource{client: &client{testRequester: f}}).Update(ctx, resource.UpdateRequest{State: groupFixtureSchemaAndState(t, ctx, sc, old), Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	if !f.child || f.settings["unmanaged"] != "keep" || f.settings["url"] != "https://example.invalid/overlay" {
		t.Fatalf("repair/overlay failed: %#v", f)
	}
}
func TestGroupSourceDeleteReferenceSafetyAndAbsence(t *testing.T) {
	for _, mode := range []string{"absent", "delete", "extra", "rebound"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			sc := groupSourceTestSchema(ctx)
			m := groupSourceTestModel()
			f := &groupSourceFixture{input: mode != "absent", parent: mode != "absent", child: mode != "absent", extraReference: mode == "extra", rebound: mode == "rebound", settings: map[string]any{}}
			resp := resource.DeleteResponse{}
			(&groupSourceResource{client: &client{testRequester: f}}).Delete(ctx, resource.DeleteRequest{State: groupFixtureSchemaAndState(t, ctx, sc, m)}, &resp)
			if resp.Diagnostics.HasError() != (mode == "extra" || mode == "rebound") {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if (mode == "extra" || mode == "rebound") && !f.input {
				t.Fatal("unsafe source deletion")
			}
			if mode == "delete" && f.input {
				t.Fatal("owned source was not removed")
			}
		})
	}
}
func TestGroupSourceCreateRetainsAbsoluteRuntimeGuard(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	m := groupSourceTestModel()
	f := &groupSourceFixture{parent: true, relative: true}
	resp := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, m)}
	(&groupSourceResource{client: &client{testRequester: f}}).Create(ctx, resource.CreateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &resp)
	if !resp.Diagnostics.HasError() || f.input {
		t.Fatal("relative-runtime mutation was not refused")
	}
	for _, call := range f.calls {
		if call.typ == "CallVendorRequest" && strings.Contains(stringValue(call.data["requestType"]), "CreateInput") {
			t.Fatal("native mutation sent")
		}
	}
}

func TestGroupSourceImportReadStartsWithNoManagedSettingKeys(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	imported := resource.ImportStateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, groupSourceModel{})}
	r := &groupSourceResource{}
	r.ImportState(ctx, resource.ImportStateRequest{ID: groupFixtureCollection + "|" + groupFixtureSceneUUID + "|" + groupFixtureGroupUUID + "|7|" + groupFixtureInputUUID + "|12"}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	f := &groupSourceFixture{input: true, child: true, parent: true, settings: map[string]any{"url": "private", "unmanaged": "keep"}}
	r.client = &client{testRequester: f}
	resp := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got groupSourceModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.SettingsJSON.ValueString() != "{}" || got.Name.ValueString() != groupFixtureInputName || got.Placement == nil {
		t.Fatalf("bad import read: %#v", got)
	}
}

func TestGroupSourceVersionedKindUsesCoreObservation(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	m := groupSourceTestModel()
	m.Kind = types.StringValue("color_source_v3")
	m.SettingsJSON = types.StringValue(`{"color":4294967295}`)
	f := &groupSourceFixture{parent: true, kind: "color_source_v3", bridgeKind: "color_source"}
	r := &groupSourceResource{client: &client{testRequester: f}}
	create := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, m)}
	r.Create(ctx, resource.CreateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var got groupSourceModel
	groupFixtureRequireState(t, ctx, read.State, &got)
	if got.Kind.ValueString() != "color_source_v3" {
		t.Fatalf("versioned kind lost: %s", got.Kind.ValueString())
	}
	m.SettingsJSON = types.StringValue(`{"color":4278190080}`)
	update := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	groupFixtureRequireState(t, ctx, update.State, &got)
	if got.Kind.ValueString() != "color_source_v3" {
		t.Fatal("update lost versioned kind")
	}
}

func TestGroupSourceRejectsWrongCoreVersionedKind(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	m := groupSourceTestModel()
	m.Kind = types.StringValue("color_source_v3")
	f := &groupSourceFixture{input: true, child: true, parent: true, kind: "color_source_v2", bridgeKind: "color_source", settings: map[string]any{}}
	r := &groupSourceResource{client: &client{testRequester: f}}
	state := groupFixtureSchemaAndState(t, ctx, sc, m)
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("Read accepted wrong versioned kind")
	}
	update := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{State: state, Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &update)
	if !update.Diagnostics.HasError() {
		t.Fatal("Update accepted wrong versioned kind")
	}
	if _, err := waitGroupSourceChild(ctx, f, m, 1, 0); err == nil {
		t.Fatal("placement postcondition accepted wrong versioned kind")
	}
}

func TestGroupSourceRepairMissingAnchorDoesNotAdoptSurvivingSibling(t *testing.T) {
	ctx := context.Background()
	sc := groupSourceTestSchema(ctx)
	m := groupSourceTestModel()
	old := m
	old.ItemID = types.Int64Value(9)
	old.GroupName = types.StringNull()
	f := &groupSourceFixture{input: true, parent: true, extraReference: true, settings: map[string]any{"url": "https://example.invalid/overlay"}}
	r := &groupSourceResource{client: &client{testRequester: f}}
	resp := resource.UpdateResponse{State: groupFixtureSchemaAndState(t, ctx, sc, old)}
	r.Update(ctx, resource.UpdateRequest{State: groupFixtureSchemaAndState(t, ctx, sc, old), Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got groupSourceModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.ItemID.ValueInt64() != 12 || got.InputUUID.ValueString() != groupFixtureInputUUID {
		t.Fatalf("repair adopted a sibling or lost source: %#v", got)
	}
	adds := 0
	for _, call := range f.calls {
		if call.typ == "CallVendorRequest" && stringValue(call.data["requestType"]) == "AddSourceToGroup" {
			adds++
		}
	}
	if adds != 1 || !f.child || !f.extraReference {
		t.Fatalf("repair must add one owned anchor and retain sibling: adds=%d fixture=%#v", adds, f)
	}
	// The old owned ID rebound to another source remains a refusal, even when
	// a sibling of the desired source is also present.
	f.rebound = true
	blocked := resource.UpdateResponse{State: resp.State}
	r.Update(ctx, resource.UpdateRequest{State: resp.State, Plan: groupFixtureSchemaAndPlan(t, ctx, sc, m)}, &blocked)
	if !blocked.Diagnostics.HasError() {
		t.Fatal("rebound owned item was accepted")
	}
}
