#ifndef OBSTF_ABSOLUTE_MODE_POLICY_H
#define OBSTF_ABSOLUTE_MODE_POLICY_H

#include <stdbool.h>
#include <stdint.h>
#include <string.h>

static inline float obstf_relative_x_from_absolute(float value, float width, float height)
{
	return (2.0f * value - width) / height;
}

static inline float obstf_absolute_x_from_relative(float value, float width, float height)
{
	return (value * height + width) / 2.0f;
}

static inline float obstf_relative_size_x_from_absolute(float value, float height)
{
	return 2.0f * value / height;
}

static inline float obstf_relative_group_resize_step(float stored_relative_x, float minimum_absolute_x,
						       float height)
{
	return stored_relative_x - obstf_relative_size_x_from_absolute(minimum_absolute_x, height);
}

enum obstf_absolute_seed_status {
	OBSTF_ABSOLUTE_SEED_SUPPORTED = 0,
	OBSTF_ABSOLUTE_SEED_WRONG_VERSION,
	OBSTF_ABSOLUTE_SEED_HAS_RESOLUTION,
	OBSTF_ABSOLUTE_SEED_HAS_MIGRATION_RESOLUTION,
	OBSTF_ABSOLUTE_SEED_HAS_PRIVATE_MODE_FIELD,
	OBSTF_ABSOLUTE_SEED_HAS_RELATIVE_TRANSFORM,
	OBSTF_ABSOLUTE_SEED_HAS_SCALE_REFERENCE,
	OBSTF_ABSOLUTE_SEED_HAS_MANAGED_GRAPH,
	OBSTF_ABSOLUTE_SEED_INVALID_BOOTSTRAP,
};

struct obstf_absolute_seed_facts {
	int64_t version;
	bool has_resolution;
	bool has_migration_resolution;
	bool has_private_mode_field;
	bool has_relative_transform;
	bool has_scale_reference;
	uint32_t managed_graph_object_count;
	uint32_t bootstrap_scene_count;
	bool bootstrap_identity_valid;
	bool bootstrap_scene_empty;
};

static inline enum obstf_absolute_seed_status
obstf_evaluate_absolute_seed(const struct obstf_absolute_seed_facts *facts)
{
	if (!facts || facts->version != 1)
		return OBSTF_ABSOLUTE_SEED_WRONG_VERSION;
	if (facts->has_resolution)
		return OBSTF_ABSOLUTE_SEED_HAS_RESOLUTION;
	if (facts->has_migration_resolution)
		return OBSTF_ABSOLUTE_SEED_HAS_MIGRATION_RESOLUTION;
	if (facts->has_private_mode_field)
		return OBSTF_ABSOLUTE_SEED_HAS_PRIVATE_MODE_FIELD;
	if (facts->has_relative_transform)
		return OBSTF_ABSOLUTE_SEED_HAS_RELATIVE_TRANSFORM;
	if (facts->has_scale_reference)
		return OBSTF_ABSOLUTE_SEED_HAS_SCALE_REFERENCE;
	if (facts->managed_graph_object_count != 0)
		return OBSTF_ABSOLUTE_SEED_HAS_MANAGED_GRAPH;
	if (facts->bootstrap_scene_count > 1 ||
	    (facts->bootstrap_scene_count == 1 &&
	     (!facts->bootstrap_identity_valid || !facts->bootstrap_scene_empty)))
		return OBSTF_ABSOLUTE_SEED_INVALID_BOOTSTRAP;
	return OBSTF_ABSOLUTE_SEED_SUPPORTED;
}

enum obstf_absolute_persisted_status {
	OBSTF_ABSOLUTE_PERSISTED_SUPPORTED = 0,
	OBSTF_ABSOLUTE_PERSISTED_WRONG_VERSION,
	OBSTF_ABSOLUTE_PERSISTED_HAS_RESOLUTION,
	OBSTF_ABSOLUTE_PERSISTED_HAS_MIGRATION_RESOLUTION,
	OBSTF_ABSOLUTE_PERSISTED_HAS_PRIVATE_MODE_FIELD,
	OBSTF_ABSOLUTE_PERSISTED_HAS_RELATIVE_TRANSFORM,
	OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_COUNT,
	OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_LOCATION,
	OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_VALUE,
};

struct obstf_absolute_persisted_facts {
	int64_t version;
	bool has_resolution;
	bool has_migration_resolution;
	bool has_private_mode_field;
	bool has_relative_transform;
	uint32_t ordinary_or_backup_scene_item_count;
	uint32_t true_group_child_count;
	uint32_t source_item_scale_reference_count;
	uint32_t group_child_scale_reference_count;
	uint32_t other_scale_reference_count;
	bool group_child_scale_references_match_fixed_canvas;
};

static inline enum obstf_absolute_persisted_status
obstf_evaluate_absolute_persisted(const struct obstf_absolute_persisted_facts *facts)
{
	if (!facts || facts->version != 1)
		return OBSTF_ABSOLUTE_PERSISTED_WRONG_VERSION;
	if (facts->has_resolution)
		return OBSTF_ABSOLUTE_PERSISTED_HAS_RESOLUTION;
	if (facts->has_migration_resolution)
		return OBSTF_ABSOLUTE_PERSISTED_HAS_MIGRATION_RESOLUTION;
	if (facts->has_private_mode_field)
		return OBSTF_ABSOLUTE_PERSISTED_HAS_PRIVATE_MODE_FIELD;
	if (facts->has_relative_transform)
		return OBSTF_ABSOLUTE_PERSISTED_HAS_RELATIVE_TRANSFORM;
	if (facts->source_item_scale_reference_count != 0 || facts->other_scale_reference_count != 0)
		return OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_LOCATION;
	if (facts->group_child_scale_reference_count != facts->true_group_child_count)
		return OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_COUNT;
	if (facts->group_child_scale_reference_count > 0 &&
	    !facts->group_child_scale_references_match_fixed_canvas)
		return OBSTF_ABSOLUTE_PERSISTED_SCALE_REFERENCE_VALUE;
	return OBSTF_ABSOLUTE_PERSISTED_SUPPORTED;
}

enum obstf_absolute_runtime_status {
	OBSTF_ABSOLUTE_RUNTIME_SUPPORTED = 0,
	OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION,
	OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN,
	OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE,
	OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN,
	OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS,
};

static inline enum obstf_absolute_runtime_status
obstf_evaluate_absolute_runtime(const char *obs_version, bool coordinate_mode_known, bool absolute_coordinates,
				 bool canvas_known, uint32_t base_width, uint32_t base_height)
{
	if (!obs_version || strcmp(obs_version, "32.2.2") != 0)
		return OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION;
	if (!coordinate_mode_known)
		return OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN;
	if (!absolute_coordinates)
		return OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE;
	if (!canvas_known)
		return OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN;
	if (base_width != 1920 || base_height != 1080)
		return OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS;
	return OBSTF_ABSOLUTE_RUNTIME_SUPPORTED;
}

static inline bool obstf_absolute_postcondition_verified(enum obstf_absolute_runtime_status runtime_status,
						   bool complete_placement_raw_exact)
{
	return runtime_status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED && complete_placement_raw_exact;
}

#endif
