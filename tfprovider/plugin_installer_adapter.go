package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DigiBugCat/terraform-provider-obs/internal/obsplugins"
	"path/filepath"
)

type nativePluginInstallation struct{}

func pluginSpec(m pluginModel) (obsplugins.Spec, string, error) {
	p, base, err := obsplugins.Load(m.ManifestPath.ValueString(), m.ManifestSHA256.ValueString(), m.PluginID.ValueString())
	if err != nil {
		return p, base, err
	}
	if p.Version != m.Version.ValueString() {
		return p, base, fmt.Errorf("manifest plugin version %q differs from configured version %q", p.Version, m.Version.ValueString())
	}
	return p, base, nil
}
func nativePluginInspection(m pluginModel, p obsplugins.Spec, r obsplugins.Receipt) pluginInspection {
	got := pluginInspection{Installed: r.Status == "matching" || r.Status == "installed" || r.Status == "unchanged", ID: filepath.Join(m.InstallDir.ValueString(), p.Bundle), BackupPath: r.Backup}
	b, _ := json.Marshal(p)
	got.PinnedSpecJSON = string(b)
	if got.Installed {
		got.ContentSHA256 = obsplugins.Digest(p)
	}
	return got
}
func (nativePluginInstallation) Inspect(_ context.Context, m pluginModel) (pluginInspection, error) {
	var p obsplugins.Spec
	if m.PinnedSpecJSON.IsNull() || m.PinnedSpecJSON.IsUnknown() || m.PinnedSpecJSON.ValueString() == "" {
		return pluginInspection{}, fmt.Errorf("installed plugin state is missing its pinned specification")
	}
	if err := json.Unmarshal([]byte(m.PinnedSpecJSON.ValueString()), &p); err != nil {
		return pluginInspection{}, fmt.Errorf("invalid saved plugin specification")
	}
	if p.ID != m.PluginID.ValueString() || p.Version != m.Version.ValueString() {
		return pluginInspection{}, fmt.Errorf("saved plugin specification identity mismatch")
	}

	return nativePluginInspection(m, p, obsplugins.Inspect(m.InstallDir.ValueString(), p)), nil
}
func (nativePluginInstallation) Install(ctx context.Context, m pluginModel, replace bool) (pluginInspection, error) {
	p, base, err := pluginSpec(m)
	if err != nil {
		return pluginInspection{}, err
	}
	target := filepath.Join(m.InstallDir.ValueString(), p.Bundle)
	if !m.ID.IsUnknown() && !m.ID.IsNull() && m.ID.ValueString() != "" && m.ID.ValueString() != target {
		return pluginInspection{}, fmt.Errorf("manifest changed installed bundle identity; use a separate explicit resource for %s", target)
	}
	r, err := obsplugins.Install(ctx, m.InstallDir.ValueString(), base, p, replace)
	got := nativePluginInspection(m, p, r)
	if err != nil && !got.Installed && got.BackupPath == "" {
		got.ID = ""
	}
	if err == nil {
		verified := obsplugins.Inspect(m.InstallDir.ValueString(), p)
		if verified.Status != "matching" {
			got.Installed = false
			got.ContentSHA256 = ""
			return got, fmt.Errorf("installed plugin failed independent final inspection")
		}
	}
	return got, err
}
