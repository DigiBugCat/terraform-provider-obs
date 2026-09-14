#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
build_dir="$script_dir/build/0.1.4"
obs_source="$bridge_root/build/deps/obs-studio-32.2.2"

mkdir -p "$build_dir"
cd "$build_dir"

if [ ! -f "$obs_source/libobs/obs.h" ]; then
	echo "pinned OBS headers are missing: $obs_source" >&2
	exit 1
fi

if command -v xcrun >/dev/null 2>&1; then
	cc_bin="xcrun clang"
else
	cc_bin="clang"
fi

# Compile the native adapter only. Linking or loading OBS is intentionally out
# of scope for this lane; the receiving bridge task owns that integration.
$cc_bin \
	-std=c11 \
	-Wall -Wextra -Werror -Wpedantic \
	-Wno-gnu-zero-variadic-macro-arguments \
	-Wno-strict-prototypes \
	-I"$script_dir" \
	-I"$bridge_root/build/deps/simde-0.8.2" \
	-I"$bridge_root/compat" \
	-I"$obs_source/libobs" \
	-I"$obs_source/frontend/api" \
	-I"$bridge_root/build/deps" \
	-c "$script_dir/descriptor.c" \
	-o "$build_dir/descriptor.o"

$cc_bin --analyze \
	-std=c11 \
	-Wall -Wextra -Werror -Wpedantic \
	-Wno-gnu-zero-variadic-macro-arguments \
	-Wno-strict-prototypes \
	-I"$script_dir" \
	-I"$bridge_root/build/deps/simde-0.8.2" \
	-I"$bridge_root/compat" \
	-I"$obs_source/libobs" \
	-I"$obs_source/frontend/api" \
	-I"$bridge_root/build/deps" \
	"$script_dir/descriptor.c"

$cc_bin \
	-std=c11 \
	-Wall -Wextra -Werror -Wpedantic \
	-Wno-strict-prototypes \
	-I"$script_dir" \
	-I"$bridge_root/build/deps/simde-0.8.2" \
	-c "$script_dir/descriptor_model.c" \
	-o "$build_dir/descriptor_model.o"

$cc_bin \
	-std=c11 \
	-Wall -Wextra -Werror -Wpedantic \
	-Wno-strict-prototypes \
	-I"$script_dir" \
	-I"$bridge_root/build/deps/simde-0.8.2" \
	-I"$bridge_root/compat" \
	-I"$obs_source/libobs" \
	-I"$obs_source/frontend/api" \
	-I"$bridge_root/build/deps" \
	-c "$script_dir/descriptor_identity.c" \
	-o "$build_dir/descriptor_identity.o"

$cc_bin --analyze \
	-std=c11 \
	-Wall -Wextra -Werror -Wpedantic \
	-I"$script_dir" \
	"$script_dir/descriptor_model.c"

$cc_bin \
	-std=c11 \
	-Wall -Wextra -Werror -Wpedantic \
	-Wno-strict-prototypes \
	-I"$script_dir" \
	-I"$bridge_root/build/deps/simde-0.8.2" \
	-I"$bridge_root/compat" \
	-I"$obs_source/libobs" \
	-I"$obs_source/frontend/api" \
	-I"$bridge_root/build/deps" \
	"$script_dir/descriptor_model.c" \
	"$script_dir/descriptor_identity.c" \
	"$script_dir/descriptor_response.c" \
	"$script_dir/descriptor_test.c" \
	-o "$build_dir/descriptor-test"

"$build_dir/descriptor-test"

$cc_bin --analyze -std=c11 -Wall -Wextra -Werror -Wpedantic \
	"$script_dir/descriptor_response.c"
$cc_bin -std=c11 -Wall -Wextra -Werror -Wpedantic \
	-fsanitize=address,undefined -fno-omit-frame-pointer \
	-Wno-strict-prototypes -I"$bridge_root/compat" -I"$obs_source/libobs" \
	-I"$bridge_root/build/deps/simde-0.8.2" \
	"$script_dir/descriptor_model.c" "$script_dir/descriptor_identity.c" \
	"$script_dir/descriptor_response.c" "$script_dir/descriptor_test.c" \
	-o "$build_dir/descriptor-test-sanitize"
ASAN_OPTIONS=halt_on_error=1 "$build_dir/descriptor-test-sanitize"

if rg -n 'obs_property_modified|obs_properties_apply_settings|obs_property_button_clicked|obs_source_filter_(add|remove)|obs_source_update' \
	"$script_dir/descriptor.c"; then
	echo "descriptor crossed the read-only/callback boundary" >&2
	exit 1
fi

if rg -n 'terraform|schema generation|complete settings schema' "$script_dir/descriptor.c" "$script_dir/descriptor.h"; then
	echo "descriptor implementation contains a forbidden provider-schema claim" >&2
	exit 1
fi

if rg -n '"(serializedLength|sha256|valueType|present)"' "$script_dir/descriptor.c" "$script_dir/descriptor_model.c"; then
	echo "descriptor redaction response contains a value-derived secret marker" >&2
	exit 1
fi

shasum -a 256 \
	"$script_dir/descriptor.h" \
	"$script_dir/descriptor.c" \
	"$script_dir/descriptor_model.h" \
	"$script_dir/descriptor_model.c" \
	"$script_dir/descriptor_identity.h" \
	"$script_dir/descriptor_identity.c" \
	"$script_dir/descriptor_response.c" \
	"$script_dir/descriptor_test.c" \
	"$build_dir/descriptor.o" \
	"$build_dir/descriptor_model.o" \
	"$build_dir/descriptor_identity.o" \
	"$build_dir/descriptor-test"
