package tfprovider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

const version = "0.1.8"

type Provider struct{ Legacy bool }

type providerModel struct {
	Host                types.String `tfsdk:"host"`
	Port                types.Int64  `tfsdk:"port"`
	Password            types.String `tfsdk:"password"`
	Connections         types.Int64  `tfsdk:"connections"`
	ReadCacheMS         types.Int64  `tfsdk:"read_cache_ms"`
	ConcurrentMutations types.Bool   `tfsdk:"concurrent_mutations"`
	CanvasWidth         types.Int64  `tfsdk:"canvas_width"`
	CanvasHeight        types.Int64  `tfsdk:"canvas_height"`
}

func New() provider.Provider { return &Provider{} }

// NewLegacy preserves historical resource names for existing state workspaces.
func NewLegacy() provider.Provider { return &Provider{Legacy: true} }

func (p *Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "obs"
	if p.Legacy {
		resp.TypeName = "obsiac"
	}
	resp.Version = version
}

func (p *Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = providerschema.Schema{
		Description: "Manages OBS Studio scenes, sources, groups, filters and placements through obs-websocket 5, with a small native bridge plugin for the group operations obs-websocket does not expose. Every write is verified by reading OBS back before the resource is reported as created or changed.",
		Attributes: map[string]providerschema.Attribute{
			"host":          providerschema.StringAttribute{Optional: true, Description: "obs-websocket host. Default 127.0.0.1.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"port":          providerschema.Int64Attribute{Optional: true, Description: "obs-websocket port. Default 4455."},
			"password":      providerschema.StringAttribute{Optional: true, Sensitive: true, Description: "obs-websocket password. When omitted on a local host the provider reads OBS's own obs-websocket config file; remote hosts require it."},
			"canvas_width":  providerschema.Int64Attribute{Optional: true, Description: "Width in pixels of the OBS base canvas this configuration was written for. Default 1920. Set together with canvas_height. The provider refuses to write when the live base canvas differs. Group resources work only on 1920x1080."},
			"canvas_height": providerschema.Int64Attribute{Optional: true, Description: "Height in pixels of the OBS base canvas this configuration was written for. Default 1080. Set together with canvas_width."},
			// Refresh throughput knobs; env fallbacks OBS_CONNECTIONS
			// and OBS_READ_CACHE_MS. read_cache_ms is experimental and off (0) by default.
			"connections":          providerschema.Int64Attribute{Optional: true, Description: "Persistent obs-websocket connections for concurrent reads (default 10; env OBS_CONNECTIONS)."},
			"concurrent_mutations": providerschema.BoolAttribute{Optional: true, Description: "EXPERIMENTAL: run mutations on different scenes, groups and sources concurrently. Default false. OBS 32.2.2 / obs-websocket 5.7.4 stopped answering under this on the reference Mac; enable only against an OBS build verified to survive multi-session writes (env OBS_CONCURRENT_MUTATIONS=1)."},
			"read_cache_ms":        providerschema.Int64Attribute{Optional: true, Description: "EXPERIMENTAL: how long list responses are reused across reads, in milliseconds. Default 0 (off). Enabling it speeds refresh ~2x but an external OBS edit may go unseen for this long (env OBS_READ_CACHE_MS)."},
		}}
}

func (p *Provider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	host, port := data.Host.ValueString(), int(data.Port.ValueInt64())
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = 4455
	}
	cfg := ws.Config{Host: host, Port: port, Password: data.Password.ValueString()}
	if cfg.Port < 1 || cfg.Port > 65535 {
		resp.Diagnostics.AddError("Invalid OBS WebSocket configuration", fmt.Sprintf("port %d is outside 1-65535", cfg.Port))
		return
	}
	// Installation-only plans do not require a running/configured WebSocket.
	// Native resource operations resolve omitted credentials on first connection.
	c := &client{cfg: cfg, discoverConfig: data.Password.IsNull(), portExplicit: !data.Port.IsNull(), credentialsUnknown: data.Password.IsUnknown() || data.Host.IsUnknown() || data.Port.IsUnknown(), connectionsCfg: -1, readCacheMS: -1, concurrentMutations: -1}
	canvas, err := canvasFromConfig(data.CanvasWidth, data.CanvasHeight)
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}
	c.canvas = canvas
	if !data.ConcurrentMutations.IsNull() && !data.ConcurrentMutations.IsUnknown() {
		c.concurrentMutations = 0
		if data.ConcurrentMutations.ValueBool() {
			c.concurrentMutations = 1
		}
	}
	if !data.Connections.IsNull() && !data.Connections.IsUnknown() {
		if data.Connections.ValueInt64() < 1 {
			resp.Diagnostics.AddError("Invalid provider configuration", "connections must be at least 1")
			return
		}
		c.connectionsCfg = int(data.Connections.ValueInt64())
	}
	if !data.ReadCacheMS.IsNull() && !data.ReadCacheMS.IsUnknown() {
		if data.ReadCacheMS.ValueInt64() < 0 {
			resp.Diagnostics.AddError("Invalid provider configuration", "read_cache_ms must be 0 or greater")
			return
		}
		c.readCacheMS = int(data.ReadCacheMS.ValueInt64())
	}
	if err := recordTrace(map[string]any{"event": "trace-ready", "provider_version": version}); err != nil {
		resp.Diagnostics.AddError("Mutation trace unavailable", err.Error())
		return
	}
	resp.DataSourceData = c
	resp.ResourceData = c
}

// maxCanvasSide is a sanity bound on a base canvas side, well above any real canvas.
const maxCanvasSide = 16384

// canvasFromConfig returns the base canvas named in the provider block, or the zero
// value (meaning the default) when neither side is set. An unknown value is left to
// the preflight default rather than guessed.
func canvasFromConfig(width, height types.Int64) ([2]int, error) {
	if width.IsUnknown() || height.IsUnknown() {
		return [2]int{}, nil
	}
	if width.IsNull() && height.IsNull() {
		return [2]int{}, nil
	}
	if width.IsNull() || height.IsNull() {
		return [2]int{}, fmt.Errorf("canvas_width and canvas_height must be set together")
	}
	w, h := width.ValueInt64(), height.ValueInt64()
	if w < 1 || h < 1 || w > maxCanvasSide || h > maxCanvasSide {
		return [2]int{}, fmt.Errorf("canvas_width and canvas_height must be between 1 and %d, got %dx%d", maxCanvasSide, w, h)
	}
	return [2]int{int(w), int(h)}, nil
}

func (p *Provider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewPluginResource, NewSceneResource, NewInputResource, NewMediaInputResource, NewSceneItemResource, NewSceneContentsResource, NewColorFilterResource, NewGenericFilterResource, NewGenericSourceResource, NewGroupResource, NewGroupSourceResource, NewCollectionSeedResource, NewGroupInputResource, NewGroupGraphResource, NewSourceFilterResource, NewValueFilterResource}
}

func (p *Provider) DataSources(_ context.Context) []func() datasource.DataSource { return nil }

func configureClient(req resource.ConfigureRequest, dst **client, diags *diag.Diagnostics) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	*dst = c
}
