---
page_title: "Importing an Existing Collection"
subcategory: ""
description: |-
  Import binds an object that already exists in OBS to a resource in your configuration. It reads the object and does not write the configuration for you.
---

# Importing an Existing Collection

Import binds an object that already exists in OBS to a resource in your configuration. It reads the object and does not write the configuration for you.

## Procedure

1. Write the resource block with the values the object has now.
2. Look up the UUIDs and scene item IDs that the import ID needs.
3. Run `terraform import` with the whole ID in single quotes, because it contains pipe characters.
4. Run `terraform plan` and settle every difference deliberately before the first apply.

## Import IDs

- [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene): `collection|scene_uuid`.
- [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source), [`obs_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/input) and [`obs_media_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/media_input): `collection|input_uuid|anchor_scene_uuid|anchor_item_id`.
- [`obs_scene_item`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_item): `collection|scene_uuid|source_uuid|item_id`, with `|group` appended for a group.
- [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group): `collection|scene_uuid|group_uuid|item_id`.
- [`obs_group_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_source) and [`obs_group_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_input): `collection|scene_uuid|group_uuid|group_item_id|input_uuid|item_id`.
- [`obs_group_graph`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_graph): `collection|scene_uuid|group_uuid|parent_item_id|stable_key=source_uuid:item_id,...`.
- [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) and the Move filters: a JSON object, shown on each resource page.
- [`obs_color_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/color_filter): collection, parent UUID, parent name and filter name, separated by pipe characters.

## Finding UUIDs and Item IDs

obs-websocket reports all of them: `GetSceneList` returns scene UUIDs, `GetInputList` returns source UUIDs and `GetSceneItemList` returns scene item IDs. Any obs-websocket client can send those requests.

## What Import Does Not Do

- It never adopts an object by name: an object with the same name and another UUID is a different object.
- An imported [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source) or [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) starts with no managed settings, and the first apply takes ownership of the keys you list.
- [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents), [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin) and [`obs_collection_seed`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/collection_seed) cannot be imported; declare them and apply.

~> Adding [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) to an imported scene removes every placement that its list leaves out.
