# Adding support for an OBS plugin

Plugin support lives in this provider, not in separate providers. Supporting a plugin
means two things, both already used by the Move plugin.

## 1. Typed resources for what the plugin adds

A plugin that adds filters gets one resource per filter kind, named
`obs_<plugin>_<filter>`, declared as a `FilterSpec` and registered in
`Provider.Resources` in `tfprovider/provider.go`. The spec lists the plugin's settings
keys with their types, optional encode and decode hooks for keys whose wire form differs
from the authored form, and an optional native validation hook that runs before the
first write. `tfprovider/move_filters.go` is the reference: `obs_move_source_filter`
and `obs_move_value_filter` are about 150 lines including the target validation.

The generic filter lifecycle does the rest: create at the end of the parent's filter
list and move to the declared index, verify enabled state, index and every declared
setting by reading OBS back, refuse to write while an output is active, and prune
nothing the resource does not own. A typed resource therefore inherits the same
guarantees as `obs_filter` and adds schema, defaults and validation for the plugin's
settings.

Plugins that add sources follow the same shape through `obs_source`'s `kind` and
`settings_json`; a typed source resource is worth adding when the settings need
validation that a JSON blob cannot express.

## 2. An installable manifest entry

`obs_plugin` installs a plugin from a manifest (`manifests/obs-plugins.<os>.json`)
whose entries pin every file by sha256 and mode. It backs up an existing install,
refuses to overwrite one unless `replace_existing` is set, and never removes files on
destroy. Declaring the plugin this way turns "the Move plugin is not installed on this
machine" into a plan error instead of a runtime failure, and lets a configuration state
the plugin version it was written against.

A plugin resource in a configuration looks like:

```hcl
resource "obs_plugin" "move" {
  manifest_path = "${path.module}/manifests/obs-plugins.macos.json"
  plugin_id     = "move-transition"
  version       = "3.2.1"
}
```

## Checklist

- `tfprovider/<plugin>_filters.go` with the `FilterSpec` declarations and tests for the
  wire mapping (`*_test.go` next to it).
- Resources registered in `tfprovider/provider.go`.
- A manifest entry for each supported OS, or a note in the resource description that
  the plugin must be installed by hand.
- `make docs` to regenerate `docs/resources/`.
