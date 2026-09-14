package tfprovider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Model deletion of the original scene while optionally retaining or replacing
// its group elsewhere. Failed observations must never be mistaken for absence.
type deletedGroupParentFake struct {
	base *groupItemFake
	mode string
}

func (f *deletedGroupParentFake) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if typ == "GetSceneList" {
		if f.mode == "failed-read" {
			return nil, fmt.Errorf("scene read failed")
		}
		ss := []any{}
		if f.mode == "surviving-group" || f.mode == "renamed-group" {
			ss = append(ss, map[string]any{"sceneName": "Other scene", "sceneUuid": "other-scene"})
		}
		return map[string]any{"scenes": ss}, nil
	}
	if typ == "GetSceneItemList" {
		name := lunaLifecycleGroupName
		if f.mode == "renamed-group" {
			name = "Renamed group"
		}
		it := itemObservation{SourceUUID: lunaLifecycleGroupUUID, SourceName: name, ItemID: 12, Transform: groupItemTransform()}
		return map[string]any{"sceneItems": []any{f.base.roster(it)}}, nil
	}
	if typ == "GetGroupList" {
		switch f.mode {
		case "malformed-list":
			return map[string]any{"groups": []any{nil}}, nil
		case "missing-list":
			return map[string]any{}, nil
		case "surviving-group", "name-conflict":
			return map[string]any{"groups": []any{lunaLifecycleGroupName}}, nil
		case "renamed-group":
			return map[string]any{"groups": []any{"Renamed group"}}, nil
		default:
			return map[string]any{"groups": []any{}}, nil
		}
	}
	if typ == "GetGroupSceneItemList" {
		return map[string]any{"sceneItems": []any{}}, nil
	}
	if typ == "CallVendorRequest" && data["requestType"] == "GetGroup" {
		return lunaLifecycleBridgeEnvelope("GetGroup", "", lunaLifecycleBridgeReply{outcome: "failed", code: "scene_not_found", message: "parent scene absent", postconditionVerified: true}), nil
	}
	return f.base.Req(ctx, typ, data)
}

func TestGroupRefreshAfterParentSceneDeletion(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"absent", "surviving-group", "renamed-group", "name-conflict", "failed-read", "malformed-list", "missing-list"} {
		t.Run(mode, func(t *testing.T) {
			f := &deletedGroupParentFake{base: newGroupItemFake(), mode: mode}
			r := &groupResource{client: &client{testRequester: f}}
			m := lunaLifecycleGroupModel(7, lunaLifecycleSceneName)
			state := lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), &m)
			read := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &read)
			wantError := mode != "absent"
			if read.Diagnostics.HasError() != wantError || read.State.Raw.IsNull() == wantError {
				t.Fatalf("read null=%v errors=%v", read.State.Raw.IsNull(), read.Diagnostics)
			}
			if wantError && !read.State.Raw.Equal(state.Raw) {
				t.Fatal("unproven absence changed state")
			}
			delete := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &delete)
			if delete.Diagnostics.HasError() != wantError {
				t.Fatal(delete.Diagnostics)
			}
			for _, call := range f.base.calls {
				if call.typ == "RemoveSceneItem" || (call.typ == "CallVendorRequest" && call.data["requestType"] != "GetCapabilities") {
					t.Fatalf("unexpected write %+v", call)
				}
			}
		})
	}
}

func TestGroupGraphRefreshAfterParentSceneDeletion(t *testing.T) {
	ctx := context.Background()
	f := &deletedGroupParentFake{base: newGroupItemFake(), mode: "absent"}
	r := &groupGraphResource{client: &client{testRequester: f}}
	m := groupGraphTestModel("/tmp/left.png", "/tmp/right.png")
	state := lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), &m)
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("graph refresh: %v", read.Diagnostics)
	}
}
