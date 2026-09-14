#!/bin/sh
set -eu

if [ "${OBSTF_NATIVE_INSTALL:-}" != "1" ]; then
  echo "refusing install: set OBSTF_NATIVE_INSTALL=1 after coordinating sole live-OBS ownership" >&2
  exit 1
fi
if pgrep -x OBS >/dev/null 2>&1; then
  echo "refusing install while OBS is running" >&2
  exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
source_bundle="$bridge_root/build/0.1.4/obstf-native-bridge.plugin"
install_root="/Users/andrew.sulistio/Library/Application Support/obs-studio/plugins"
target_bundle="$install_root/obstf-native-bridge.plugin"

test -d "$source_bundle"
if [ -e "$target_bundle" ]; then
  echo "refusing to overwrite existing $target_bundle" >&2
  exit 1
fi

mkdir -p "$install_root"
cp -R "$source_bundle" "$target_bundle"
echo "installed $target_bundle; launch OBS only within the coordinated test window"

