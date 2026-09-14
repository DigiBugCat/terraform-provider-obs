package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

type sceneContentsCLIFake struct {
	mu   sync.Mutex
	base *sceneContentsFake
}

func (f *sceneContentsCLIFake) Req(ctx context.Context, kind string, data map[string]any) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.base.Req(ctx, kind, data)
}
func TestTerraformCLISceneContentsDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI fake regression")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	terraform := filepath.Join(root, ".tools/terraform/1.15.7/terraform")
	if _, err = os.Stat(terraform); err != nil {
		t.Skip(err)
	}
	f := &sceneContentsCLIFake{base: newSceneContentsFake("scene", 31)}
	server := newGenericFilterWebsocketServer(t, f)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	providers := filepath.Join(work, "providers")
	if err = os.Mkdir(providers, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(providers, "terraform-provider-obs"), ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	rc := filepath.Join(work, "terraform.rc")
	if err = os.WriteFile(rc, []byte(fmt.Sprintf(`provider_installation { dev_overrides { "registry.terraform.io/digibugcat/obs" = %q } direct {} }`, providers)), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`terraform {
 required_providers { obs = { source = "digibugcat/obs" } }
}
provider "obs" {
 host = "127.0.0.1"
 port = %s
 password = "fake"
}
resource "obs_scene_contents" "test" {
 collection = %q
 scene_name = %q
 scene_uuid = %q
 item_ids = [31]
}
`, endpoint.Port(), lunaLifecycleCollection, f.base.sceneName, lunaLifecycleSceneUUID)
	if err = os.WriteFile(filepath.Join(work, "main.tf"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(code int, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(terraform, args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_DATA_DIR="+filepath.Join(work, ".terraform"), "TF_IN_AUTOMATION=1")
		out, err := cmd.CombinedOutput()
		got := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				got = ee.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if got != code {
			t.Fatalf("%v exit=%d expected=%d: %s", args, got, code, out)
		}
		return out
	}
	run(0, "apply", "-auto-approve", "-input=false", "-no-color")
	run(0, "plan", "-detailed-exitcode", "-input=false", "-no-color")
	f.mu.Lock()
	f.base.items[99] = itemObservation{SceneName: f.base.sceneName, SourceName: "Manual", SourceUUID: "manual-source", ItemID: 99, Index: 1, Transform: groupItemTransform()}
	f.mu.Unlock()
	run(2, "plan", "-detailed-exitcode", "-input=false", "-no-color", "-out=prune.tfplan")
	raw := run(0, "show", "-json", "prune.tfplan")
	var plan struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Change  struct {
				Actions       []string `json:"actions"`
				Before, After map[string]any
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("plan JSON: %v %s", err, raw)
	}
	if len(plan.ResourceChanges) != 1 || len(plan.ResourceChanges[0].Change.Actions) != 1 || plan.ResourceChanges[0].Change.Actions[0] != "update" {
		t.Fatalf("missing visible update: %s", raw)
	}
	change := plan.ResourceChanges[0].Change
	if len(sliceValue(change.Before["item_ids"])) != 2 || len(sliceValue(change.After["item_ids"])) != 1 {
		t.Fatalf("plan did not show extra placement removal: %s", raw)
	}
	run(0, "apply", "-input=false", "-no-color", "prune.tfplan")
	run(0, "plan", "-detailed-exitcode", "-input=false", "-no-color")
	run(0, "destroy", "-auto-approve", "-input=false", "-no-color")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.base.items) != 1 {
		t.Fatal("destroy must release contents contract and leave owned placement")
	}
}
