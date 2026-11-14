---
page_title: "obs_move_source_filter Resource - obs"
subcategory: "Filters"
description: |-
  Manages one Move Source filter from the Move plugin, which animates a placement to a target position, scale and rotation. The filter sits on a scene or group and names the placement to move by its source name.
---

# obs_move_source_filter (Resource)

Manages one Move Source filter from the Move plugin, which animates a placement to a target position, scale and rotation. The filter sits on a scene or group and names the placement to move by its source name.

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

# The filter sits on the scene and moves the camera placement inside it.
resource "obs_move_source_filter" "slide_in" {
  collection         = "My Show"
  parent_name        = obs_scene.main.name
  parent_uuid        = obs_scene.main.scene_uuid
  name               = "Slide Camera In"
  enabled            = true
  index              = 0
  target_source_name = obs_source.camera.name
  position_x         = 480
  position_y         = 270
  scale_x            = 0.5
  scale_y            = 0.5
  duration_ms        = 500
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
- `target_source_name` (String) Unique source name of the scene item in the parent scene/group.

### Optional

- `custom_duration` (Boolean) Use duration_ms instead of current OBS transition duration.
- `duration_ms` (Number) Custom animation duration in milliseconds.
- `easing` (Number) 0 none, 1 in, 2 out, 3 in-out.
- `easing_function` (Number) 1 quadratic, 2 cubic, 3 quartic, 4 quintic, 5 sine, 6 circular, 7 exponential, 8 elastic, 9 bounce, 10 back.
- `enabled_match_moving` (Boolean) Keep false to avoid automatically starting on enable. This slice rejects true.
- `position_x` (Number) Target horizontal position in pixels. Defaults to 0.
- `position_y` (Number) Target vertical position in pixels. Defaults to 0.
- `rotation` (Number) Target rotation in degrees. Defaults to 0.
- `scale_x` (Number) Target horizontal scale factor. Defaults to 1.
- `scale_y` (Number) Target vertical scale factor. Defaults to 1.
- `start_trigger` (Number) Manual profile: must remain 0 (none).
- `stop_trigger` (Number) Manual profile: must remain 0 (none).

### Read-Only

- `id` (String) The ID of this resource.
- `kind` (String) Always `move_source_filter`.

## Import

Import with a JSON object that names the collection, parent UUID, parent name and filter name:

```shell
terraform import obs_move_source_filter.slide_in '{"collection":"My Show","parent_uuid":"SCENE-UUID","parent_name":"Main","name":"Slide Camera In"}'
```
