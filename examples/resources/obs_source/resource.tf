resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

# Only the keys listed in settings_json are managed.
# Every other setting of the source keeps the value it has in OBS.
resource "obs_source" "overlay" {
  collection        = "My Show"
  name              = "Chat Overlay"
  kind              = "browser_source"
  settings_json     = jsonencode({ url = "https://example.com/overlay", width = 1920, height = 1080 })
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
