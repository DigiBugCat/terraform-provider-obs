---
page_title: "Getting Started"
subcategory: ""
description: |-
  This guide takes an empty scene collection to one scene with a coloured background and a filter. Along the way it shows the plan, apply and verify loop that the provider is built around.
---

# Getting Started

This guide takes an empty scene collection to one scene with a coloured background and a filter. Along the way it shows the plan, apply and verify loop that the provider is built around.

## Before You Start

1. Install OBS Studio and enable the WebSocket server under Tools, WebSocket Server Settings.
2. Set the base canvas to 1920x1080 under Settings, Video.
3. Create a scene collection for this guide, here called My Show, and leave it selected.
4. Stop streaming, recording, the virtual camera and the replay buffer, because the provider refuses to write while any output is active.

~> Use a scene collection made for Terraform: `obs_scene_contents` removes every placement it does not list.

## Configuration

Save this as `main.tf` in an empty directory:

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

- The `collection` local names the scene collection, and every resource repeats it.
- [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source) creates the source together with its first placement, the anchor, in the scene.
- [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) attaches to the source through `parent_uuid`, so Terraform creates the source first.
- [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) lists every placement the scene holds, bottom layer first.

## Apply

```shell
terraform init
terraform plan -out=show.tfplan
terraform apply show.tfplan
```

OBS shows the new scene as soon as the apply finishes. Run `terraform plan` again: it reports no changes, which confirms that what OBS holds is what the configuration declares.

## Change Something

Edit a field of `anchor_transform`, for example `x`, then plan and apply. The plan shows one placement changing in place, and the source keeps its UUID. Now drag the source somewhere else in OBS and plan again: the provider reports the drift, and the next apply puts the placement back.

## Next Steps

- [Ownership, Placements and Ordering](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/ownership-and-ordering) explains anchors, extra placements and layer order.
- [Importing an Existing Collection](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/importing) brings scenes and sources that already exist under management.
- [How Writes Are Verified](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/verified-writes) describes what the provider checks around every write.
