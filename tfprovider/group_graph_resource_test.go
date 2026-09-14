package tfprovider

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type groupGraphFakeChild struct {
	name, uuid, kind, file string
	unload                 bool
	itemID, width, height  int64
	placement              bridgePlacementModel
}

type groupGraphFakeOBS struct {
	mu                sync.Mutex
	calls             []lunaLifecycleCall
	groupExists       bool
	groupUUID         string
	parentItemID      int64
	parentPlacement   bridgePlacementModel
	children          []groupGraphFakeChild
	detached          []groupGraphFakeChild
	nextChildID       int64
	failScreenshot    bool
	failScreenshotAt  int
	screenshotCount   int
	activeOutput      string
	capabilityCalls   int
	capabilityFailAt  int
	capabilityUnknown bool
	rebaseAfterRemove bool
	pendingRebase     bool
}

func (f *groupGraphFakeOBS) Req(_ context.Context, typ string, data map[string]any) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, lunaLifecycleCall{typ: typ, data: data})
	allRequests := []string{"CallVendorRequest", "GetSourceScreenshot", "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemTransform", "SetSceneItemIndex", "SetInputSettings", "CreateSceneItem", "RemoveSceneItem", "GetStreamStatus", "GetRecordStatus", "GetVirtualCamStatus", "GetReplayBufferStatus"}
	switch typ {
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}, nil
	case "GetSceneList":
		return lunaLifecycleSceneList(), nil
	case "GetGroupList":
		groups := []any{}
		if f.groupExists {
			groups = append(groups, lunaLifecycleGroupName)
		}
		return map[string]any{"groups": groups}, nil
	case "GetVersion":
		return lunaLifecycleVersion(allRequests...), nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}, nil
	case "GetStreamStatus", "GetRecordStatus", "GetVirtualCamStatus", "GetReplayBufferStatus":
		return map[string]any{"outputActive": f.activeOutput == typ}, nil
	case "GetInputList":
		values := make([]any, 0, len(f.children)+len(f.detached))
		seen := map[string]bool{}
		for _, child := range append(append([]groupGraphFakeChild(nil), f.children...), f.detached...) {
			if seen[child.uuid] {
				continue
			}
			seen[child.uuid] = true
			values = append(values, map[string]any{"inputName": child.name, "inputUuid": child.uuid, "inputKind": child.kind})
		}
		return map[string]any{"inputs": values}, nil
	case "GetInputSettings":
		uuid := stringValue(data["inputUuid"])
		for _, child := range append(append([]groupGraphFakeChild(nil), f.children...), f.detached...) {
			if child.uuid == uuid {
				return map[string]any{"inputKind": child.kind, "inputSettings": map[string]any{"file": child.file, "unload": child.unload}}, nil
			}
		}
		return nil, fmt.Errorf("unknown input %s", uuid)
	case "GetInputDefaultSettings":
		return lunaLifecycleDefaultImageSettings(), nil
	case "GetSourceScreenshot":
		f.screenshotCount++
		if stringValue(data["sourceUuid"]) != lunaLifecycleSceneUUID {
			return nil, fmt.Errorf("screenshot source is not exact parent scene")
		}
		if f.failScreenshot || (f.failScreenshotAt > 0 && f.screenshotCount == f.failScreenshotAt) {
			return nil, fmt.Errorf("injected screenshot failure")
		}
		if f.pendingRebase {
			for i := range f.children {
				f.children[i].placement.Transform.X = types.Float64Value(0)
			}
			f.parentPlacement.Transform.X = types.Float64Value(960)
			f.pendingRebase = false
		}
		return map[string]any{"imageData": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{1, 2, 3})}, nil
	case "GetSourceFilterList":
		return map[string]any{"filters": []any{}}, nil
	case "GetSceneItemList":
		items := []any{}
		if f.groupExists && f.parentItemID > 0 {
			p, _ := f.parentPlacement.request()
			items = append(items, map[string]any{"sourceName": lunaLifecycleGroupName, "sourceUuid": f.groupUUID, "sceneItemId": float64(f.parentItemID), "sceneItemIndex": float64(f.parentPlacement.Index.ValueInt64()), "sceneItemEnabled": f.parentPlacement.Enabled.ValueBool(), "sceneItemTransform": p["sceneItemTransform"]})
		}
		return map[string]any{"sceneItems": items}, nil
	case "GetGroupSceneItemList":
		items := make([]any, 0, len(f.children))
		for _, child := range f.children {
			p, _ := child.placement.request()
			items = append(items, map[string]any{"sourceName": child.name, "sourceUuid": child.uuid, "sceneItemId": float64(child.itemID), "sceneItemIndex": float64(child.placement.Index.ValueInt64()), "sceneItemEnabled": child.placement.Enabled.ValueBool(), "sceneItemTransform": p["sceneItemTransform"]})
		}
		return map[string]any{"sceneItems": items}, nil
	case "SetInputSettings":
		uuid := stringValue(data["inputUuid"])
		settings := mapValue(data["inputSettings"])
		for i := range f.children {
			if f.children[i].uuid == uuid {
				f.children[i].file, f.children[i].unload = stringValue(settings["file"]), boolValue(settings["unload"])
				return map[string]any{}, nil
			}
		}
		for i := range f.detached {
			if f.detached[i].uuid == uuid {
				f.detached[i].file, f.detached[i].unload = stringValue(settings["file"]), boolValue(settings["unload"])
				return map[string]any{}, nil
			}
		}
		return nil, fmt.Errorf("unknown input %s", uuid)
	case "SetSceneItemTransform":
		itemID := int64Value(data["sceneItemId"])
		tr := mapValue(data["sceneItemTransform"])
		if stringValue(data["sceneName"]) == lunaLifecycleSceneName && itemID == f.parentItemID {
			merged, err := groupGraphFakeCoreTransform(f.parentPlacement.Transform, tr)
			if err != nil {
				return nil, err
			}
			f.parentPlacement.Transform = merged
			return map[string]any{}, nil
		}
		for i := range f.children {
			if f.children[i].itemID == itemID {
				merged, err := groupGraphFakeCoreTransform(f.children[i].placement.Transform, tr)
				if err != nil {
					return nil, err
				}
				f.children[i].placement.Transform = merged
				return map[string]any{}, nil
			}
		}
		return nil, fmt.Errorf("unknown item %d", itemID)
	case "SetSceneItemEnabled", "SetSceneItemLocked", "SetSceneItemIndex":
		itemID := int64Value(data["sceneItemId"])
		if stringValue(data["sceneName"]) == lunaLifecycleSceneName && itemID == f.parentItemID {
			switch typ {
			case "SetSceneItemEnabled":
				f.parentPlacement.Enabled = types.BoolValue(boolValue(data["sceneItemEnabled"]))
			case "SetSceneItemLocked":
				f.parentPlacement.Locked = types.BoolValue(boolValue(data["sceneItemLocked"]))
			case "SetSceneItemIndex":
				f.parentPlacement.Index = types.Int64Value(int64Value(data["sceneItemIndex"]))
			}
			return map[string]any{}, nil
		}
		for i := range f.children {
			if f.children[i].itemID != itemID {
				continue
			}
			switch typ {
			case "SetSceneItemEnabled":
				f.children[i].placement.Enabled = types.BoolValue(boolValue(data["sceneItemEnabled"]))
			case "SetSceneItemLocked":
				f.children[i].placement.Locked = types.BoolValue(boolValue(data["sceneItemLocked"]))
			case "SetSceneItemIndex":
				oldIndex, newIndex := f.children[i].placement.Index.ValueInt64(), int64Value(data["sceneItemIndex"])
				observed := make([]bridgeChildObservation, len(f.children))
				for j := range f.children {
					observed[j].Placement = f.children[j].placement
				}
				applyObservedGroupIndexMove(observed, i, oldIndex, newIndex)
				for j := range f.children {
					f.children[j].placement.Index = observed[j].Placement.Index
				}
				f.children[i].placement.Index = types.Int64Value(newIndex)
			}
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("unknown item %d", itemID)
	case "RemoveSceneItem":
		if int64Value(data["sceneItemId"]) != f.parentItemID {
			return nil, fmt.Errorf("unknown parent item")
		}
		f.groupExists = false
		return map[string]any{}, nil
	case "CreateSceneItem":
		if stringValue(data["sourceUuid"]) != f.groupUUID || !f.groupExists || f.parentItemID > 0 {
			return nil, fmt.Errorf("group source is unavailable or already anchored")
		}
		f.parentItemID = 99
		return map[string]any{"sceneItemId": 99.0}, nil
	case "CallVendorRequest":
		return f.vendor(data)
	default:
		return nil, fmt.Errorf("unexpected %s", typ)
	}
}

// groupGraphFakeCoreTransform applies obs-websocket 5.7.4's partial update
// semantics and its real lower bound for explicitly supplied bounds dimensions.
// Omitted fields retain the current native value.
func groupGraphFakeCoreTransform(current *bridgeTransformModel, update map[string]any) (*bridgeTransformModel, error) {
	for _, key := range []string{"boundsWidth", "boundsHeight"} {
		if value, present := update[key]; present {
			if number, ok := numberValue(value); !ok || number < 1 {
				return nil, fmt.Errorf("obs-websocket 5.7.4 schema: %s is below minimum 1", key)
			}
		}
	}
	base := bridgePlacementModel{Enabled: types.BoolValue(true), Locked: types.BoolValue(false), Index: types.Int64Value(0), Transform: current}
	native, err := base.request()
	if err != nil {
		return nil, err
	}
	merged := mapValue(native["sceneItemTransform"])
	for key, value := range update {
		merged[key] = value
	}
	parsed, err := bridgePlacementFromNative(native)
	if err != nil {
		return nil, err
	}
	return parsed.Transform, nil
}

func (f *groupGraphFakeOBS) vendor(data map[string]any) (map[string]any, error) {
	typ := stringValue(data["requestType"])
	req := mapValue(data["requestData"])
	operationID := stringValue(req["clientOperationId"])
	reply := lunaLifecycleBridgeReply{outcome: "completed", mutationAttempted: operationID != "", postconditionVerified: true}
	switch typ {
	case "GetCapabilities":
		f.capabilityCalls++
		reply.mutationAttempted = false
		reply.result = lunaLifecycleCapabilities([]string{"GetGroup", "CreateGroup", "CreateInputInGroup", "AddSourceToGroup", "RemoveGroupChildPlacement"})
		if f.capabilityCalls == f.capabilityFailAt {
			if f.capabilityUnknown {
				delete(reply.result, "streamingActive")
			} else {
				reply.result["streamingActive"] = true
			}
		}
		if f.activeOutput != "" {
			key := map[string]string{"GetStreamStatus": "streamingActive", "GetRecordStatus": "recordingActive", "GetReplayBufferStatus": "replayBufferActive", "GetVirtualCamStatus": "virtualCameraActive"}[f.activeOutput]
			reply.result[key] = true
		}
	case "GetGroup":
		reply.mutationAttempted = false
		groupIdentity := mapValue(req["group"])
		if !f.groupExists {
			reply.outcome, reply.code, reply.result = "failed", "group_absent", nil
		} else if f.parentItemID <= 0 || int64Value(groupIdentity["sceneItemId"]) != f.parentItemID {
			reply.outcome, reply.code, reply.message, reply.result = "failed", "ambiguous_group_item_id", "parent anchor absent", nil
		} else {
			reply.result = f.groupResult()
		}
	case "CreateGroup":
		f.groupExists, f.groupUUID, f.parentItemID = true, lunaLifecycleGroupUUID, 7
		f.parentPlacement, _ = bridgePlacementFromNative(mapValue(req["placement"]))
		reply.result = f.groupResult()
	case "CreateInputInGroup":
		f.nextChildID++
		name := stringValue(req["inputName"])
		uuid := "input-" + fmt.Sprintf("%d", f.nextChildID)
		if name == "Party Light Left" {
			uuid = "input-left-new"
		} else if name == "Party Light Right" {
			uuid = "input-right"
		}
		placement, _ := bridgePlacementFromNative(mapValue(req["placement"]))
		settings := mapValue(req["inputSettings"])
		child := groupGraphFakeChild{name: name, uuid: uuid, kind: stringValue(req["inputKind"]), file: stringValue(settings["file"]), unload: boolValue(settings["unload"]), itemID: f.nextChildID, width: 960, height: 1080, placement: placement}
		f.children = append(f.children, child)
		reply.result = f.childResult(child)
	case "AddSourceToGroup":
		source := mapValue(req["source"])
		found := -1
		for i := range f.detached {
			if f.detached[i].uuid == stringValue(source["sourceUuid"]) && f.detached[i].name == stringValue(source["sourceName"]) {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("detached source unavailable")
		}
		f.nextChildID++
		child := f.detached[found]
		child.itemID = f.nextChildID
		child.placement, _ = bridgePlacementFromNative(mapValue(req["placement"]))
		f.detached = append(f.detached[:found], f.detached[found+1:]...)
		f.children = append(f.children, child)
		reply.result = f.childResult(child)
	case "RemoveGroupChildPlacement":
		childIdentity := mapValue(req["child"])
		itemID := int64Value(childIdentity["sceneItemId"])
		found := -1
		for i := range f.children {
			if f.children[i].itemID == itemID && f.children[i].uuid == stringValue(childIdentity["sourceUuid"]) {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("unknown removal child")
		}
		f.children = append(f.children[:found], f.children[found+1:]...)
		if f.rebaseAfterRemove && len(f.children) == 1 {
			f.pendingRebase = true
		}
		reply.result = map[string]any{"sourceStillExists": false}
	default:
		return nil, fmt.Errorf("unexpected vendor request %s", typ)
	}
	return lunaLifecycleBridgeEnvelope(typ, operationID, reply), nil
}

func (f *groupGraphFakeOBS) childResult(child groupGraphFakeChild) map[string]any {
	p, _ := child.placement.request()
	return map[string]any{"sourceName": child.name, "sourceUuid": child.uuid, "inputKind": child.kind, "sceneItemId": float64(child.itemID), "sourceWidth": float64(child.width), "sourceHeight": float64(child.height), "placement": p}
}

func (f *groupGraphFakeOBS) groupResult() map[string]any {
	p, _ := f.parentPlacement.request()
	ordered := append([]groupGraphFakeChild(nil), f.children...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].placement.Index.ValueInt64() < ordered[j].placement.Index.ValueInt64()
	})
	children := make([]any, 0, len(ordered))
	maxX, maxY := float64(0), float64(0)
	for _, child := range ordered {
		children = append(children, f.childResult(child))
		x := child.placement.Transform.X.ValueFloat64() + float64(child.width)*child.placement.Transform.ScaleX.ValueFloat64()
		y := child.placement.Transform.Y.ValueFloat64() + float64(child.height)*child.placement.Transform.ScaleY.ValueFloat64()
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	return map[string]any{
		"scene":    map[string]any{"sourceName": lunaLifecycleSceneName, "sourceUuid": lunaLifecycleSceneUUID},
		"group":    map[string]any{"sourceName": lunaLifecycleGroupName, "sourceUuid": f.groupUUID, "sceneItemId": float64(f.parentItemID), "groupWidth": maxX, "groupHeight": maxY, "placement": p},
		"children": children,
	}
}

func groupGraphSignificantCalls(f *groupGraphFakeOBS) []string {
	var out []string
	for _, call := range f.calls {
		if call.typ == "GetSourceScreenshot" || call.typ == "SetSceneItemTransform" {
			out = append(out, call.typ)
		}
		if call.typ == "CallVendorRequest" {
			typ := stringValue(call.data["requestType"])
			if typ != "GetCapabilities" {
				out = append(out, typ)
			}
		}
	}
	return out
}

func TestGroupGraphCreateCheckpointsAndFramesEveryStructuralMutation(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	model := groupGraphTestModel(left, right)
	model.ID, model.GroupUUID, model.ParentItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	model.ObservedComplete, model.ObservedGroupWidth, model.ObservedGroupHeight, model.RepairNonce = types.BoolUnknown(), types.Int64Unknown(), types.Int64Unknown(), types.StringUnknown()
	for i := range model.Children {
		model.Children[i].SourceUUID, model.Children[i].ItemID = types.StringUnknown(), types.Int64Unknown()
	}
	fake := &groupGraphFakeOBS{parentPlacement: *model.ParentPlacement, nextChildID: 0}
	sc := groupGraphSchema()
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, model)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Create(ctx, resource.CreateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, model)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v; calls=%v", response.Diagnostics, groupGraphSignificantCalls(fake))
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if !got.ObservedComplete.ValueBool() || got.GroupUUID.ValueString() != lunaLifecycleGroupUUID || got.Children[0].SourceUUID.ValueString() != "input-left-new" || got.Children[1].SourceUUID.ValueString() != "input-right" {
		t.Fatalf("final state=%#v", got)
	}
	want := []string{"CreateGroup", "GetSourceScreenshot", "GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup", "CreateInputInGroup", "GetSourceScreenshot", "GetGroup"}
	gotCalls := groupGraphSignificantCalls(fake)
	if len(gotCalls) < len(want) {
		t.Fatalf("significant calls=%v want prefix=%v", gotCalls, want)
	}
	for i := range want {
		if gotCalls[i] != want[i] {
			t.Fatalf("significant calls=%v want prefix=%v", gotCalls, want)
		}
	}
}

func TestGroupGraphCreateReachesMutationWithWhollyUnknownComputedObservations(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	model := groupGraphTestModel(left, right)
	model.ID, model.GroupUUID, model.ParentItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	model.ObservedComplete, model.ObservedGroupWidth, model.ObservedGroupHeight, model.RepairNonce = types.BoolUnknown(), types.Int64Unknown(), types.Int64Unknown(), types.StringUnknown()
	for i := range model.Children {
		model.Children[i].SourceUUID, model.Children[i].ItemID = types.StringUnknown(), types.Int64Unknown()
	}
	fake := &groupGraphFakeOBS{parentPlacement: *model.ParentPlacement}
	plan := lunaLifecycleSchemaAndPlan(t, ctx, groupGraphSchema(), groupGraphUnknownObservationPlanModel(t, model))
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), model)}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:CreateGroup")
	(&groupGraphResource{client: &client{testRequester: fake}}).Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "injected interruption") {
		t.Fatalf("create diagnostics=%v, want authoritative post-CreateGroup interruption", response.Diagnostics)
	}
	if !fake.groupExists {
		t.Fatal("CreateGroup was not reached after unknown observation decode")
	}
	var createCalls int
	for _, call := range fake.calls {
		if call.typ == "CallVendorRequest" && stringValue(call.data["requestType"]) == "CreateGroup" {
			createCalls++
		}
	}
	if createCalls != 1 {
		t.Fatalf("CreateGroup calls=%d, want 1; calls=%v", createCalls, groupGraphSignificantCalls(fake))
	}
}

func TestGroupGraphRepairCompensatesSurvivorThenParentAndSecondPassWritesZero(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	state.ObservedComplete = types.BoolValue(false)
	parentDrift := groupGraphTestPlacement(960, 0)
	rightDrift := groupGraphTestPlacement(0, 1)
	state.ParentPlacement = &parentDrift
	state.Children[1].Placement = &rightDrift
	fake := &groupGraphFakeOBS{
		groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: parentDrift, nextChildID: 2,
		children: []groupGraphFakeChild{{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: rightDrift}},
	}
	plan := groupGraphTestModel(left, right)
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	r := &groupGraphResource{client: &client{testRequester: fake}}
	r.Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("repair diagnostics: %v; calls=%v", response.Diagnostics, groupGraphSignificantCalls(fake))
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.Children[0].SourceUUID.ValueString() != "input-left-new" || got.Children[1].SourceUUID.ValueString() != "input-right" || got.Children[1].ItemID.ValueInt64() != 2 || got.ParentItemID.ValueInt64() != 7 || !got.ObservedComplete.ValueBool() {
		t.Fatalf("repaired state=%#v", got)
	}
	var transforms []string
	for _, call := range fake.calls {
		if call.typ == "SetSceneItemTransform" {
			transforms = append(transforms, fmt.Sprintf("%s/%d", stringValue(call.data["sceneName"]), int64Value(call.data["sceneItemId"])))
		}
	}
	if fmt.Sprint(transforms) != fmt.Sprint([]string{lunaLifecycleGroupName + "/2", lunaLifecycleSceneName + "/7"}) {
		t.Fatalf("transform order=%v", transforms)
	}
	before := len(fake.calls)
	second := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, got)}
	r.Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, got), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, got)}, &second)
	if second.Diagnostics.HasError() {
		t.Fatalf("second reconcile diagnostics: %v", second.Diagnostics)
	}
	for _, call := range fake.calls[before:] {
		if call.typ == "SetSceneItemTransform" || call.typ == "SetInputSettings" || (call.typ == "CallVendorRequest" && stringValue(call.data["requestData"].(map[string]any)["clientOperationId"]) != "") {
			t.Fatalf("second reconciliation sent write: %#v", call)
		}
	}
}

func TestGroupGraphExtraChildRefusesBeforeMembership(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	plan := groupGraphTestModel(left, right)
	rightPlacement := *plan.Children[1].Placement
	extraPlacement := groupGraphTestPlacement(300, 2)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *plan.ParentPlacement, nextChildID: 3, children: []groupGraphFakeChild{
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: rightPlacement},
		{name: "Unmanaged", uuid: "input-extra", kind: "image_source", file: right, itemID: 3, width: 10, height: 10, placement: extraPlacement},
	}}
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected extra-child refusal")
	}
	for _, call := range fake.calls {
		if call.typ == "CallVendorRequest" {
			typ := stringValue(call.data["requestType"])
			if typ == "CreateInputInGroup" || typ == "AddSourceToGroup" {
				t.Fatalf("membership mutation sent after extra-child observation: %s", typ)
			}
		}
	}
}

func TestGroupGraphReadSchedulesExplicitMissingChildRepair(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	parentDrift := groupGraphTestPlacement(960, 0)
	rightDrift := groupGraphTestPlacement(0, 1)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: parentDrift, children: []groupGraphFakeChild{
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: rightDrift},
	}}
	sc := groupGraphSchema()
	response := resource.ReadResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Read(ctx, resource.ReadRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.Children[0].Present.ValueBool() || got.Children[0].SourceUUID.ValueString() != "input-left" || got.Children[0].ItemID.ValueInt64() != 1 || got.ObservedComplete.ValueBool() {
		t.Fatalf("missing child state=%#v", got.Children[0])
	}
	if got.ParentPlacement.Transform.X.ValueFloat64() != 960 || got.Children[1].Placement.Transform.X.ValueFloat64() != 0 {
		t.Fatalf("rebased actual placement was not recorded")
	}
}

func TestGroupGraphFrameFailureStopsAfterCheckpointedGroup(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	model := groupGraphTestModel(left, right)
	model.ID, model.GroupUUID, model.ParentItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	for i := range model.Children {
		model.Children[i].SourceUUID, model.Children[i].ItemID = types.StringUnknown(), types.Int64Unknown()
	}
	fake := &groupGraphFakeOBS{parentPlacement: *model.ParentPlacement, failScreenshot: true}
	sc := groupGraphSchema()
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, model)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Create(ctx, resource.CreateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, model)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected frame failure")
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != lunaLifecycleGroupUUID || got.ParentItemID.ValueInt64() != 7 {
		t.Fatalf("created group identity was not checkpointed: %#v", got)
	}
	if got.Children[0].Present.ValueBool() || got.Children[1].Present.ValueBool() {
		t.Fatalf("uncreated children were falsely checkpointed present: %#v", got.Children)
	}
	if count := lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateInputInGroup"); count != 0 {
		t.Fatalf("child creation continued after frame failure: %d", count)
	}
}

func TestGroupGraphFirstChildBarrierFailureKeepsOnlyCreatedPrefixPresent(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	model := groupGraphTestModel(left, right)
	model.ID, model.GroupUUID, model.ParentItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	for i := range model.Children {
		model.Children[i].SourceUUID, model.Children[i].ItemID = types.StringUnknown(), types.Int64Unknown()
	}
	fake := &groupGraphFakeOBS{parentPlacement: *model.ParentPlacement, failScreenshotAt: 2}
	sc := groupGraphSchema()
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, model)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Create(ctx, resource.CreateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, model)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected first-child frame failure")
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if !got.ParentPresent.ValueBool() || !got.Children[0].Present.ValueBool() || got.Children[0].SourceUUID.ValueString() != "input-left-new" || got.Children[1].Present.ValueBool() || !got.Children[1].SourceUUID.IsUnknown() {
		t.Fatalf("partial create prefix state=%#v", got)
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateInputInGroup") != 1 {
		t.Fatal("second child creation continued after failed first-child barrier")
	}
}

func TestGroupGraphOutputBecomingActiveRefusesBeforeMembership(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	parentDrift := groupGraphTestPlacement(960, 0)
	rightDrift := groupGraphTestPlacement(0, 1)
	state.ParentPlacement, state.Children[1].Placement = &parentDrift, &rightDrift
	plan := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: parentDrift, activeOutput: "GetStreamStatus", children: []groupGraphFakeChild{
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: rightDrift},
	}}
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected active-output refusal")
	}
	if count := lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateInputInGroup"); count != 0 {
		t.Fatalf("membership sent while output active: %d", count)
	}
}

func TestGroupGraphOutputBecomingUnsafeRefusesAtSetterPhaseBoundary(t *testing.T) {
	for _, tc := range []struct {
		name             string
		capabilityFailAt int
		unknown          bool
		wantSetters      int
	}{
		{name: "active before child setter", capabilityFailAt: 2, wantSetters: 0},
		{name: "unknown before child setter", capabilityFailAt: 2, unknown: true, wantSetters: 0},
		{name: "active before parent setter", capabilityFailAt: 4, wantSetters: 1},
		{name: "unknown before parent setter", capabilityFailAt: 4, unknown: true, wantSetters: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
			state := groupGraphTestModel(left, right)
			state.Children[0].Present = types.BoolValue(false)
			state.ObservedComplete = types.BoolValue(false)
			parentDrift := groupGraphTestPlacement(960, 0)
			rightDrift := groupGraphTestPlacement(0, 1)
			state.ParentPlacement, state.Children[1].Placement = &parentDrift, &rightDrift
			plan := groupGraphTestModel(left, right)
			fake := &groupGraphFakeOBS{
				groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: parentDrift, nextChildID: 2,
				capabilityFailAt: tc.capabilityFailAt, capabilityUnknown: tc.unknown,
				children: []groupGraphFakeChild{{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: rightDrift}},
			}
			sc := groupGraphSchema()
			response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}
			(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("unsafe phase output was accepted")
			}
			if got := lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "SetSceneItemTransform"); got != tc.wantSetters {
				t.Fatalf("setter count=%d want %d; calls=%#v", got, tc.wantSetters, fake.calls)
			}
			for _, call := range fake.calls {
				if call.typ == "SetSceneItemTransform" && int64Value(call.data["sceneItemId"]) == 7 {
					t.Fatal("parent setter was sent after an unsafe phase output")
				}
			}
			var partial groupGraphModel
			lunaLifecycleRequireState(t, ctx, response.State, &partial)
			if partial.ParentPlacement.Transform.X.ValueFloat64() != 960 {
				t.Fatalf("unexecuted parent was not retained at actual X=960: %#v", partial.ParentPlacement)
			}
			leftIndex, rightIndex := groupGraphChildIndexByStableKey(partial.Children, "left"), groupGraphChildIndexByStableKey(partial.Children, "right")
			if leftIndex < 0 || partial.Children[leftIndex].SourceUUID.ValueString() != "input-left-new" || partial.Children[leftIndex].ItemID.ValueInt64() != 3 {
				t.Fatalf("completed replacement membership was not retained: %#v", partial.Children)
			}
			wantRightX := float64(0)
			if tc.wantSetters == 1 {
				wantRightX = 960
			}
			if rightIndex < 0 || partial.Children[rightIndex].Placement.Transform.X.ValueFloat64() != wantRightX {
				t.Fatalf("completed phase prefix right X=%v want %v", partial.Children[rightIndex].Placement.Transform.X.ValueFloat64(), wantRightX)
			}
		})
	}
}

func TestGroupGraphDeleteFramesEachRemovalBeforeNextExpectedPlacement(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	m := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *m.ParentPlacement, rebaseAfterRemove: true, children: []groupGraphFakeChild{
		{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: left, itemID: 1, width: 960, height: 1080, placement: *m.Children[0].Placement},
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *m.Children[1].Placement},
	}}
	response := resource.DeleteResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), m)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Delete(ctx, resource.DeleteRequest{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), m)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	var significant []string
	for _, call := range fake.calls {
		if call.typ == "GetSourceScreenshot" || call.typ == "RemoveSceneItem" {
			significant = append(significant, call.typ)
		} else if call.typ == "CallVendorRequest" {
			typ := stringValue(call.data["requestType"])
			if typ == "RemoveGroupChildPlacement" || typ == "GetGroup" {
				significant = append(significant, typ)
			}
		}
	}
	want := []string{"GetGroup", "RemoveGroupChildPlacement", "GetSourceScreenshot", "GetGroup", "RemoveGroupChildPlacement", "GetSourceScreenshot", "GetGroup", "RemoveSceneItem"}
	if fmt.Sprint(significant) != fmt.Sprint(want) {
		t.Fatalf("delete sequence=%v want %v", significant, want)
	}
}

func TestGroupGraphDeleteResumesAfterAuthoritativeRemovalWithoutReplay(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	m := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *m.ParentPlacement, rebaseAfterRemove: true, children: []groupGraphFakeChild{
		{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: left, itemID: 1, width: 960, height: 1080, placement: *m.Children[0].Placement},
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *m.Children[1].Placement},
	}}
	sc := groupGraphSchema()
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "obstf:RemoveGroupChildPlacement")
	first := resource.DeleteResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, m)}
	r := &groupGraphResource{client: &client{testRequester: fake}}
	r.Delete(ctx, resource.DeleteRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, m)}, &first)
	if !first.Diagnostics.HasError() {
		t.Fatal("expected interruption after first authoritative removal")
	}
	var partial groupGraphModel
	lunaLifecycleRequireState(t, ctx, first.State, &partial)
	if partial.Children[0].Present.ValueBool() || partial.Children[0].SourceUUID.ValueString() != "input-left" || len(fake.children) != 1 || fake.children[0].uuid != "input-right" {
		t.Fatalf("first removal partial state=%#v live=%#v", partial.Children, fake.children)
	}

	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	fake.failScreenshotAt = fake.screenshotCount + 1
	barrierFailure := resource.DeleteResponse{State: first.State}
	r.Delete(ctx, resource.DeleteRequest{State: first.State}, &barrierFailure)
	if !barrierFailure.Diagnostics.HasError() {
		t.Fatal("expected resumed-delete owed barrier failure")
	}
	var barrierState groupGraphModel
	lunaLifecycleRequireState(t, ctx, barrierFailure.State, &barrierState)
	if len(barrierState.Children) != 2 || barrierState.Children[0].Present.ValueBool() {
		t.Fatalf("failed owed barrier discarded the completed-prefix marker: %#v", barrierState.Children)
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "RemoveGroupChildPlacement") != 1 {
		t.Fatal("next removal was sent after a failed resumed-delete barrier")
	}

	fake.failScreenshotAt = 0
	refreshedAgain := resource.ReadResponse{State: barrierFailure.State}
	r.Read(ctx, resource.ReadRequest{State: barrierFailure.State}, &refreshedAgain)
	if refreshedAgain.Diagnostics.HasError() {
		t.Fatalf("second refresh after owed barrier failure: %v", refreshedAgain.Diagnostics)
	}
	var refreshedModel groupGraphModel
	lunaLifecycleRequireState(t, ctx, refreshedAgain.State, &refreshedModel)
	if refreshedModel.Children[0].Present.ValueBool() || refreshedModel.Children[1].SourceUUID.ValueString() != "input-right" {
		t.Fatalf("refresh lost completed-prefix evidence: %#v", refreshedModel.Children)
	}
	resumeStart := len(fake.calls)
	second := resource.DeleteResponse{State: refreshedAgain.State}
	r.Delete(ctx, resource.DeleteRequest{State: refreshedAgain.State}, &second)
	if second.Diagnostics.HasError() {
		t.Fatalf("resumed delete diagnostics: %v", second.Diagnostics)
	}
	var resumeOrder []string
	for _, call := range fake.calls[resumeStart:] {
		if call.typ == "GetSourceScreenshot" || call.typ == "RemoveSceneItem" {
			resumeOrder = append(resumeOrder, call.typ)
		}
		if call.typ == "CallVendorRequest" {
			typ := stringValue(call.data["requestType"])
			if typ == "GetGroup" || typ == "RemoveGroupChildPlacement" {
				resumeOrder = append(resumeOrder, typ)
			}
		}
	}
	wantResumePrefix := []string{"GetGroup", "GetSourceScreenshot", "GetGroup", "RemoveGroupChildPlacement"}
	if len(resumeOrder) < len(wantResumePrefix) || fmt.Sprint(resumeOrder[:len(wantResumePrefix)]) != fmt.Sprint(wantResumePrefix) {
		t.Fatalf("resumed delete order=%v want prefix %v", resumeOrder, wantResumePrefix)
	}
	var removedIDs []int64
	for _, call := range fake.calls {
		if call.typ != "CallVendorRequest" || stringValue(call.data["requestType"]) != "RemoveGroupChildPlacement" {
			continue
		}
		requestData := mapValue(call.data["requestData"])
		removedIDs = append(removedIDs, int64Value(mapValue(requestData["child"])["sceneItemId"]))
	}
	if fmt.Sprint(removedIDs) != fmt.Sprint([]int64{1, 2}) {
		t.Fatalf("removal IDs=%v; completed left removal was replayed", removedIDs)
	}
	var expectedX []float64
	for _, call := range fake.calls {
		if call.typ != "CallVendorRequest" || stringValue(call.data["requestType"]) != "RemoveGroupChildPlacement" {
			continue
		}
		requestData := mapValue(call.data["requestData"])
		expectedPlacement := mapValue(requestData["expectedPlacement"])
		expectedTransform := mapValue(expectedPlacement["sceneItemTransform"])
		x, _ := numberValue(expectedTransform["positionX"])
		expectedX = append(expectedX, x)
	}
	if len(expectedX) != 2 || expectedX[1] != 0 {
		t.Fatalf("resumed second removal expected X values=%v; want post-render rebased right X=0", expectedX)
	}
}

func TestGroupGraphDeleteResumeRefusesSameNameUnknownSource(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	m := groupGraphTestModel(left, right)
	m.Children[0].Present = types.BoolValue(false)
	m.ObservedComplete = types.BoolValue(false)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *m.ParentPlacement,
		children: []groupGraphFakeChild{{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *m.Children[1].Placement}},
		detached: []groupGraphFakeChild{{name: "Party Light Left", uuid: "wrong-left", kind: "image_source", file: left, width: 960, height: 1080, placement: *m.Children[0].Placement}},
	}
	response := resource.DeleteResponse{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), m)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Delete(ctx, resource.DeleteRequest{State: lunaLifecycleSchemaAndState(t, ctx, groupGraphSchema(), m)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("same-name replacement was accepted as a completed delete prefix")
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "RemoveGroupChildPlacement") != 0 {
		t.Fatal("surviving child was removed after ambiguous prior-prefix source")
	}
}

func TestGroupGraphDeleteResumesAfterLaterChildFrameFailure(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	m := groupGraphTestModel(left, right)
	m.Children[0].Present = types.BoolValue(false)
	m.ObservedComplete = types.BoolValue(false)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *m.ParentPlacement, failScreenshotAt: 2, rebaseAfterRemove: true, pendingRebase: true,
		children: []groupGraphFakeChild{{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *m.Children[1].Placement}},
	}
	sc := groupGraphSchema()
	first := resource.DeleteResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, m)}
	r := &groupGraphResource{client: &client{testRequester: fake}}
	r.Delete(ctx, resource.DeleteRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, m)}, &first)
	if !first.Diagnostics.HasError() {
		t.Fatal("expected later-child post-removal frame failure")
	}
	var partial groupGraphModel
	lunaLifecycleRequireState(t, ctx, first.State, &partial)
	if len(partial.Children) != 1 || partial.Children[0].StableKey.ValueString() != "right" || partial.Children[0].Present.ValueBool() || len(fake.children) != 0 {
		t.Fatalf("later-child partial state=%#v live=%#v", partial.Children, fake.children)
	}
	fake.failScreenshotAt = 0
	refreshed := resource.ReadResponse{State: first.State}
	r.Read(ctx, resource.ReadRequest{State: first.State}, &refreshed)
	if refreshed.Diagnostics.HasError() {
		t.Fatalf("refresh after later-child frame failure: %v", refreshed.Diagnostics)
	}
	second := resource.DeleteResponse{State: refreshed.State}
	r.Delete(ctx, resource.DeleteRequest{State: refreshed.State}, &second)
	if second.Diagnostics.HasError() {
		t.Fatalf("resume after later-child frame failure: %v", second.Diagnostics)
	}
	var removedIDs []int64
	for _, call := range fake.calls {
		if call.typ == "CallVendorRequest" && stringValue(call.data["requestType"]) == "RemoveGroupChildPlacement" {
			removedIDs = append(removedIDs, int64Value(mapValue(mapValue(call.data["requestData"])["child"])["sceneItemId"]))
		}
	}
	if fmt.Sprint(removedIDs) != fmt.Sprint([]int64{2}) {
		t.Fatalf("later-child removal was replayed: %v", removedIDs)
	}
}

func TestGroupGraphRepairReattachesSurvivingSourceWithoutRecreation(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	state.ObservedComplete = types.BoolValue(false)
	plan := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{
		groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *plan.ParentPlacement, nextChildID: 2,
		children: []groupGraphFakeChild{{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *plan.Children[1].Placement}},
		detached: []groupGraphFakeChild{{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: left, width: 960, height: 1080, placement: *plan.Children[0].Placement}},
	}
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("reattach diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.Children[0].SourceUUID.ValueString() != "input-left" || got.Children[0].ItemID.ValueInt64() != 3 {
		t.Fatalf("reattached binding=%#v", got.Children[0])
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "AddSourceToGroup") != 1 || lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateInputInGroup") != 0 {
		t.Fatalf("wrong repair primitive: %v", groupGraphSignificantCalls(fake))
	}
}

func TestGroupGraphParentAnchorRepairKeepsStableResourceIdentity(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.ParentPresent = types.BoolValue(false)
	state.ObservedComplete = types.BoolValue(false)
	plan := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 0, parentPlacement: *plan.ParentPlacement, children: []groupGraphFakeChild{
		{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: left, itemID: 1, width: 960, height: 1080, placement: *plan.Children[0].Placement},
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *plan.Children[1].Placement},
	}}
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("anchor repair diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.ID.ValueString() != state.ID.ValueString() || got.GroupUUID.ValueString() != state.GroupUUID.ValueString() || got.ParentItemID.ValueInt64() != 99 || !got.ParentPresent.ValueBool() {
		t.Fatalf("anchor repair state=%#v", got)
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateSceneItem") != 1 {
		t.Fatal("anchor repair did not create exactly one parent item")
	}
}

func TestGroupGraphReadRemovesStateOnlyForAuthoritativeGroupAbsence(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{groupExists: false, groupUUID: lunaLifecycleGroupUUID, parentItemID: 0, parentPlacement: *state.ParentPlacement}
	sc := groupGraphSchema()
	response := resource.ReadResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Read(ctx, resource.ReadRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("group absence diagnostics: %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("authoritatively absent group did not remove aggregate state")
	}
}

func TestGroupGraphReadRecordsMissingParentWithoutDiscardingBindings(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 0, parentPlacement: *state.ParentPlacement, children: []groupGraphFakeChild{
		{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: left, itemID: 1, width: 960, height: 1080, placement: *state.Children[0].Placement},
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *state.Children[1].Placement},
	}}
	sc := groupGraphSchema()
	response := resource.ReadResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Read(ctx, resource.ReadRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("missing parent read diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.ParentPresent.ValueBool() || got.ParentItemID.ValueInt64() != 7 || got.GroupUUID.ValueString() != lunaLifecycleGroupUUID || got.ObservedComplete.ValueBool() {
		t.Fatalf("missing parent state=%#v", got)
	}
}

func TestGroupGraphConcurrentPlacementChangeRefusesBeforeMembership(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	plan := groupGraphTestModel(left, right)
	concurrent := groupGraphTestPlacement(15, 1)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *plan.ParentPlacement, children: []groupGraphFakeChild{
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: concurrent},
	}}
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("concurrent placement change was normalized")
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateInputInGroup") != 0 {
		t.Fatal("membership mutation followed concurrent placement change")
	}
}

func TestGroupGraphStableKeyReorderPreservesNativeBindings(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	plan := groupGraphTestModel(left, right)
	plan.Children = []groupGraphChildModel{plan.Children[1], plan.Children[0]}
	plan.Children[0].Placement = placementPointer(groupGraphTestPlacement(960, 0))
	plan.Children[1].Placement = placementPointer(groupGraphTestPlacement(0, 1))
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *state.ParentPlacement, children: []groupGraphFakeChild{
		{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: left, itemID: 1, width: 960, height: 1080, placement: *state.Children[0].Placement},
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: *state.Children[1].Placement},
	}}
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, plan)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("reorder diagnostics: %v", response.Diagnostics)
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.Children[0].StableKey.ValueString() != "right" || got.Children[0].SourceUUID.ValueString() != "input-right" || got.Children[0].ItemID.ValueInt64() != 2 || got.Children[1].StableKey.ValueString() != "left" || got.Children[1].SourceUUID.ValueString() != "input-left" || got.Children[1].ItemID.ValueInt64() != 1 {
		t.Fatalf("reordered bindings=%#v", got.Children)
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "SetSceneItemIndex") != 1 || lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "CreateInputInGroup") != 0 {
		t.Fatalf("reorder sent wrong mutations: %#v", fake.calls)
	}
}

func TestGroupGraphPartialSetterStateKeepsUnexecutedParentActual(t *testing.T) {
	ctx := context.Background()
	left, right := groupGraphTestAsset(t, "left.png"), groupGraphTestAsset(t, "right.png")
	state := groupGraphTestModel(left, right)
	state.Children[0].Present = types.BoolValue(false)
	state.ObservedComplete = types.BoolValue(false)
	parentDrift := groupGraphTestPlacement(960, 0)
	rightDrift := groupGraphTestPlacement(0, 1)
	state.ParentPlacement, state.Children[1].Placement = &parentDrift, &rightDrift
	plan := groupGraphTestModel(left, right)
	fake := &groupGraphFakeOBS{
		groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: parentDrift, nextChildID: 2,
		children: []groupGraphFakeChild{{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: right, itemID: 2, width: 960, height: 1080, placement: rightDrift}},
	}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "SetSceneItemTransform")
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected injected interruption")
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.ParentPlacement.Transform.X.ValueFloat64() != 960 || got.ObservedParent.Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("unexecuted parent was falsely checkpointed desired: parent=%v observed=%v", got.ParentPlacement.Transform.X.ValueFloat64(), got.ObservedParent.Placement.Transform.X.ValueFloat64())
	}
	rightIndex := groupGraphChildIndexByStableKey(got.Children, "right")
	if rightIndex < 0 || got.Children[rightIndex].Placement.Transform.X.ValueFloat64() != 960 {
		t.Fatalf("completed right setter was not checkpointed: %#v", got.Children)
	}
	leftIndex := groupGraphChildIndexByStableKey(got.Children, "left")
	if leftIndex < 0 || got.Children[leftIndex].SourceUUID.ValueString() != "input-left-new" || got.Children[leftIndex].ItemID.ValueInt64() != 3 {
		t.Fatalf("replacement binding was not retained: %#v", got.Children)
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "SetSceneItemTransform") != 1 {
		t.Fatal("parent setter continued after injected right-child interruption")
	}
}

func TestGroupGraphPartialSettingsStateKeepsUntouchedSiblingActual(t *testing.T) {
	ctx := context.Background()
	leftOld, rightOld := groupGraphTestAsset(t, "left-old.png"), groupGraphTestAsset(t, "right-old.png")
	leftNew, rightNew := groupGraphTestAsset(t, "left-new.png"), groupGraphTestAsset(t, "right-new.png")
	state := groupGraphTestModel(leftOld, rightOld)
	plan := groupGraphTestModel(leftNew, rightNew)
	fake := &groupGraphFakeOBS{groupExists: true, groupUUID: lunaLifecycleGroupUUID, parentItemID: 7, parentPlacement: *state.ParentPlacement, children: []groupGraphFakeChild{
		{name: "Party Light Left", uuid: "input-left", kind: "image_source", file: leftOld, itemID: 1, width: 960, height: 1080, placement: *state.Children[0].Placement},
		{name: "Party Light Right", uuid: "input-right", kind: "image_source", file: rightOld, itemID: 2, width: 960, height: 1080, placement: *state.Children[1].Placement},
	}}
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "SetInputSettings")
	sc := groupGraphSchema()
	response := resource.UpdateResponse{State: lunaLifecycleSchemaAndState(t, ctx, sc, state)}
	(&groupGraphResource{client: &client{testRequester: fake}}).Update(ctx, resource.UpdateRequest{State: lunaLifecycleSchemaAndState(t, ctx, sc, state), Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc, plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected injected settings interruption")
	}
	var got groupGraphModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	leftIndex, rightIndex := groupGraphChildIndexByStableKey(got.Children, "left"), groupGraphChildIndexByStableKey(got.Children, "right")
	if got.Children[leftIndex].File.ValueString() != leftNew {
		t.Fatalf("completed left settings were not checkpointed: %s", got.Children[leftIndex].File.ValueString())
	}
	if got.Children[rightIndex].File.ValueString() != rightOld {
		t.Fatalf("untouched right settings falsely claimed desired: got %s want actual %s", got.Children[rightIndex].File.ValueString(), rightOld)
	}
	if lunaLifecycleMutationCount(&lunaLifecycleScript{calls: fake.calls}, "SetInputSettings") != 1 {
		t.Fatal("settings loop continued after authoritative interruption")
	}
}
