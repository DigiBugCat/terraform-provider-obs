resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

resource "obs_group" "lights" {
  collection = "My Show"
  scene_name = obs_scene.main.name
  scene_uuid = obs_scene.main.scene_uuid
  name       = "Party Lights"
  placement = {
    enabled = true
    locked  = false
    index   = 0
    transform = {
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
      bounds_width     = 0
      bounds_height    = 0
      crop_to_bounds   = false
    }
  }
}

resource "obs_group_input" "left_light" {
  collection    = "My Show"
  scene_name    = obs_scene.main.name
  scene_uuid    = obs_scene.main.scene_uuid
  group_name    = obs_group.lights.name
  group_uuid    = obs_group.lights.group_uuid
  group_item_id = obs_group.lights.item_id
  name          = "Party Light Left"
  kind          = "image_source"
  file          = "C:/Program Files/Git/Users/me/show/light-left.png"
  unload        = false
  placement = {
    enabled = true
    locked  = false
    index   = 0
    transform = {
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
      bounds_width     = 0
      bounds_height    = 0
      crop_to_bounds   = false
    }
  }
}
