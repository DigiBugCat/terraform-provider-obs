resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_media_input" "intro" {
  collection          = "My Show"
  name                = "Intro Video"
  local_file          = "C:/Program Files/Git/Users/me/show/intro.mp4"
  is_local_file       = true
  looping             = false
  restart_on_activate = true
  close_when_inactive = true
  anchor_scene_name   = obs_scene.main.name
  anchor_scene_uuid   = obs_scene.main.scene_uuid
  anchor_enabled      = true
  anchor_index        = 0
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
