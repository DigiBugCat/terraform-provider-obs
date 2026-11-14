---
page_title: "Filters and Plugin Sources"
subcategory: ""
description: |-
  Filters and plugin sources are addressed by kind strings and setting keys. These are the names OBS uses internally, not the labels shown in its interface.
---

# Filters and Plugin Sources

Filters and plugin sources are addressed by kind strings and setting keys. These are the names OBS uses internally, not the labels shown in its interface.

## Generic Resources

[`obs_source`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/source) and [`obs_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/filter) work with any kind the running OBS has registered, including kinds that a plugin adds. Both take a `kind` and a `settings_json` object built with `jsonencode`.

- The object owns exactly its top-level keys, and a write overlays those keys onto the settings in OBS.
- A key you leave out keeps its value in OBS.
- Removing a key stops managing it without resetting it.
- A nested object is owned as a whole.
- The order of keys never counts as a difference.

## Typed Resources

A typed resource replaces the JSON object with attributes that Terraform can check.

- [`obs_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/input) and [`obs_media_input`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/media_input) for image and media sources.
- [`obs_color_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/color_filter) for Color Correction.
- [`obs_move_source_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/move_source_filter) and [`obs_move_value_filter`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/move_value_filter) for the Move plugin.

Do not manage one filter or source with both a generic and a typed resource.

## Filter Order

A filter `index` is its position in the filter list of its parent, starting at 0. The provider never displaces a filter it does not manage, so declare positions that the managed filters can take.

## Finding Kinds and Setting Keys

Create the source or filter once by hand in OBS, save, and open the scene collection file that OBS writes. Every source and filter is listed there with its kind and its settings under the exact keys OBS uses.

## Plugins

A kind exists only while its plugin is loaded. Declare the plugin with [`obs_plugin`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/plugin), or install it by hand, and add `depends_on` to the resources that need it. A dependency orders the work in Terraform but does not make a running OBS load a new plugin. Install first, restart OBS, then apply the sources and filters.
