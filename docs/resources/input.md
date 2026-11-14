---
page_title: "obs_input Resource - obs"
subcategory: "Sources"
description: |-
  Manages one image source, with typed attributes for its file, and its first placement in a scene. It is the typed form of `obs_source` for the `image_source` kind: the provider checks the file before it writes.
---

# obs_input (Resource)

Manages one image source, with typed attributes for its file, and its first placement in a scene. It is the typed form of [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source) for the `image_source` kind: the provider checks the file before it writes.

## Example Usage

```terraform
resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_input" "logo" {
  collection        = "My Show"
  name              = "Logo"
  kind              = "image_source"
  file              = "C:/Program Files/Git/Users/me/show/logo.png"
  unload            = false
  anchor_scene_name = obs_scene.main.name
  anchor_scene_uuid = obs_scene.main.scene_uuid
  anchor_enabled    = true
  anchor_index      = 0
  anchor_transform = {
    x                = 1600
    y                = 40
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

## Schema

### Required

- `anchor_enabled` (Boolean) Whether the anchor placement is visible.
- `anchor_index` (Number) Zero-based position of the anchor placement in its scene, counted from the bottom layer up.
- `anchor_scene_name` (String) Name of the scene that holds the anchor placement. It follows a rename of the scene without replacing the source.
- `anchor_scene_uuid` (String) UUID of the scene that holds the anchor placement, normally `scene_uuid` of [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene). Changing it replaces the resource.
- `anchor_transform` (Attributes) Position, scale, rotation, crop and bounding box of the anchor placement. (see [below for nested schema](#nestedatt--anchor_transform))
- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `file` (String) Absolute path of the image file. The provider checks that it is a regular file it can read.
- `kind` (String) Must be `image_source`.
- `name` (String) Name of the source as shown in OBS. Changing it replaces the resource.
- `unload` (Boolean) Whether OBS unloads the image while it is not showing.

### Read-Only

- `anchor_item_id` (Number) Scene item ID that OBS assigned to the anchor placement. Reference it from `item_ids` of [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents).
- `id` (String) The ID of this resource.
- `input_uuid` (String) UUID that OBS assigned to the source. Use it wherever another resource asks for a source or parent UUID.

<a id="nestedatt--anchor_transform"></a>
### Nested Schema for `anchor_transform`

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

Import by collection name, source UUID, anchor scene UUID and anchor scene item ID, separated by pipe characters:

```text
collection|input_uuid|anchor_scene_uuid|anchor_item_id
```

```shell
terraform import obs_input.logo 'My Show|SOURCE-UUID|SCENE-UUID|7'
```
