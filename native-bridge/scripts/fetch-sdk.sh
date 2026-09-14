#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
deps_dir="$bridge_root/build/deps"
obs_archive="$deps_dir/obs-studio-32.2.2.tar.gz"
obs_source="$deps_dir/obs-studio-32.2.2"
websocket_header="$deps_dir/obs-websocket-api-5.7.4.h"
simde_archive="$deps_dir/simde-0.8.2.tar.gz"
simde_source="$deps_dir/simde-0.8.2"

mkdir -p "$deps_dir"

if [ ! -f "$obs_archive" ]; then
  curl -fL --retry 3 -o "$obs_archive" \
    https://codeload.github.com/obsproject/obs-studio/tar.gz/refs/tags/32.2.2
fi

archive_hash=$(shasum -a 256 "$obs_archive" | awk '{print $1}')
if [ "$archive_hash" != "35d3cd0979d65664fada7119fdb612eca7c34b61a1623a330caec74bf72626c4" ]; then
  echo "OBS source archive hash mismatch: $archive_hash" >&2
  exit 1
fi

if [ ! -f "$websocket_header" ]; then
  curl -fL --retry 3 -o "$websocket_header" \
    https://raw.githubusercontent.com/obsproject/obs-websocket/5.7.4/lib/obs-websocket-api.h
fi

header_hash=$(shasum -a 256 "$websocket_header" | awk '{print $1}')
if [ "$header_hash" != "c29e8e38ee66c36db79cae86217ab0f270aad39f97e82090809d087c281b7948" ]; then
  echo "obs-websocket API header hash mismatch: $header_hash" >&2
  exit 1
fi

if [ ! -f "$simde_archive" ]; then
  curl -fL --retry 3 -o "$simde_archive" \
    https://codeload.github.com/simd-everywhere/simde/tar.gz/refs/tags/v0.8.2
fi

simde_hash=$(shasum -a 256 "$simde_archive" | awk '{print $1}')
if [ "$simde_hash" != "ed2a3268658f2f2a9b5367628a85ccd4cf9516460ed8604eed369653d49b25fb" ]; then
  echo "SIMDe source archive hash mismatch: $simde_hash" >&2
  exit 1
fi

extract_root=$(mktemp -d "$deps_dir/.verified-extract.XXXXXX")
trap 'rm -rf "$extract_root"' EXIT HUP INT TERM
tar -xzf "$obs_archive" -C "$extract_root"
tar -xzf "$simde_archive" -C "$extract_root"
test -f "$extract_root/obs-studio-32.2.2/libobs/obs.h"
test -f "$extract_root/obs-studio-32.2.2/frontend/api/obs-frontend-api.h"
test -f "$extract_root/simde-0.8.2/simde/x86/sse2.h"

# The compiler must never reuse a locally modified ignored extraction. Replace only
# these two pinned dependency trees after both verified archives extracted cleanly.
rm -rf "$obs_source" "$simde_source"
mv "$extract_root/obs-studio-32.2.2" "$obs_source"
mv "$extract_root/simde-0.8.2" "$simde_source"
rmdir "$extract_root"
trap - EXIT HUP INT TERM

echo "OBS SDK source: 32.2.2 ($archive_hash)"
echo "obs-websocket API header: 5.7.4 ($header_hash)"
echo "SIMDe headers: 0.8.2 ($simde_hash)"
