---
page_title: "obs_group_input Resource - obs"
subcategory: "Groups"
description: |-
  Manages one image source that is created directly inside a group, with its placement there. It is the group counterpart of `obs_input`.
---

# obs_group_input (Resource)

Manages one image source that is created directly inside a group, with its placement there. It is the group counterpart of [`obs_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/input).

-> Group resources need the native bridge plugin loaded in OBS; see [Groups and the Native Bridge](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/groups).

## Example Usage

```terraform
resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_group" "lights" {
  collection = "My Show"
  scene_name = obs_scene.main.name
  scene_uuid = obs_scene.main.scene_uuid
  name       = "Party Lights"
  placement = {
    enabled = true
    locked  = false
    index   = 0
    transform = {
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
      bounds_width     = 0
      bounds_height    = 0
      crop_to_bounds   = false
    }
  }
}

resource "obs_group_input" "left_light" {
  collection    = "My Show"
  scene_name    = obs_scene.main.name
  scene_uuid    = obs_scene.main.scene_uuid
  group_name    = obs_group.lights.name
  group_uuid    = obs_group.lights.group_uuid
  group_item_id = obs_group.lights.item_id
  name          = "Party Light Left"
  kind          = "image_source"
  file          = "C:/Program Files/Git/Users/me/show/light-left.png"
  unload        = false
  placement = {
    enabled = true
    locked  = false
    index   = 0
    transform = {
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
      bounds_width     = 0
      bounds_height    = 0
      crop_to_bounds   = false
    }
  }
}
```

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `file` (String) Absolute path of the image file. The provider checks that it is a regular file it can read.
- `group_item_id` (Number) Scene item ID of the group in its scene, normally `item_id` of [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group).
- `group_name` (String) Name of the group that receives the source.
- `group_uuid` (String) UUID of the group that receives the source, normally `group_uuid` of [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group). Changing it replaces the resource.
- `kind` (String) Must be `image_source`.
- `name` (String) Name of the source as shown in OBS. Changing it replaces the resource.
- `placement` (Attributes) Visibility, lock state, order and transform of the source inside the group. Positions are measured from the top-left corner of the group. (see [below for nested schema](#nestedatt--placement))
- `scene_name` (String) Name of the scene that contains the group. It follows a rename of the scene.
- `scene_uuid` (String) UUID of the scene that contains the group, normally `scene_uuid` of [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene). Changing it replaces the resource.
- `unload` (Boolean) Whether OBS unloads the image while it is not showing.

### Read-Only

- `id` (String) The ID of this resource.
- `input_uuid` (String) UUID that OBS assigned to the source. Use it wherever another resource asks for a source or parent UUID.
- `item_id` (Number) Scene item ID that OBS assigned to the placement inside the group.
- `observed_source_height` (Number) Height in pixels that OBS reports for the source.
- `observed_source_width` (Number) Width in pixels that OBS reports for the source.

<a id="nestedatt--placement"></a>
### Nested Schema for `placement`

Required:

- `enabled` (Boolean) Whether the placement is visible.
- `index` (Number) Zero-based position in the containing scene or group, counted from the bottom layer up.
- `locked` (Boolean) Whether the placement is locked against editing in the OBS interface.
- `transform` (Attributes) Position, scale, rotation, crop and bounding box of the placement. Every field is declared, so nothing about the layout is left to an OBS default. (see [below for nested schema](#nestedatt--placement--transform))

<a id="nestedatt--placement--transform"></a>
### Nested Schema for `placement.transform`

Required:

- `alignment` (Number) Which point of the source the position refers to, as OBS alignment flags added together: 1 left, 2 right, 4 top, 8 bottom, 0 center. `5` is the top-left corner.
- `bounds_alignment` (Number) Alignment of the source inside its bounding box, using the same flags as `alignment`.
- `bounds_height` (Number) Height of the bounding box in pixels. Use `0` with `OBS_BOUNDS_NONE`.
- `bounds_type` (String) Bounding box mode: `OBS_BOUNDS_NONE`, `OBS_BOUNDS_STRETCH`, `OBS_BOUNDS_SCALE_INNER`, `OBS_BOUNDS_SCALE_OUTER`, `OBS_BOUNDS_SCALE_TO_WIDTH`, `OBS_BOUNDS_SCALE_TO_HEIGHT` or `OBS_BOUNDS_MAX_ONLY`.
- `bounds_width` (Number) Width of the bounding box in pixels. Use `0` with `OBS_BOUNDS_NONE`.
- `crop_bottom` (Number) Pixels cropped from the bottom edge of the source.
- `crop_left` (Number) Pixels cropped from the left edge of the source.
- `crop_right` (Number) Pixels cropped from the right edge of the source.
- `crop_to_bounds` (Boolean) Whether the source is cropped to its bounding box.
- `crop_top` (Number) Pixels cropped from the top edge of the source.
- `rotation` (Number) Static orientation in degrees; WebSocket setters wrap excess full turns while state retains equivalent authored angles.
- `scale_x` (Number) Horizontal scale factor. `1` is the native width of the source.
- `scale_y` (Number) Vertical scale factor. `1` is the native height of the source.
- `x` (Number) Horizontal position in pixels of the alignment point, measured from the left edge of the containing scene or group.
- `y` (Number) Vertical position in pixels of the alignment point, measured from the top edge of the containing scene or group.

## Import

Import by collection name, scene UUID, group UUID, group scene item ID, source UUID and child scene item ID, separated by pipe characters:

```text
collection|scene_uuid|group_uuid|group_item_id|input_uuid|item_id
```

```shell
terraform import obs_group_input.left_light 'My Show|SCENE-UUID|GROUP-UUID|4|SOURCE-UUID|9'
```
