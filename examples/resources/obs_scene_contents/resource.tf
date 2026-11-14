resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_source" "background" {
  collection        = "My Show"
  name              = "Background"
  kind              = "color_source_v3"
  settings_json     = jsonencode({ color = 4283782485, width = 1920, height = 1080 })
  anchor_scene_name = obs_scene.main.name
  anchor_scene_uuid = obs_scene.main.scene_uuid
  anchor_enabled    = true
  anchor_index      = 0
  anchor_transform = {
    x                = 0
    y                = 0
    scale_x          = 1
    scale_y          = 1
    rotation         = 0
    crop_left        = 0
    crop_top         = 0
    crop_right       = 0
    crop_bottom      = 0
    alignment        = 5
    bounds_type      = "OBS_BOUNDS_NONE"
    bounds_alignment = 0
  }
}

# The complete contents of the scene, bottom layer first.
# A placement added by hand in OBS is removed on the next apply.
resource "obs_scene_contents" "main" {
  collection = "My Show"
  scene_name = obs_scene.main.name
  scene_uuid = obs_scene.main.scene_uuid
  item_ids   = [obs_source.background.anchor_item_id]
}
