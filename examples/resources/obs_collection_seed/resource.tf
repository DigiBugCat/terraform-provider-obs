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
