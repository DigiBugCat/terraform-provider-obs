package tfprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework-validators/boolvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type pluginModel struct {
	ID              types.String `tfsdk:"id"`
	ManifestPath    types.String `tfsdk:"manifest_path"`
	ManifestSHA256  types.String `tfsdk:"manifest_sha256"`
	PluginID        types.String `tfsdk:"plugin_id"`
	Version         types.String `tfsdk:"version"`
	InstallDir      types.String `tfsdk:"install_dir"`
	ReplaceExisting types.Bool   `tfsdk:"replace_existing"`
	RetainOnDestroy types.Bool   `tfsdk:"retain_on_destroy"`
	Installed       types.Bool   `tfsdk:"installed"`
	ContentSHA256   types.String `tfsdk:"content_sha256"`
	BackupPath      types.String `tfsdk:"backup_path"`
	PinnedSpecJSON  types.String `tfsdk:"pinned_spec_json"`
}

// pluginInstallation is an apply-only boundary. Inspect must be local and read-only.
// Install implementations verify the pinned manifest and bundle before any swap.
type pluginInstallation interface {
	Inspect(context.Context, pluginModel) (pluginInspection, error)
	Install(context.Context, pluginModel, bool) (pluginInspection, error)
}
type pluginInspection struct {
	Installed                                     bool
	ID, ContentSHA256, BackupPath, PinnedSpecJSON string
}
type pluginResource struct{ installer pluginInstallation }

func NewPluginResource() resource.Resource { return &pluginResource{} }
func (r *pluginResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin"
}
func (r *pluginResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	home, _ := os.UserHomeDir()
	resp.Schema = schema.Schema{Description: "Explicit manifest-pinned OBS plugin installation. Plan and refresh inspect only; apply installs with OBS stopped. Destroy retains the installed plugin.", Attributes: map[string]schema.Attribute{
		"id":                identityString(),
		"pinned_spec_json":  schema.StringAttribute{Computed: true, Sensitive: true, Description: "Pinned specification retained for read-only refresh even if the manifest file changes."},
		"manifest_path":     schema.StringAttribute{Required: true, Description: "Local manifest file; read and hash-verified before install."},
		"manifest_sha256":   schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(64, 64)}},
		"plugin_id":         immutableString(true),
		"version":           schema.StringAttribute{Required: true},
		"install_dir":       schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(filepath.Join(home, "Library", "Application Support", "obs-studio", "plugins")), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"replace_existing":  schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Allow initial takeover of a differing existing bundle, preserving a backup. Updates to the managed installation already preserve and replace drift."},
		"retain_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Validators: []validator.Bool{boolvalidator.Equals(true)}, Description: "Must be true. Terraform forgets the binding on destroy; plugin removal is an explicit separate operation."},
		"installed":         schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Validators: []validator.Bool{boolvalidator.Equals(true)}, Description: "Desired installed state, always true. Refresh sets false for missing or mismatched payload, producing a visible repair plan."},
		"content_sha256":    schema.StringAttribute{Computed: true, Description: "Verified installed content digest; empty when contents are missing or differ."},
		"backup_path":       schema.StringAttribute{Computed: true, Description: "Backup retained by the most recent replacing install, if any."},
	}}
}
func (r *pluginResource) backend() pluginInstallation {
	if r.installer != nil {
		return r.installer
	}
	return nativePluginInstallation{}
}
func pluginConfigured(m pluginModel) error {
	if m.PluginID.ValueString() == "" || m.Version.ValueString() == "" || m.ManifestPath.ValueString() == "" || m.InstallDir.ValueString() == "" {
		return fmt.Errorf("plugin_id, version, manifest_path and install_dir must be non-empty")
	}
	if !filepath.IsAbs(m.ManifestPath.ValueString()) || !filepath.IsAbs(m.InstallDir.ValueString()) {
		return fmt.Errorf("manifest_path and install_dir must be absolute paths")
	}
	return nil
}
func writePluginInspection(m *pluginModel, got pluginInspection, retainBackup bool) {
	m.ID = types.StringValue(got.ID)
	if got.PinnedSpecJSON != "" {
		m.PinnedSpecJSON = types.StringValue(got.PinnedSpecJSON)
	}
	m.Installed = types.BoolValue(got.Installed)
	m.ContentSHA256 = types.StringValue(got.ContentSHA256)
	if !retainBackup || got.BackupPath != "" {
		m.BackupPath = types.StringValue(got.BackupPath)
	}
}
func (r *pluginResource) install(ctx context.Context, m *pluginModel, replace bool) diag.Diagnostics {
	var ds diag.Diagnostics
	if err := pluginConfigured(*m); err != nil {
		ds.AddError("Invalid plugin installation", err.Error())
		m.ID = types.StringNull()
		return ds
	}
	got, err := r.backend().Install(ctx, *m, replace)
	if got.ID != "" {
		writePluginInspection(m, got, false)
	} else {
		m.ID = types.StringNull()
	}
	if err != nil {
		ds.AddError("Plugin install failed", err.Error())
		return ds
	}
	if !got.Installed || got.ID == "" {
		ds.AddError("Plugin install not verified", "Installer did not return a verified installed identity.")
	}
	return ds
}
func (r *pluginResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m pluginModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.install(ctx, &m, m.ReplaceExisting.ValueBool())...)
	// Preserve an authoritative installer receipt even when subsequent verification fails.
	if !m.ID.IsUnknown() && !m.ID.IsNull() && m.ID.ValueString() != "" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *pluginResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m pluginModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := pluginConfigured(m); err != nil {
		resp.Diagnostics.AddError("Invalid plugin installation", err.Error())
		return
	}
	got, err := r.backend().Inspect(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Plugin inspection failed", err.Error())
		return
	}
	if got.ID == "" {
		got.ID = m.ID.ValueString()
	}
	writePluginInspection(&m, got, true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *pluginResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, state pluginModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.install(ctx, &m, true)...)
	if m.BackupPath.ValueString() == "" {
		m.BackupPath = state.BackupPath
	}
	if !m.ID.IsUnknown() && !m.ID.IsNull() && m.ID.ValueString() != "" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *pluginResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Retention is required by schema: deleting Terraform ownership never removes files.
}

// ValidateConfig only reads the local pinned manifest. It never downloads,
// creates directories, installs bundles, or asks OBS to stop/restart.
func (r *pluginResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m pluginModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, v := range []types.String{m.ManifestPath, m.ManifestSHA256, m.PluginID, m.Version} {
		if v.IsNull() || v.IsUnknown() {
			return
		}
	}
	if !filepath.IsAbs(m.ManifestPath.ValueString()) {
		resp.Diagnostics.AddError("Invalid plugin manifest path", "manifest_path must be absolute.")
		return
	}
	if _, _, err := pluginSpec(m); err != nil {
		resp.Diagnostics.AddError("Pinned plugin manifest invalid", err.Error())
	}
}

var _ resource.ResourceWithValidateConfig = (*pluginResource)(nil)
