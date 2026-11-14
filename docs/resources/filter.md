---
page_title: "obs_filter Resource - obs"
subcategory: "Filters"
description: |-
  Manages one filter of any installed kind on a source, scene or group, with the settings you choose to manage. OBS has no filter UUID, so a filter is identified by its parent UUID together with its name. Only one resource may own a given parent and filter name; do not declare this resource over a filter that a typed filter resource manages.
---

# obs_filter (Resource)

Manages one filter of any installed kind on a source, scene or group, with the settings you choose to manage. OBS has no filter UUID, so a filter is identified by its parent UUID together with its name. Only one resource may own a given parent and filter name; do not declare this resource over a filter that a typed filter resource manages.

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

resource "obs_filter" "sharpen" {
  collection    = "My Show"
  parent_name   = obs_source.camera.name
  parent_uuid   = obs_source.camera.input_uuid
  name          = "Sharpen"
  kind          = "sharpness_filter_v2"
  enabled       = true
  index         = 0
  settings_json = jsonencode({ sharpness = 0.2 })
}
```

### Managed Settings

The `settings_json` object owns exactly its top-level keys, with the same rules as [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source). OBS creates every filter enabled and the provider then sets the declared state, so a plugin may briefly see a filter enabled that is declared with `enabled` false.

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `enabled` (Boolean) Whether the filter is active.
- `index` (Number) Zero-based position in the filter list of the parent. The provider refuses to displace a filter it does not manage.
- `kind` (String) OBS filter kind, for example `color_filter_v2`. It must be a kind the running OBS has registered. Changing it replaces the resource.
- `name` (String) Name of the filter as shown in OBS. Changing it replaces the resource.
- `parent_name` (String) Name of the source, scene or group that carries the filter. It follows a rename of the parent without replacing the filter.
- `parent_uuid` (String) UUID of the source, scene or group that carries the filter. Changing it replaces the resource.
- `settings_json` (String, Sensitive) JSON object of managed top-level settings. Writes overlay these keys; omitted keys retain OBS values. Nested objects are managed as whole values.

### Read-Only

- `id` (String) The ID of this resource.

## Import

Import with a JSON object that names the collection, parent UUID, parent name, filter name, kind and the settings to manage:

```shell
terraform import obs_filter.sharpen '{"collection":"My Show","parent_uuid":"SOURCE-UUID","parent_name":"Camera","name":"Sharpen","kind":"sharpness_filter_v2","settings":{"sharpness":0.2}}'
```
