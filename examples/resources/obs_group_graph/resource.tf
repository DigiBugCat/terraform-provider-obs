resource "obs_scene" "main" {
  collection = "My Show"
  name       = "Main"
}

variable "asset_directory" {
  type        = string
  description = "Absolute directory that holds the light images on the machine running OBS."
}

# One placement shared by the group and its children.
locals {
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

resource "obs_group_graph" "lights" {
  collection       = "My Show"
  scene_name       = obs_scene.main.name
  scene_uuid       = obs_scene.main.scene_uuid
  name             = "Party Lights"
  parent_present   = true
  parent_placement = local.placement
  children = [
    {
      stable_key = "left"
      name       = "Party Light Left"
      kind       = "image_source"
      file       = "${var.asset_directory}/light-left.png"
      unload     = false
      present    = true
      placement  = local.placement
    },
    {
      stable_key = "right"
      name       = "Party Light Right"
      kind       = "image_source"
      file       = "${var.asset_directory}/light-right.png"
      unload     = false
      present    = true
      placement  = merge(local.placement, { index = 1, transform = merge(local.placement.transform, { x = 960 }) })
    }
  ]
}
