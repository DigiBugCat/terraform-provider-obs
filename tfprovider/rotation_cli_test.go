package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This is a protocol regression against an isolated fake, not native OBS
// qualification. Capture and reject the raw setter value before the shared
// fake's transform serializer can conceal a provider normalization bug.
type rotationCLIFake struct {
	mu       sync.Mutex
	base     *genericSourceFake
	supplied []float64
}

func (f *rotationCLIFake) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if typ == "SetSceneItemTransform" {
		transform := mapValue(data["sceneItemTransform"])
		rotation, ok := numberValue(transform["rotation"])
		if !ok {
			return nil, fmt.Errorf("rotation must be numeric")
		}
		f.supplied = append(f.supplied, rotation)
		if math.IsNaN(rotation) || math.IsInf(rotation, 0) || rotation < -360 || rotation > 360 {
			return nil, fmt.Errorf("rotation %g outside [-360,360]", rotation)
		}
		// libobs stores scene-item rotation as a float.
		transform["rotation"] = float64(float32(rotation))
	}
	response, err := f.base.Req(ctx, typ, data)
	if err == nil && typ == "GetSceneItemList" {
		// Return the stored rotation directly, independent of native(), which
		// is itself production code under test.
		for _, raw := range sliceValue(response["sceneItems"]) {
			mapValue(mapValue(raw)["sceneItemTransform"])["rotation"] = f.base.transform.Rotation.ValueFloat64()
		}
	}
	return response, err
}

func TestTerraformCLIRotationNormalization(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI regression")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	terraform := filepath.Join(root, ".tools/terraform/1.15.7/terraform")
	if _, err := os.Stat(terraform); err != nil {
		t.Skip(err)
	}
	f := &rotationCLIFake{base: newGenericSourceFake()}
	server := newGenericFilterWebsocketServer(t, f)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	providers := filepath.Join(work, "providers")
	if err := os.Mkdir(providers, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(providers, "terraform-provider-obs"), ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("provider build: %v\n%s", err, out)
	}
	rc := filepath.Join(work, "terraform.rc")
	if err := os.WriteFile(rc, []byte(fmt.Sprintf(`provider_installation { dev_overrides { "registry.terraform.io/digibugcat/obs" = %q } direct {} }`, providers)), 0600); err != nil {
		t.Fatal(err)
	}
	writeConfig := func(rotation float64) {
		t.Helper()
		config := fmt.Sprintf(`terraform {
 required_providers { obs = { source = "digibugcat/obs" } }
}
provider "obs" {
 host = "127.0.0.1"
 port = %s
 password = "fake"
}
resource "obs_source" "wheel" {
 collection = "Lab"
 name = "Plugin"
 kind = "plugin_input_v2"
 settings_json = "{\"gain\":1}"
 anchor_scene_name = "Scene"
 anchor_scene_uuid = "scene-1"
 anchor_enabled = true
 anchor_index = 0
 anchor_transform = {
  x = 0
  y = 0
  scale_x = 1
  scale_y = 1
  rotation = %.17g
  crop_left = 0
  crop_top = 0
  crop_right = 0
  crop_bottom = 0
  alignment = 5
  bounds_type = "OBS_BOUNDS_NONE"
  bounds_alignment = 0
 }
}
`, endpoint.Port(), rotation)
		if err := os.WriteFile(filepath.Join(work, "main.tf"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(terraform, args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_DATA_DIR="+filepath.Join(work, ".terraform"), "TF_IN_AUTOMATION=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("terraform %v: %v\n%s", args, err, out)
		}
	}
	assertState := func(want float64) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(work, "terraform.tfstate"))
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Resources []struct {
				Type      string `json:"type"`
				Instances []struct {
					Attributes struct {
						Transform struct {
							Rotation float64 `json:"rotation"`
						} `json:"anchor_transform"`
					} `json:"attributes"`
				} `json:"instances"`
			} `json:"resources"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Resources) != 1 || state.Resources[0].Type != "obs_source" || len(state.Resources[0].Instances) != 1 {
			t.Fatalf("unexpected state: %s", data)
		}
		if got := state.Resources[0].Instances[0].Attributes.Transform.Rotation; got != want {
			t.Fatalf("state rotation = %g; want authored %g", got, want)
		}
	}
	writeCount := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.base.writes())
	}
	assertNoOp := func() {
		t.Helper()
		before := writeCount()
		run("plan", "-detailed-exitcode", "-input=false", "-no-color")
		if got := writeCount(); got != before {
			t.Fatalf("no-op plan wrote to OBS fake: %d -> %d", before, got)
		}
	}
	assertNative := func(want float64) {
		t.Helper()
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.supplied) == 0 || f.supplied[len(f.supplied)-1] != want {
			t.Fatalf("raw setter rotations = %v; want latest %g", f.supplied, want)
		}
		if got := f.base.transform.Rotation.ValueFloat64(); got != float64(float32(want)) {
			t.Fatalf("stored native rotation = %g; want %g", got, want)
		}
	}

	const authored = 12524.91015625
	writeConfig(authored)
	run("apply", "-auto-approve", "-input=false", "-no-color")
	assertNative(284.91015625)
	assertState(authored)
	assertNoOp()

	// A full-turn-only configuration edit updates Terraform's authored value,
	// but should not touch the already-equivalent native transform.
	writeConfig(authored + 360)
	before := writeCount()
	run("apply", "-auto-approve", "-input=false", "-no-color")
	if got := writeCount(); got != before {
		t.Fatalf("equivalent full-turn edit wrote to OBS fake: %d -> %d", before, got)
	}
	assertState(authored + 360)
	assertNoOp()

	f.mu.Lock()
	f.base.transform.Rotation = types.Float64Value(12)
	f.mu.Unlock()
	before = writeCount()
	run("apply", "-auto-approve", "-input=false", "-no-color")
	if writeCount() <= before {
		t.Fatal("real angular drift was not repaired")
	}
	assertNative(284.91015625)
	assertState(authored + 360)
	assertNoOp()

	writeConfig(-authored)
	run("apply", "-auto-approve", "-input=false", "-no-color")
	assertNative(-284.91015625)
	assertState(-authored)
	assertNoOp()

	// This remainder is not exactly representable as a native float. Keep the
	// precise authored decimal in state without perpetual refresh drift.
	writeConfig(1080.1)
	run("apply", "-auto-approve", "-input=false", "-no-color")
	assertNative(float64(float32(0.1)))
	assertState(1080.1)
	assertNoOp()
}
