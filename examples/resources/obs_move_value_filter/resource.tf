resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_source" "camera" {
  collection        = "My Show"
  name              = "Camera"
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

resource "obs_color_filter" "tint" {
  collection  = "My Show"
  parent_name = obs_source.camera.name
  parent_uuid = obs_source.camera.input_uuid
  name        = "Tint"
  enabled     = true
  index       = 0
  hue_shift   = 0
  opacity     = 1
}

# Animates the opacity setting of the Tint filter down to zero.
resource "obs_move_value_filter" "fade_out" {
  collection         = "My Show"
  parent_name        = obs_source.camera.name
  parent_uuid        = obs_source.camera.input_uuid
  name               = "Fade Out"
  enabled            = true
  index              = 1
  target_filter_name = obs_color_filter.tint.name
  setting_name       = "opacity"
  value              = 0
  duration_ms        = 800
}
