package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestTerraformCLIGroupGraphCreateWithUnknownObservations crosses the boundary
// that callback-only tests cannot: Terraform Core constructs the initial plan,
// starts the built provider as a plugin, and sends wholly unknown computed
// nested observations through the protocol. An intentional interruption after
// the authoritative CreateGroup response keeps the fake transport test bounded
// while proving aggregate Create was reached.
func TestTerraformCLIGroupGraphCreateWithUnknownObservations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Terraform CLI/provider subprocess regression in short mode")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	terraform := filepath.Join(root, ".tools", "terraform", "1.15.7", "terraform")
	if _, err := os.Stat(terraform); err != nil {
		t.Skipf("pinned Terraform CLI unavailable: %v", err)
	}

	fake := &groupGraphFakeOBS{parentPlacement: lunaLifecyclePlacement()}
	server := newGroupGraphWebsocketServer(t, fake)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(serverURL.Port())
	if err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	providerDir := filepath.Join(work, "providers")
	if err := os.Mkdir(providerDir, 0700); err != nil {
		t.Fatal(err)
	}
	providerBinary := filepath.Join(providerDir, "terraform-provider-obs")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", providerBinary, ".")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, output)
	}

	asset := filepath.Join(work, "left.png")
	if err := os.WriteFile(asset, []byte("synthetic asset"), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`
terraform {
  required_version = "= 1.15.7"
  required_providers {
    obs = {
      source  = "digibugcat/obs"
      version = "0.1.0"
    }
  }
}

provider "obs" {
  host     = "127.0.0.1"
  port     = %d
  password = "local-fake-no-auth"
}

resource "obs_group_graph" "unknown_plan" {
  collection     = %s
  scene_name     = %s
  scene_uuid     = %s
  name           = %s
  parent_present = true
  parent_placement = {
    enabled = false
    locked  = false
    index   = 0
    transform = {
      x = 0
      y = 0
      scale_x = 1
      scale_y = 1
      rotation = 0
      alignment = 5
      bounds_type = "OBS_BOUNDS_NONE"
      bounds_alignment = 0
      bounds_width = 0
      bounds_height = 0
      crop_to_bounds = false
      crop_left = 0
      crop_top = 0
      crop_right = 0
      crop_bottom = 0
    }
  }
  children = [{
    stable_key = "left"
    name       = "Party Light Left"
    kind       = "image_source"
    file       = %s
    unload     = false
    present    = true
    placement = {
      enabled = true
      locked  = false
      index   = 0
      transform = {
        x = 0
        y = 0
        scale_x = 1
        scale_y = 1
        rotation = 0
        alignment = 5
        bounds_type = "OBS_BOUNDS_NONE"
        bounds_alignment = 0
        bounds_width = 0
        bounds_height = 0
        crop_to_bounds = false
        crop_left = 0
        crop_top = 0
        crop_right = 0
        crop_bottom = 0
      }
    }
  }]
}
`, port, strconv.Quote(lunaLifecycleCollection), strconv.Quote(lunaLifecycleSceneName), strconv.Quote(lunaLifecycleSceneUUID), strconv.Quote(lunaLifecycleGroupName), strconv.Quote(asset))
	if err := os.WriteFile(filepath.Join(work, "main.tf"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cliConfig := fmt.Sprintf(`provider_installation {
  dev_overrides {
    "registry.terraform.io/digibugcat/obs" = %s
  }
  direct {}
}
`, strconv.Quote(providerDir))
	cliConfigPath := filepath.Join(work, "dev.tfrc")
	if err := os.WriteFile(cliConfigPath, []byte(cliConfig), 0600); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(work, "trace.jsonl")
	cmd := exec.Command(terraform, "apply", "-auto-approve", "-input=false", "-no-color")
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"TF_CLI_CONFIG_FILE="+cliConfigPath,
		"TF_IN_AUTOMATION=1",
		"OBSTF_TF_TRACE_PATH="+tracePath,
		"OBSTF_TF_INJECT_AFTER_NATIVE_TYPE=obstf:CreateGroup",
	)
	output, applyErr := cmd.CombinedOutput()
	if applyErr == nil {
		t.Fatalf("Terraform apply unexpectedly succeeded; intentional interruption was not honored\n%s", output)
	}
	text := string(output)
	if !strings.Contains(text, "injected interruption after authoritative obstf:CreateGroup response") {
		t.Fatalf("Terraform apply did not reach bounded post-CreateGroup interruption: %v\n%s", applyErr, text)
	}
	if strings.Contains(text, "Value Conversion Error") || strings.Contains(text, "target type cannot handle unknown values") {
		t.Fatalf("unknown computed observations regressed during real Terraform apply:\n%s", text)
	}
	fake.mu.Lock()
	groupExists := fake.groupExists
	calls := append([]lunaLifecycleCall(nil), fake.calls...)
	fake.mu.Unlock()
	if !groupExists {
		t.Fatal("fake transport did not observe CreateGroup")
	}
	var createCalls int
	for _, call := range calls {
		if call.typ == "CallVendorRequest" && stringValue(call.data["requestType"]) == "CreateGroup" {
			createCalls++
		}
	}
	if createCalls != 1 {
		t.Fatalf("fake transport CreateGroup calls=%d, want 1; calls=%v", createCalls, calls)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read mutation trace: %v", err)
	}
	if !strings.Contains(string(trace), `"request_type":"CallVendorRequest/obstf:CreateGroup"`) || !strings.Contains(string(trace), `"status":"completed"`) {
		t.Fatalf("trace does not retain authoritative CreateGroup receipt:\n%s", trace)
	}
}

// TestTerraformCLIGroupGraphRepairUsesSchemaCompatibleNoneBounds proves the
// actual Terraform Core -> provider process -> obs-websocket wire path for the
// repair that failed natively. The fake enforces obs-websocket 5.7.4's real
// optional bounds dimension minimum and partial-update semantics.
func TestTerraformCLIGroupGraphRepairUsesSchemaCompatibleNoneBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Terraform CLI/provider subprocess regression in short mode")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	terraform := filepath.Join(root, ".tools", "terraform", "1.15.7", "terraform")
	if _, err := os.Stat(terraform); err != nil {
		t.Skipf("pinned Terraform CLI unavailable: %v", err)
	}

	fake := &groupGraphFakeOBS{parentPlacement: lunaLifecyclePlacement()}
	server := newGroupGraphWebsocketServer(t, fake)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(serverURL.Port())
	if err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	providerDir := filepath.Join(work, "providers")
	if err := os.Mkdir(providerDir, 0700); err != nil {
		t.Fatal(err)
	}
	providerBinary := filepath.Join(providerDir, "terraform-provider-obs")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", providerBinary, ".")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, output)
	}

	leftAsset := filepath.Join(work, "left.png")
	rightAsset := filepath.Join(work, "right.png")
	for _, asset := range []string{leftAsset, rightAsset} {
		if err := os.WriteFile(asset, []byte("synthetic asset"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "main.tf"), []byte(groupGraphCLIRepairConfig(port, leftAsset, rightAsset)), 0600); err != nil {
		t.Fatal(err)
	}
	cliConfig := fmt.Sprintf(`provider_installation {
  dev_overrides {
    "registry.terraform.io/digibugcat/obs" = %s
  }
  direct {}
}
`, strconv.Quote(providerDir))
	cliConfigPath := filepath.Join(work, "dev.tfrc")
	if err := os.WriteFile(cliConfigPath, []byte(cliConfig), 0600); err != nil {
		t.Fatal(err)
	}
	baseEnv := append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfigPath, "TF_IN_AUTOMATION=1")
	initialTrace := filepath.Join(work, "initial-trace.jsonl")
	initial := exec.Command(terraform, "apply", "-auto-approve", "-input=false", "-no-color")
	initial.Dir = work
	initial.Env = append(baseEnv, "OBSTF_TF_TRACE_PATH="+initialTrace)
	if output, err := initial.CombinedOutput(); err != nil {
		t.Fatalf("initial Terraform apply: %v\n%s", err, output)
	}
	fake.mu.Lock()
	initialChildCount := len(fake.children)
	var rightUUID string
	var rightItemID int64
	if initialChildCount == 2 {
		rightUUID, rightItemID = fake.children[1].uuid, fake.children[1].itemID
		right := fake.children[1]
		right.placement.Transform.X = types.Float64Value(0)
		fake.children = []groupGraphFakeChild{right}
		fake.parentPlacement.Transform.X = types.Float64Value(960)
	}
	fake.mu.Unlock()
	if initialChildCount != 2 {
		t.Fatalf("initial fake children=%d want 2", initialChildCount)
	}

	repairPlan := filepath.Join(work, "repair.plan")
	plan := exec.Command(terraform, "plan", "-input=false", "-no-color", "-out", repairPlan)
	plan.Dir = work
	plan.Env = baseEnv
	if output, err := plan.CombinedOutput(); err != nil {
		t.Fatalf("repair Terraform plan: %v\n%s", err, output)
	}
	show := exec.Command(terraform, "show", "-json", repairPlan)
	show.Dir = work
	show.Env = baseEnv
	showOutput, err := show.Output()
	if err != nil {
		t.Fatalf("show repair plan: %v", err)
	}
	var planDocument map[string]any
	if err := json.Unmarshal(showOutput, &planDocument); err != nil {
		t.Fatal(err)
	}
	var afterUnknown map[string]any
	for _, raw := range sliceValue(planDocument["resource_changes"]) {
		change := mapValue(raw)
		if stringValue(change["address"]) == "obs_group_graph.repair" {
			afterUnknown = mapValue(mapValue(change["change"])["after_unknown"])
			break
		}
	}
	unknownChildren := sliceValue(afterUnknown["children"])
	if len(unknownChildren) != 2 || !boolValue(mapValue(unknownChildren[0])["source_uuid"]) || !boolValue(mapValue(unknownChildren[0])["item_id"]) || boolValue(mapValue(unknownChildren[1])["source_uuid"]) || boolValue(mapValue(unknownChildren[1])["item_id"]) || !boolValue(afterUnknown["repair_nonce"]) {
		t.Fatalf("repair turnover fields are not precisely unknown: %#v", afterUnknown)
	}
	partialTrace := filepath.Join(work, "partial-trace.jsonl")
	partial := exec.Command(terraform, "apply", "-input=false", "-no-color", repairPlan)
	partial.Dir = work
	partial.Env = append(baseEnv, "OBSTF_TF_TRACE_PATH="+partialTrace, "OBSTF_TF_INJECT_AFTER_NATIVE_TYPE=obstf:CreateInputInGroup")
	partialOutput, partialErr := partial.CombinedOutput()
	if partialErr == nil || !strings.Contains(string(partialOutput), "injected interruption after authoritative obstf:CreateInputInGroup response") {
		t.Fatalf("partial repair did not stop after authoritative replacement: %v\n%s", partialErr, partialOutput)
	}
	if strings.Contains(string(partialOutput), "Provider produced inconsistent result after apply") {
		t.Fatalf("repair turnover fields remained incorrectly known:\n%s", partialOutput)
	}

	continuationPlan := filepath.Join(work, "continuation.plan")
	continuation := exec.Command(terraform, "plan", "-input=false", "-no-color", "-out", continuationPlan)
	continuation.Dir = work
	continuation.Env = baseEnv
	if output, err := continuation.CombinedOutput(); err != nil {
		t.Fatalf("partial continuation Terraform plan: %v\n%s", err, output)
	}
	show = exec.Command(terraform, "show", "-json", continuationPlan)
	show.Dir = work
	show.Env = baseEnv
	showOutput, err = show.Output()
	if err != nil {
		t.Fatalf("show continuation plan: %v", err)
	}
	planDocument = nil
	if err := json.Unmarshal(showOutput, &planDocument); err != nil {
		t.Fatal(err)
	}
	var continuationChange map[string]any
	for _, raw := range sliceValue(planDocument["resource_changes"]) {
		change := mapValue(raw)
		if stringValue(change["address"]) == "obs_group_graph.repair" {
			continuationChange = mapValue(change["change"])
			break
		}
	}
	continuationAfter := mapValue(continuationChange["after"])
	continuationChildren := sliceValue(continuationAfter["children"])
	continuationUnknown := mapValue(continuationChange["after_unknown"])
	continuationUnknownChildren := sliceValue(continuationUnknown["children"])
	if len(continuationChildren) != 2 || len(continuationUnknownChildren) != 2 || stringValue(mapValue(continuationChildren[0])["source_uuid"]) != "input-left-new" || int64Value(mapValue(continuationChildren[0])["item_id"]) != 3 || stringValue(mapValue(continuationChildren[1])["source_uuid"]) != rightUUID || int64Value(mapValue(continuationChildren[1])["item_id"]) != rightItemID || boolValue(mapValue(continuationUnknownChildren[0])["source_uuid"]) || boolValue(mapValue(continuationUnknownChildren[0])["item_id"]) || boolValue(mapValue(continuationUnknownChildren[1])["source_uuid"]) || boolValue(mapValue(continuationUnknownChildren[1])["item_id"]) || boolValue(continuationUnknown["repair_nonce"]) {
		t.Fatalf("partial continuation did not retain exact replacement/survivor identities: after=%#v unknown=%#v", continuationAfter, continuationUnknown)
	}
	fake.mu.Lock()
	callStart := len(fake.calls)
	fake.mu.Unlock()
	repairTrace := filepath.Join(work, "continuation-trace.jsonl")
	repair := exec.Command(terraform, "apply", "-input=false", "-no-color", continuationPlan)
	repair.Dir = work
	repair.Env = append(baseEnv, "OBSTF_TF_TRACE_PATH="+repairTrace)
	if output, err := repair.CombinedOutput(); err != nil {
		t.Fatalf("partial continuation Terraform apply: %v\n%s", err, output)
	}
	fake.mu.Lock()
	groupExists, groupUUID, parentItemID := fake.groupExists, fake.groupUUID, fake.parentItemID
	children := append([]groupGraphFakeChild(nil), fake.children...)
	repairCalls := append([]lunaLifecycleCall(nil), fake.calls[callStart:]...)
	parentX := fake.parentPlacement.Transform.X.ValueFloat64()
	fake.mu.Unlock()
	if !groupExists || groupUUID != lunaLifecycleGroupUUID || parentItemID != 7 {
		t.Fatalf("group identity changed during repair: exists=%t uuid=%s item=%d", groupExists, groupUUID, parentItemID)
	}
	var leftAfter, rightAfter *groupGraphFakeChild
	for i := range children {
		switch children[i].name {
		case "Party Light Left":
			leftAfter = &children[i]
		case "Party Light Right":
			rightAfter = &children[i]
		}
	}
	if len(children) != 2 || rightAfter == nil || rightAfter.uuid != rightUUID || rightAfter.itemID != rightItemID {
		t.Fatalf("repair did not preserve right identity: %#v", children)
	}
	if leftAfter == nil || leftAfter.placement.Transform.X.ValueFloat64() != 0 || rightAfter.placement.Transform.X.ValueFloat64() != 960 || parentX != 0 {
		t.Fatalf("repair geometry=%#v parent_x=%v", children, parentX)
	}
	var setters int
	var membershipReplays int
	for _, call := range repairCalls {
		if call.typ == "CallVendorRequest" && stringValue(call.data["requestType"]) == "CreateInputInGroup" {
			membershipReplays++
		}
		if call.typ != "SetSceneItemTransform" {
			continue
		}
		setters++
		transform := mapValue(call.data["sceneItemTransform"])
		if stringValue(transform["boundsType"]) == "OBS_BOUNDS_NONE" {
			if _, present := transform["boundsWidth"]; present {
				t.Fatalf("repair setter emitted schema-invalid NONE boundsWidth: %#v", transform)
			}
			if _, present := transform["boundsHeight"]; present {
				t.Fatalf("repair setter emitted schema-invalid NONE boundsHeight: %#v", transform)
			}
		}
	}
	if setters != 2 {
		t.Fatalf("repair SetSceneItemTransform calls=%d want survivor+parent", setters)
	}
	if membershipReplays != 0 {
		t.Fatalf("partial continuation replayed CreateInputInGroup %d time(s)", membershipReplays)
	}
	trace, err := os.ReadFile(repairTrace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(trace), `"status":"failed-or-uncertain"`) || !strings.Contains(string(trace), `"request_type":"SetSceneItemTransform"`) {
		t.Fatalf("repair trace did not prove successful setter path:\n%s", trace)
	}

	noOpPlan := filepath.Join(work, "noop.plan")
	noOp := exec.Command(terraform, "plan", "-input=false", "-no-color", "-detailed-exitcode", "-out", noOpPlan)
	noOp.Dir = work
	noOp.Env = baseEnv
	if output, err := noOp.CombinedOutput(); err != nil {
		t.Fatalf("post-repair plan is not empty: %v\n%s", err, output)
	}
	noOpTrace := filepath.Join(work, "noop-trace.jsonl")
	noOpApply := exec.Command(terraform, "apply", "-input=false", "-no-color", noOpPlan)
	noOpApply.Dir = work
	noOpApply.Env = append(baseEnv, "OBSTF_TF_TRACE_PATH="+noOpTrace)
	if output, err := noOpApply.CombinedOutput(); err != nil {
		t.Fatalf("post-repair no-op apply: %v\n%s", err, output)
	}
	noOpTraceBytes, err := os.ReadFile(noOpTrace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(noOpTraceBytes), `"event":"trace-ready"`) || strings.Contains(string(noOpTraceBytes), `"event":"mutation"`) {
		t.Fatalf("post-repair no-op trace is not initialized/zero-write:\n%s", noOpTraceBytes)
	}
}

func groupGraphCLIRepairConfig(port int, leftAsset, rightAsset string) string {
	return fmt.Sprintf(`
terraform {
  required_version = "= 1.15.7"
  required_providers {
    obs = { source = "digibugcat/obs", version = "0.1.0" }
  }
}

provider "obs" {
  host = "127.0.0.1"
  port = %d
  password = "local-fake-no-auth"
}

locals {
  placement = {
    enabled = true
    locked = false
    index = 0
    transform = {
      x = 0
      y = 0
      scale_x = 1
      scale_y = 1
      rotation = 0
      alignment = 5
      bounds_type = "OBS_BOUNDS_NONE"
      bounds_alignment = 0
      bounds_width = 0
      bounds_height = 0
      crop_to_bounds = false
      crop_left = 0
      crop_top = 0
      crop_right = 0
      crop_bottom = 0
    }
  }
}

resource "obs_group_graph" "repair" {
  collection = %s
  scene_name = %s
  scene_uuid = %s
  name = %s
  parent_present = true
  parent_placement = merge(local.placement, { enabled = false })
  children = [
    {
      stable_key = "left"
      name = "Party Light Left"
      kind = "image_source"
      file = %s
      unload = false
      present = true
      placement = local.placement
    },
    {
      stable_key = "right"
      name = "Party Light Right"
      kind = "image_source"
      file = %s
      unload = false
      present = true
      placement = merge(local.placement, {
        index = 1
        transform = merge(local.placement.transform, { x = 960 })
      })
    }
  ]
}
`, port, strconv.Quote(lunaLifecycleCollection), strconv.Quote(lunaLifecycleSceneName), strconv.Quote(lunaLifecycleSceneUUID), strconv.Quote(lunaLifecycleGroupName), strconv.Quote(leftAsset), strconv.Quote(rightAsset))
}

func newGroupGraphWebsocketServer(t *testing.T, fake *groupGraphFakeOBS) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := context.Background()
		if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 0, map[string]any{"obsWebSocketVersion": "5.7.4", "rpcVersion": 1}); err != nil {
			return
		}
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var identify struct {
			Op int `json:"op"`
		}
		if err := json.Unmarshal(payload, &identify); err != nil || identify.Op != 1 {
			return
		}
		if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 2, map[string]any{"negotiatedRpcVersion": 1}); err != nil {
			return
		}
		for {
			_, payload, err = conn.Read(ctx)
			if err != nil {
				return
			}
			var envelope struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				return
			}
			serve := func(typ string, data map[string]any) (map[string]any, map[string]any) {
				responseData, requestErr := fake.Req(ctx, typ, data)
				status := map[string]any{"result": requestErr == nil, "code": 100}
				if requestErr != nil {
					status["code"], status["comment"] = 500, requestErr.Error()
				}
				return status, responseData
			}
			switch envelope.Op {
			case 6:
				var request struct {
					Type string         `json:"requestType"`
					ID   string         `json:"requestId"`
					Data map[string]any `json:"requestData"`
				}
				if err := json.Unmarshal(envelope.D, &request); err != nil {
					return
				}
				status, responseData := serve(request.Type, request.Data)
				if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 7, map[string]any{"requestType": request.Type, "requestId": request.ID, "requestStatus": status, "responseData": responseData}); err != nil {
					return
				}
			case 8: // RequestBatch: positional results, optional halt on first failure
				var batch struct {
					ID       string `json:"requestId"`
					Halt     bool   `json:"haltOnFailure"`
					Requests []struct {
						Type string         `json:"requestType"`
						Data map[string]any `json:"requestData"`
					} `json:"requests"`
				}
				if err := json.Unmarshal(envelope.D, &batch); err != nil {
					return
				}
				results := []any{}
				for _, item := range batch.Requests {
					status, responseData := serve(item.Type, item.Data)
					results = append(results, map[string]any{"requestType": item.Type, "requestStatus": status, "responseData": responseData})
					if batch.Halt && status["result"] == false {
						break
					}
				}
				if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 9, map[string]any{"requestId": batch.ID, "results": results}); err != nil {
					return
				}
			default:
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeGroupGraphWebsocketEnvelope(ctx context.Context, conn *websocket.Conn, op int, data any) error {
	payload, err := json.Marshal(map[string]any{"op": op, "d": data})
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}
