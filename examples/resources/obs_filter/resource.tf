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

resource "obs_filter" "sharpen" {
  collection    = "My Show"
  parent_name   = obs_source.camera.name
  parent_uuid   = obs_source.camera.input_uuid
  name          = "Sharpen"
  kind          = "sharpness_filter_v2"
  enabled       = true
  index         = 0
  settings_json = jsonencode({ sharpness = 0.2 })
}
