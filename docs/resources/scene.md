---
page_title: "obs_scene Resource - obs"
subcategory: "Scenes"
description: |-
  Manages one scene in an OBS scene collection. The scene is tracked by the UUID that OBS assigns, so changing `name` renames the same scene in place and every placement inside it survives. Destroying the resource removes the scene and the placements it contains.
---

# obs_scene (Resource)

Manages one scene in an OBS scene collection. The scene is tracked by the UUID that OBS assigns, so changing `name` renames the same scene in place and every placement inside it survives. Destroying the resource removes the scene and the placements it contains.

## Example Usage

```terraform
resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}
```

## Schema

### Required

- `collection` (String) Name of the OBS scene collection this resource belongs to. The provider refuses to read or write while OBS has a different collection active. Changing it replaces the resource.
- `name` (String) Name of the scene as shown in OBS. Changing it renames the scene in place.

### Read-Only

- `id` (String) The ID of this resource.
- `scene_uuid` (String) UUID that OBS assigned to the scene. Other resources use it to say which scene they belong to.

## Import

Import a scene by its collection name and scene UUID, separated by a pipe character:

```text
collection|scene_uuid
```

```shell
terraform import obs_scene.main 'My Show|5f0c2f1e-8d3b-4a57-9c41-2b7e6a1d9f03'
```

Write the matching configuration first: import reads the existing scene, it does not generate HCL.
