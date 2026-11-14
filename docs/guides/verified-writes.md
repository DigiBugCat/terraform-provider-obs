---
page_title: "How Writes Are Verified"
subcategory: ""
description: |-
  The provider treats OBS as a live system that people and other tools also change. Every write is guarded before it starts and checked after it finishes.
---

# How Writes Are Verified

The provider treats OBS as a live system that people and other tools also change. Every write is guarded before it starts and checked after it finishes.

## Before a Write

Before the first write of each operation the provider reads the OBS version, the active scene collection, the base canvas and the state of every output. It refuses to continue when any of these is true:

- OBS has a scene collection active other than the `collection` of the resource.
- The base canvas is not 1920x1080.
- The stream, a recording, the virtual camera or the replay buffer is active.
- The running OBS does not offer the request that the write needs.

## After a Write

After each write the provider reads the object back and compares every field it set. An acknowledgement from OBS is not treated as success until the readback agrees. When the readback does not match, the operation fails and Terraform marks the resource as tainted, so the next plan starts from what OBS really holds.

## One Writer at a Time

Writes run one at a time, and a read never overlaps a write from the same provider. Reads run concurrently over a pool of persistent connections.

## Settings That Affect Speed

- `connections` sets the size of the read pool and defaults to 10; the `OBS_CONNECTIONS` environment variable sets it too.
- `read_cache_ms` reuses list responses across reads; it is experimental, defaults to 0, and hides an edit made in OBS for that long.
- `concurrent_mutations` runs writes to different scenes, groups and sources at once; it is experimental and off by default.

## What Destroy Leaves Behind

- Destroying [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin) leaves the plugin files installed.
- Destroying [`obs_collection_seed`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/collection_seed) leaves the collection file and its scenes.
- Destroying [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) leaves every placement where it is.
