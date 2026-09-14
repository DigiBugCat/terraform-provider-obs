#!/bin/sh
set -eu

if [ "${OBSTF_NATIVE_UNINSTALL:-}" != "1" ]; then
  echo "refusing uninstall: set OBSTF_NATIVE_UNINSTALL=1 after coordinating sole live-OBS ownership" >&2
  exit 1
fi
if pgrep -x OBS >/dev/null 2>&1; then
  echo "refusing uninstall while OBS is running" >&2
  exit 1
fi

target_bundle="/Users/andrew.sulistio/Library/Application Support/obs-studio/plugins/obstf-native-bridge.plugin"
if [ ! -e "$target_bundle" ]; then
  echo "not installed: $target_bundle"
  exit 0
fi

trash_root="/Users/andrew.sulistio/.Trash"
stamp=$(date '+%Y%m%d-%H%M%S')
trash_target="$trash_root/obstf-native-bridge.plugin.$stamp"
mv "$target_bundle" "$trash_target"
echo "moved plugin to recoverable Trash location: $trash_target"

