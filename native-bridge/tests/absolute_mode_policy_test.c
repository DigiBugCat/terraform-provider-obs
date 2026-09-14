#include <stdio.h>

#include "../proposal/absolute_mode_policy.h"

static int failures;

static void expect(bool condition, const char *name)
{
	if (!condition) {
		fprintf(stderr, "FAIL: %s\n", name);
		failures++;
	}
}

int main(void)
{
	const float width = 1920.0f;
	const float height = 1080.0f;
	const float left_relative = obstf_relative_x_from_absolute(0.0f, width, height);
	const float left_absolute = obstf_absolute_x_from_relative(left_relative, width, height);
	const float resize_delta = obstf_relative_size_x_from_absolute(left_absolute, height);
	expect(left_relative == -1.7777777910232544f,
	       "relative storage for authored left zero matches the OBS float32 value");
	expect(left_absolute == -0.000007152557373046875f,
	       "relative readback for authored left zero matches the live residue");
	expect(resize_delta == -0.000000013245476715439963f,
	       "group resize converts the negative left residue to the exact relative decrement");

	float right_relative = 0.0f;
	for (int cycle = 0; cycle < 4; cycle++)
		right_relative = obstf_relative_group_resize_step(right_relative, left_absolute, height);
	expect(right_relative == 0.00000005298190686175985f,
	       "four resize recurrences reproduce the pre-failure stored right relative value");
	expect(obstf_absolute_x_from_relative(right_relative, width, height) == 960.0f,
	       "four recurrences still reconstruct authored right X");
	right_relative = obstf_relative_group_resize_step(right_relative, left_absolute, height);
	expect(right_relative == 0.00000006622738624173508f,
	       "fifth resize recurrence reproduces the persisted failing relative value");
	expect(obstf_absolute_x_from_relative(right_relative, width, height) == 960.0000610351562f,
	       "fifth recurrence reproduces the observed one-ULP sibling drift");

	const struct obstf_absolute_seed_facts empty_seed = {
		.version = 1,
		.bootstrap_scene_empty = true,
	};
	expect(obstf_evaluate_absolute_seed(&empty_seed) == OBSTF_ABSOLUTE_SEED_SUPPORTED,
	       "empty version-1 shell without migration or relative metadata is supported");
	struct obstf_absolute_seed_facts seed = empty_seed;
	seed.bootstrap_scene_count = 1;
	seed.bootstrap_identity_valid = true;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_SUPPORTED,
	       "one exact empty unmanaged bootstrap scene is supported");
	seed = empty_seed;
	seed.version = 2;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_WRONG_VERSION,
	       "version-2 seed is rejected");
	seed = empty_seed;
	seed.has_resolution = true;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_HAS_RESOLUTION,
	       "version-1 seed with resolution is rejected because OBS migrates it");
	seed = empty_seed;
	seed.has_migration_resolution = true;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_HAS_MIGRATION_RESOLUTION,
	       "migration-resolution metadata is rejected");
	seed = empty_seed;
	seed.has_private_mode_field = true;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_HAS_PRIVATE_MODE_FIELD,
	       "invented top-level AbsoluteCoordinates field is rejected");
	seed = empty_seed;
	seed.has_relative_transform = true;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_HAS_RELATIVE_TRANSFORM,
	       "relative transform metadata is rejected");
	seed = empty_seed;
	seed.has_scale_reference = true;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_HAS_SCALE_REFERENCE,
	       "scale_ref metadata is rejected");
	seed = empty_seed;
	seed.managed_graph_object_count = 1;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_HAS_MANAGED_GRAPH,
	       "precompiled managed group graph is rejected");
	seed = empty_seed;
	seed.bootstrap_scene_count = 1;
	seed.bootstrap_identity_valid = true;
	seed.bootstrap_scene_empty = false;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_INVALID_BOOTSTRAP,
	       "populated bootstrap scene is rejected");
	seed = empty_seed;
	seed.bootstrap_scene_count = 1;
	seed.bootstrap_identity_valid = false;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_INVALID_BOOTSTRAP,
	       "unrecognized bootstrap identity is rejected");
	seed = empty_seed;
	seed.bootstrap_scene_count = 2;
	expect(obstf_evaluate_absolute_seed(&seed) == OBSTF_ABSOLUTE_SEED_INVALID_BOOTSTRAP,
	       "multiple bootstrap scenes are rejected");

	const struct obstf_absolute_persisted_facts empty_persisted = {
		.version = 1,
		.group_child_scale_references_match_fixed_canvas = true,
	};
	expect(obstf_evaluate_absolute_persisted(&empty_persisted) == OBSTF_ABSOLUTE_PERSISTED_SUPPORTED,
	       "empty saved absolute shell is supported");
	struct obstf_absolute_persisted_facts persisted = empty_persisted;
	persisted.ordinary_or_backup_scene_item_count = 4;
	persisted.true_group_child_count = 2;
	persisted.group_child_scale_reference_count = 2;
	expect(obstf_evaluate_absolute_persisted(&persisted) == OBSTF_ABSOLUTE_PERSISTED_SUPPORTED,
	       "only true group children retain exact fixed-canvas scale_ref after save");
	persisted = empty_persisted;
	persisted.version = 2;
	expect(obstf_evaluate_absolute_persisted(&persisted) == OBSTF_ABSOLUTE_PERSISTED_WRONG_VERSION,
	       "saved version 2 is rejected");
	persisted = empty_persisted;
	persisted.has_resolution = true;
	expect(obstf_evaluate_absolute_persisted(&persisted) == OBSTF_ABSOLUTE_PERSISTED_HAS_RESOLUTION,
	       "saved resolution metadata is rejected");
	persisted = empty_persisted;
	persisted.has_migration_resolution = true;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_HAS_MIGRATION_RESOLUTION,
	       "saved migration-resolution metadata is rejected");
	persisted = empty_persisted;
	persisted.has_private_mode_field = true;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_HAS_PRIVATE_MODE_FIELD,
	       "saved invented private mode field is rejected");
	persisted = empty_persisted;
	persisted.has_relative_transform = true;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_HAS_RELATIVE_TRANSFORM,
	       "saved pos_rel, scale_rel or bounds_rel is rejected");
	persisted = empty_persisted;
	persisted.ordinary_or_backup_scene_item_count = 4;
	persisted.true_group_child_count = 2;
	persisted.group_child_scale_reference_count = 1;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_COUNT,
	       "missing true-group-child scale_ref is rejected");
	persisted.group_child_scale_reference_count = 3;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_COUNT,
	       "extra true-group-child scale_ref is rejected");
	persisted.group_child_scale_reference_count = 2;
	persisted.source_item_scale_reference_count = 4;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_LOCATION,
	       "all-items scale_ref including ordinary items and backups is rejected");
	persisted.source_item_scale_reference_count = 0;
	persisted.other_scale_reference_count = 1;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_LOCATION,
	       "top-level or otherwise unclassified scale_ref is rejected");
	persisted.other_scale_reference_count = 0;
	persisted.group_child_scale_references_match_fixed_canvas = false;
	expect(obstf_evaluate_absolute_persisted(&persisted) ==
		       OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_VALUE,
	       "group-child scale_ref other than exact 1920x1080 is rejected");

	const enum obstf_absolute_runtime_status supported =
		obstf_evaluate_absolute_runtime("32.2.2", true, true, true, 1920, 1080);
	expect(supported == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED,
	       "pinned known absolute 1920x1080 runtime is supported");
	expect(obstf_evaluate_absolute_runtime("32.2.3", true, true, true, 1920, 1080) ==
		       OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION,
	       "other OBS version is rejected");
	expect(obstf_evaluate_absolute_runtime("32.2.2", false, true, true, 1920, 1080) ==
		       OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN,
	       "unknown coordinate mode is rejected");
	expect(obstf_evaluate_absolute_runtime("32.2.2", true, false, true, 1920, 1080) ==
		       OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE,
	       "relative mode is rejected even at the pinned canvas");
	expect(obstf_evaluate_absolute_runtime("32.2.2", true, true, false, 1920, 1080) ==
		       OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN,
	       "unknown canvas is rejected");
	expect(obstf_evaluate_absolute_runtime("32.2.2", true, true, true, 1280, 720) ==
		       OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS,
	       "other canvas is rejected");

	expect(obstf_absolute_postcondition_verified(supported, true),
	       "supported runtime plus raw-exact complete placement verifies");
	expect(!obstf_absolute_postcondition_verified(supported, false),
	       "any placement mismatch fails without tolerance");
	expect(!obstf_absolute_postcondition_verified(OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE, true),
	       "raw-equal placement in relative mode cannot verify");

	if (failures)
		return 1;
	puts("absolute-mode policy proposal tests: PASS");
	return 0;
}
