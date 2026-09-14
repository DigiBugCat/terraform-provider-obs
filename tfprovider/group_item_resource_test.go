package tfprovider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Protocol fake: these tests verify provider ownership and request routing, not
// libobs group geometry or live/native crash safety.
type groupItemFake struct {
	groupExists, duplicateParent, nested, relative bool
	sceneName, groupName, sourceName               string
	items                                          map[int64]itemObservation
	next                                           int64
	calls                                          []groupFixtureCall
	fail, bridgeOutcome                            string
}

func newGroupItemFake() *groupItemFake {
	return &groupItemFake{groupExists: true, sceneName: groupFixtureSceneName, groupName: groupFixtureGroupName, sourceName: groupFixtureInputName, items: map[int64]itemObservation{}, next: 20}
}
func groupItemTransform() transformModel {
	return transformFromNative(map[string]any{"positionX": 10., "positionY": 20., "scaleX": 1., "scaleY": 1., "rotation": 0., "alignment": 5., "boundsType": "OBS_BOUNDS_NONE"})
}
func (f *groupItemFake) roster(item itemObservation) map[string]any {
	return map[string]any{"sourceName": item.SourceName, "sourceUuid": item.SourceUUID, "sceneItemId": item.ItemID, "sceneItemIndex": item.Index, "sceneItemEnabled": item.Enabled, "sceneItemTransform": item.Transform.native()}
}
func (f *groupItemFake) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, groupFixtureCall{typ: typ, data: data})
	if f.fail == typ {
		return nil, fmt.Errorf("injected %s", typ)
	}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": groupFixtureCollection}, nil
	case "GetVersion":
		return map[string]any{"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "rpcVersion": float64(1), "availableRequests": []any{"CallVendorRequest", "CreateSceneItem", "RemoveSceneItem", "SetSceneItemEnabled", "SetSceneItemTransform", "SetSceneItemIndex", "GetStreamStatus"}}, nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920., "baseHeight": 1080.}, nil
	case "GetStreamStatus":
		return map[string]any{"outputActive": false}, nil
	case "GetSceneList":
		return map[string]any{"scenes": []any{map[string]any{"sceneName": f.sceneName, "sceneUuid": groupFixtureSceneUUID}}}, nil
	case "GetInputSettings":
		return map[string]any{"inputSettings": map[string]any{}, "inputKind": "image_source"}, nil
	case "GetInputList":
		return map[string]any{"inputs": []any{map[string]any{"inputName": f.sourceName, "inputUuid": groupFixtureInputUUID, "inputKind": "image_source"}}}, nil
	case "GetGroupList":
		groups := []any{}
		if f.groupExists {
			groups = append(groups, f.groupName)
		}
		return map[string]any{"groups": groups}, nil
	case "GetSceneItemList":
		if data["sceneName"] == f.groupName {
			return nil, fmt.Errorf("GetSceneItemList does not accept groups")
		}
		items := []any{}
		if f.groupExists {
			group := itemObservation{SourceName: f.groupName, SourceUUID: groupFixtureGroupUUID, ItemID: 7, Transform: groupItemTransform()}
			items = append(items, f.roster(group))
			if f.duplicateParent {
				group.ItemID = 8
				items = append(items, f.roster(group))
			}
		}
		for _, item := range f.items {
			if item.SceneName == f.sceneName {
				items = append(items, f.roster(item))
			}
		}
		return map[string]any{"sceneItems": items}, nil
	case "GetGroupSceneItemList":
		if data["sceneName"] != f.groupName {
			return nil, fmt.Errorf("wrong group name")
		}
		items := []any{}
		for _, item := range f.items {
			if item.SceneName == f.groupName {
				items = append(items, f.roster(item))
			}
		}
		if f.nested {
			items = append(items, f.roster(itemObservation{SourceName: f.groupName, SourceUUID: groupFixtureGroupUUID, ItemID: 99, Transform: groupItemTransform()}))
		}
		return map[string]any{"sceneItems": items}, nil
	case "CreateSceneItem":
		if data["sceneUuid"] != groupFixtureSceneUUID {
			return nil, fmt.Errorf("CreateSceneItem does not accept groups")
		}
		f.next++
		f.items[f.next] = itemObservation{SceneName: f.sceneName, SourceName: f.sourceName, SourceUUID: groupFixtureInputUUID, ItemID: f.next, Enabled: boolValue(data["sceneItemEnabled"]), Transform: groupItemTransform()}
		return map[string]any{"sceneItemId": f.next}, nil
	case "SetSceneItemTransform", "SetSceneItemEnabled", "SetSceneItemIndex":
		id := int64Value(data["sceneItemId"])
		it, ok := f.items[id]
		if !ok || data["sceneName"] != it.SceneName {
			return nil, fmt.Errorf("wrong item/container")
		}
		switch typ {
		case "SetSceneItemTransform":
			it.Transform = transformFromNative(mapValue(data["sceneItemTransform"]))
		case "SetSceneItemEnabled":
			it.Enabled = boolValue(data["sceneItemEnabled"])
		case "SetSceneItemIndex":
			it.Index = int64Value(data["sceneItemIndex"])
		}
		f.items[id] = it
		return map[string]any{}, nil
	case "RemoveSceneItem":
		if data["sceneUuid"] != groupFixtureSceneUUID {
			return nil, fmt.Errorf("RemoveSceneItem does not accept groups")
		}
		delete(f.items, int64Value(data["sceneItemId"]))
		return map[string]any{}, nil
	case "CallVendorRequest":
		kind := stringValue(data["requestType"])
		rd := mapValue(data["requestData"])
		result := map[string]any{}
		outcome := "completed"
		mutation := kind != "GetCapabilities" && kind != "GetGroup"
		switch kind {
		case "GetCapabilities":
			result = groupFixtureCapabilities([]string{"AddSourceToGroup", "GetGroup", "RemoveGroupChildPlacement"})
			if f.relative {
				result["absoluteCoordinates"] = false
				g := mapValue(result["absoluteRuntimeGuard"])
				g["absoluteCoordinates"] = false
				g["supported"] = false
				g["status"] = "relative_mode_unsupported"
				g["comparisonMode"] = "ineligible"
				mapValue(result["supports"])["absoluteGroupGraphMutations"] = false
			}
		case "AddSourceToGroup":
			if mapValue(rd["scene"])["sourceUuid"] != groupFixtureSceneUUID || mapValue(rd["group"])["sourceUuid"] != groupFixtureGroupUUID {
				return nil, fmt.Errorf("wrong bridge parent identities")
			}
			placement, err := bridgePlacementFromNative(rd["placement"])
			if err != nil {
				return nil, err
			}
			f.next++
			native, _ := placement.request()
			it := itemObservation{SceneName: f.groupName, SourceName: f.sourceName, SourceUUID: groupFixtureInputUUID, ItemID: f.next, Enabled: placement.Enabled.ValueBool(), Index: placement.Index.ValueInt64(), Transform: transformFromNative(mapValue(native["sceneItemTransform"]))}
			f.items[it.ItemID] = it
			result = groupFixtureChildResult(it.ItemID, it.SourceUUID, it.SourceName, placement)
			if f.bridgeOutcome != "" {
				outcome = f.bridgeOutcome
			}
		case "GetGroup":
			children := []any{}
			for _, it := range f.items {
				if it.SceneName != f.groupName {
					continue
				}
				m := groupItemModel(f, "group")
				m.Transform = &it.Transform
				m.Index = types.Int64Value(it.Index)
				m.Enabled = types.BoolValue(it.Enabled)
				raw, _ := sceneItemBridgePlacement(m)
				placement, _ := bridgePlacementFromNative(raw)
				children = append(children, groupFixtureChildResult(it.ItemID, it.SourceUUID, it.SourceName, placement))
			}
			result = groupFixtureGroupResult(7, groupFixtureGroupUUID, f.groupName, groupFixturePlacement(), children)
			result["scene"] = map[string]any{"sourceName": f.sceneName, "sourceUuid": groupFixtureSceneUUID}
		case "RemoveGroupChildPlacement":
			delete(f.items, int64Value(mapValue(rd["child"])["sceneItemId"]))
			result["sourceStillExists"] = true
		default:
			return nil, fmt.Errorf("unexpected bridge request %s", kind)
		}
		return groupFixtureBridgeEnvelope(kind, stringValue(rd["clientOperationId"]), groupFixtureBridgeReply{outcome: outcome, mutationAttempted: mutation, postconditionVerified: outcome != "uncertain", result: result}), nil
	}
	return nil, fmt.Errorf("unexpected request %s", typ)
}
func groupItemModel(f *groupItemFake, kind string) sceneItemModel {
	name, uuid := f.sceneName, groupFixtureSceneUUID
	if kind == "group" {
		name, uuid = f.groupName, groupFixtureGroupUUID
	}
	tr := groupItemTransform()
	return sceneItemModel{ID: types.StringUnknown(), Collection: types.StringValue(groupFixtureCollection), SceneName: types.StringValue(name), SceneUUID: types.StringValue(uuid), SourceName: types.StringValue(f.sourceName), SourceUUID: types.StringValue(groupFixtureInputUUID), ItemID: types.Int64Unknown(), Enabled: types.BoolValue(true), Index: types.Int64Value(1), Transform: &tr, ParentKind: types.StringValue(kind), AllowDuplicate: types.BoolValue(false)}
}
func groupItemSchema() schema.Schema {
	var resp resource.SchemaResponse
	(&sceneItemResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}
func groupItemState(t *testing.T, m sceneItemModel) tfsdk.State {
	return groupFixtureSchemaAndState(t, context.Background(), groupItemSchema(), m)
}
func groupItemPlan(t *testing.T, m sceneItemModel) tfsdk.Plan {
	return groupFixtureSchemaAndPlan(t, context.Background(), groupItemSchema(), m)
}
func groupItemCreate(t *testing.T, f *groupItemFake, m sceneItemModel) resource.CreateResponse {
	t.Helper()
	r := &sceneItemResource{client: &client{testRequester: f}}
	resp := resource.CreateResponse{State: groupItemState(t, m)}
	r.Create(context.Background(), resource.CreateRequest{Plan: groupItemPlan(t, m)}, &resp)
	return resp
}
func TestGroupItemCRUDAndSharedSourceOwnership(t *testing.T) {
	for _, kind := range []string{"scene", "group"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			f := newGroupItemFake()
			m := groupItemModel(f, kind)
			m.AllowDuplicate = types.BoolValue(true)
			f.items[11] = itemObservation{SceneName: m.SceneName.ValueString(), SourceName: f.sourceName, SourceUUID: groupFixtureInputUUID, ItemID: 11, Transform: groupItemTransform()}
			cr := groupItemCreate(t, f, m)
			if cr.Diagnostics.HasError() {
				t.Fatal(cr.Diagnostics)
			}
			var made sceneItemModel
			groupFixtureRequireState(t, ctx, cr.State, &made)
			if made.ItemID.ValueInt64() != 21 || len(f.items) != 2 {
				t.Fatalf("duplicate placement ownership lost: %+v", made)
			}
			r := &sceneItemResource{client: &client{testRequester: f}}
			rr := resource.ReadResponse{State: cr.State}
			r.Read(ctx, resource.ReadRequest{State: cr.State}, &rr)
			if rr.Diagnostics.HasError() {
				t.Fatal(rr.Diagnostics)
			}
			m.Transform.X = types.Float64Value(75)
			ur := resource.UpdateResponse{State: rr.State}
			r.Update(ctx, resource.UpdateRequest{Plan: groupItemPlan(t, m), State: rr.State}, &ur)
			if ur.Diagnostics.HasError() {
				t.Fatal(ur.Diagnostics)
			}
			if f.items[11].Transform.X.ValueFloat64() != 10 || f.items[21].Transform.X.ValueFloat64() != 75 {
				t.Fatal("updated sibling placement")
			}
			dr := resource.DeleteResponse{}
			r.Delete(ctx, resource.DeleteRequest{State: ur.State}, &dr)
			if dr.Diagnostics.HasError() {
				t.Fatal(dr.Diagnostics)
			}
			if len(f.items) != 1 || f.items[11].SourceUUID != groupFixtureInputUUID {
				t.Fatal("destroyed shared source/sibling")
			}
			for _, c := range f.calls {
				if c.typ == "CreateInput" || c.typ == "RemoveInput" {
					t.Fatal("placement must not own source lifecycle")
				}
			}
		})
	}
}
func TestGroupItemDuplicatesRequireOptIn(t *testing.T) {
	for _, kind := range []string{"scene", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newGroupItemFake()
			m := groupItemModel(f, kind)
			f.items[11] = itemObservation{SceneName: m.SceneName.ValueString(), SourceName: f.sourceName, SourceUUID: groupFixtureInputUUID, ItemID: 11, Transform: groupItemTransform()}
			cr := groupItemCreate(t, f, m)
			if !cr.Diagnostics.HasError() || !strings.Contains(cr.Diagnostics.Errors()[0].Detail(), "already has a placement") {
				t.Fatal(cr.Diagnostics)
			}
			if f.next != 20 {
				t.Fatal("created duplicate without opt in")
			}
		})
	}
}
func TestGroupItemReadDeletedAndRejectsRebound(t *testing.T) {
	for _, mode := range []string{"missing-item", "missing-group", "rebound-source", "renamed-group"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupItemFake()
			cr := groupItemCreate(t, f, groupItemModel(f, "group"))
			if cr.Diagnostics.HasError() {
				t.Fatal(cr.Diagnostics)
			}
			switch mode {
			case "missing-item":
				delete(f.items, 21)
			case "missing-group":
				f.groupExists = false
			case "rebound-source":
				it := f.items[21]
				it.SourceUUID = "replacement"
				f.items[21] = it
			case "renamed-group":
				f.groupName = "Renamed"
			}
			rr := resource.ReadResponse{State: cr.State}
			(&sceneItemResource{client: &client{testRequester: f}}).Read(context.Background(), resource.ReadRequest{State: cr.State}, &rr)
			if strings.HasPrefix(mode, "missing") {
				if rr.Diagnostics.HasError() || !rr.State.Raw.IsNull() {
					t.Fatalf("not removed from state: %v", rr.Diagnostics)
				}
			} else if !rr.Diagnostics.HasError() {
				t.Fatal("silently adopted changed identity")
			}
		})
	}
}
func TestGroupItemRejectsAmbiguousParentAndRelativeMutations(t *testing.T) {
	for _, mode := range []string{"duplicate-parent", "nested", "relative"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupItemFake()
			switch mode {
			case "duplicate-parent":
				f.duplicateParent = true
			case "nested":
				f.nested = true
			case "relative":
				f.relative = true
			}
			cr := groupItemCreate(t, f, groupItemModel(f, "group"))
			if !cr.Diagnostics.HasError() {
				t.Fatal("unsafe parent/runtime accepted")
			}
			if f.next != 20 {
				t.Fatal("native write sent before refusal")
			}
		})
	}
}
func TestGroupItemPartialCreateRetainsOnlyAuthoritativeIdentity(t *testing.T) {
	for _, mode := range []string{"set-failed", "authoritative-interrupt", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			f := newGroupItemFake()
			if mode == "set-failed" {
				f.fail = "SetSceneItemTransform"
			}
			if mode == "authoritative-interrupt" {
				t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:AddSourceToGroup")
			}
			if mode == "uncertain" {
				f.bridgeOutcome = "uncertain"
			}
			cr := groupItemCreate(t, f, groupItemModel(f, "group"))
			if !cr.Diagnostics.HasError() {
				t.Fatal("expected failure")
			}
			var got sceneItemModel
			groupFixtureRequireState(t, context.Background(), cr.State, &got)
			if mode == "uncertain" {
				if !got.ItemID.IsUnknown() {
					t.Fatal("adopted uncertain mutation identity")
				}
			} else if got.ItemID.ValueInt64() != 21 {
				t.Fatal("lost authoritative partial identity")
			}
		})
	}
}
func TestGroupItemImportAndLegacyDefaults(t *testing.T) {
	for _, kind := range []string{"scene", "group"} {
		t.Run(kind, func(t *testing.T) {
			s := groupItemSchema()
			resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(context.Background()), nil)}}
			id := containerSceneItemID("Lab", "parent", "source", 42, kind)
			(&sceneItemResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var m sceneItemModel
			groupFixtureRequireState(t, context.Background(), resp.State, &m)
			if m.ParentKind.ValueString() != kind || m.ItemID.ValueInt64() != 42 || m.AllowDuplicate.ValueBool() {
				t.Fatal("incorrect import identity")
			}
		})
	}
	f := newGroupItemFake()
	m := groupItemModel(f, "scene")
	m.ParentKind = types.StringNull()
	m.AllowDuplicate = types.BoolNull()
	cr := groupItemCreate(t, f, m)
	if cr.Diagnostics.HasError() {
		t.Fatal(cr.Diagnostics)
	}
}
