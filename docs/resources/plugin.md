---
page_title: "obs_plugin Resource - obs"
subcategory: "Setup"
description: |-
  Installs one OBS plugin from a manifest that pins every file by SHA-256, and records the installation in state. Planning and refreshing only inspect the installed files; the install itself happens on apply and requires OBS to be closed. Destroying the resource forgets the installation and leaves the plugin files in place.
---

# obs_plugin (Resource)

Installs one OBS plugin from a manifest that pins every file by SHA-256, and records the installation in state. Planning and refreshing only inspect the installed files; the install itself happens on apply and requires OBS to be closed. Destroying the resource forgets the installation and leaves the plugin files in place.

Declaring a plugin turns a missing or altered installation into a visible plan change instead of a failure when a source or filter first needs it.

~> The installer currently supports macOS only, and `install_dir` defaults to the per-user OBS plugin folder on macOS.

## Example Usage

```terraform
variable "manifest_path" {
  type        = string
  description = "Absolute path of the reviewed plugin manifest."
}

variable "manifest_sha256" {
  type        = string
  description = "SHA-256 of the manifest, written out by hand so that a changed manifest is a deliberate change."
}

# Apply this while OBS is closed, then start OBS before applying sources and filters.
resource "obs_plugin" "move" {
  manifest_path   = var.manifest_path
  manifest_sha256 = var.manifest_sha256
  plugin_id       = "move"
  version         = "3.2.1"
}
```

### Two Apply Stages

OBS loads plugins only at startup, so a new plugin is not usable in the apply that installs it.

1. With OBS closed, apply a configuration that contains only `obs_plugin` resources.
2. Start OBS, then apply the scenes, sources and filters that use the plugin.

Keep the plugin resources in their own root module, because scene resources cannot be refreshed while OBS is closed.

## Schema

### Required

- `manifest_path` (String) Local manifest file; read and hash-verified before install.
- `manifest_sha256` (String) SHA-256 of the manifest file, as 64 hexadecimal characters. The install is refused when the file does not match.
- `plugin_id` (String) Identifier of the plugin entry in the manifest. Changing it replaces the resource.
- `version` (String) Plugin version the configuration was written against. It must equal the version in the manifest entry.

### Optional

- `install_dir` (String) Directory that receives the plugin. Defaults to the per-user OBS plugin folder on macOS. Changing it replaces the resource.
- `installed` (Boolean) Desired installed state, always true. Refresh sets false for missing or mismatched payload, producing a visible repair plan.
- `replace_existing` (Boolean) Allow initial takeover of a differing existing bundle, preserving a backup. Updates to the managed installation already preserve and replace drift.
- `retain_on_destroy` (Boolean) Must be true. Terraform forgets the binding on destroy; plugin removal is an explicit separate operation.

### Read-Only

- `backup_path` (String) Backup retained by the most recent replacing install, if any.
- `content_sha256` (String) Verified installed content digest; empty when contents are missing or differ.
- `id` (String) The ID of this resource.
- `pinned_spec_json` (String, Sensitive) Pinned specification retained for read-only refresh even if the manifest file changes.
