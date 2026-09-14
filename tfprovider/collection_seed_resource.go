package tfprovider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DigiBugCat/terraform-provider-obs/internal/absoluteshell"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// collectionSeedResource owns initial collection existence and absolute mode,
// never the graph subsequently saved by OBS. It has no Configure method: all
// operations are local filesystem operations and never connect to OBS.
type collectionSeedResource struct{ stopped func(context.Context) error }
type collectionSeedModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Path               types.String `tfsdk:"path"`
	Directory          types.String `tfsdk:"scene_collection_directory"`
	BootstrapSceneUUID types.String `tfsdk:"bootstrap_scene_uuid"`
	SeedSHA256         types.String `tfsdk:"seed_sha256"`
}

func NewCollectionSeedResource() resource.Resource { return &collectionSeedResource{} }
func (r *collectionSeedResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_collection_seed"
}
func (r *collectionSeedResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Cold bootstrap of a dedicated local absolute-coordinate OBS collection. Apply only with OBS closed, then start OBS and select the collection before applying scene resources. Never converts existing collections. Destroy retains the file and all scenes; it only forgets this binding.", Attributes: map[string]schema.Attribute{
		"id":                         identityString(),
		"name":                       immutableString(true),
		"path":                       immutableString(true),
		"scene_collection_directory": immutableString(true),
		"bootstrap_scene_uuid":       identityString(),
		"seed_sha256":                identityString(),
	}}
}
func collectionSeedConfigured(m collectionSeedModel) (string, error) {
	base, err := absoluteshell.CollectionFilename(m.Name.ValueString())
	if err != nil {
		return "", err
	}
	dir, path := m.Directory.ValueString(), m.Path.ValueString()
	if !filepath.IsAbs(dir) || !filepath.IsAbs(path) || filepath.Clean(dir) != dir || filepath.Clean(path) != path {
		return "", fmt.Errorf("path and scene_collection_directory must be clean absolute paths")
	}
	if filepath.Dir(path) != dir || filepath.Base(path) != base {
		return "", fmt.Errorf("path must be exactly scene_collection_directory/%s", base)
	}
	return base, nil
}
func (r *collectionSeedResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m collectionSeedModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, v := range []types.String{m.Name, m.Path, m.Directory} {
		if v.IsUnknown() || v.IsNull() {
			return
		}
	}
	if _, err := collectionSeedConfigured(m); err != nil {
		resp.Diagnostics.AddError("Invalid OBS collection seed", err.Error())
	}
}
func collectionSeedRoot(m collectionSeedModel) (*os.Root, string, error) {
	base, err := collectionSeedConfigured(m)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(m.Directory.ValueString())
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("scene_collection_directory must be an existing directory, not a symlink")
	}
	root, err := os.OpenRoot(m.Directory.ValueString())
	return root, base, err
}
func collectionSeedReadFile(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("collection file %q must be regular, not a symlink", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, fmt.Errorf("collection file changed during inspection")
	}
	// Bound accidental reads of unrelated or corrupt files.
	b, err := io.ReadAll(io.LimitReader(f, 64<<20+1))
	if err == nil && len(b) > 64<<20 {
		err = fmt.Errorf("collection file exceeds 64 MiB inspection limit")
	}
	return b, err
}
func collectionSeedJSON(b []byte) (map[string]any, error) {
	var raw map[string]any
	err := json.Unmarshal(b, &raw)
	if err == nil && raw == nil {
		err = fmt.Errorf("collection must be a JSON object")
	}
	return raw, err
}
func collectionSeedCheckStopped(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "comm=").Output()
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return fmt.Errorf("cannot establish OBS process status; refusing collection bootstrap")
	}
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.ToLower(strings.TrimSpace(line))
		base := filepath.Base(s)
		if base == "obs" || base == "obs64.exe" || base == "obs32.exe" || strings.Contains(s, "obs.app/contents/macos/") {
			return fmt.Errorf("OBS is running; quit OBS before applying the collection bootstrap")
		}
	}
	return nil
}
func (r *collectionSeedResource) checkStopped(ctx context.Context) error {
	if r.stopped != nil {
		return r.stopped(ctx)
	}
	return collectionSeedCheckStopped(ctx)
}
func collectionSeedSetIdentity(m *collectionSeedModel, seed []byte) {
	raw, _ := collectionSeedJSON(seed) // Bytes has already validated this shape.
	sources := raw["sources"].([]any)
	m.ID = types.StringValue(m.Path.ValueString())
	m.BootstrapSceneUUID = types.StringValue(sources[0].(map[string]any)["uuid"].(string))
	sum := sha256.Sum256(seed)
	m.SeedSHA256 = types.StringValue(hex.EncodeToString(sum[:]))
}
func (r *collectionSeedResource) create(ctx context.Context, m *collectionSeedModel) error {
	root, base, err := collectionSeedRoot(*m)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := r.checkStopped(ctx); err != nil {
		return err
	}
	seed, err := absoluteshell.Bytes(m.Name.ValueString())
	if err != nil {
		return err
	}
	// OBS collection identity is its name, not its filename: refuse duplicate
	// names in another file before making a new one discoverable to OBS.
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		b, err := collectionSeedReadFile(root, entry.Name())
		if err != nil {
			return err
		}
		raw, err := collectionSeedJSON(b)
		if err != nil {
			return fmt.Errorf("inspect collection %s: %w", entry.Name(), err)
		}
		if entry.Name() != base && raw["name"] == m.Name.ValueString() {
			return fmt.Errorf("collection %q already exists in %s; refusing duplicate", m.Name.ValueString(), entry.Name())
		}
	}
	if b, err := collectionSeedReadFile(root, base); err == nil {
		if !bytes.Equal(b, seed) {
			return fmt.Errorf("target collection already exists and differs from the empty seed; refusing overwrite or adoption")
		}
		collectionSeedSetIdentity(m, seed)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	staged := ".terraform-obs-seed-" + hex.EncodeToString(nonce[:])
	f, err := root.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(staged)
	_, writeErr := f.Write(seed)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := r.checkStopped(ctx); err != nil {
		return err
	}
	// Link publishes a complete file atomically and fails if the target exists;
	// unlike rename it can never replace a collection created since inspection.
	if err := root.Link(staged, base); err != nil {
		return fmt.Errorf("publish collection without replacement: %w", err)
	}
	collectionSeedSetIdentity(m, seed)
	return nil
}
func (r *collectionSeedResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m collectionSeedModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.create(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Create OBS collection seed failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func collectionSeedInspect(m collectionSeedModel) (bool, error) {
	root, base, err := collectionSeedRoot(m)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	b, err := collectionSeedReadFile(root, base)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := collectionSeedJSON(b)
	if err != nil {
		return false, err
	}
	// OBS is expected to populate this file. Never compare its current digest to
	// seed_sha256 or replace it to make it match the original empty shell.
	if _, err := absoluteshell.ValidatePersisted(raw, m.Name.ValueString()); err != nil {
		return false, err
	}
	return true, nil
}
func (r *collectionSeedResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m collectionSeedModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	exists, err := collectionSeedInspect(m)
	if err != nil {
		resp.Diagnostics.AddError("Inspect OBS collection seed failed", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *collectionSeedResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("OBS collection seed is immutable", "Replace the Terraform binding to change the seed configuration. Existing collection files are always retained.")
}
func (r *collectionSeedResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Never unlink the collection: it can contain arbitrary user-created scenes.
}

var _ resource.ResourceWithValidateConfig = (*collectionSeedResource)(nil)
