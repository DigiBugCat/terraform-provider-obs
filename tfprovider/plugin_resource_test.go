package tfprovider

import (
	"context"
	"errors"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"testing"
)

type pluginFakeInstaller struct {
	got                pluginInspection
	err                error
	installs, inspects int
	replace            bool
}

func (f *pluginFakeInstaller) Inspect(context.Context, pluginModel) (pluginInspection, error) {
	f.inspects++
	return f.got, f.err
}
func (f *pluginFakeInstaller) Install(_ context.Context, _ pluginModel, replace bool) (pluginInspection, error) {
	f.installs++
	f.replace = replace
	return f.got, f.err
}
func pluginTestModel() pluginModel {
	return pluginModel{ID: types.StringValue("/tmp/plugins/f.plugin"), ManifestPath: types.StringValue("/tmp/manifest.json"), ManifestSHA256: types.StringValue("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), PluginID: types.StringValue("f"), Version: types.StringValue("1"), InstallDir: types.StringValue("/tmp/plugins"), ReplaceExisting: types.BoolValue(false), RetainOnDestroy: types.BoolValue(true), Installed: types.BoolValue(true), ContentSHA256: types.StringValue("digest"), BackupPath: types.StringValue("prior-backup"), PinnedSpecJSON: types.StringValue("{}")}
}
func TestPluginFailedUpdatePreservesPriorStateAndBackup(t *testing.T) {
	ctx := context.Background()
	f := &pluginFakeInstaller{err: errors.New("pinned archive refused")}
	r := &pluginResource{installer: f}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	old := pluginTestModel()
	plan := old
	plan.Version = types.StringValue("2")
	state := groupFixtureSchemaAndState(t, ctx, sc.Schema, &old)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &plan), State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("failure accepted")
	}
	var got pluginModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.Version.ValueString() != "1" || got.BackupPath.ValueString() != "prior-backup" {
		t.Fatal("failed update changed version or backup", got)
	}
	if !f.replace {
		t.Fatal("owned repair did not request backup replacement")
	}
}
func TestPluginReadDriftAndDestroyAreReadOnly(t *testing.T) {
	ctx := context.Background()
	f := &pluginFakeInstaller{got: pluginInspection{ID: "/tmp/plugins/f.plugin", Installed: false}}
	r := &pluginResource{installer: f}
	var sc resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sc)
	m := pluginTestModel()
	state := groupFixtureSchemaAndState(t, ctx, sc.Schema, &m)
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got pluginModel
	groupFixtureRequireState(t, ctx, resp.State, &got)
	if got.Installed.ValueBool() || got.BackupPath.ValueString() != "prior-backup" {
		t.Fatal("drift or backup lost")
	}
	r.Delete(ctx, resource.DeleteRequest{State: resp.State}, &resource.DeleteResponse{})
	if f.installs != 0 || f.inspects != 1 {
		t.Fatal("read/destroy installed files")
	}
}
func TestPluginCreateFailureAndAuthoritativeBackupReceipt(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		ctx := context.Background()
		f := &pluginFakeInstaller{err: errors.New("install failed")}
		if receipt {
			f.got = pluginInspection{ID: "/tmp/plugins/f.plugin", BackupPath: "saved-old-bundle", PinnedSpecJSON: "{}"}
		}
		r := &pluginResource{installer: f}
		var sc resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sc)
		m := pluginTestModel()
		m.ID = types.StringUnknown()
		plan := groupFixtureSchemaAndPlan(t, ctx, sc.Schema, &m)
		state := groupFixtureSchemaAndState(t, ctx, sc.Schema, &m)
		resp := resource.CreateResponse{State: state}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("failed create accepted")
		}
		var got pluginModel
		groupFixtureRequireState(t, ctx, resp.State, &got)
		if receipt && got.BackupPath.ValueString() != "saved-old-bundle" {
			t.Fatal("recovery receipt lost")
		}
		if !receipt && !got.ID.IsUnknown() {
			t.Fatal("invented installed identity")
		}
		if f.replace {
			t.Fatal("initial takeover implicitly allowed")
		}
	}
}
