terraform {
  required_providers {
    obs = { source = "digibugcat/obs" }
  }
}

# Connects to obs-websocket on the local machine.
# With no password set, the provider reads the credentials OBS itself stores.
provider "obs" {
  host = "127.0.0.1"
  port = 4455
}
