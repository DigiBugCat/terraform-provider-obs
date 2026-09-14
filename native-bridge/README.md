# obstf native bridge

Small native OBS companion for group graph operations and exact-instance filter discovery absent from obs-websocket 5.x.
Read `CONTRACT.md` before integration.

Bridge 0.1.4 preserves the 0.1.3 group behavior and intentionally supports mutations only in OBS 32.2.2's known
absolute-coordinate mode on a live 1920x1080 main canvas. It refuses relative mode before
any structural write or save checkpoint; the 0.1.2 relative persistence failure is
preserved in the private development repository.
minimal version-1 cold shell and post-save file invariants.

## Supported build

The first artifact is pinned to the installed `/Applications/OBS.app`:

- OBS Studio 32.2.2
- macOS arm64
- obs-websocket vendor API header from 5.7.4
- Xcode command-line clang; no CMake or global SDK installation

Build and perform offline binary checks:

```sh
cd /Users/andrew.sulistio/aviary/products/obstf/native-bridge
make check
```

`scripts/fetch-sdk.sh` downloads only pinned public headers/source into ignored
`build/deps/`, verifies their recorded SHA-256 values, and replaces the compiler input
trees from a fresh verified extraction on every build. It never changes global state.

## Explicit local installation

Installation is an external setup step and is never run by Terraform/apply. The sole
live-OBS owner first verifies OBS is stopped and then runs:

```sh
OBSTF_NATIVE_INSTALL=1 ./scripts/install-macos.sh
```

The script refuses a running OBS process and refuses to overwrite an existing plugin.
It installs to:

`/Users/andrew.sulistio/Library/Application Support/obs-studio/plugins/obstf-native-bridge.plugin`

Launch/restart OBS only inside the coordinated isolated-lab window. Confirm the plugin
through `GetCapabilities`; file presence alone is not load evidence.

Uninstall while OBS is stopped:

```sh
OBSTF_NATIVE_UNINSTALL=1 ./scripts/uninstall-macos.sh
```

Uninstall moves the bundle to `~/.Trash` with a timestamp so it is recoverable.

## Qualification boundary

`make check` proves only that an arm64 Mach-O bundle was compiled and linked against the
installed OBS 32.2.2 frameworks with the required module exports. It does not prove OBS
loaded the plugin or that group operations persist. The acceptance sequence in
`CONTRACT.md` belongs to the existing sole live-OBS owner.

Windows source compatibility and packaging remain separate work. No Windows artifact or
runtime behavior is claimed by this Mac build.


## Filter discovery

Bridge 0.1.4 adds `DescribeFilter`; see the complete request and callback/redaction
boundary in `CONTRACT.md`. Metadata mode avoids native property/default builders.
Explicit inspection mode may execute plugin callbacks through libobs; it does
not claim third-party callback purity or a complete Terraform schema.

New bundles and validation binaries are emitted under `build/0.1.4/`; the
previous unversioned 0.1.3 bundle is preserved. `make check` also runs the
production descriptor assembler tests and ASan/UBSan checks. Successful offline
checks do not establish a live built-in/plugin response; that remains a separate
operator-owned qualification step.
