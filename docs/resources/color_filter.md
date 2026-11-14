---
page_title: "obs_color_filter Resource - obs"
subcategory: "Filters"
description: |-
  Manages one Color Correction filter with typed attributes for hue and opacity. It is the typed form of `obs_filter` for the `color_filter_v2` kind and follows the same lifecycle.
---

# obs_color_filter (Resource)

Manages one Color Correction filter with typed attributes for hue and opacity. It is the typed form of [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) for the `color_filter_v2` kind and follows the same lifecycle.

## Example Usage

```terraform
resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_source" "camera" {
  collection        = "My Show"
  name              = "Camera"
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

resource "obs_color_filter" "tint" {
  collection  = "My Show"
  parent_name = obs_source.camera.name
  parent_uuid = obs_source.camera.input_uuid
  name        = "Tint"
  enabled     = true
  index       = 0
  hue_shift   = 180
  opacity     = 1
}
```

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `enabled` (Boolean) Whether the filter is active.
- `hue_shift` (Number) Hue rotation in degrees.
- `index` (Number) Zero-based position in the filter list of the parent. The provider refuses to displace a filter it does not manage.
- `name` (String) Name of the filter as shown in OBS. Changing it replaces the resource.
- `opacity` (Number) Opacity of the parent, from 0 (transparent) to 1 (opaque).
- `parent_name` (String) Name of the source, scene or group that carries the filter. It follows a rename of the parent without replacing the filter.
- `parent_uuid` (String) UUID of the source, scene or group that carries the filter. Changing it replaces the resource.

### Read-Only

- `id` (String) The ID of this resource.
- `kind` (String) Always `color_filter_v2`.

## Import

Import by collection name, parent UUID, parent name and filter name, separated by pipe characters:

```shell
terraform import obs_color_filter.tint 'My Show|SOURCE-UUID|Camera|Tint'
```
