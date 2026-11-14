---
page_title: "obs_collection_seed Resource - obs"
subcategory: "Setup"
description: |-
  Creates a new, empty scene collection file while OBS is closed, in the form the group resources require. Apply it with OBS closed, then start OBS and select the collection before applying scenes and sources. It never converts or overwrites an existing collection, and it refuses a name that another collection file already uses. Destroying the resource forgets the binding and keeps the file and every scene in it.
---

# obs_collection_seed (Resource)

Creates a new, empty scene collection file while OBS is closed, in the form the group resources require. Apply it with OBS closed, then start OBS and select the collection before applying scenes and sources. It never converts or overwrites an existing collection, and it refuses a name that another collection file already uses. Destroying the resource forgets the binding and keeps the file and every scene in it.

~> The resource checks that OBS is not running by inspecting the process list, which is supported on macOS.

## Example Usage

```terraform
variable "scene_collection_directory" {
  type        = string
  description = "Absolute path of the OBS scenes directory, for example the basic/scenes folder of the OBS configuration."
}

# Apply this while OBS is closed, then start OBS and select the collection.
resource "obs_collection_seed" "show" {
  name                       = "My Show"
  scene_collection_directory = var.scene_collection_directory
  path                       = "${var.scene_collection_directory}/My_Show.json"
}
```

## Schema

### Required

- `name` (String) Name of the scene collection. Letters, digits and single spaces only. Changing it replaces the resource.
- `path` (String) Absolute path of the collection file: the directory, then the name with spaces replaced by underscores and a `.json` extension. Changing it replaces the resource.
- `scene_collection_directory` (String) Absolute path of the existing directory where OBS keeps scene collection files. Changing it replaces the resource.

### Read-Only

- `bootstrap_scene_uuid` (String) UUID of the single empty scene the new collection starts with.
- `id` (String) The ID of this resource.
- `seed_sha256` (String) SHA-256 of the collection file as it was written.
