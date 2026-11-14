---
page_title: "obs_move_value_filter Resource - obs"
subcategory: "Filters"
description: |-
  Manages one Move Value filter from the Move plugin, which animates one numeric setting to a target value. The setting belongs either to the parent source itself or to another filter on the same parent.
---

# obs_move_value_filter (Resource)

Manages one Move Value filter from the Move plugin, which animates one numeric setting to a target value. The setting belongs either to the parent source itself or to another filter on the same parent.

-> The Move plugin must be installed in OBS; declare it with [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin) or install it by hand.

The provider configures the filter and never starts it: triggering the animation is left to a hotkey, a controller or the OBS interface. Only manual triggering is supported, so `start_trigger` and `stop_trigger` stay 0 and `enabled_match_moving` stays false.

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
  hue_shift   = 0
  opacity     = 1
}

# Animates the opacity setting of the Tint filter down to zero.
resource "obs_move_value_filter" "fade_out" {
  collection         = "My Show"
  parent_name        = obs_source.camera.name
  parent_uuid        = obs_source.camera.input_uuid
  name               = "Fade Out"
  enabled            = true
  index              = 1
  target_filter_name = obs_color_filter.tint.name
  setting_name       = "opacity"
  value              = 0
  duration_ms        = 800
}
```

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `enabled` (Boolean) Whether the filter is active.
- `index` (Number) Zero-based position in the filter list of the parent. The provider refuses to displace a filter it does not manage.
- `name` (String) Name of the filter as shown in OBS. Changing it replaces the resource.
- `parent_name` (String) Name of the source, scene or group that carries the filter. It follows a rename of the parent without replacing the filter.
- `parent_uuid` (String) UUID of the source, scene or group that carries the filter. Changing it replaces the resource.
- `setting_name` (String) Native floating-point property key, e.g. opacity on color_filter_v2.
- `value` (Number) Absolute floating-point target value. Integer/color/text settings are outside this slice.

### Optional

- `custom_duration` (Boolean) Use duration_ms instead of current OBS transition duration.
- `duration_ms` (Number) Custom animation duration in milliseconds.
- `easing` (Number) 0 none, 1 in, 2 out, 3 in-out.
- `easing_function` (Number) 1 quadratic, 2 cubic, 3 quartic, 4 quintic, 5 sine, 6 circular, 7 exponential, 8 elastic, 9 bounce, 10 back.
- `enabled_match_moving` (Boolean) Keep false to avoid automatically starting on enable. This slice rejects true.
- `start_trigger` (Number) Manual profile: must remain 0 (none).
- `stop_trigger` (Number) Manual profile: must remain 0 (none).
- `target_filter_name` (String) Filter on the parent to animate; empty animates a setting on the parent itself.

### Read-Only

- `id` (String) The ID of this resource.
- `kind` (String) Always `move_value_filter`.

## Import

Import with a JSON object that names the collection, parent UUID, parent name and filter name:

```shell
terraform import obs_move_value_filter.fade_out '{"collection":"My Show","parent_uuid":"SOURCE-UUID","parent_name":"Camera","name":"Fade Out"}'
```
