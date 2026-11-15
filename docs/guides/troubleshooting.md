---
page_title: "Troubleshooting"
subcategory: ""
description: |-
  Most failures are the provider declining to act. Each section quotes the message the provider prints and then names the cause.
---

# Troubleshooting

Most failures are the provider declining to act. Each section quotes the message the provider prints and then names the cause.

## Wrong Scene Collection

```text
refusing OBS write/read: current collection
```

OBS has a different scene collection selected than the `collection` of the resource. Select the right collection in OBS and plan again.

## Unexpected Canvas

```text
refusing mutation on unexpected live canvas
```

The live base canvas differs from the one the provider expects, which is 1920x1080 unless the provider block says otherwise. Set `canvas_width` and `canvas_height` to the size of the collection, or change the canvas under Settings, Video.

-> Group resources work only on a 1920x1080 canvas.

## An Output Is Active

```text
reports active output
```

Stop streaming, recording, the virtual camera and the replay buffer, then apply again.

## No Connection Settings

```text
no obs-websocket config found
```

The provider looked for the configuration file OBS writes and did not find one. Enable the WebSocket server in OBS, or set `host`, `port` and `password` in [the provider configuration](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs).

## Password Problems

```text
obs-websocket requires a password and none was provided
```

Set `password` to the value shown in the WebSocket Server Settings of OBS. A remote `host` always needs `password`, because the local configuration file describes only the local OBS.

## Bridge Version Mismatch

```text
ordinary obs-websocket version identity mismatch
```

Group resources run only against OBS 32.2.2 with obs-websocket 5.7.4; see [Groups and the Native Bridge](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/guides/groups).

## A Filter Would Be Displaced

```text
refusing to displace filter
```

The declared `index` is held by a filter the provider does not manage. Choose a free position, or bring the other filter under management first.

## A Plan That Never Comes Back Empty

Something else keeps changing a value that Terraform manages, such as a controller, a hotkey or an animation filter. Decide which of the two owns the value, and remove it from the other.

A scene without [`obs_scene_contents`](https://registry.terraform.io/providers/DigiBugCat/obs/latest/docs/resources/scene_contents) can also need a second plan before its layer order settles.

## After a Timeout

Run a fresh plan when a write times out or reports an uncertain result. The provider observes OBS again and plans from what it finds, and it does not replay the earlier write.
