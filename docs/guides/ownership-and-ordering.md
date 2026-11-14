---
page_title: "Ownership, Placements and Ordering"
subcategory: ""
description: |-
  OBS separates a source from the places where it appears. This guide explains which resource owns which part, and how stacking order is declared.
---

# Ownership, Placements and Ordering

OBS separates a source from the places where it appears. This guide explains which resource owns which part, and how stacking order is declared.

## One Owner for Every Object

| Resource | Owns |
|---|---|
| [`obs_scene`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene) | One scene |
| [`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source), [`obs_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/input), [`obs_media_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/media_input) | One source, its settings and its first placement |
| [`obs_scene_item`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_item) | One further placement of a source that already exists |
| [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) | Which placements a scene or group holds, and their order |
| [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group) | One group and its placement in a scene |
| [`obs_group_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_source), [`obs_group_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_input) | One source created inside a group |
| [`obs_group_graph`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_graph) | A group together with all of its children |
| [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) and the typed filter resources | One filter on one parent |

Give each object in OBS exactly one owner.

## The Anchor

obs-websocket creates a source by placing it in a scene, so a source resource owns that first placement. That placement is the anchor, and its attributes carry the `anchor_` prefix. Add further placements of the same source with [`obs_scene_item`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_item).

-> Never declare an `obs_scene_item` for an anchor, because two resources would then own one placement.

## Names, UUIDs and Item IDs

Names are what people and controllers see, while UUIDs and scene item IDs are what OBS assigned. Resources refer to each other by UUID and item ID, which also tells Terraform the order in which to create them. A reference takes both the name and the UUID of its target, as the examples show.

## Layer Order

An `index` counts from the bottom layer: index 0 is drawn first and everything above it covers it. Terraform may create siblings in any order, so a placement whose index cannot be reached yet is placed last. [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) then moves every placement to its declared position, which is why a scene with several placements should have one. List the placements in `item_ids` in the same order as their `index` values, bottom layer first.

A scene without [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) settles on the plan after the one that created its placements.

## Exclusive Contents

[`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) is exclusive: a placement added by hand in OBS is removed on the next apply, and one removed by hand is created again. Leave it out for a scene that people arrange by hand, and manage only the individual placements you care about.

## Renaming and Replacing

Renaming a resource label in configuration is a `moved` block and changes nothing in OBS. Changing a scene `name` renames the scene in place. Changing the `name` of a source, group or filter replaces it, and the new object gets a new UUID. Check every hotkey, controller and script that referred to the old object after a replacement.
