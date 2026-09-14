#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
obs_app=${OBSTF_OBS_APP:-/Applications/OBS.app}
obs_version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$obs_app/Contents/Info.plist")
host_arch=$(uname -m)

if [ "$obs_version" != "32.2.2" ]; then
  echo "expected OBS 32.2.2 at $obs_app, found $obs_version" >&2
  exit 1
fi
if [ "$host_arch" != "arm64" ]; then
  echo "this first build is pinned to arm64, found $host_arch" >&2
  exit 1
fi

"$script_dir/fetch-sdk.sh"

obs_source="$bridge_root/build/deps/obs-studio-32.2.2"
bundle="$bridge_root/build/0.1.4/obstf-native-bridge.plugin"
binary="$bundle/Contents/MacOS/obstf-native-bridge"
frameworks="$obs_app/Contents/Frameworks"
obs_executable_hash=$(shasum -a 256 "$obs_app/Contents/MacOS/OBS" | awk '{print $1}')
libobs_hash=$(shasum -a 256 "$frameworks/libobs.framework/Versions/A/libobs" | awk '{print $1}')
frontend_api_hash=$(shasum -a 256 "$frameworks/libobs-frontend-api.1.dylib" | awk '{print $1}')

echo "linked OBS executable SHA-256: $obs_executable_hash"
echo "linked libobs SHA-256: $libobs_hash"
echo "linked frontend API SHA-256: $frontend_api_hash"

mkdir -p "$bundle/Contents/MacOS"
cp "$bridge_root/packaging/Info.plist" "$bundle/Contents/Info.plist"

xcrun clang \
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
  -I"$obs_source/libobs" \
  -I"$obs_source/frontend/api" \
  -I"$bridge_root/build/deps" \
  "$bridge_root/src/plugin.c" \
  "$bridge_root/descriptor/descriptor.c" \
  "$bridge_root/descriptor/descriptor_model.c" \
  "$bridge_root/descriptor/descriptor_identity.c" \
  "$bridge_root/descriptor/descriptor_response.c" \
  -bundle \
  -F"$frameworks" \
  -framework libobs \
  "$frameworks/libobs-frontend-api.1.dylib" \
  -Wl,-rpath,"$frameworks" \
  -o "$binary"

codesign --force --sign - "$bundle" >/dev/null
echo "$bundle"
