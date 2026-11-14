---
page_title: "obs_scene_contents Resource - obs"
subcategory: "Scenes and placements"
description: |-
  Declares the complete, ordered list of placements in one scene or group. On apply the provider removes every placement that is not in the list and puts the rest in the declared order. A source that is also placed in another scene keeps its other placements.
---

# obs_scene_contents (Resource)

Declares the complete, ordered list of placements in one scene or group. On apply the provider removes every placement that is not in the list and puts the rest in the declared order. A source that is also placed in another scene keeps its other placements.

Use exactly one of these per container, and none for a container whose contents people arrange by hand. Destroying the resource ends the contract and leaves every placement where it is.

## Example Usage

```terraform
resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_source" "background" {
  collection        = "My Show"
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

# The complete contents of the scene, bottom layer first.
# A placement added by hand in OBS is removed on the next apply.
resource "obs_scene_contents" "main" {
  collection = "My Show"
  scene_name = obs_scene.main.name
  scene_uuid = obs_scene.main.scene_uuid
  item_ids   = [obs_source.background.anchor_item_id]
}
```

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `item_ids` (List of Number) Complete desired list of native placement IDs, in ascending OBS sceneItemIndex order (bottom to top). Reference every owned placement, including group and nested-scene placements. An empty list removes all placements in this container.
- `scene_name` (String) Name of the scene, or of the group when `parent_kind` is `group`, whose contents are declared. Changing it replaces the resource.
- `scene_uuid` (String) UUID of the scene or group whose contents are declared. Changing it replaces the resource.

### Optional

- `parent_kind` (String) Container type, `scene` or `group`. Defaults to `scene`. Changing it replaces the resource.

### Read-Only

- `id` (String) The ID of this resource.
- `item_sources` (Map of String) Observed placement ID to source UUID identities, used to reject identity rebinding.
