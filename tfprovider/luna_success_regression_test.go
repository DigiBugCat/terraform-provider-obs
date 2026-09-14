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

// lunaSuccessCancelRequester stops a deliberately non-converging wait after
// the first observed mismatch. The callback still receives the mismatched
// native observation; cancellation only keeps this deterministic test from
// sleeping through the production four-second retry budget.
type lunaSuccessCancelRequester struct {
	base      *lunaLifecycleScript
	cancel    context.CancelFunc
	target    string
	cancelled bool
}

func (r *lunaSuccessCancelRequester) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	response, err := r.base.Req(ctx, typ, data)
	if !r.cancelled && typ == "CallVendorRequest" && stringValue(data["requestType"]) == r.target {
		r.cancelled = true
		r.cancel()
	}
	return response, err
}

func lunaSuccessTraceMustContain(t *testing.T, path, needle string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(contents), needle) {
		t.Fatalf("trace=%s want %q", contents, needle)
	}
}

func TestLunaSuccessGroupCreateRunsCompletionWaitAndPersistsPostcondition(t *testing.T) {
	ctx := context.Background()
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("OBSTF_TF_TRACE_PATH", trace)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	placement := lunaLifecyclePlacement()
	created := lunaLifecycleGroupResult(31, "group-success", lunaLifecycleGroupName, placement, []any{})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: created}},
			"GetGroup":    {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: created}},
		},
	}
	model := lunaLifecycleGroupModel(0, lunaLifecycleSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.GroupUUID.ValueString() != "group-success" || got.ItemID.ValueInt64() != 31 || got.ID.ValueString() != groupID(lunaLifecycleCollection, lunaLifecycleSceneUUID, "group-success", 31) {
		t.Fatalf("state=%#v; successful group identity was not persisted", got)
	}
	if lunaLifecycleMutationCount(script, "CreateGroup") != 1 || lunaLifecycleMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("completion calls: CreateGroup=%d GetGroup=%d", lunaLifecycleMutationCount(script, "CreateGroup"), lunaLifecycleMutationCount(script, "GetGroup"))
	}
	lunaSuccessTraceMustContain(t, trace, `"object":"group","status":"passed"`)
}

func TestLunaSuccessGroupCreateRejectsFailedOrMismatchedCompletion(t *testing.T) {
	ctx := context.Background()
	placement := lunaLifecyclePlacement()
	valid := lunaLifecycleGroupResult(32, "group-failed", lunaLifecycleGroupName, placement, []any{})
	for _, tc := range []struct {
		name     string
		reply    lunaLifecycleBridgeReply
		contains string
	}{
		{
			name: "failed inner outcome",
			reply: lunaLifecycleBridgeReply{
				outcome: "failed", code: "native_failed", message: "native group creation failed",
				mutationAttempted: true, postconditionVerified: true, result: valid,
			},
			contains: "failed",
		},
		{
			name: "completed identity mismatch",
			reply: lunaLifecycleBridgeReply{
				outcome: "completed", mutationAttempted: true, postconditionVerified: true,
				result: lunaLifecycleGroupResult(32, "group-failed", "Unexpected Group", placement, []any{}),
			},
			contains: "mismatched identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
			script := &lunaLifecycleScript{
				capabilityRequests: []string{"CreateGroup"},
				direct: map[string][]lunaLifecycleDirectReply{
					"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
					"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
					"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
					"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
				},
				bridge: map[string][]lunaLifecycleBridgeReply{"CreateGroup": {tc.reply}},
			}
			model := lunaLifecycleGroupModel(0, lunaLifecycleSceneName)
			model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
			plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), model)
			response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), model)}
			resourceUnderTest := &groupResource{client: &client{testRequester: script}}
			resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

			lunaLifecycleAssertDiagnostic(t, response.Diagnostics, tc.contains)
			if lunaLifecycleMutationCount(script, "GetGroup") != 0 {
				t.Fatal("failed/mismatched Create advanced into a successful postcondition wait")
			}
		})
	}
}

func TestLunaSuccessGroupCreateRejectsMismatchedWaitObservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	placement := lunaLifecyclePlacement()
	created := lunaLifecycleGroupResult(33, "group-wait", lunaLifecycleGroupName, placement, []any{})
	mismatched := lunaLifecycleGroupResult(33, "different-group", lunaLifecycleGroupName, placement, []any{})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetSceneList":           {lunaLifecycleDirect(lunaLifecycleSceneList())},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: created}},
			"GetGroup":    {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: mismatched}},
		},
	}
	cancellable := &lunaSuccessCancelRequester{base: script, cancel: cancel, target: "GetGroup"}
	model := lunaLifecycleGroupModel(0, lunaLifecycleSceneName)
	model.ID, model.GroupUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupSchema(ctx), model)}
	resourceUnderTest := &groupResource{client: &client{testRequester: cancellable}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if !response.Diagnostics.HasError() {
		t.Fatal("mismatched group wait was reported as successful")
	}
	if lunaLifecycleMutationCount(script, "GetGroup") != 1 {
		t.Fatalf("GetGroup calls=%d", lunaLifecycleMutationCount(script, "GetGroup"))
	}
	var got groupModel
	lunaLifecycleRequireState(t, context.Background(), response.State, &got)
	if got.GroupUUID.ValueString() != "group-wait" || got.ItemID.ValueInt64() != 33 {
		t.Fatalf("state=%#v; assigned identity was discarded after wait failure", got)
	}
}

func TestLunaSuccessGroupInputCreateRunsChildAndInputCompletionWaits(t *testing.T) {
	ctx := context.Background()
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBSTF_TF_TRACE_PATH", trace)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	placement := lunaLifecyclePlacement()
	child := lunaLifecycleChildResult(12, lunaLifecycleInputUUID, lunaLifecycleInputName, placement)
	group := lunaLifecycleGroupResult(7, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{child})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList":  {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetInputList":            {lunaLifecycleDirect(map[string]any{"inputs": []any{}}), lunaLifecycleDirect(lunaLifecycleInputList())},
			"GetVersion":              {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":        {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
			"GetInputSettings":        {lunaLifecycleDirect(lunaLifecycleImageSettings(asset))},
			"GetInputDefaultSettings": {lunaLifecycleDirect(lunaLifecycleDefaultImageSettings())},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: child}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	model := lunaLifecycleGroupInputModel(asset, 7, 0, lunaLifecycleGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	var got groupInputModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	wantID := groupInputID(lunaLifecycleCollection, lunaLifecycleSceneUUID, lunaLifecycleGroupUUID, lunaLifecycleInputUUID, 12)
	if got.InputUUID.ValueString() != lunaLifecycleInputUUID || got.ItemID.ValueInt64() != 12 || got.ID.ValueString() != wantID || got.SourceWidth.ValueInt64() != 1920 || got.SourceHeight.ValueInt64() != 1080 {
		t.Fatalf("state=%#v; successful group input identity/postcondition was not persisted", got)
	}
	if lunaLifecycleMutationCount(script, "CreateInputInGroup") != 1 || lunaLifecycleMutationCount(script, "GetGroup") != 1 || lunaLifecycleMutationCount(script, "GetInputList") != 2 {
		t.Fatalf("completion calls: CreateInputInGroup=%d GetGroup=%d GetInputList=%d", lunaLifecycleMutationCount(script, "CreateInputInGroup"), lunaLifecycleMutationCount(script, "GetGroup"), lunaLifecycleMutationCount(script, "GetInputList"))
	}
	lunaSuccessTraceMustContain(t, trace, `"object":"group-input","status":"passed"`)
	lunaSuccessTraceMustContain(t, trace, `"object":"input","status":"passed"`)
}

func TestLunaSuccessGroupInputCreateRejectsMismatchedChildWait(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := lunaLifecyclePlacement()
	created := lunaLifecycleChildResult(12, lunaLifecycleInputUUID, lunaLifecycleInputName, placement)
	wrongChild := lunaLifecycleChildResult(12, "different-input", lunaLifecycleInputName, placement)
	group := lunaLifecycleGroupResult(7, lunaLifecycleGroupUUID, lunaLifecycleGroupName, placement, []any{wrongChild})
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetInputList":           {lunaLifecycleDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateInputInGroup": {{outcome: "completed", mutationAttempted: true, postconditionVerified: true, result: created}},
			"GetGroup":           {{outcome: "completed", mutationAttempted: false, postconditionVerified: true, result: group}},
		},
	}
	model := lunaLifecycleGroupInputModel(asset, 7, 0, lunaLifecycleGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "rebound")
	if lunaLifecycleMutationCount(script, "GetGroup") != 1 || lunaLifecycleMutationCount(script, "GetInputSettings") != 0 {
		t.Fatalf("mismatched child advanced incorrectly: GetGroup=%d GetInputSettings=%d", lunaLifecycleMutationCount(script, "GetGroup"), lunaLifecycleMutationCount(script, "GetInputSettings"))
	}
	var got groupInputModel
	lunaLifecycleRequireState(t, ctx, response.State, &got)
	if got.InputUUID.ValueString() != lunaLifecycleInputUUID || got.ItemID.ValueInt64() != 12 {
		t.Fatalf("state=%#v; assigned child identity was discarded after wait mismatch", got)
	}
}

func TestLunaSuccessGroupInputCreateRejectsFailedInnerOutcome(t *testing.T) {
	ctx := context.Background()
	asset := filepath.Join(t.TempDir(), "camera.png")
	if err := os.WriteFile(asset, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	placement := lunaLifecyclePlacement()
	child := lunaLifecycleChildResult(13, "input-failed", lunaLifecycleInputName, placement)
	script := &lunaLifecycleScript{
		capabilityRequests: []string{"CreateInputInGroup"},
		direct: map[string][]lunaLifecycleDirectReply{
			"GetSceneCollectionList": {lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection}), lunaLifecycleDirect(map[string]any{"currentSceneCollectionName": lunaLifecycleCollection})},
			"GetInputList":           {lunaLifecycleDirect(map[string]any{"inputs": []any{}})},
			"GetVersion":             {lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest")), lunaLifecycleDirect(lunaLifecycleVersion("CallVendorRequest"))},
			"GetVideoSettings":       {lunaLifecycleDirect(map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0})},
		},
		bridge: map[string][]lunaLifecycleBridgeReply{
			"CreateInputInGroup": {{outcome: "failed", code: "native_failed", message: "native child creation failed", mutationAttempted: true, postconditionVerified: true, result: child}},
		},
	}
	model := lunaLifecycleGroupInputModel(asset, 7, 0, lunaLifecycleGroupName)
	model.ID, model.InputUUID, model.ItemID = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
	plan := lunaLifecycleSchemaAndPlan(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)
	response := resource.CreateResponse{State: lunaLifecycleSchemaAndState(t, ctx, lunaLifecycleGroupInputSchema(ctx), model)}
	resourceUnderTest := &groupInputResource{client: &client{testRequester: script}}
	resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)

	lunaLifecycleAssertDiagnostic(t, response.Diagnostics, "failed")
	if lunaLifecycleMutationCount(script, "GetGroup") != 0 {
		t.Fatal("failed child Create advanced into a successful postcondition wait")
	}
}
