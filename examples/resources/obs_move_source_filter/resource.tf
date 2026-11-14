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

# The filter sits on the scene and moves the camera placement inside it.
resource "obs_move_source_filter" "slide_in" {
  collection         = "My Show"
  parent_name        = obs_scene.main.name
  parent_uuid        = obs_scene.main.scene_uuid
  name               = "Slide Camera In"
  enabled            = true
  index              = 0
  target_source_name = obs_source.camera.name
  position_x         = 480
  position_y         = 270
  scale_x            = 0.5
  scale_y            = 0.5
  duration_ms        = 500
}
