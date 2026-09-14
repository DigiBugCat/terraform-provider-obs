package tfprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// groupSuccessCancelRequester stops a deliberately non-converging wait after
// the first observed mismatch. The callback still receives the mismatched
// native observation; cancellation only keeps this deterministic test from
// sleeping through the production four-second retry budget.
type groupSuccessCancelRequester struct {
	base      *groupFixtureScript
	cancel    context.CancelFunc
	target    string
	cancelled bool
}

func (r *groupSuccessCancelRequester) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	response, err := r.base.Req(ctx, typ, data)
	if !r.cancelled && typ == "CallVendorRequest" && stringValue(data["requestType"]) == r.target {
		r.cancelled = true
		r.cancel()
	}
	return response, err
}

func groupSuccessTraceMustContain(t *testing.T, path, needle string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(contents), needle) {
		t.Fatalf("trace=%s want %q", contents, needle)
	}
}

func TestSuccessGroupCreateRunsCompletionWaitAndPersistsPostcondition(t *testing.T) {
	ctx := context.Background()
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("OBSTF_TF_TRACE_PATH", trace)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	placement := groupFixturePlacement()
	created := groupFixtureGroupResult(31, "group-success", groupFixtureGroupName, placement, []any{})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: created}},
			"GetGroup":    {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: created}},
		},
	}
	model := groupFixtureGroupModel(0, groupFixtureSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-success" || got.ItemID.ValueInt64() != 31 || got.ID.ValueString() != groupID(groupFixtureCollection, groupFixtureSceneUUID, "group-success", 31) {
		t.Fatalf("state=%#v; successful group identity was not persisted", got)
	}
	if groupFixtureMutationCount(script, "CreateGroup") != 1 || groupFixtureMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("completion calls: CreateGroup=%d GetGroup=%d", groupFixtureMutationCount(script, "CreateGroup"), groupFixtureMutationCount(script, "GetGroup"))
	}
	groupSuccessTraceMustContain(t, trace, `"object":"group","status":"passed"`)
}

func TestSuccessGroupCreateRejectsFailedOrMismatchedCompletion(t *testing.T) {
	ctx := context.Background()
	placement := groupFixturePlacement()
	valid := groupFixtureGroupResult(32, "group-failed", groupFixtureGroupName, placement, []any{})
	for _, tc := range []struct {
		name     string
		reply    groupFixtureBridgeReply
		contains string
	}{
		{
			name: "failed inner outcome",
			reply: groupFixtureBridgeReply{
				outcome: "failed", code: "native_failed", message: "native group creation failed",
				mutationAttempted: true, postconditionVerified: true, result: valid,
			},
			contains: "failed",
		},
		{
			name: "completed identity mismatch",
			reply: groupFixtureBridgeReply{
				outcome: "completed", mutationAttempted: true, postconditionVerified: true,
				result: groupFixtureGroupResult(32, "group-failed", "Unexpected Group", placement, []any{}),
			},
			contains: "mismatched identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
			script := &groupFixtureScript{
				capabilityRequests: []string{"CreateGroup"},
				direct: map[string][]groupFixtureDirectReply{
					"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
					"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
					"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
					"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
				},
				bridge: map[string][]groupFixtureBridgeReply{"CreateGroup": {tc.reply}},
			}
			model := groupFixtureGroupModel(0, groupFixtureSceneName)
			model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
			plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupSchema(ctx), model)
			response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), model)}
			resourceUnderTest := &groupResource{client: &client{testRequester: script}}
			resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

			groupFixtureAssertDiagnostic(t, response.Diagnostics, tc.contains)
			if groupFixtureMutationCount(script, "GetGroup") != 0 {
				t.Fatal("failed/mismatched Create advanced into a successful postcondition wait")
			}
		})
	}
}

func TestSuccessGroupCreateRejectsMismatchedWaitObservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	placement := groupFixturePlacement()
	created := groupFixtureGroupResult(33, "group-wait", groupFixtureGroupName, placement, []any{})
	mismatched := groupFixtureGroupResult(33, "different-group", groupFixtureGroupName, placement, []any{})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetSceneList":           {groupFixtureDirect(groupFixtureSceneList())},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: created}},
			"GetGroup":    {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: mismatched}},
		},
	}
	cancellable := &groupSuccessCancelRequester{base: script, cancel: cancel, target: "GetGroup"}
	model := groupFixtureGroupModel(0, groupFixtureSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: cancellable}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if !response.Diagnostics.HasError() {
		t.Fatal("mismatched group wait was reported as successful")
	}
	if groupFixtureMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("GetGroup calls=%d", groupFixtureMutationCount(script, "GetGroup"))
	}
	var got groupModel
	groupFixtureRequireState(t, context.Background(), response.State, &got)
	if got.GroupUUID.ValueString() != "group-wait" || got.ItemID.ValueInt64() != 33 {
		t.Fatalf("state=%#v; assigned identity was discarded after wait failure", got)
	}
}

func TestSuccessGroupInputCreateRunsChildAndInputCompletionWaits(t *testing.T) {
	ctx := context.Background()
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBSTF_TF_TRACE_PATH", trace)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	placement := groupFixturePlacement()
	child := groupFixtureChildResult(12, groupFixtureInputUUID, groupFixtureInputName, placement)
	group := groupFixtureGroupResult(7, groupFixtureGroupUUID, groupFixtureGroupName, placement, []any{child})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList":  {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetInputList":            {groupFixtureDirect(map[string]any{"inputs": []any{}}), groupFixtureDirect(groupFixtureInputList())},
			"GetVersion":              {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":        {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetInputSettings":        {groupFixtureDirect(groupFixtureImageSettings(asset))},
			"GetInputDefaultSettings": {groupFixtureDirect(groupFixtureDefaultImageSettings())},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: child}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	model := groupFixtureGroupInputModel(asset, 7, 0, groupFixtureGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupInputModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	wantID := groupInputID(groupFixtureCollection, groupFixtureSceneUUID, groupFixtureGroupUUID, groupFixtureInputUUID, 12)
	if got.InputUUID.ValueString() != groupFixtureInputUUID || got.ItemID.ValueInt64() != 12 || got.ID.ValueString() != wantID || got.SourceWidth.ValueInt64() != 1920 || got.SourceHeight.ValueInt64() != 1080 {
		t.Fatalf("state=%#v; successful group input identity/postcondition was not persisted", got)
	}
	if groupFixtureMutationCount(script, "CreateInputInGroup") != 1 || groupFixtureMutationCount(script, "GetGroup") != 1 || groupFixtureMutationCount(script, "GetInputList") != 2 {
		t.Fatalf("completion calls: CreateInputInGroup=%d GetGroup=%d GetInputList=%d", groupFixtureMutationCount(script, "CreateInputInGroup"), groupFixtureMutationCount(script, "GetGroup"), groupFixtureMutationCount(script, "GetInputList"))
	}
	groupSuccessTraceMustContain(t, trace, `"object":"group-input","status":"passed"`)
	groupSuccessTraceMustContain(t, trace, `"object":"input","status":"passed"`)
}

func TestSuccessGroupInputCreateRejectsMismatchedChildWait(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := groupFixturePlacement()
	created := groupFixtureChildResult(12, groupFixtureInputUUID, groupFixtureInputName, placement)
	wrongChild := groupFixtureChildResult(12, "different-input", groupFixtureInputName, placement)
	group := groupFixtureGroupResult(7, groupFixtureGroupUUID, groupFixtureGroupName, placement, []any{wrongChild})
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetInputList":           {groupFixtureDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: created}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	model := groupFixtureGroupInputModel(asset, 7, 0, groupFixtureGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "rebound")
	if groupFixtureMutationCount(script, "GetGroup") != 1 || groupFixtureMutationCount(script, "GetInputSettings") != 0 {
		t.Fatalf("mismatched child advanced incorrectly: GetGroup=%d GetInputSettings=%d", groupFixtureMutationCount(script, "GetGroup"), groupFixtureMutationCount(script, "GetInputSettings"))
	}
	var got groupInputModel
	groupFixtureRequireState(t, ctx, response.State, &got)
	if got.InputUUID.ValueString() != groupFixtureInputUUID || got.ItemID.ValueInt64() != 12 {
		t.Fatalf("state=%#v; assigned child identity was discarded after wait mismatch", got)
	}
}

func TestSuccessGroupInputCreateRejectsFailedInnerOutcome(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := groupFixturePlacement()
	child := groupFixtureChildResult(13, "input-failed", groupFixtureInputName, placement)
	script := &groupFixtureScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]groupFixtureDirectReply{
			"GetSceneCollectionList": {groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection}), groupFixtureDirect(map[string]any{"currentSceneCollectionName": groupFixtureCollection})},
			"GetInputList":           {groupFixtureDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {groupFixtureDirect(groupFixtureVersion("CallVendorRequest")), groupFixtureDirect(groupFixtureVersion("CallVendorRequest"))},
			"GetVideoSettings":       {groupFixtureDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]groupFixtureBridgeReply{
			"CreateInputInGroup": {{outcome: "failed", code: "native_failed", message: "native child creation failed", mutationAttempted: true, postconditionVerified: true, result: child}},
		},
	}
	model := groupFixtureGroupInputModel(asset, 7, 0, groupFixtureGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := groupFixtureSchemaAndPlan(t, ctx, groupFixtureGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: groupFixtureSchemaAndState(t, ctx, groupFixtureGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	groupFixtureAssertDiagnostic(t, response.Diagnostics, "failed")
	if groupFixtureMutationCount(script, "GetGroup") != 0 {
		t.Fatal("failed child Create advanced into a successful postcondition wait")
	}
}
