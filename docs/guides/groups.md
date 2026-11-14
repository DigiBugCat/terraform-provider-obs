---
page_title: "Groups and the Native Bridge"
subcategory: ""
description: |-
  obs-websocket cannot create a group or place a source directly inside one. The provider does both through the native bridge, a small OBS plugin that ships in the provider repository.
---

# Groups and the Native Bridge

obs-websocket cannot create a group or place a source directly inside one. The provider does both through the native bridge, a small OBS plugin that ships in the provider repository.

## What the Bridge Does

The bridge adds group requests to obs-websocket as vendor requests under the vendor name `obstf`. The provider checks the version and capabilities of the bridge before every group write. After each request the bridge verifies the result inside OBS, and the provider accepts only a verified outcome.

## Requirements

- OBS Studio 32.2.2 with obs-websocket 5.7.4, because the group resources refuse any other version.
- A base canvas of 1920x1080.
- A scene collection in absolute coordinate mode, which is what [`obs_collection_seed`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/collection_seed) creates.
- macOS on Apple silicon, the only platform the bridge is built for today.

~> Groups are not available on Windows or Linux yet; scenes, sources, filters and placements outside groups do not need the bridge.

## Installing the Bridge

Build the plugin from the provider repository:

```shell
git clone https://github.com/DigiBugCat/terraform-provider-obs
make -C terraform-provider-obs/native-bridge build
```

Install the built bundle while OBS is closed, either by copying it into the OBS plugin folder or with [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin) and a manifest that pins it. Start OBS afterwards, because a plugin is loaded only at startup.

## Choosing Group Resources

- [`obs_group`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group) with [`obs_group_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_source) or [`obs_group_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_input) gives every child its own resource and its own line in the plan.
- [`obs_group_graph`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/group_graph) manages a group and its image children as one unit and repairs the group as a whole.

Pick one model for each group and do not mix them.

## Existing Sources in a Group

[`obs_scene_item`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_item) with `parent_kind` set to `group` places a source that already exists into a group. [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) with the same setting declares the complete contents of a group.

## Coordinates

Positions of group children are measured from the top-left corner of the group, not of the canvas. The group reports its own size in `observed_group_width` and `observed_group_height`.
