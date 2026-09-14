terraform {
  required_providers {
    obs = { source = "digibugcat/obs" }
  }
}

# Connects to obs-websocket on the local OBS. With no password set, the provider reads
# the credentials from OBS's own obs-websocket config file.
provider "obs" {
  host = "127.0.0.1"
  port = 4455
}
