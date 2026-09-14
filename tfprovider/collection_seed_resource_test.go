package tfprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DigiBugCat/terraform-provider-obs/internal/absoluteshell"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func collectionSeedFixture(t *testing.T) collectionSeedModel {
	t.Helper()
	dir := t.TempDir()
	return collectionSeedModel{Name: types.StringValue("Reference Hierarchy Lab"), Directory: types.StringValue(dir), Path: types.StringValue(filepath.Join(dir, "Mini_Arross_Office_Hierarchy_Lab.json"))}
}
func seedStopped(context.Context) error { return nil }
func seedWrite(t *testing.T, path string, raw map[string]any) {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestCollectionSeedColdCreateAndPopulatedRefresh(t *testing.T) {
	ctx := context.Background()
	m := collectionSeedFixture(t)
	calls := 0
	r := &collectionSeedResource{stopped: func(context.Context) error { calls++; return nil }}
	if err := r.create(ctx, &m); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || m.ID.ValueString() != m.Path.ValueString() || m.BootstrapSceneUUID.ValueString() == "" || len(m.SeedSHA256.ValueString()) != 64 {
		t.Fatalf("invalid receipt: %+v; checks=%d", m, calls)
	}
	initial, err := os.ReadFile(m.Path.ValueString())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := collectionSeedJSON(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := absoluteshell.Validate(raw, m.Name.ValueString()); err != nil {
		t.Fatal(err)
	}
	// Exact seeds are idempotent, including recovery after state storage failed.
	if err := r.create(ctx, &m); err != nil {
		t.Fatal(err)
	}
	raw["sources"] = append(raw["sources"].([]any), map[string]any{"name": "A user-owned scene", "id": "scene", "settings": map[string]any{"items": []any{}}})
	seedWrite(t, m.Path.ValueString(), raw)
	before, _ := os.ReadFile(m.Path.ValueString())
	exists, err := collectionSeedInspect(m)
	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	r.Delete(ctx, resource.DeleteRequest{}, &resource.DeleteResponse{})
	after, _ := os.ReadFile(m.Path.ValueString())
	if string(before) != string(after) {
		t.Fatal("refresh/delete modified populated collection")
	}
	if err := r.create(ctx, &m); err == nil || !strings.Contains(err.Error(), "refusing overwrite") {
		t.Fatalf("populated create: %v", err)
	}
	after, _ = os.ReadFile(m.Path.ValueString())
	if string(before) != string(after) {
		t.Fatal("create modified existing collection")
	}
}
func TestCollectionSeedProcessGuards(t *testing.T) {
	for _, stopAt := range []int{1, 2} {
		t.Run(string(rune('0'+stopAt)), func(t *testing.T) {
			m := collectionSeedFixture(t)
			calls := 0
			r := &collectionSeedResource{stopped: func(context.Context) error {
				calls++
				if calls == stopAt {
					return errors.New("OBS is running")
				}
				return nil
			}}
			if err := r.create(context.Background(), &m); err == nil || !strings.Contains(err.Error(), "OBS is running") {
				t.Fatalf("err=%v", err)
			}
			entries, _ := os.ReadDir(m.Directory.ValueString())
			if len(entries) != 0 {
				t.Fatalf("left files after refusal: %v", entries)
			}
		})
	}
}
func TestCollectionSeedRefusesUnsafePathsAndDuplicateIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *collectionSeedModel)
	}{
		{"outside directory", func(t *testing.T, m *collectionSeedModel) {
			m.Path = types.StringValue(filepath.Join(t.TempDir(), filepath.Base(m.Path.ValueString())))
		}},
		{"noncanonical name", func(t *testing.T, m *collectionSeedModel) { m.Name = types.StringValue("MAIN") }},
		{"wrong filename", func(t *testing.T, m *collectionSeedModel) {
			m.Path = types.StringValue(filepath.Join(m.Directory.ValueString(), "MAIN.json"))
		}},
		{"target symlink", func(t *testing.T, m *collectionSeedModel) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), m.Path.ValueString()); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory symlink", func(t *testing.T, m *collectionSeedModel) {
			alias := filepath.Join(t.TempDir(), "linked")
			if err := os.Symlink(m.Directory.ValueString(), alias); err != nil {
				t.Fatal(err)
			}
			m.Directory = types.StringValue(alias)
			m.Path = types.StringValue(filepath.Join(alias, filepath.Base(m.Path.ValueString())))
		}},
		{"duplicate identity", func(t *testing.T, m *collectionSeedModel) {
			raw, _ := absoluteshell.Build(m.Name.ValueString())
			seedWrite(t, filepath.Join(m.Directory.ValueString(), "different.json"), raw)
		}},
		{"malformed sibling", func(t *testing.T, m *collectionSeedModel) {
			if err := os.WriteFile(filepath.Join(m.Directory.ValueString(), "corrupt.json"), []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := collectionSeedFixture(t)
			tc.edit(t, &m)
			r := &collectionSeedResource{stopped: seedStopped}
			if err := r.create(context.Background(), &m); err == nil {
				t.Fatal("unsafe create accepted")
			}
		})
	}
}
func TestCollectionSeedAtomicPublishRefusesRacingFile(t *testing.T) {
	m := collectionSeedFixture(t)
	calls := 0
	sentinel := []byte(`{"name":"Other collection","version":1}`)
	r := &collectionSeedResource{stopped: func(context.Context) error {
		calls++
		if calls == 2 {
			return os.WriteFile(m.Path.ValueString(), sentinel, 0600)
		}
		return nil
	}}
	if err := r.create(context.Background(), &m); err == nil {
		t.Fatal("replaced concurrent collection")
	}
	got, _ := os.ReadFile(m.Path.ValueString())
	if string(got) != string(sentinel) {
		t.Fatal("concurrent collection changed")
	}
	entries, _ := os.ReadDir(m.Directory.ValueString())
	if len(entries) != 1 {
		t.Fatalf("staging leak: %v", entries)
	}
}
func TestCollectionSeedReadMissingAndDrift(t *testing.T) {
	m := collectionSeedFixture(t)
	if exists, err := collectionSeedInspect(m); err != nil || exists {
		t.Fatalf("missing: %v %v", exists, err)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"identity", func(raw map[string]any) { raw["name"] = "MAIN" }},
		{"relative mode", func(raw map[string]any) { raw["version"] = 2 }},
		{"relative field", func(raw map[string]any) { raw["pos_rel"] = map[string]any{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := absoluteshell.Build(m.Name.ValueString())
			tc.edit(raw)
			seedWrite(t, m.Path.ValueString(), raw)
			if exists, err := collectionSeedInspect(m); err == nil || exists {
				t.Fatalf("drift accepted: %v %v", exists, err)
			}
		})
	}
}

func TestCollectionSeedTerraformCallbacksRetainGraphAndRemoveMissingState(t *testing.T) {
	ctx := context.Background()
	m := collectionSeedFixture(t)
	m.ID = types.StringUnknown()
	m.BootstrapSceneUUID = types.StringUnknown()
	m.SeedSHA256 = types.StringUnknown()
	r := &collectionSeedResource{stopped: seedStopped}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	state := lunaLifecycleSchemaAndState(t, ctx, sc.Schema, &m)
	created := resource.CreateResponse{State: state}
	r.Create(ctx, resource.CreateRequest{Plan: lunaLifecycleSchemaAndPlan(t, ctx, sc.Schema, &m)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var got collectionSeedModel
	lunaLifecycleRequireState(t, ctx, created.State, &got)
	if got.ID.IsNull() || got.ID.IsUnknown() {
		t.Fatal("missing created identity")
	}
	// Refresh is allowed while OBS runs: it must never call the stopped checker.
	r.stopped = func(context.Context) error { t.Fatal("refresh checked OBS process status"); return nil }
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if err := os.Remove(m.Path.ValueString()); err != nil {
		t.Fatal(err)
	}
	missing := resource.ReadResponse{State: read.State}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &missing)
	if missing.Diagnostics.HasError() {
		t.Fatal(missing.Diagnostics)
	}
	if !missing.State.Raw.IsNull() {
		t.Fatal("missing collection retained Terraform identity")
	}
}
