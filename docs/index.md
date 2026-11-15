---
page_title: "obs Provider"
description: |-
  The obs provider manages an OBS Studio scene collection as Terraform resources: scenes, sources, groups, filters and the placements that arrange them. It talks to OBS over obs-websocket 5, and uses a small native OBS plugin for the group operations that obs-websocket does not expose. Every write is read back from OBS before a resource is reported as created or changed, and the provider refuses to write while OBS is streaming or recording. Use it to keep a stream layout in version control, review each change as a plan and rebuild a scene collection from text.
---

# obs Provider

The obs provider manages an OBS Studio scene collection as Terraform resources: scenes, sources, groups, filters and the placements that arrange them. It talks to OBS over obs-websocket 5, and uses a small native OBS plugin for the group operations that obs-websocket does not expose. Every write is read back from OBS before a resource is reported as created or changed, and the provider refuses to write while OBS is streaming or recording. Use it to keep a stream layout in version control, review each change as a plan and rebuild a scene collection from text.

## What You Can Manage

- Scenes and layout: [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene) creates a scene, [`obs_scene_item`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_item) adds a placement and [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) declares the layer order.
- Sources: [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source) creates a source of any installed kind, while [`obs_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/input) and [`obs_media_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/media_input) are typed for images and media files.
- Filters: [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) handles any installed filter kind, and [`obs_color_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/color_filter) is typed for Color Correction.
- Animation: [`obs_move_source_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/move_source_filter) and [`obs_move_value_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/move_value_filter) manage the filters of the Move plugin.
- Groups: [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group), [`obs_group_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_source), [`obs_group_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_input) and [`obs_group_graph`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_graph) manage groups and what they hold.
- Setup: [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin) installs an OBS plugin, and [`obs_collection_seed`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/collection_seed) creates an empty scene collection.

-> Group resources and the two setup resources run only on macOS; see [Groups and the Native Bridge](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/groups) and [Filters and Plugin Sources](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/filters-and-plugins).

## Getting Started

Four steps take an empty scene collection to a first scene with a coloured background and a filter.

1. Install OBS Studio and enable the WebSocket server under Tools, WebSocket Server Settings.
2. Create a scene collection made for Terraform, here called My Show, and leave it selected.
3. Save the configuration below as `main.tf` in an empty directory.
4. Run the three commands that follow it.

```terraform
terraform {
  required_providers {
    obs = { source = "digibugcat/obs" }
  }
}

provider "obs" {}

locals {
  collection = "My Show"
}

resource "obs_scene" "main" {
  collection = local.collection
  name       = "Main"
}

resource "obs_source" "background" {
  collection        = local.collection
  name              = "Background"
  kind              = "color_source_v3"
  settings_json     = jsonencode({ color = 4283782485, width = 1920, height = 1080 })
  anchor_scene_name = obs_scene.main.name
  anchor_scene_uuid = obs_scene.main.scene_uuid
  anchor_enabled    = true
  anchor_index      = 0
  anchor_transform = {
    x                = 0
    y                = 0
    scale_x          = 1
    scale_y          = 1
    rotation         = 0
    crop_left        = 0
    crop_top         = 0
    crop_right       = 0
    crop_bottom      = 0
    alignment        = 5
    bounds_type      = "OBS_BOUNDS_NONE"
    bounds_alignment = 0
  }
}

resource "obs_filter" "background_color" {
  collection    = local.collection
  parent_name   = obs_source.background.name
  parent_uuid   = obs_source.background.input_uuid
  name          = "Color Correction"
  kind          = "color_filter_v2"
  enabled       = true
  index         = 0
  settings_json = jsonencode({ saturation = 0.1 })
}

resource "obs_scene_contents" "main" {
  collection = local.collection
  scene_name = obs_scene.main.name
  scene_uuid = obs_scene.main.scene_uuid
  item_ids   = [obs_source.background.anchor_item_id]
}
```

```shell
terraform init
terraform plan -out=show.tfplan
terraform apply show.tfplan
```

OBS shows the new scene as soon as the apply finishes, and a second plan reports no changes. [Getting Started](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/getting-started) explains each block of this configuration and what to try next.

~> Use a scene collection made for Terraform: `obs_scene_contents` removes every placement it does not list.

## Example Usage

For OBS on the same machine the provider block needs no arguments.

```terraform
terraform {
  required_providers {
    obs = { source = "digibugcat/obs" }
  }
}

# Connects to obs-websocket on the local machine.
# With no password set, the provider reads the credentials OBS itself stores.
provider "obs" {
  host = "127.0.0.1"
  port = 4455
}
```

## Requirements

- OBS Studio with obs-websocket 5 enabled; the provider is verified against OBS 32.2.2 with obs-websocket 5.7.4.
- A base canvas of 1920x1080, or another size named with `canvas_width` and `canvas_height` in the provider block.
- Terraform 1.5 or later.
- The native bridge plugin, only when the configuration uses group resources; see [Groups and the Native Bridge](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/groups).

## Authentication

With `password` omitted and a local `host`, the provider reads the obs-websocket port and password from the configuration file that OBS itself writes. For OBS on another machine, set `password` from a sensitive variable and keep it out of version control.

Every resource names its `collection`, and the provider refuses to read or write while OBS has a different scene collection active.

## Resources

| Resource | Manages |
|---|---|
| [`obs_collection_seed`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/collection_seed) | Creates a new, empty scene collection file while OBS is closed, in the form the group resources require. |
| [`obs_color_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/color_filter) | Manages one Color Correction filter with typed attributes for hue and opacity. |
| [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) | Manages one filter of any installed kind on a source, scene or group, with the settings you choose to manage. |
| [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group) | Manages one group inside a scene: the group itself and its placement in that scene. |
| [`obs_group_graph`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_graph) | Manages a whole group as one resource: the group, its placement in the scene, and an ordered list of image children with their placements. |
| [`obs_group_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_input) | Manages one image source that is created directly inside a group, with its placement there. |
| [`obs_group_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_source) | Manages one input of any installed kind that is created directly inside a group, with its placement there. |
| [`obs_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/input) | Manages one image source, with typed attributes for its file, and its first placement in a scene. |
| [`obs_media_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/media_input) | Manages one media source that plays a local file, with typed playback settings, and its first placement in a scene. |
| [`obs_move_source_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/move_source_filter) | Manages one Move Source filter from the Move plugin, which animates a placement to a target position, scale and rotation. |
| [`obs_move_value_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/move_value_filter) | Manages one Move Value filter from the Move plugin, which animates one numeric setting to a target value. |
| [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin) | Installs one OBS plugin from a manifest that pins every file by SHA-256, and records the installation in state. |
| [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene) | Manages one scene in an OBS scene collection. |
| [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) | Declares the complete, ordered list of placements in one scene or group. |
| [`obs_scene_item`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_item) | Manages one additional placement of an existing source in a scene or group: its visibility, stacking position and transform. |
| [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source) | Manages one OBS input of any installed kind, the settings you choose to manage, and its first placement in a scene. |

## Guides

- [Getting Started](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/getting-started) builds a first scene with a source and a filter.
- [Ownership, Placements and Ordering](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/ownership-and-ordering) explains anchors, extra placements and how layer order is declared.
- [Groups and the Native Bridge](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/groups) covers the group resources and the plugin they depend on.
- [Filters and Plugin Sources](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/filters-and-plugins) covers generic and typed filters, plugin kinds and plugin installation.
- [Importing an Existing Collection](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/importing) brings scenes and sources that already exist under management.
- [How Writes Are Verified](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/verified-writes) describes the checks around every write and the settings that affect speed.
- [Troubleshooting](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/troubleshooting) lists the refusals you are most likely to meet and what each one means.

## Schema

### Optional

- `canvas_height` (Number) Height in pixels of the OBS base canvas this configuration was written for. Default 1080. Set together with canvas_width.
- `canvas_width` (Number) Width in pixels of the OBS base canvas this configuration was written for. Default 1920. Set together with canvas_height. The provider refuses to write when the live base canvas differs. Group resources work only on 1920x1080.
- `concurrent_mutations` (Boolean) EXPERIMENTAL: run mutations on different scenes, groups and sources concurrently. Default false. OBS 32.2.2 / obs-websocket 5.7.4 stopped answering under this on the reference Mac; enable only against an OBS build verified to survive multi-session writes (env OBS_CONCURRENT_MUTATIONS=1).
- `connections` (Number) Persistent obs-websocket connections for concurrent reads (default 10; env OBS_CONNECTIONS).
- `host` (String) obs-websocket host. Default 127.0.0.1.
- `password` (String, Sensitive) obs-websocket password. When omitted on a local host the provider reads OBS's own obs-websocket config file; remote hosts require it.
- `port` (Number) obs-websocket port. Default 4455.
- `read_cache_ms` (Number) EXPERIMENTAL: how long list responses are reused across reads, in milliseconds. Default 0 (off). Enabling it speeds refresh ~2x but an external OBS edit may go unseen for this long (env OBS_READ_CACHE_MS).
