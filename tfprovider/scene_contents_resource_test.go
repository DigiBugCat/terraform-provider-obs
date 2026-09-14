package tfprovider

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Protocol fake only: verifies Terraform drift and placement ownership; not a
// qualification of native OBS group deletion or rendering.
type sceneContentsFake struct {
	*groupItemFake
	hook func(string, map[string]any)
}

func newSceneContentsFake(kind string, ids ...int64) *sceneContentsFake {
	f := &sceneContentsFake{groupItemFake: newGroupItemFake()}
	f.groupExists = kind == "group"
	name := f.sceneName
	if kind == "group" {
		name = f.groupName
	}
	for i, id := range ids {
		f.items[id] = itemObservation{SceneName: name, SourceName: "Shared source", SourceUUID: groupFixtureInputUUID, ItemID: id, Index: int64(i), Transform: groupItemTransform()}
	}
	return f
}
func (f *sceneContentsFake) Req(ctx context.Context, kind string, data map[string]any) (map[string]any, error) {
	if f.hook != nil {
		f.hook(kind, data)
	}
	if kind == "SetSceneItemIndex" {
		f.calls = append(f.calls, groupFixtureCall{typ: kind, data: data})
		id := int64Value(data["sceneItemId"])
		it, ok := f.items[id]
		if !ok {
			return nil, fmt.Errorf("missing placement")
		}
		index := int64Value(data["sceneItemIndex"])
		for k, other := range f.items {
			if other.SceneName != it.SceneName || k == id {
				continue
			}
			if it.Index > index && other.Index >= index && other.Index < it.Index {
				other.Index++
			}
			if it.Index < index && other.Index > it.Index && other.Index <= index {
				other.Index--
			}
			f.items[k] = other
		}
		it.Index = index
		f.items[id] = it
		return map[string]any{}, nil
	}
	out, err := f.groupItemFake.Req(ctx, kind, data)
	if kind == "RemoveSceneItem" || (kind == "CallVendorRequest" && data["requestType"] == "RemoveGroupChildPlacement") {
		byContainer := map[string][]itemObservation{}
		for _, it := range f.items {
			byContainer[it.SceneName] = append(byContainer[it.SceneName], it)
		}
		for _, items := range byContainer {
			sort.Slice(items, func(i, j int) bool { return items[i].Index < items[j].Index })
			for i, it := range items {
				it.Index = int64(i)
				f.items[it.ItemID] = it
			}
		}
	}
	return out, err
}
func sceneContentsTestModel(t *testing.T, f *sceneContentsFake, kind string, ids ...int64) sceneContentsModel {
	t.Helper()
	name, uuid := f.sceneName, groupFixtureSceneUUID
	if kind == "group" {
		name, uuid = f.groupName, groupFixtureGroupUUID
	}
	if ids == nil {
		ids = []int64{}
	}
	list, d := types.ListValueFrom(context.Background(), types.Int64Type, ids)
	if d.HasError() {
		t.Fatal(d)
	}
	return sceneContentsModel{ID: types.StringUnknown(), Collection: types.StringValue(groupFixtureCollection), SceneName: types.StringValue(name), SceneUUID: types.StringValue(uuid), ParentKind: types.StringValue(kind), ItemIDs: list, ItemSources: types.MapUnknown(types.StringType)}
}
func sceneContentsSchema() schema.Schema {
	var resp resource.SchemaResponse
	(&sceneContentsResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}
func TestSceneContentsPrunesAndReordersPlacementOnly(t *testing.T) {
	for _, kind := range []string{"scene", "group"} {
		t.Run(kind, func(t *testing.T) {
			f := newSceneContentsFake(kind, 31, 99, 32)
			m := sceneContentsTestModel(t, f, kind, 32, 31)
			// A placement in another container shares the source being pruned.
			f.items[100] = itemObservation{SceneName: "Other scene", SourceName: "Shared source", SourceUUID: groupFixtureInputUUID, ItemID: 100, Index: 0, Transform: groupItemTransform()}
			if err := reconcileSceneContents(context.Background(), f, &m, nil); err != nil {
				t.Fatal(err)
			}
			ids, err := sceneContentsIDs(context.Background(), m.ItemIDs)
			if err != nil || !reflect.DeepEqual(ids, []int64{32, 31}) {
				t.Fatalf("%v %v", ids, err)
			}
			if _, ok := f.items[99]; ok {
				t.Fatal("extra placement survived")
			}
			if _, ok := f.items[100]; !ok {
				t.Fatal("shared source placement elsewhere removed")
			}
			for _, call := range f.calls {
				if call.typ == "RemoveInput" || call.typ == "CreateInput" {
					t.Fatalf("source lifetime touched: %s", call.typ)
				}
			}
			f.calls = nil
			if err := reconcileSceneContents(context.Background(), f, &m, nil); err != nil {
				t.Fatal(err)
			}
			for _, call := range f.calls {
				if strings.HasPrefix(call.typ, "Set") || strings.HasPrefix(call.typ, "Remove") {
					t.Fatalf("no-op wrote %s", call.typ)
				}
			}
		})
	}
}
func TestSceneContentsEmptyManifestPrunesAll(t *testing.T) {
	f := newSceneContentsFake("scene", 31, 32)
	m := sceneContentsTestModel(t, f, "scene")
	if err := reconcileSceneContents(context.Background(), f, &m, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.items) != 0 {
		t.Fatal("roster not empty")
	}
}
func TestSceneContentsRefusesMissingDesiredBeforeAnyWrites(t *testing.T) {
	f := newSceneContentsFake("scene", 31, 99)
	m := sceneContentsTestModel(t, f, "scene", 31, 32)
	err := reconcileSceneContents(context.Background(), f, &m, nil)
	if err == nil || !strings.Contains(err.Error(), "desired placement 32 is absent") {
		t.Fatal(err)
	}
	if len(f.items) != 2 {
		t.Fatal("pruned before validating desired roster")
	}
}
func TestSceneContentsRefusesRosterRaceAndRebound(t *testing.T) {
	for _, change := range []string{"race", "identity", "container", "read"} {
		t.Run(change, func(t *testing.T) {
			f := newSceneContentsFake("scene", 31, 99)
			m := sceneContentsTestModel(t, f, "scene", 31)
			prior := map[string]string{}
			switch change {
			case "race":
				count := 0
				f.hook = func(kind string, _ map[string]any) {
					if kind == "GetSceneItemList" {
						count++
						if count == 2 {
							it := f.items[99]
							it.SourceUUID = "replacement"
							f.items[99] = it
						}
					}
				}
			case "identity":
				prior["99"] = "old-source"
			case "container":
				m.SceneName = types.StringValue("old scene name")
			case "read":
				f.fail = "GetSceneItemList"
			}
			if err := reconcileSceneContents(context.Background(), f, &m, prior); err == nil {
				t.Fatal("unsafe reconcile succeeded")
			}
			if len(f.items) != 2 {
				t.Fatal("wrote on failed precondition")
			}
		})
	}
}
func TestSceneContentsReadExposesAddedItemAndDeleteReleasesOnly(t *testing.T) {
	ctx := context.Background()
	f := newSceneContentsFake("scene", 31)
	m := sceneContentsTestModel(t, f, "scene", 31)
	if err := reconcileSceneContents(ctx, f, &m, nil); err != nil {
		t.Fatal(err)
	}
	before := groupFixtureSchemaAndState(t, ctx, sceneContentsSchema(), m)
	f.items[99] = itemObservation{SceneName: f.sceneName, SourceName: "Manual", SourceUUID: "manual-source", ItemID: 99, Index: 1, Transform: groupItemTransform()}
	r := &sceneContentsResource{client: &client{testRequester: f}}
	resp := resource.ReadResponse{State: before}
	r.Read(ctx, resource.ReadRequest{State: before}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var observed sceneContentsModel
	if d := resp.State.Get(ctx, &observed); d.HasError() {
		t.Fatal(d)
	}
	ids, err := sceneContentsIDs(ctx, observed.ItemIDs)
	if err != nil || !reflect.DeepEqual(ids, []int64{31, 99}) {
		t.Fatalf("refresh hid drift: %v %v", ids, err)
	}
	update := resource.UpdateResponse{State: resp.State}
	r.Update(ctx, resource.UpdateRequest{State: resp.State, Plan: groupFixtureSchemaAndPlan(t, ctx, sceneContentsSchema(), m)}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	if _, ok := f.items[99]; ok {
		t.Fatal("apply did not prune manual placement")
	}
	calls := len(f.calls)
	deletion := resource.DeleteResponse{State: update.State}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &deletion)
	if len(f.calls) != calls || len(f.items) != 1 {
		t.Fatal("release changed OBS")
	}
}
func TestSceneContentsReadMissingContainerRemovesResource(t *testing.T) {
	ctx := context.Background()
	f := newSceneContentsFake("group", 31)
	m := sceneContentsTestModel(t, f, "group", 31)
	if err := reconcileSceneContents(ctx, f, &m, nil); err != nil {
		t.Fatal(err)
	}
	state := groupFixtureSchemaAndState(t, ctx, sceneContentsSchema(), m)
	f.groupExists = false
	f.items = map[int64]itemObservation{}
	resp := resource.ReadResponse{State: state}
	(&sceneContentsResource{client: &client{testRequester: f}}).Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Fatalf("missing container not removed: %s", resp.Diagnostics)
	}
}
func TestSceneContentsRejectsUnknownAndDuplicateIDs(t *testing.T) {
	for _, value := range []types.List{types.ListUnknown(types.Int64Type), types.ListNull(types.Int64Type), types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Unknown()}), types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Value(3), types.Int64Value(3)})} {
		if _, err := sceneContentsIDs(context.Background(), value); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}
