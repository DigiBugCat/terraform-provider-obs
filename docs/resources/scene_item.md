---
page_title: "obs_scene_item Resource - obs"
subcategory: "Scenes and placements"
description: |-
  Manages one additional placement of an existing source in a scene or group: its visibility, stacking position and transform. A source resource already owns its first placement, the anchor, so use this resource only for the second and later places the same source appears. Both placements show the same source, and a change to the source settings is seen in every scene that places it.
---

# obs_scene_item (Resource)

Manages one additional placement of an existing source in a scene or group: its visibility, stacking position and transform. A source resource already owns its first placement, the anchor, so use this resource only for the second and later places the same source appears. Both placements show the same source, and a change to the source settings is seen in every scene that places it.

## Example Usage

```terraform
resource "obs_scene" "office" {
  collection = "My Show"
  name       = "Office"
}

resource "obs_scene" "gameplay" {
  collection = "My Show"
  name       = "Gameplay"
}

# The camera is created once, with its first placement in the Office scene.
resource "obs_source" "camera" {
  collection        = "My Show"
  name              = "Camera"
  kind              = "color_source_v3"
  settings_json     = jsonencode({ color = 4283782485, width = 1920, height = 1080 })
  anchor_scene_name = obs_scene.office.name
  anchor_scene_uuid = obs_scene.office.scene_uuid
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

# A second placement of the same camera, smaller, in the Gameplay scene.
resource "obs_scene_item" "camera_in_gameplay" {
  collection  = "My Show"
  scene_name  = obs_scene.gameplay.name
  scene_uuid  = obs_scene.gameplay.scene_uuid
  source_name = obs_source.camera.name
  source_uuid = obs_source.camera.input_uuid
  enabled     = true
  index       = 0
  transform = {
    x                = 1440
    y                = 810
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
```

### Placing a Source in a Group

Set `parent_kind` to `group` and pass the name and UUID of the group in `scene_name` and `scene_uuid`. The group must already exist and must have exactly one placement in a scene.

-> Never declare an `obs_scene_item` for the anchor of a source, because two resources would then own one placement.

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `enabled` (Boolean) Whether the placement is visible.
- `index` (Number) Zero-based position in the scene or group, counted from the bottom layer up. A placement created before its lower siblings exist is placed last and moves once they do.
- `scene_name` (String) Name of the scene, or of the group when `parent_kind` is `group`, that receives the placement. It follows a rename.
- `scene_uuid` (String) UUID of the scene or group that receives the placement. Changing it replaces the resource.
- `source_name` (String) Name of the source to place. It follows a rename of the source.
- `source_uuid` (String) UUID of the source to place, for example `input_uuid` of [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source). Changing it replaces the resource.
- `transform` (Attributes) Position, scale, rotation, crop and bounding box of the placement. Every field is declared, so nothing about the layout is left to an OBS default. (see [below for nested schema](#nestedatt--transform))

### Optional

- `allow_duplicate` (Boolean) Allow another placement of the same source in this container. Each resource still owns its exact item ID; source data is shared.
- `parent_kind` (String) Container type. For group, scene_name and scene_uuid identify the existing native group; it must have one direct parent scene placement.

### Read-Only

- `id` (String) The ID of this resource.
- `item_id` (Number) Scene item ID that OBS assigned to this placement. Reference it from `item_ids` of [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents).

<a id="nestedatt--transform"></a>
### Nested Schema for `transform`

Required:

- `alignment` (Number) Which point of the source the position refers to, as OBS alignment flags added together: 1 left, 2 right, 4 top, 8 bottom, 0 center. `5` is the top-left corner.
- `bounds_alignment` (Number) Alignment of the source inside its bounding box, using the same flags as `alignment`.
- `bounds_type` (String) Bounding box mode: `OBS_BOUNDS_NONE`, `OBS_BOUNDS_STRETCH`, `OBS_BOUNDS_SCALE_INNER`, `OBS_BOUNDS_SCALE_OUTER`, `OBS_BOUNDS_SCALE_TO_WIDTH`, `OBS_BOUNDS_SCALE_TO_HEIGHT` or `OBS_BOUNDS_MAX_ONLY`.
- `crop_bottom` (Number) Pixels cropped from the bottom edge of the source.
- `crop_left` (Number) Pixels cropped from the left edge of the source.
- `crop_right` (Number) Pixels cropped from the right edge of the source.
- `crop_top` (Number) Pixels cropped from the top edge of the source.
- `rotation` (Number) Static orientation in degrees. Excess full turns are wrapped for OBS; equivalent native readback retains the authored value.
- `scale_x` (Number) Horizontal scale factor. `1` is the native width of the source.
- `scale_y` (Number) Vertical scale factor. `1` is the native height of the source.
- `x` (Number) Horizontal position in pixels of the alignment point, measured from the left edge of the containing scene or group.
- `y` (Number) Vertical position in pixels of the alignment point, measured from the top edge of the containing scene or group.

Optional:

- `bounds_height` (Number) Native bounding box height; required with an active bounds_type. Omit to leave disabled bounds metadata unmanaged.
- `bounds_width` (Number) Native bounding box width; required with an active bounds_type. Omit to leave disabled bounds metadata unmanaged.
- `crop_to_bounds` (Boolean) Crop to the bounding box. Omit to leave the native flag unmanaged.

## Import

Import a placement by collection name, container UUID, source UUID and scene item ID, separated by pipe characters:

```text
collection|scene_uuid|source_uuid|item_id
```

Append `|group` when the container is a group.

```shell
terraform import obs_scene_item.camera_in_gameplay 'My Show|SCENE-UUID|SOURCE-UUID|12'
```
