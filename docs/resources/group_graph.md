---
page_title: "obs_group_graph Resource - obs"
subcategory: "Groups"
description: |-
  Manages a whole group as one resource: the group, its placement in the scene, and an ordered list of image children with their placements. Because the group is one resource, the provider repairs it as a unit: a child deleted in OBS is recreated in place without disturbing its siblings. Use it when a group must always be complete, and never manage the same group with `obs_group` as well.
---

# obs_group_graph (Resource)

Manages a whole group as one resource: the group, its placement in the scene, and an ordered list of image children with their placements. Because the group is one resource, the provider repairs it as a unit: a child deleted in OBS is recreated in place without disturbing its siblings. Use it when a group must always be complete, and never manage the same group with [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group) as well.

-> Group resources need the native bridge plugin loaded in OBS; see [Groups and the Native Bridge](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/groups).

## Example Usage

```terraform
resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

variable "asset_directory" {
  type        = string
  description = "Absolute directory that holds the light images on the machine running OBS."
}

# One placement shared by the group and its children.
locals {
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

resource "obs_group_graph" "lights" {
  collection       = "My Show"
  scene_name       = obs_scene.main.name
  scene_uuid       = obs_scene.main.scene_uuid
  name             = "Party Lights"
  parent_present   = true
  parent_placement = local.placement
  children = [
    {
      stable_key = "left"
      name       = "Party Light Left"
      kind       = "image_source"
      file       = "${var.asset_directory}/light-left.png"
      unload     = false
      present    = true
      placement  = local.placement
    },
    {
      stable_key = "right"
      name       = "Party Light Right"
      kind       = "image_source"
      file       = "${var.asset_directory}/light-right.png"
      unload     = false
      present    = true
      placement  = merge(local.placement, { index = 1, transform = merge(local.placement.transform, { x = 960 }) })
    }
  ]
}
```

### Rules for Children

- Every child is an `image_source` with an absolute `file`.
- Each child has a `stable_key`, the identity Terraform tracks it by, which must match `[a-z0-9_-]+` and be unique in the group.
- The `index` in each child placement must equal the position of the child in the `children` list.
- Renaming the group, changing the key, name or kind of an existing child, and removing a child are rejected at plan time.

## Schema

### Required

- `children` (Attributes List) Ordered list of the sources inside the group, bottom layer first. At least one child is required. (see [below for nested schema](#nestedatt--children))
- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `name` (String) Name of the group as shown in OBS. A rename is rejected at plan time.
- `parent_placement` (Attributes) Visibility, lock state, order and transform of the group in its scene. (see [below for nested schema](#nestedatt--parent_placement))
- `parent_present` (Boolean) Must be `true`: the group is always placed in its scene.
- `scene_name` (String) Name of the scene that contains the group. It follows a rename of the scene.
- `scene_uuid` (String) UUID of the scene that contains the group, normally `scene_uuid` of [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene). Changing it replaces the resource.

### Read-Only

- `group_uuid` (String) UUID that OBS assigned to the group.
- `id` (String) The ID of this resource.
- `observed_children` (Attributes List) The children as OBS reported them at the last read, including any that are missing. (see [below for nested schema](#nestedatt--observed_children))
- `observed_complete` (Boolean) Whether the last read found the group, its placement and every declared child.
- `observed_group_height` (Number) Height in pixels that OBS reports for the group.
- `observed_group_width` (Number) Width in pixels that OBS reports for the group.
- `observed_parent` (Attributes) The group placement as OBS reported it at the last read. (see [below for nested schema](#nestedatt--observed_parent))
- `parent_item_id` (Number) Scene item ID of the group in its scene. Reference it from `item_ids` of [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents).
- `repair_nonce` (String) Marker that becomes unknown in a plan when the group needs repair, so that the repair shows up as a change.

<a id="nestedatt--children"></a>
### Nested Schema for `children`

Required:

- `file` (String) Absolute path of the image file. The provider checks that it is a regular file it can read.
- `kind` (String) Must be `image_source`.
- `name` (String) Name of the child source as shown in OBS. It must be unique in the group and cannot change once the child exists.
- `placement` (Attributes) Visibility, lock state, order and transform of the child inside the group. Positions are measured from the top-left corner of the group. (see [below for nested schema](#nestedatt--children--placement))
- `present` (Boolean) Must be `true`: every declared child exists.
- `stable_key` (String) Identity of the child in Terraform, matching `[a-z0-9_-]+`. It cannot change once the child exists.
- `unload` (Boolean) Whether OBS unloads the image while it is not showing.

Read-Only:

- `item_id` (Number) Scene item ID that OBS assigned to the child inside the group.
- `source_uuid` (String) UUID that OBS assigned to the child source.

<a id="nestedatt--observed_children"></a>
### Nested Schema for `observed_children`

Read-Only:

- `height` (Number) Height in pixels that OBS reported for the child source.
- `item_id` (Number) Scene item ID of the child that OBS reported.
- `kind` (String) Kind of the child source that OBS reported.
- `name` (String) Name of the child source that OBS reported.
- `placement` (Attributes) Visibility, lock state, order and transform of the placement. (see [below for nested schema](#nestedatt--observed_children--placement))
- `present` (Boolean) Whether the child existed in the group at the last read.
- `source_uuid` (String) UUID of the child source that OBS reported.
- `stable_key` (String) Key of the declared child this observation belongs to.
- `width` (Number) Width in pixels that OBS reported for the child source.

<a id="nestedatt--observed_parent"></a>
### Nested Schema for `observed_parent`

Read-Only:

- `placement` (Attributes) Visibility, lock state, order and transform of the placement. (see [below for nested schema](#nestedatt--observed_parent--placement))
- `present` (Boolean) Whether the group was placed in its scene at the last read.

<a id="nestedatt--parent_placement"></a>
### Nested Schema for `parent_placement`

Required:

- `enabled` (Boolean) Whether the group is visible in its scene.
- `index` (Number) Zero-based position of the group in its scene, counted from the bottom layer up.
- `locked` (Boolean) Whether the group is locked against editing in the OBS interface.
- `transform` (Attributes) Position, scale, rotation, crop and bounding box of the placement. Every field is declared, so nothing about the layout is left to an OBS default. (see [below for nested schema](#nestedatt--parent_placement--transform))

<a id="nestedatt--children--placement"></a>
### Nested Schema for `children.placement`

Required:

- `enabled` (Boolean) Whether the placement is visible.
- `index` (Number) Zero-based position in the containing scene or group, counted from the bottom layer up.
- `locked` (Boolean) Whether the placement is locked against editing in the OBS interface.
- `transform` (Attributes) Position, scale, rotation, crop and bounding box of the placement. Every field is declared, so nothing about the layout is left to an OBS default. (see [below for nested schema](#nestedatt--children--placement--transform))

<a id="nestedatt--observed_children--placement"></a>
### Nested Schema for `observed_children.placement`

Read-Only:

- `enabled` (Boolean) Whether the placement is visible.
- `index` (Number) Zero-based position in the containing scene or group, counted from the bottom layer up.
- `locked` (Boolean) Whether the placement is locked against editing in the OBS interface.
- `transform` (Attributes) Position, scale, rotation, crop and bounding box of the placement. Every field is declared, so nothing about the layout is left to an OBS default. (see [below for nested schema](#nestedatt--observed_children--placement--transform))

<a id="nestedatt--observed_parent--placement"></a>
### Nested Schema for `observed_parent.placement`

Read-Only:

- `enabled` (Boolean) Whether the placement is visible.
- `index` (Number) Zero-based position in the containing scene or group, counted from the bottom layer up.
- `locked` (Boolean) Whether the placement is locked against editing in the OBS interface.
- `transform` (Attributes) Position, scale, rotation, crop and bounding box of the placement. Every field is declared, so nothing about the layout is left to an OBS default. (see [below for nested schema](#nestedatt--observed_parent--placement--transform))

<a id="nestedatt--parent_placement--transform"></a>
### Nested Schema for `parent_placement.transform`

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

<a id="nestedatt--children--placement--transform"></a>
### Nested Schema for `children.placement.transform`

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

<a id="nestedatt--observed_children--placement--transform"></a>
### Nested Schema for `observed_children.placement.transform`

Read-Only:

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
- `rotation` (Number) Rotation in degrees, clockwise.
- `scale_x` (Number) Horizontal scale factor. `1` is the native width of the source.
- `scale_y` (Number) Vertical scale factor. `1` is the native height of the source.
- `x` (Number) Horizontal position in pixels of the alignment point, measured from the left edge of the containing scene or group.
- `y` (Number) Vertical position in pixels of the alignment point, measured from the top edge of the containing scene or group.

<a id="nestedatt--observed_parent--placement--transform"></a>
### Nested Schema for `observed_parent.placement.transform`

Read-Only:

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
- `rotation` (Number) Rotation in degrees, clockwise.
- `scale_x` (Number) Horizontal scale factor. `1` is the native width of the source.
- `scale_y` (Number) Vertical scale factor. `1` is the native height of the source.
- `x` (Number) Horizontal position in pixels of the alignment point, measured from the left edge of the containing scene or group.
- `y` (Number) Vertical position in pixels of the alignment point, measured from the top edge of the containing scene or group.

## Import

Import by collection name, scene UUID, group UUID and group scene item ID, followed by one binding for each child:

```text
collection|scene_uuid|group_uuid|parent_item_id|stable_key=source_uuid:item_id,...
```

```shell
terraform import obs_group_graph.lights 'My Show|SCENE-UUID|GROUP-UUID|4|left=LEFT-UUID:9,right=RIGHT-UUID:10'
```
