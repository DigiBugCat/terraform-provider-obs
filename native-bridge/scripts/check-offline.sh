#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
bundle="$bridge_root/build/0.1.4/obstf-native-bridge.plugin"
binary="$bundle/Contents/MacOS/obstf-native-bridge"
validation_test="$bridge_root/build/0.1.4/validation-test"
absolute_policy_test="$bridge_root/build/0.1.4/absolute-mode-policy-test"
obs_source="$bridge_root/build/deps/obs-studio-32.2.2"
frameworks="/Applications/OBS.app/Contents/Frameworks"

test -x "$binary"
plutil -lint "$bundle/Contents/Info.plist"
test "$(lipo -archs "$binary")" = "arm64"
codesign --verify --strict "$bundle"

for symbol in \
  _obs_module_load \
  _obs_module_post_load \
  _obs_module_unload \
  _obs_module_ver \
  _obs_module_set_pointer; do
  nm -gU "$binary" | awk '{print $3}' | grep -qx "$symbol"
done

for symbol in \
  _obs_get_main_canvas \
  _obs_canvas_get_video_info; do
  nm -u "$binary" | grep -qx "$symbol"
done

otool -L "$binary" | grep -q '@rpath/libobs.framework/Versions/A/libobs'
otool -L "$binary" | grep -q '@rpath/obs-frontend-api.dylib'

strings "$binary" | grep -qx 'obstf'
strings "$binary" | grep -qx '0.1.4'
strings "$binary" | grep -qx 'absolute_runtime_relative_mode_unsupported'
strings "$binary" | grep -qx 'absoluteGroupGraphMutations'
strings "$binary" | grep -qx 'CreateInputInGroup'
strings "$binary" | grep -qx 'MoveParentItemToGroup'
strings "$binary" | grep -qx 'SaveSceneCollectionCheckpoint'

python3 -m json.tool "$bridge_root/samples/color-filter-v2.source-derived.json" >/dev/null
if rg -n 'obs_source_media_|obs_source_filter_(add|remove)|obs_frontend_(streaming|recording|replay_buffer|virtualcam)_(start|stop)' \
  "$bridge_root/src"; then
  echo "bridge source crossed the filter/media/output-control boundary" >&2
  exit 1
fi
if rg -n 'obs_(set|apply)_private_data|relative_position_[xy]_roundtrip|project_supported_group_position|canonicalizationApplied|nativeExpected|projectedFields' \
  "$bridge_root/src"; then
  echo "bridge source retained a forbidden hot mode switch or relative projection path" >&2
  exit 1
fi

xcrun clang \
  -std=c11 -arch arm64 -mmacosx-version-min=13.0 \
  -Wall -Wextra -Werror -Wpedantic \
  -Wno-gnu-zero-variadic-macro-arguments -Wno-strict-prototypes \
  -I"$bridge_root/compat" \
  -I"$bridge_root/build/deps/simde-0.8.2" \
  -I"$obs_source/libobs" \
  -I"$obs_source/frontend/api" \
  -I"$bridge_root/build/deps" \
  "$bridge_root/tests/validation_test.c" \
  "$bridge_root/descriptor/descriptor.c" \
  "$bridge_root/descriptor/descriptor_model.c" \
  "$bridge_root/descriptor/descriptor_identity.c" \
  "$bridge_root/descriptor/descriptor_response.c" \
  -F"$frameworks" -framework libobs "$frameworks/libobs-frontend-api.1.dylib" \
  -Wl,-rpath,"$frameworks" \
  -o "$validation_test"
"$validation_test"

xcrun clang \
  -std=c11 -Wall -Wextra -Werror -Wpedantic \
  "$bridge_root/tests/absolute_mode_policy_test.c" \
  -o "$absolute_policy_test"
"$absolute_policy_test"

echo "offline bundle checks: PASS"
file "$binary"
otool -L "$binary"
shasum -a 256 "$binary" "$bundle/Contents/Info.plist" "$bridge_root/CONTRACT.md"
