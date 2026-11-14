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
