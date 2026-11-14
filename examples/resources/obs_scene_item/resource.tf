resource "obs_scene" "office" {
  collection = "My Show"
  name       = "Office"
}

resource "obs_scene" "gameplay" {
  collection = "My Show"
  name       = "Gameplay"
}

# The camera is created once, with its first placement in the Office scene.
resource "obs_source" "camera" {
  collection        = "My Show"
  name              = "Camera"
  kind              = "color_source_v3"
  settings_json     = jsonencode({ color = 4283782485, width = 1920, height = 1080 })
  anchor_scene_name = obs_scene.office.name
  anchor_scene_uuid = obs_scene.office.scene_uuid
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

# A second placement of the same camera, smaller, in the Gameplay scene.
resource "obs_scene_item" "camera_in_gameplay" {
  collection  = "My Show"
  scene_name  = obs_scene.gameplay.name
  scene_uuid  = obs_scene.gameplay.scene_uuid
  source_name = obs_source.camera.name
  source_uuid = obs_source.camera.input_uuid
  enabled     = true
  index       = 0
  transform = {
    x                = 1440
    y                = 810
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
