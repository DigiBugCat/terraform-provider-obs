resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_input" "logo" {
  collection        = "My Show"
  name              = "Logo"
  kind              = "image_source"
  file              = "C:/Program Files/Git/Users/me/show/logo.png"
  unload            = false
  anchor_scene_name = obs_scene.main.name
  anchor_scene_uuid = obs_scene.main.scene_uuid
  anchor_enabled    = true
  anchor_index      = 0
  anchor_transform = {
    x                = 1600
    y                = 40
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
