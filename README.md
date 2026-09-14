# Terraform provider for OBS Studio

`terraform-provider-obs` manages an OBS Studio scene collection as Terraform
resources: scenes, sources, groups, group children, filters, scene items and the
ordered contents of each container. It talks to OBS over obs-websocket 5 and, for the
group operations obs-websocket does not expose, through a small native plugin that
ships in this repository.

Every write is checked by reading OBS back. A resource is reported as created or
changed only after the provider has observed the exact result it asked for, and it
refuses to write at all while OBS is streaming or recording.

The provider is verified against a production collection of 1,166 resources
(23 scenes, 52 groups, 630 inputs, 181 filters) that is not part of this repository. On that collection a full cycle is
`destroy` in 35 seconds with nothing left behind, `apply` from empty in 30 seconds,
and `plan` in under 4 seconds reporting no changes. The numbers and how they were
measured are in [Performance](#performance).

## Requirements

- OBS Studio 32.2.2 with obs-websocket 5.7.4 enabled. Other versions are untested.
- Terraform 1.5 or later.
- The `obstf-native-bridge` OBS plugin from [`native-bridge/`](native-bridge/README.md)
  if the configuration uses groups. Scenes, sources, filters and scene items work
  without it.
- macOS or Windows host for OBS. The provider binary runs anywhere Go does.

## Install

The provider is not on the Terraform Registry yet. Build it and point Terraform at the
binary with a development override:

```sh
go build -o /usr/local/lib/terraform-provider-obs/terraform-provider-obs .
```

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "digibugcat/obs" = "/usr/local/lib/terraform-provider-obs"
  }
  direct {}
}
```

With the override in place, `terraform init` is not needed for this provider and
`terraform plan` prints a warning that development overrides are in effect.

## Use

```hcl
terraform {
  required_providers {
    obs = { source = "digibugcat/obs" }
  }
}

provider "obs" {
  host = "127.0.0.1"
  port = 4455
  # password is read from OBS's own obs-websocket config when omitted on localhost.
}

resource "obs_scene" "office" {
  collection = "My Show"
  name       = "Office"
}

resource "obs_source" "camera" {
  collection        = "My Show"
  name              = "Camera"
  kind              = "av_capture_input_v2"
  settings_json     = jsonencode({ device = "0x1234" })
  anchor_scene_name = obs_scene.office.name
  anchor_scene_uuid = obs_scene.office.scene_uuid
  anchor_enabled    = true
  anchor_index      = 0
  anchor_transform = {
    x = 0, y = 0, scale_x = 1, scale_y = 1, rotation = 0
    crop_left = 0, crop_top = 0, crop_right = 0, crop_bottom = 0
    alignment = 5, bounds_type = "OBS_BOUNDS_NONE", bounds_alignment = 0
    bounds_width = 0, bounds_height = 0, crop_to_bounds = false
  }
}

resource "obs_filter" "camera_color" {
  collection    = "My Show"
  parent_name   = obs_source.camera.name
  parent_uuid   = obs_source.camera.input_uuid
  name          = "Color Correction"
  kind          = "color_filter_v2"
  enabled       = true
  index         = 0
  settings_json = jsonencode({ saturation = 0.2 })
}

# Declares the complete ordered contents of the scene. Placements added by hand in OBS
# are removed on the next apply; placements removed by hand are recreated.
resource "obs_scene_contents" "office" {
  collection = "My Show"
  scene_name = obs_scene.office.name
  scene_uuid = obs_scene.office.scene_uuid
  item_ids   = [obs_source.camera.anchor_item_id]
}
```

The `collection` attribute names the OBS scene collection every resource belongs to.
The provider refuses to read or write when a different collection is active in OBS.

A complete small configuration is in [`examples/basic`](examples/basic/main.tf).
Generated reference documentation for every resource is in [`docs/`](docs/index.md).

## Resources

| Resource | Manages |
|---|---|
| `obs_scene` | A scene, by name and OBS UUID. |
| `obs_source` | An input of any kind with its settings and one anchor placement in a scene. |
| `obs_input` | An image input with a typed `file` attribute and an anchor placement. |
| `obs_media_input` | A media (ffmpeg) input with typed playback settings and an anchor placement. |
| `obs_scene_item` | An additional placement of an existing source in a scene or group: enabled, index, transform. |
| `obs_scene_contents` | The complete ordered membership of a scene or group. Prunes undeclared placements. |
| `obs_group` | A group inside a scene, created through the native bridge. |
| `obs_group_source` | An input created directly inside a group, with its placement. |
| `obs_group_input` | An image input created directly inside a group. |
| `obs_group_graph` | A whole group and its children as one resource, for group-level repair. |
| `obs_filter` | A filter of any kind on a source, with settings and index. |
| `obs_color_filter` | A color filter with typed attributes. |
| `obs_plugin` | Records that a plugin is installed. Never installs or removes files. |
| `obs_collection_seed` | Records the scene collection a configuration targets. Never deletes it. |
| `obs_move_source_filter` | A Move plugin "Move Source" filter: animates one placement's transform, visibility or order. |
| `obs_move_value_filter` | A Move plugin "Move Value" filter: animates a numeric setting of a source or filter. |

The two Move resources need the [Move](https://github.com/exeldro/obs-move-transition)
plugin installed in OBS; declare it with `obs_plugin` so a missing install is a plan
error rather than a runtime surprise. Support for further plugins is added to this
provider the same way; see [docs/adding-a-plugin.md](docs/adding-a-plugin.md).

## How writes work

Mutations run one at a time. Before the first write of each operation the provider
reads the OBS version, the active collection, the base canvas and the stream, record,
virtual camera and replay buffer status, and it refuses to continue if the collection
is wrong, the canvas is not 1920x1080, or any output is active. Reads run
concurrently over a pool of persistent connections.

After each write the provider reads the object back and compares every field it set.
If the readback does not match within four seconds the operation fails and Terraform
reports the resource as tainted, so the next plan shows the real state.

Siblings can be created in any order. A placement declared at index 5 that is created
while only two siblings exist is placed last and `obs_scene_contents` moves it to
index 5 once all siblings exist. A container without an `obs_scene_contents`
resource converges on the following plan.

Deleting Terraform's ownership of a plugin or a collection never removes the plugin
files or the collection.

## Performance

Measured on the reference collection (1,166 resources) against OBS 32.2.2 on an Apple
silicon Mac, Terraform 1.15.7, both on the same machine.

| Command | Time | Notes |
|---|---|---|
| `terraform plan`, converged | 9.7 s | 3.7 s with `read_cache_ms = 2000` |
| `terraform apply` from empty | 30 s | 1,166 created, zero errors |
| `terraform destroy -refresh=false` | 35 s | zero managed objects left in OBS |

Provider settings that affect speed:

- `connections` (default 10): persistent obs-websocket connections used for
  concurrent reads.
- `read_cache_ms` (default 0): reuse list responses across reads for this many
  milliseconds. Experimental. It speeds refresh by about 2.5x, and an edit made in OBS
  during that window is not observed until the next read.
- `concurrent_mutations` (default false): run writes to different containers
  concurrently. Experimental and off because OBS 32.2.2 stopped answering under it on
  the reference machine.

Destroy time is now dominated by Terraform's own per-resource work, not by OBS.

## Native bridge

obs-websocket cannot create a group or place a source directly inside one.
[`native-bridge/`](native-bridge/README.md) is an OBS plugin that adds those requests
as obs-websocket vendor requests under the vendor name `obstf`. The provider calls it
for `obs_group`, `obs_group_source`, `obs_group_input` and `obs_group_graph`, and
checks the bridge's version and capabilities before every group write. The request
contract, including every postcondition the bridge verifies, is in
[`native-bridge/CONTRACT.md`](native-bridge/CONTRACT.md).

Build it with `make -C native-bridge build` and copy
`native-bridge/build/0.1.4/obstf-native-bridge.plugin` into OBS's plugin directory,
then restart OBS.

## Lab tools

Small commands used to verify the provider against a running OBS. All of them read
OBS's connection settings from its own config file.

- `go run ./cmd/obs-inventory "[TF] "` counts scenes, groups, inputs and
  filters whose names carry a prefix. Exit code 2 when anything is found. Used to
  prove a destroy left nothing behind.
- `go run ./cmd/obs-add-item <scene> <source> <new-input>` adds placements to a
  scene the way a person would, to exercise pruning.
- `go run ./cmd/obs-purge-prefix <prefix>` removes every prefixed object. This is the
  recovery step after a crashed run leaves objects that state no longer tracks.
- `go run ./cmd/ws-handshake-bench` times the obs-websocket transport strategies.

## Repository layout

```
main.go             provider entry point
tfprovider/         the provider: resources, obs-websocket client, native bridge client
internal/           supporting packages (websocket protocol, collection model, checks)
native-bridge/      the OBS plugin for group operations, with its contract and tests
docs/               generated resource reference plus authoring guides
examples/           example configurations
cmd/                lab tools used to verify the provider against a running OBS
tools/              protocol vectors pinned against obs-websocket
```

## Development

```sh
make test     # go test ./tfprovider/ ./internal/...
make vet
make docs     # regenerate docs/ from the provider schema
```

Tests run offline against scripted obs-websocket fakes. `go test -short` skips the
`TestTerraformCLI*` tests, which run the pinned Terraform binary from
`.tools/terraform/1.15.7/` against a provider built from the working tree
(`tfprovider/cli_harness_test.go`). The lifecycle checks (destroy, inventory, apply,
plan) need a running OBS with the target collection selected.

Two environment variables exist for tests and diagnostics: `OBSTF_TF_TRACE_PATH`
writes mutation receipts as JSONL with settings redacted, and
`OBSTF_TF_INJECT_AFTER_NATIVE_TYPE=<request type>` makes the provider report an
authoritative interruption after that request completes, so tests can prove identity
survives a lost response.

Releases are cut by pushing a `v*` tag. The release workflow signs the checksums with
the `GPG_PRIVATE_KEY` repository secret, which is not configured yet, so no release
has been published.

## License

[Mozilla Public License 2.0](LICENSE).
