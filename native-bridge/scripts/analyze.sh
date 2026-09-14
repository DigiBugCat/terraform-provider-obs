#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

"$script_dir/fetch-sdk.sh" >/dev/null
mkdir -p "$bridge_root/build"
cd "$bridge_root/build"

xcrun clang --analyze \
  -std=c11 \
  -arch arm64 \
  -mmacosx-version-min=13.0 \
  -fvisibility=hidden \
  -fno-common \
  -Wall -Wextra -Werror -Wpedantic \
  -Wno-gnu-zero-variadic-macro-arguments \
  -Wno-strict-prototypes \
  -I"$bridge_root/compat" \
  -I"$bridge_root/build/deps/simde-0.8.2" \
  -I"$bridge_root/build/deps/obs-studio-32.2.2/libobs" \
  -I"$bridge_root/build/deps/obs-studio-32.2.2/frontend/api" \
  -I"$bridge_root/build/deps" \
  "$bridge_root/src/plugin.c"

echo "clang static analyzer: PASS"
