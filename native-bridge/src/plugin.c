#include <float.h>
#include <limits.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <obs-module.h>
#include <obs-frontend-api.h>
#include <obs-websocket-api-5.7.4.h>

#include "../proposal/absolute_mode_policy.h"
#include "../descriptor/descriptor.h"

#define BRIDGE_VERSION "0.1.4"
#define CONTRACT_VERSION 1
#define VENDOR_NAME "obstf"

OBS_DECLARE_MODULE()
OBS_MODULE_AUTHOR("Aviary")

MODULE_EXPORT const char *obs_module_name(void)
{
	return "obstf Native Bridge";
}

MODULE_EXPORT const char *obs_module_description(void)
{
	return "Narrow versioned group-graph vendor requests for obstf";
}

struct placement {
	bool enabled;
	bool locked;
	int index;
	struct obs_transform_info transform;
	struct obs_sceneitem_crop crop;
};

struct resolved_scene {
	obs_source_t *source;
	obs_scene_t *scene;
};

struct resolved_group {
	struct resolved_scene parent;
	obs_sceneitem_t *item;
	obs_source_t *source;
	obs_scene_t *scene;
};

struct child_identity {
	char *source_uuid;
	char *source_name;
	int64_t scene_item_id;
};

typedef void (*request_handler_t)(obs_data_t *, obs_data_t *);

struct request_registration {
	const char *name;
	request_handler_t handler;
	bool mutation;
	bool requires_absolute_runtime;
};

struct request_task {
	const struct request_registration *registration;
	obs_data_t *request;
	obs_data_t *response;
};

static obs_websocket_vendor bridge_vendor;

static const char *safe_string(const char *value)
{
	return value ? value : "";
}

static void response_base(obs_data_t *response, const char *request_name, obs_data_t *request)
{
	const char *operation_id = obs_data_get_string(request, "clientOperationId");

	obs_data_set_int(response, "contractVersion", CONTRACT_VERSION);
	obs_data_set_string(response, "bridgeVersion", BRIDGE_VERSION);
	obs_data_set_string(response, "requestType", request_name);
	obs_data_set_string(response, "clientOperationId", safe_string(operation_id));
	obs_data_set_string(response, "outcome", "uncertain");
	obs_data_set_string(response, "code", "handler_not_completed");
	obs_data_set_string(response, "message", "handler did not establish a result");
	obs_data_set_bool(response, "mutationAttempted", false);
	obs_data_set_bool(response, "postconditionVerified", false);
}

static void response_status(obs_data_t *response, const char *outcome, const char *code, const char *message,
			    bool mutation_attempted, bool postcondition_verified)
{
	obs_data_set_string(response, "outcome", outcome);
	obs_data_set_string(response, "code", code);
	obs_data_set_string(response, "message", message);
	obs_data_set_bool(response, "mutationAttempted", mutation_attempted);
	obs_data_set_bool(response, "postconditionVerified", postcondition_verified);
}

static void response_failed(obs_data_t *response, const char *code, const char *message)
{
	response_status(response, "failed", code, message, false, true);
}

static void response_result(obs_data_t *response, obs_data_t *result)
{
	obs_data_set_obj(response, "result", result);
}

static bool user_value_has_type(obs_data_t *data, const char *key, enum obs_data_type type)
{
	if (!data || !obs_data_has_user_value(data, key))
		return false;
	obs_data_item_t *item = obs_data_item_byname(data, key);
	if (!item)
		return false;
	const bool matches = obs_data_item_gettype(item) == type;
	obs_data_item_release(&item);
	return matches;
}

static bool require_integer(obs_data_t *data, const char *key, int64_t minimum, int64_t maximum, int64_t *value,
			    obs_data_t *response)
{
	if (!user_value_has_type(data, key, OBS_DATA_NUMBER)) {
		response_failed(response, "invalid_integer_field", key);
		return false;
	}
	obs_data_item_t *item = obs_data_item_byname(data, key);
	if (!item || obs_data_item_numtype(item) != OBS_DATA_NUM_INT) {
		if (item)
			obs_data_item_release(&item);
		response_failed(response, "invalid_integer_field", key);
		return false;
	}
	const int64_t observed = obs_data_item_get_int(item);
	obs_data_item_release(&item);
	if (observed < minimum || observed > maximum) {
		response_failed(response, "integer_field_out_of_range", key);
		return false;
	}
	*value = observed;
	return true;
}

static bool require_boolean(obs_data_t *data, const char *key, bool *value, obs_data_t *response)
{
	if (!user_value_has_type(data, key, OBS_DATA_BOOLEAN)) {
		response_failed(response, "invalid_boolean_field", key);
		return false;
	}
	*value = obs_data_get_bool(data, key);
	return true;
}

static bool require_contract(obs_data_t *request, obs_data_t *response, bool mutation)
{
	int64_t version;
	if (!require_integer(request, "contractVersion", 1, INT64_MAX, &version, response))
		return false;
	if (version != CONTRACT_VERSION) {
		response_failed(response, "unsupported_contract_version", "only contractVersion 1 is supported");
		return false;
	}

	if (mutation) {
		if (!user_value_has_type(request, "clientOperationId", OBS_DATA_STRING)) {
			response_failed(response, "invalid_client_operation_id",
					"mutation requests require a string clientOperationId");
			return false;
		}
		const char *operation_id = obs_data_get_string(request, "clientOperationId");
		if (!operation_id || !*operation_id) {
			response_failed(response, "missing_client_operation_id",
					"mutation requests require a non-empty clientOperationId");
			return false;
		}
	}

	return true;
}

static bool required_string(obs_data_t *data, const char *key, const char **value, obs_data_t *response)
{
	if (!user_value_has_type(data, key, OBS_DATA_STRING)) {
		response_failed(response, "invalid_string_field", key);
		return false;
	}

	*value = obs_data_get_string(data, key);
	if (!*value || !**value) {
		response_failed(response, "invalid_field", key);
		return false;
	}
	return true;
}

static bool optional_name_matches(obs_data_t *identity, obs_source_t *source, obs_data_t *response)
{
	if (!obs_data_has_user_value(identity, "sourceName"))
		return true;
	if (!user_value_has_type(identity, "sourceName", OBS_DATA_STRING)) {
		response_failed(response, "invalid_string_field", "sourceName");
		return false;
	}

	const char *expected = obs_data_get_string(identity, "sourceName");
	const char *observed = obs_source_get_name(source);
	if (!expected || !*expected || strcmp(expected, safe_string(observed)) != 0) {
		response_failed(response, "identity_name_mismatch", "sourceName does not match the resolved UUID");
		return false;
	}
	return true;
}

static const char *bounds_type_name(enum obs_bounds_type type)
{
	switch (type) {
	case OBS_BOUNDS_NONE:
		return "OBS_BOUNDS_NONE";
	case OBS_BOUNDS_STRETCH:
		return "OBS_BOUNDS_STRETCH";
	case OBS_BOUNDS_SCALE_INNER:
		return "OBS_BOUNDS_SCALE_INNER";
	case OBS_BOUNDS_SCALE_OUTER:
		return "OBS_BOUNDS_SCALE_OUTER";
	case OBS_BOUNDS_SCALE_TO_WIDTH:
		return "OBS_BOUNDS_SCALE_TO_WIDTH";
	case OBS_BOUNDS_SCALE_TO_HEIGHT:
		return "OBS_BOUNDS_SCALE_TO_HEIGHT";
	case OBS_BOUNDS_MAX_ONLY:
		return "OBS_BOUNDS_MAX_ONLY";
	default:
		return "OBS_BOUNDS_UNKNOWN";
	}
}

static bool parse_bounds_type(const char *name, enum obs_bounds_type *type)
{
	if (strcmp(name, "OBS_BOUNDS_NONE") == 0)
		*type = OBS_BOUNDS_NONE;
	else if (strcmp(name, "OBS_BOUNDS_STRETCH") == 0)
		*type = OBS_BOUNDS_STRETCH;
	else if (strcmp(name, "OBS_BOUNDS_SCALE_INNER") == 0)
		*type = OBS_BOUNDS_SCALE_INNER;
	else if (strcmp(name, "OBS_BOUNDS_SCALE_OUTER") == 0)
		*type = OBS_BOUNDS_SCALE_OUTER;
	else if (strcmp(name, "OBS_BOUNDS_SCALE_TO_WIDTH") == 0)
		*type = OBS_BOUNDS_SCALE_TO_WIDTH;
	else if (strcmp(name, "OBS_BOUNDS_SCALE_TO_HEIGHT") == 0)
		*type = OBS_BOUNDS_SCALE_TO_HEIGHT;
	else if (strcmp(name, "OBS_BOUNDS_MAX_ONLY") == 0)
		*type = OBS_BOUNDS_MAX_ONLY;
	else
		return false;
	return true;
}

static bool require_numeric(obs_data_t *data, const char *key, double *value, obs_data_t *response)
{
	if (!user_value_has_type(data, key, OBS_DATA_NUMBER)) {
		response_failed(response, "invalid_numeric_field", key);
		return false;
	}
	*value = obs_data_get_double(data, key);
	if (!isfinite(*value) || *value > FLT_MAX || *value < -FLT_MAX || !isfinite((float)*value)) {
		response_failed(response, "non_finite_placement", key);
		return false;
	}
	return true;
}

static bool parse_placement(obs_data_t *data, struct placement *placement, obs_data_t *response)
{
	memset(placement, 0, sizeof(*placement));
	if (!data) {
		response_failed(response, "missing_placement", "placement object is required");
		return false;
	}

	int64_t scene_item_index;
	if (!require_boolean(data, "sceneItemEnabled", &placement->enabled, response) ||
	    !require_boolean(data, "sceneItemLocked", &placement->locked, response) ||
	    !require_integer(data, "sceneItemIndex", 0, INT_MAX, &scene_item_index, response))
		return false;
	placement->index = (int)scene_item_index;

	obs_data_t *transform = obs_data_get_obj(data, "sceneItemTransform");
	if (!transform) {
		response_failed(response, "missing_scene_item_transform", "sceneItemTransform object is required");
		return false;
	}

	double position_x, position_y, rotation, scale_x, scale_y, bounds_width, bounds_height;
	bool valid = require_numeric(transform, "positionX", &position_x, response) &&
		     require_numeric(transform, "positionY", &position_y, response) &&
		     require_numeric(transform, "rotation", &rotation, response) &&
		     require_numeric(transform, "scaleX", &scale_x, response) &&
		     require_numeric(transform, "scaleY", &scale_y, response) &&
		     require_numeric(transform, "boundsWidth", &bounds_width, response) &&
		     require_numeric(transform, "boundsHeight", &bounds_height, response);
	if (!valid) {
		obs_data_release(transform);
		return false;
	}

	int64_t alignment, bounds_alignment, crop_left, crop_top, crop_right, crop_bottom;
	bool crop_to_bounds;
	const char *bounds_type;
	valid = required_string(transform, "boundsType", &bounds_type, response) &&
		require_integer(transform, "alignment", 0, UINT32_MAX, &alignment, response) &&
		require_integer(transform, "boundsAlignment", 0, UINT32_MAX, &bounds_alignment, response) &&
		require_boolean(transform, "cropToBounds", &crop_to_bounds, response) &&
		require_integer(transform, "cropLeft", 0, 100000, &crop_left, response) &&
		require_integer(transform, "cropTop", 0, 100000, &crop_top, response) &&
		require_integer(transform, "cropRight", 0, 100000, &crop_right, response) &&
		require_integer(transform, "cropBottom", 0, 100000, &crop_bottom, response);
	if (!valid) {
		obs_data_release(transform);
		return false;
	}
	if (!parse_bounds_type(bounds_type, &placement->transform.bounds_type)) {
		response_failed(response, "invalid_bounds_type", "boundsType is not supported by contract v1");
		obs_data_release(transform);
		return false;
	}

	placement->transform.pos.x = (float)position_x;
	placement->transform.pos.y = (float)position_y;
	placement->transform.rot = (float)rotation;
	placement->transform.scale.x = (float)scale_x;
	placement->transform.scale.y = (float)scale_y;
	placement->transform.alignment = (uint32_t)alignment;
	placement->transform.bounds_alignment = (uint32_t)bounds_alignment;
	placement->transform.bounds.x = (float)bounds_width;
	placement->transform.bounds.y = (float)bounds_height;
	placement->transform.crop_to_bounds = crop_to_bounds;
	placement->crop.left = (int)crop_left;
	placement->crop.top = (int)crop_top;
	placement->crop.right = (int)crop_right;
	placement->crop.bottom = (int)crop_bottom;
	obs_data_release(transform);

	if (position_x < -90001.0 || position_x > 90001.0 || position_y < -90001.0 || position_y > 90001.0 ||
	    rotation < -360.0 || rotation > 360.0 || bounds_width < 0.0 || bounds_height < 0.0 ||
	    (placement->transform.bounds_type != OBS_BOUNDS_NONE && (bounds_width < 1.0 || bounds_height < 1.0))) {
		response_failed(response, "placement_field_out_of_range", "placement value is outside contract v1 limits");
		return false;
	}
	return true;
}

static void read_placement(obs_sceneitem_t *item, struct placement *placement)
{
	memset(placement, 0, sizeof(*placement));
	placement->enabled = obs_sceneitem_visible(item);
	placement->locked = obs_sceneitem_locked(item);
	placement->index = obs_sceneitem_get_order_position(item);
	obs_sceneitem_get_info2(item, &placement->transform);
	obs_sceneitem_get_crop(item, &placement->crop);
}

static bool placement_equal(const struct placement *left, const struct placement *right)
{
	return left->enabled == right->enabled && left->locked == right->locked && left->index == right->index &&
	       left->transform.pos.x == right->transform.pos.x && left->transform.pos.y == right->transform.pos.y &&
	       left->transform.rot == right->transform.rot && left->transform.scale.x == right->transform.scale.x &&
	       left->transform.scale.y == right->transform.scale.y &&
	       left->transform.alignment == right->transform.alignment &&
	       left->transform.bounds_type == right->transform.bounds_type &&
	       left->transform.bounds_alignment == right->transform.bounds_alignment &&
	       left->transform.bounds.x == right->transform.bounds.x &&
	       left->transform.bounds.y == right->transform.bounds.y &&
	       left->transform.crop_to_bounds == right->transform.crop_to_bounds &&
	       left->crop.left == right->crop.left && left->crop.top == right->crop.top &&
	       left->crop.right == right->crop.right && left->crop.bottom == right->crop.bottom;
}

static bool item_matches_placement(obs_sceneitem_t *item, const struct placement *expected)
{
	struct placement observed;
	read_placement(item, &observed);
	return placement_equal(&observed, expected);
}

static obs_data_t *placement_to_data(const struct placement *placement)
{
	obs_data_t *data = obs_data_create();
	obs_data_t *transform = obs_data_create();

	obs_data_set_bool(data, "sceneItemEnabled", placement->enabled);
	obs_data_set_bool(data, "sceneItemLocked", placement->locked);
	obs_data_set_int(data, "sceneItemIndex", placement->index);
	obs_data_set_double(transform, "positionX", placement->transform.pos.x);
	obs_data_set_double(transform, "positionY", placement->transform.pos.y);
	obs_data_set_double(transform, "rotation", placement->transform.rot);
	obs_data_set_double(transform, "scaleX", placement->transform.scale.x);
	obs_data_set_double(transform, "scaleY", placement->transform.scale.y);
	obs_data_set_int(transform, "alignment", placement->transform.alignment);
	obs_data_set_string(transform, "boundsType", bounds_type_name(placement->transform.bounds_type));
	obs_data_set_int(transform, "boundsAlignment", placement->transform.bounds_alignment);
	obs_data_set_double(transform, "boundsWidth", placement->transform.bounds.x);
	obs_data_set_double(transform, "boundsHeight", placement->transform.bounds.y);
	obs_data_set_bool(transform, "cropToBounds", placement->transform.crop_to_bounds);
	obs_data_set_int(transform, "cropLeft", placement->crop.left);
	obs_data_set_int(transform, "cropTop", placement->crop.top);
	obs_data_set_int(transform, "cropRight", placement->crop.right);
	obs_data_set_int(transform, "cropBottom", placement->crop.bottom);
	obs_data_set_obj(data, "sceneItemTransform", transform);
	obs_data_release(transform);
	return data;
}

static bool get_absolute_coordinates(bool *absolute_coordinates)
{
	obs_data_t *private_data = obs_get_private_data();
	if (!private_data)
		return false;
	const bool known = user_value_has_type(private_data, "AbsoluteCoordinates", OBS_DATA_BOOLEAN);
	if (known)
		*absolute_coordinates = obs_data_get_bool(private_data, "AbsoluteCoordinates");
	obs_data_release(private_data);
	return known;
}

struct absolute_runtime_observation {
	const char *obs_version;
	bool coordinate_mode_known;
	bool absolute_coordinates;
	bool canvas_known;
	uint32_t base_width;
	uint32_t base_height;
	enum obstf_absolute_runtime_status status;
};

static void observe_absolute_runtime(struct absolute_runtime_observation *observation)
{
	memset(observation, 0, sizeof(*observation));
	observation->obs_version = safe_string(obs_get_version_string());
	observation->coordinate_mode_known = get_absolute_coordinates(&observation->absolute_coordinates);

	obs_canvas_t *main_canvas = obs_get_main_canvas();
	if (main_canvas) {
		struct obs_video_info video_info;
		observation->canvas_known = obs_canvas_get_video_info(main_canvas, &video_info);
		if (observation->canvas_known) {
			observation->base_width = video_info.base_width;
			observation->base_height = video_info.base_height;
		}
		obs_canvas_release(main_canvas);
	}

	observation->status = obstf_evaluate_absolute_runtime(
		observation->obs_version, observation->coordinate_mode_known, observation->absolute_coordinates,
		observation->canvas_known, observation->base_width, observation->base_height);
}

static const char *absolute_runtime_status_name(enum obstf_absolute_runtime_status status)
{
	switch (status) {
	case OBSTF_ABSOLUTE_RUNTIME_SUPPORTED:
		return "supported";
	case OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION:
		return "wrong_obs_version";
	case OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN:
		return "coordinate_mode_unknown";
	case OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE:
		return "relative_mode_unsupported";
	case OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN:
		return "main_canvas_unknown";
	case OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS:
		return "wrong_main_canvas";
	}
	return "unknown_guard_status";
}

static const char *absolute_runtime_error_code(enum obstf_absolute_runtime_status status)
{
	switch (status) {
	case OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION:
		return "absolute_runtime_wrong_obs_version";
	case OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN:
		return "absolute_runtime_coordinate_mode_unknown";
	case OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE:
		return "absolute_runtime_relative_mode_unsupported";
	case OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN:
		return "absolute_runtime_main_canvas_unknown";
	case OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS:
		return "absolute_runtime_wrong_main_canvas";
	case OBSTF_ABSOLUTE_RUNTIME_SUPPORTED:
		return "ok";
	}
	return "absolute_runtime_unknown_status";
}

static const char *absolute_runtime_error_message(enum obstf_absolute_runtime_status status)
{
	switch (status) {
	case OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION:
		return "group mutations require OBS 32.2.2";
	case OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN:
		return "group mutations require a known absolute coordinate mode";
	case OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE:
		return "relative-coordinate group mutation is unsupported after persistence drift";
	case OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN:
		return "group mutations require a readable main base canvas";
	case OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS:
		return "group mutations require a 1920x1080 main base canvas";
	case OBSTF_ABSOLUTE_RUNTIME_SUPPORTED:
		return "absolute runtime guard passed";
	}
	return "absolute runtime guard returned an unknown status";
}

static obs_data_t *absolute_runtime_to_data(const struct absolute_runtime_observation *observation)
{
	obs_data_t *data = obs_data_create();
	obs_data_set_string(data, "status", absolute_runtime_status_name(observation->status));
	obs_data_set_bool(data, "supported", observation->status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED);
	obs_data_set_string(data, "obsVersion", observation->obs_version);
	obs_data_set_bool(data, "coordinateModeKnown", observation->coordinate_mode_known);
	obs_data_set_bool(data, "absoluteCoordinates", observation->absolute_coordinates);
	obs_data_set_bool(data, "mainCanvasKnown", observation->canvas_known);
	obs_data_set_int(data, "baseWidth", observation->base_width);
	obs_data_set_int(data, "baseHeight", observation->base_height);
	obs_data_set_string(data, "comparisonMode", observation->status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED
							   ? "absolute-raw-exact"
							   : "ineligible");
	return data;
}

static bool apply_absolute_runtime_guard(const struct absolute_runtime_observation *observation,
					 obs_data_t *response)
{
	obs_data_t *receipt = absolute_runtime_to_data(observation);
	obs_data_set_obj(response, "runtimeGuard", receipt);
	obs_data_release(receipt);
	if (observation->status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED)
		return true;
	response_failed(response, absolute_runtime_error_code(observation->status),
			absolute_runtime_error_message(observation->status));
	return false;
}

static bool require_absolute_runtime(obs_data_t *response)
{
	struct absolute_runtime_observation observation;
	observe_absolute_runtime(&observation);
	return apply_absolute_runtime_guard(&observation, response);
}

static obs_data_t *placement_postcondition(obs_sceneitem_t *item, const struct placement *requested, bool *verified)
{
	struct placement observed;
	read_placement(item, &observed);
	struct absolute_runtime_observation runtime;
	observe_absolute_runtime(&runtime);
	const bool raw_exact = placement_equal(&observed, requested);
	*verified = obstf_absolute_postcondition_verified(runtime.status, raw_exact);

	obs_data_t *result = obs_data_create();
	obs_data_t *requested_data = placement_to_data(requested);
	obs_data_t *observed_data = placement_to_data(&observed);
	obs_data_t *runtime_data = absolute_runtime_to_data(&runtime);
	obs_data_set_string(result, "comparisonMode",
			    runtime.status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED ? "absolute-raw-exact" : "ineligible");
	obs_data_set_bool(result, "runtimeSupported", runtime.status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED);
	obs_data_set_obj(result, "requested", requested_data);
	obs_data_set_obj(result, "observed", observed_data);
	obs_data_set_obj(result, "runtimeGuard", runtime_data);
	obs_data_set_bool(result, "rawExactMatch", raw_exact);
	obs_data_set_bool(result, "verified", *verified);

	obs_data_release(runtime_data);
	obs_data_release(observed_data);
	obs_data_release(requested_data);
	return result;
}

static void apply_placement(obs_sceneitem_t *item, const struct placement *placement)
{
	obs_sceneitem_defer_update_begin(item);
	obs_sceneitem_set_info2(item, &placement->transform);
	obs_sceneitem_set_crop(item, &placement->crop);
	obs_sceneitem_defer_update_end(item);
	obs_sceneitem_set_visible(item, placement->enabled);
	obs_sceneitem_set_locked(item, placement->locked);
	obs_sceneitem_set_order_position(item, placement->index);
	obs_sceneitem_force_update_transform(item);
}

static obs_data_t *source_identity_to_data(obs_source_t *source)
{
	obs_data_t *data = obs_data_create();
	obs_data_set_string(data, "sourceUuid", safe_string(obs_source_get_uuid(source)));
	obs_data_set_string(data, "sourceName", safe_string(obs_source_get_name(source)));
	return data;
}

static obs_data_t *item_to_data(obs_sceneitem_t *item)
{
	obs_data_t *data = source_identity_to_data(obs_sceneitem_get_source(item));
	obs_source_t *source = obs_sceneitem_get_source(item);
	struct placement placement;
	read_placement(item, &placement);
	obs_data_t *placement_data = placement_to_data(&placement);

	obs_data_set_int(data, "sceneItemId", obs_sceneitem_get_id(item));
	obs_data_set_string(data, "inputKind", safe_string(obs_source_get_unversioned_id(source)));
	obs_data_set_int(data, "sourceWidth", obs_source_get_width(source));
	obs_data_set_int(data, "sourceHeight", obs_source_get_height(source));
	obs_data_set_obj(data, "placement", placement_data);
	obs_data_release(placement_data);
	return data;
}

static bool append_item(obs_scene_t *scene, obs_sceneitem_t *item, void *parameter)
{
	UNUSED_PARAMETER(scene);
	obs_data_array_t *array = parameter;
	obs_data_t *data = item_to_data(item);
	obs_data_array_push_back(array, data);
	obs_data_release(data);
	return true;
}

struct item_id_count_context {
	int64_t item_id;
	size_t count;
};

static bool count_matching_item_id(obs_scene_t *scene, obs_sceneitem_t *item, void *parameter)
{
	UNUSED_PARAMETER(scene);
	struct item_id_count_context *context = parameter;
	if (obs_sceneitem_get_id(item) == context->item_id)
		context->count++;
	return true;
}

static size_t scene_item_id_count(obs_scene_t *scene, int64_t item_id)
{
	struct item_id_count_context context = {.item_id = item_id, .count = 0};
	obs_scene_enum_items(scene, count_matching_item_id, &context);
	return context.count;
}

static obs_data_t *group_to_data(const struct resolved_group *group)
{
	obs_data_t *result = obs_data_create();
	obs_data_t *scene_identity = source_identity_to_data(group->parent.source);
	obs_data_t *group_identity = source_identity_to_data(group->source);
	struct placement group_placement;
	read_placement(group->item, &group_placement);
	obs_data_t *placement_data = placement_to_data(&group_placement);
	obs_data_array_t *children = obs_data_array_create();

	obs_data_set_int(group_identity, "sceneItemId", obs_sceneitem_get_id(group->item));
	obs_data_set_int(group_identity, "groupWidth", obs_source_get_width(group->source));
	obs_data_set_int(group_identity, "groupHeight", obs_source_get_height(group->source));
	obs_data_set_obj(group_identity, "placement", placement_data);
	obs_scene_enum_items(group->scene, append_item, children);

	obs_data_set_obj(result, "scene", scene_identity);
	obs_data_set_obj(result, "group", group_identity);
	obs_data_set_array(result, "children", children);

	obs_data_array_release(children);
	obs_data_release(placement_data);
	obs_data_release(group_identity);
	obs_data_release(scene_identity);
	return result;
}

static bool resolve_scene(obs_data_t *request, struct resolved_scene *resolved, obs_data_t *response)
{
	memset(resolved, 0, sizeof(*resolved));
	obs_data_t *identity = obs_data_get_obj(request, "scene");
	if (!identity) {
		response_failed(response, "missing_scene_identity", "scene identity object is required");
		return false;
	}

	const char *uuid;
	if (!required_string(identity, "sourceUuid", &uuid, response)) {
		obs_data_release(identity);
		return false;
	}

	resolved->source = obs_get_source_by_uuid(uuid);
	if (!resolved->source) {
		response_failed(response, "scene_not_found", "no source exists with scene.sourceUuid");
		obs_data_release(identity);
		return false;
	}

	resolved->scene = obs_scene_from_source(resolved->source);
	if (!resolved->scene) {
		response_failed(response, "source_is_not_scene", "scene.sourceUuid does not resolve to a real scene");
		obs_source_release(resolved->source);
		memset(resolved, 0, sizeof(*resolved));
		obs_data_release(identity);
		return false;
	}

	if (!optional_name_matches(identity, resolved->source, response)) {
		obs_source_release(resolved->source);
		memset(resolved, 0, sizeof(*resolved));
		obs_data_release(identity);
		return false;
	}

	obs_data_release(identity);
	return true;
}

static void release_scene(struct resolved_scene *resolved)
{
	if (resolved->source)
		obs_source_release(resolved->source);
	memset(resolved, 0, sizeof(*resolved));
}

static bool resolve_group(obs_data_t *request, struct resolved_group *resolved, obs_data_t *response)
{
	memset(resolved, 0, sizeof(*resolved));
	if (!resolve_scene(request, &resolved->parent, response))
		return false;

	obs_data_t *identity = obs_data_get_obj(request, "group");
	if (!identity) {
		response_failed(response, "missing_group_identity", "group identity object is required");
		release_scene(&resolved->parent);
		return false;
	}

	const char *uuid;
	int64_t item_id;
	if (!required_string(identity, "sourceUuid", &uuid, response) ||
	    !require_integer(identity, "sceneItemId", 0, INT64_MAX, &item_id, response)) {
		obs_data_release(identity);
		release_scene(&resolved->parent);
		return false;
	}

	if (scene_item_id_count(resolved->parent.scene, item_id) != 1) {
		response_failed(response, "ambiguous_group_item_id",
				"group.sceneItemId is absent or duplicated in the parent scene");
		obs_data_release(identity);
		release_scene(&resolved->parent);
		return false;
	}
	resolved->item = obs_scene_find_sceneitem_by_id(resolved->parent.scene, item_id);
	if (!resolved->item || !obs_sceneitem_is_group(resolved->item)) {
		response_failed(response, "group_item_not_found", "group item ID does not resolve to a direct real group");
		obs_data_release(identity);
		release_scene(&resolved->parent);
		return false;
	}

	resolved->source = obs_sceneitem_get_source(resolved->item);
	if (strcmp(safe_string(obs_source_get_uuid(resolved->source)), uuid) != 0 ||
	    !optional_name_matches(identity, resolved->source, response)) {
		if (strcmp(safe_string(obs_source_get_uuid(resolved->source)), uuid) != 0)
			response_failed(response, "group_identity_mismatch", "group UUID and scene-item ID disagree");
		obs_data_release(identity);
		release_scene(&resolved->parent);
		memset(resolved, 0, sizeof(*resolved));
		return false;
	}

	resolved->scene = obs_sceneitem_group_get_scene(resolved->item);
	if (!resolved->scene) {
		response_failed(response, "group_context_unavailable", "resolved group has no child scene context");
		obs_data_release(identity);
		release_scene(&resolved->parent);
		memset(resolved, 0, sizeof(*resolved));
		return false;
	}

	obs_data_release(identity);
	return true;
}

static void release_group(struct resolved_group *resolved)
{
	release_scene(&resolved->parent);
	memset(resolved, 0, sizeof(*resolved));
}

static obs_sceneitem_t *resolve_child(obs_data_t *request, const struct resolved_group *group, obs_data_t *response)
{
	obs_data_t *identity = obs_data_get_obj(request, "child");
	if (!identity) {
		response_failed(response, "missing_child_identity", "child identity object is required");
		return NULL;
	}

	const char *uuid;
	int64_t item_id;
	if (!required_string(identity, "sourceUuid", &uuid, response) ||
	    !require_integer(identity, "sceneItemId", 0, INT64_MAX, &item_id, response)) {
		obs_data_release(identity);
		return NULL;
	}

	if (scene_item_id_count(group->scene, item_id) != 1) {
		response_failed(response, "ambiguous_child_item_id",
				"child.sceneItemId is absent or duplicated in the exact group");
		obs_data_release(identity);
		return NULL;
	}
	obs_sceneitem_t *item = obs_scene_find_sceneitem_by_id(group->scene, item_id);
	if (!item) {
		response_failed(response, "child_not_found", "child item ID is absent from the exact group");
		obs_data_release(identity);
		return NULL;
	}

	obs_source_t *source = obs_sceneitem_get_source(item);
	if (strcmp(safe_string(obs_source_get_uuid(source)), uuid) != 0) {
		response_failed(response, "child_identity_mismatch", "child UUID and scene-item ID disagree");
		obs_data_release(identity);
		return NULL;
	}
	if (!optional_name_matches(identity, source, response)) {
		obs_data_release(identity);
		return NULL;
	}

	obs_data_release(identity);
	return item;
}

static bool source_name_available(const char *name)
{
	obs_source_t *existing = obs_get_source_by_name(name);
	if (!existing)
		return true;
	obs_source_release(existing);
	return false;
}

static bool declared_object_matches(obs_data_t *expected, obs_data_t *actual);

static bool declared_array_matches(obs_data_array_t *expected, obs_data_array_t *actual)
{
	if (!expected || !actual || obs_data_array_count(expected) != obs_data_array_count(actual))
		return false;
	for (size_t i = 0; i < obs_data_array_count(expected); i++) {
		obs_data_t *expected_entry = obs_data_array_item(expected, i);
		obs_data_t *actual_entry = obs_data_array_item(actual, i);
		const bool matches = declared_object_matches(expected_entry, actual_entry);
		obs_data_release(expected_entry);
		obs_data_release(actual_entry);
		if (!matches)
			return false;
	}
	return true;
}

static bool declared_item_matches(obs_data_item_t *expected, obs_data_item_t *actual)
{
	if (!expected || !actual || obs_data_item_gettype(expected) != obs_data_item_gettype(actual))
		return false;

	switch (obs_data_item_gettype(expected)) {
	case OBS_DATA_NULL:
		return true;
	case OBS_DATA_STRING:
		return strcmp(safe_string(obs_data_item_get_string(expected)), safe_string(obs_data_item_get_string(actual))) ==
		       0;
	case OBS_DATA_NUMBER:
		if (obs_data_item_numtype(expected) == OBS_DATA_NUM_INT &&
		    obs_data_item_numtype(actual) == OBS_DATA_NUM_INT)
			return obs_data_item_get_int(expected) == obs_data_item_get_int(actual);
		return obs_data_item_get_double(expected) == obs_data_item_get_double(actual);
	case OBS_DATA_BOOLEAN:
		return obs_data_item_get_bool(expected) == obs_data_item_get_bool(actual);
	case OBS_DATA_OBJECT: {
		obs_data_t *expected_object = obs_data_item_get_obj(expected);
		obs_data_t *actual_object = obs_data_item_get_obj(actual);
		const bool matches = declared_object_matches(expected_object, actual_object);
		obs_data_release(expected_object);
		obs_data_release(actual_object);
		return matches;
	}
	case OBS_DATA_ARRAY: {
		obs_data_array_t *expected_array = obs_data_item_get_array(expected);
		obs_data_array_t *actual_array = obs_data_item_get_array(actual);
		const bool matches = declared_array_matches(expected_array, actual_array);
		obs_data_array_release(expected_array);
		obs_data_array_release(actual_array);
		return matches;
	}
	}
	return false;
}

static bool declared_object_matches(obs_data_t *expected, obs_data_t *actual)
{
	if (!expected || !actual)
		return false;
	for (obs_data_item_t *item = obs_data_first(expected); item; obs_data_item_next(&item)) {
		obs_data_item_t *observed = obs_data_item_byname(actual, obs_data_item_get_name(item));
		const bool matches = declared_item_matches(item, observed);
		if (observed)
			obs_data_item_release(&observed);
		if (!matches) {
			obs_data_item_release(&item);
			return false;
		}
	}
	return true;
}

static obs_data_t *verify_declared_settings(obs_data_t *declared, obs_source_t *source, bool *verified)
{
	obs_data_t *result = obs_data_create();
	obs_data_array_t *mismatches = obs_data_array_create();
	obs_data_t *actual = obs_source_get_settings(source);
	int64_t declared_count = 0;
	int64_t matched_count = 0;

	if (actual) {
		for (obs_data_item_t *item = obs_data_first(declared); item; obs_data_item_next(&item)) {
			declared_count++;
			const char *key = obs_data_item_get_name(item);
			obs_data_item_t *observed = obs_data_item_byname(actual, key);
			const bool matches = declared_item_matches(item, observed);
			if (observed)
				obs_data_item_release(&observed);
			if (matches) {
				matched_count++;
			} else {
				obs_data_t *entry = obs_data_create();
				obs_data_set_string(entry, "key", key);
				obs_data_set_string(entry, "reason", "effective value or type mismatch");
				obs_data_array_push_back(mismatches, entry);
				obs_data_release(entry);
			}
		}
	}

	*verified = actual && declared_count == matched_count;
	obs_data_set_bool(result, "settingsReadbackAvailable", actual != NULL);
	obs_data_set_string(result, "comparison", "declaredKeysAgainstEffectiveCurrentSettings");
	obs_data_set_int(result, "declaredKeyCount", declared_count);
	obs_data_set_int(result, "matchedKeyCount", matched_count);
	obs_data_set_bool(result, "allDeclaredSettingsMatched", *verified);
	obs_data_set_array(result, "mismatchedKeys", mismatches);
	obs_data_array_release(mismatches);
	if (actual)
		obs_data_release(actual);
	return result;
}

static void get_capabilities(obs_data_t *request, obs_data_t *response)
{
	UNUSED_PARAMETER(request);

	obs_data_t *result = obs_data_create();
	obs_data_t *supports = obs_data_create();
	obs_data_t *execution = obs_data_create();
	obs_data_array_t *requests = obs_data_array_create();

	obs_data_set_string(result, "vendorName", VENDOR_NAME);
	obs_data_set_int(result, "contractVersion", CONTRACT_VERSION);
	obs_data_set_string(result, "bridgeVersion", BRIDGE_VERSION);
	obs_data_set_string(result, "obsVersion", safe_string(obs_get_version_string()));
	obs_data_set_int(result, "obsWebSocketApiVersion", obs_websocket_get_api_version());
	struct absolute_runtime_observation runtime;
	observe_absolute_runtime(&runtime);
	obs_data_t *runtime_data = absolute_runtime_to_data(&runtime);
	obs_data_set_bool(result, "coordinateModeKnown", runtime.coordinate_mode_known);
	obs_data_set_bool(result, "absoluteCoordinates", runtime.absolute_coordinates);
	obs_data_set_bool(result, "mainCanvasKnown", runtime.canvas_known);
	obs_data_set_int(result, "baseWidth", runtime.base_width);
	obs_data_set_int(result, "baseHeight", runtime.base_height);
	obs_data_set_obj(result, "absoluteRuntimeGuard", runtime_data);
	char *scene_collection = obs_frontend_get_current_scene_collection();
	obs_data_set_string(result, "currentSceneCollectionName", safe_string(scene_collection));
	if (scene_collection)
		bfree(scene_collection);
	obs_data_set_bool(result, "outputStateObserved", true);
	obs_data_set_bool(result, "streamingActive", obs_frontend_streaming_active());
	obs_data_set_bool(result, "recordingActive", obs_frontend_recording_active());
	obs_data_set_bool(result, "replayBufferActive", obs_frontend_replay_buffer_active());
	obs_data_set_bool(result, "virtualCameraActive", obs_frontend_virtualcam_active());
#if defined(__APPLE__)
	obs_data_set_string(result, "platform", "macos");
#elif defined(_WIN32)
	obs_data_set_string(result, "platform", "windows");
#else
	obs_data_set_string(result, "platform", "linux");
#endif
#if defined(__aarch64__) || defined(__arm64__)
	obs_data_set_string(result, "architecture", "arm64");
#elif defined(__x86_64__) || defined(_M_X64)
	obs_data_set_string(result, "architecture", "x86_64");
#else
	obs_data_set_string(result, "architecture", "unknown");
#endif

	obs_data_set_bool(supports, "describeFilter", true);
	obs_data_set_bool(supports, "filterDescriptorMetadata", true);
	obs_data_set_bool(supports, "filterDescriptorInspectProperties", true);
	obs_data_set_bool(supports, "realGroupCreate", true);
	obs_data_set_bool(supports, "strictJsonRequestTypes", true);
	obs_data_set_bool(supports, "absoluteGroupGraphMutations",
			  runtime.status == OBSTF_ABSOLUTE_RUNTIME_SUPPORTED);
	obs_data_set_bool(supports, "relativeGroupGraphMutations", false);
	obs_data_set_bool(supports, "placementCanonicalization", false);
	obs_data_set_bool(supports, "directInputCreateInGroup", true);
	obs_data_set_bool(supports, "declaredInputSettingsPostcondition", true);
	obs_data_set_bool(supports, "directExistingSourcePlacementInGroup", true);
	obs_data_set_bool(supports, "moveParentItemToGroup", true);
	obs_data_set_bool(supports, "exactGroupChildObservation", true);
	obs_data_set_bool(supports, "groupChildUpdate", false);
	obs_data_set_bool(supports, "groupChildUpdateViaCoreWebSocket", true);
	obs_data_set_bool(supports, "groupChildPlacementRemoval", true);
	obs_data_set_bool(supports, "moveChildToParent", true);
	obs_data_set_bool(supports, "ungroup", true);
	obs_data_set_bool(supports, "ungroupReturnsReassignedItemIds", true);
	obs_data_set_bool(supports, "sourceDelete", false);
	obs_data_set_bool(supports, "frontendSaveCheckpoint", true);
	obs_data_set_bool(supports, "diskDurabilityReceipt", false);

	obs_data_set_string(execution, "vendorCallbackThread", "obs-websocket request thread");
	obs_data_set_string(execution, "graphOperationThread", "OBS_TASK_UI");
	obs_data_set_bool(execution, "requestSynchronous", true);
	obs_data_set_bool(execution, "multiRequestAtomic", false);
	obs_data_set_bool(execution, "compareAndSwap", false);

	const char *names[] = {"GetCapabilities",
			       "GetGroup",
			       "CreateGroup",
			       "CreateInputInGroup",
			       "AddSourceToGroup",
			       "MoveParentItemToGroup",
			       "RemoveGroupChildPlacement",
			       "MoveGroupChildToParent",
			       "Ungroup",
			       "SaveSceneCollectionCheckpoint",
			       "DescribeFilter"};
	for (size_t i = 0; i < sizeof(names) / sizeof(names[0]); i++) {
		obs_data_t *entry = obs_data_create();
		obs_data_set_string(entry, "requestType", names[i]);
		obs_data_set_bool(entry, "mutation", i >= 2 && i < 10);
		obs_data_set_bool(entry, "requiresAbsoluteRuntime", i >= 2 && i < 10);
		obs_data_array_push_back(requests, entry);
		obs_data_release(entry);
	}

	obs_data_set_obj(result, "supports", supports);
	obs_data_set_obj(result, "execution", execution);
	obs_data_set_array(result, "requests", requests);
	response_result(response, result);
	response_status(response, "completed", "ok", "capabilities observed", false, true);

	obs_data_array_release(requests);
	obs_data_release(execution);
	obs_data_release(supports);
	obs_data_release(runtime_data);
	obs_data_release(result);
}

static void get_group(obs_data_t *request, obs_data_t *response)
{
	struct resolved_group group;
	if (!resolve_group(request, &group, response))
		return;
	obs_data_t *result = group_to_data(&group);
	response_result(response, result);
	response_status(response, "completed", "ok", "exact group graph observed", false, true);
	obs_data_release(result);
	release_group(&group);
}

static void create_group(obs_data_t *request, obs_data_t *response)
{
	const char *group_name;
	if (!required_string(request, "groupName", &group_name, response))
		return;
	obs_data_t *placement_data = obs_data_get_obj(request, "placement");
	struct placement desired;
	if (!parse_placement(placement_data, &desired, response)) {
		if (placement_data)
			obs_data_release(placement_data);
		return;
	}
	obs_data_release(placement_data);

	struct resolved_scene scene;
	if (!resolve_scene(request, &scene, response))
		return;
	if (!source_name_available(group_name)) {
		response_failed(response, "group_name_exists", "groupName is already used by a source");
		release_scene(&scene);
		return;
	}

	obs_data_set_bool(response, "mutationAttempted", true);
	obs_sceneitem_t *item = obs_scene_add_group2(scene.scene, group_name, true);
	if (!item) {
		response_status(response, "uncertain", "group_create_failed",
				"libobs returned no group item; authoritative absence was not established", true, false);
		release_scene(&scene);
		return;
	}
	apply_placement(item, &desired);

	obs_source_t *group_source = obs_sceneitem_get_source(item);
	struct resolved_group created = {.parent = scene,
					 .item = item,
					 .source = group_source,
					 .scene = obs_sceneitem_group_get_scene(item)};
	bool placement_verified = false;
	obs_data_t *placement_receipt = placement_postcondition(item, &desired, &placement_verified);
	bool verified = created.scene && scene_item_id_count(scene.scene, obs_sceneitem_get_id(item)) == 1 &&
			strcmp(safe_string(obs_source_get_name(group_source)), group_name) == 0 && placement_verified;
	obs_data_t *result = group_to_data(&created);
	obs_data_set_obj(result, "placementPostcondition", placement_receipt);
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok", "group created and raw-exact postcondition observed", true,
				true);
	else
		response_status(response, "uncertain", "group_postcondition_mismatch",
				"group create returned but the raw-exact absolute postcondition did not match", true, false);
	obs_data_release(placement_receipt);
	obs_data_release(result);
	release_group(&created);
}

static bool parse_input_create(obs_data_t *request, const char **kind, const char **name, obs_data_t **settings,
			       struct placement *placement, obs_data_t *response)
{
	if (!required_string(request, "inputKind", kind, response) ||
	    !required_string(request, "inputName", name, response))
		return false;
	*settings = obs_data_get_obj(request, "inputSettings");
	if (!*settings) {
		response_failed(response, "missing_input_settings", "inputSettings object is required");
		return false;
	}
	obs_data_t *placement_data = obs_data_get_obj(request, "placement");
	if (!parse_placement(placement_data, placement, response)) {
		if (placement_data)
			obs_data_release(placement_data);
		obs_data_release(*settings);
		*settings = NULL;
		return false;
	}
	obs_data_release(placement_data);
	return true;
}

static const char *resolve_installed_input_kind(const char *requested_kind)
{
	const char *versioned = NULL;
	const char *unversioned = NULL;
	for (size_t index = 0; obs_enum_input_types2(index, &versioned, &unversioned); index++) {
		if ((versioned && strcmp(requested_kind, versioned) == 0) ||
		    (unversioned && strcmp(requested_kind, unversioned) == 0))
			return versioned;
	}
	return NULL;
}

static obs_sceneitem_t *add_source_directly_to_group(struct resolved_group *group, obs_source_t *source,
						     const struct placement *placement)
{
	obs_sceneitem_defer_group_resize_begin(group->item);
	obs_sceneitem_t *child = obs_scene_add(group->scene, source);
	if (child)
		apply_placement(child, placement);
	obs_sceneitem_defer_group_resize_end(group->item);
	return child;
}

static void create_input_in_group(obs_data_t *request, obs_data_t *response)
{
	const char *kind;
	const char *name;
	obs_data_t *settings = NULL;
	struct placement desired;
	if (!parse_input_create(request, &kind, &name, &settings, &desired, response))
		return;

	struct resolved_group group;
	if (!resolve_group(request, &group, response)) {
		obs_data_release(settings);
		return;
	}
	if (!source_name_available(name)) {
		response_failed(response, "input_name_exists", "inputName is already used by a source");
		release_group(&group);
		obs_data_release(settings);
		return;
	}
	const char *versioned_kind = resolve_installed_input_kind(kind);
	if (!versioned_kind) {
		response_failed(response, "input_kind_unavailable", "inputKind is not installed as an OBS input type");
		release_group(&group);
		obs_data_release(settings);
		return;
	}

	obs_data_set_bool(response, "mutationAttempted", true);
	obs_source_t *source = obs_source_create(versioned_kind, name, settings, NULL);
	if (!source) {
		obs_data_release(settings);
		response_status(response, "uncertain", "input_create_failed",
				"libobs returned no source; authoritative absence was not established", true, false);
		release_group(&group);
		return;
	}
	if (strcmp(safe_string(obs_source_get_name(source)), name) != 0) {
		obs_source_release(source);
		obs_data_release(settings);
		response_status(response, "uncertain", "input_name_race",
				"libobs uniquified inputName; release occurred but authoritative absence was not established", true,
				false);
		release_group(&group);
		return;
	}

	obs_sceneitem_t *child = add_source_directly_to_group(&group, source, &desired);
	if (!child) {
		obs_source_release(source);
		obs_data_release(settings);
		response_status(response, "uncertain", "group_child_create_failed",
				"source was created but no group placement was returned; recovery requires observation", true,
				false);
		release_group(&group);
		return;
	}

	const int64_t item_id = obs_sceneitem_get_id(child);
	char *source_uuid = bstrdup(safe_string(obs_source_get_uuid(source)));
	obs_source_release(source);
	obs_sceneitem_t *observed = obs_scene_find_sceneitem_by_id(group.scene, item_id);
	obs_source_t *observed_source = observed ? obs_sceneitem_get_source(observed) : NULL;
	bool graph_verified = observed_source && scene_item_id_count(group.scene, item_id) == 1 && *source_uuid &&
			      strcmp(safe_string(obs_source_get_uuid(observed_source)), source_uuid) == 0 &&
			      strcmp(safe_string(obs_source_get_name(observed_source)), name) == 0 &&
			      strcmp(safe_string(obs_source_get_id(observed_source)), versioned_kind) == 0;
	bool placement_verified = false;
	obs_data_t *placement_receipt =
		observed ? placement_postcondition(observed, &desired, &placement_verified) : obs_data_create();
	bool settings_verified = false;
	obs_data_t *settings_postcondition =
		observed_source ? verify_declared_settings(settings, observed_source, &settings_verified)
			 : obs_data_create();
	if (!observed) {
		obs_data_set_bool(settings_postcondition, "settingsReadbackAvailable", false);
		obs_data_set_bool(settings_postcondition, "allDeclaredSettingsMatched", false);
	}
	obs_data_release(settings);
	bool verified = graph_verified && placement_verified && settings_verified;
	obs_data_t *result = observed ? item_to_data(observed) : obs_data_create();
	obs_data_set_string(result, "sourceUuid", source_uuid);
	obs_data_set_int(result, "sceneItemId", item_id);
	obs_data_set_bool(result, "directGroupPlacement", true);
	obs_data_set_bool(result, "parentAnchorCreated", false);
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	obs_data_set_obj(result, "placementPostcondition", placement_receipt);
	obs_data_set_obj(result, "settingsPostcondition", settings_postcondition);
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok",
				"input, declared settings and raw-exact group placement observed", true, true);
	else
		response_status(response, "uncertain", "group_input_postcondition_mismatch",
				"native calls returned but graph, placement or declared-settings postcondition did not match", true,
				false);
	obs_data_release(settings_postcondition);
	obs_data_release(placement_receipt);
	obs_data_release(result);
	bfree(source_uuid);
	release_group(&group);
}

static bool resolve_existing_input(obs_data_t *request, obs_source_t **source, obs_data_t *response)
{
	obs_data_t *identity = obs_data_get_obj(request, "source");
	if (!identity) {
		response_failed(response, "missing_source_identity", "source identity object is required");
		return false;
	}
	const char *uuid;
	if (!required_string(identity, "sourceUuid", &uuid, response)) {
		obs_data_release(identity);
		return false;
	}
	*source = obs_get_source_by_uuid(uuid);
	if (!*source) {
		response_failed(response, "source_not_found", "source.sourceUuid does not exist");
		obs_data_release(identity);
		return false;
	}
	if (!optional_name_matches(identity, *source, response) || obs_source_get_type(*source) != OBS_SOURCE_TYPE_INPUT) {
		if (obs_source_get_type(*source) != OBS_SOURCE_TYPE_INPUT)
			response_failed(response, "source_not_input", "only existing input sources can be placed in a group");
		obs_source_release(*source);
		*source = NULL;
		obs_data_release(identity);
		return false;
	}
	obs_data_release(identity);
	return true;
}

static void add_source_to_group(obs_data_t *request, obs_data_t *response)
{
	obs_data_t *placement_data = obs_data_get_obj(request, "placement");
	struct placement desired;
	if (!parse_placement(placement_data, &desired, response)) {
		if (placement_data)
			obs_data_release(placement_data);
		return;
	}
	obs_data_release(placement_data);

	struct resolved_group group;
	if (!resolve_group(request, &group, response))
		return;
	obs_source_t *source = NULL;
	if (!resolve_existing_input(request, &source, response)) {
		release_group(&group);
		return;
	}

	obs_data_set_bool(response, "mutationAttempted", true);
	obs_sceneitem_t *child = add_source_directly_to_group(&group, source, &desired);
	char *source_uuid = bstrdup(safe_string(obs_source_get_uuid(source)));
	obs_source_release(source);
	if (!child) {
		bfree(source_uuid);
		response_status(response, "uncertain", "group_placement_create_failed",
				"libobs returned no group placement; authoritative absence was not established", true, false);
		release_group(&group);
		return;
	}

	const int64_t item_id = obs_sceneitem_get_id(child);
	obs_sceneitem_t *observed = obs_scene_find_sceneitem_by_id(group.scene, item_id);
	bool placement_verified = false;
	obs_data_t *placement_receipt =
		observed ? placement_postcondition(observed, &desired, &placement_verified) : obs_data_create();
	bool verified = observed && scene_item_id_count(group.scene, item_id) == 1 &&
			strcmp(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(observed))), source_uuid) == 0 &&
			placement_verified;
	obs_data_t *result = observed ? item_to_data(observed) : obs_data_create();
	obs_data_set_bool(result, "directGroupPlacement", true);
	obs_data_set_bool(result, "sourceDuplicated", false);
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	obs_data_set_obj(result, "placementPostcondition", placement_receipt);
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok",
				"existing source placed directly in group with raw-exact placement", true, true);
	else
		response_status(response, "uncertain", "group_placement_postcondition_mismatch",
				"native add returned but exact group child postcondition did not match", true, false);
	obs_data_release(result);
	obs_data_release(placement_receipt);
	bfree(source_uuid);
	release_group(&group);
}

static bool parse_expected(obs_data_t *request, struct placement *expected, obs_data_t *response)
{
	obs_data_t *expected_data = obs_data_get_obj(request, "expectedPlacement");
	if (!parse_placement(expected_data, expected, response)) {
		if (expected_data)
			obs_data_release(expected_data);
		return false;
	}
	obs_data_release(expected_data);
	return true;
}

static bool parse_desired(obs_data_t *request, struct placement *desired, obs_data_t *response)
{
	obs_data_t *desired_data = obs_data_get_obj(request, "placement");
	if (!parse_placement(desired_data, desired, response)) {
		if (desired_data)
			obs_data_release(desired_data);
		return false;
	}
	obs_data_release(desired_data);
	return true;
}

static obs_sceneitem_t *resolve_parent_item(obs_data_t *request, const struct resolved_group *group,
					    obs_data_t *response)
{
	obs_data_t *identity = obs_data_get_obj(request, "parentItem");
	if (!identity) {
		response_failed(response, "missing_parent_item_identity", "parentItem identity object is required");
		return NULL;
	}
	const char *uuid;
	int64_t item_id;
	if (!required_string(identity, "sourceUuid", &uuid, response) ||
	    !require_integer(identity, "sceneItemId", 0, INT64_MAX, &item_id, response)) {
		obs_data_release(identity);
		return NULL;
	}
	if (scene_item_id_count(group->parent.scene, item_id) != 1) {
		response_failed(response, "ambiguous_parent_item_id",
				"parentItem.sceneItemId is absent or duplicated in the parent scene");
		obs_data_release(identity);
		return NULL;
	}
	obs_sceneitem_t *item = obs_scene_find_sceneitem_by_id(group->parent.scene, item_id);
	if (!item || obs_sceneitem_is_group(item)) {
		response_failed(response, "parent_item_not_found",
				"parent item ID does not resolve to a direct non-group placement");
		obs_data_release(identity);
		return NULL;
	}
	obs_source_t *source = obs_sceneitem_get_source(item);
	if (obs_source_get_type(source) != OBS_SOURCE_TYPE_INPUT ||
	    strcmp(safe_string(obs_source_get_uuid(source)), uuid) != 0) {
		response_failed(response, "parent_item_identity_mismatch",
				"parent item must resolve to the exact input source UUID");
		obs_data_release(identity);
		return NULL;
	}
	if (!optional_name_matches(identity, source, response)) {
		obs_data_release(identity);
		return NULL;
	}
	obs_data_release(identity);
	return item;
}

static void move_parent_item_to_group(obs_data_t *request, obs_data_t *response)
{
	struct placement expected, desired;
	if (!parse_expected(request, &expected, response) || !parse_desired(request, &desired, response))
		return;
	struct resolved_group group;
	if (!resolve_group(request, &group, response))
		return;
	obs_sceneitem_t *item = resolve_parent_item(request, &group, response);
	if (!item) {
		release_group(&group);
		return;
	}
	if (!item_matches_placement(item, &expected)) {
		response_failed(response, "stale_parent_item_placement",
				"fresh parent placement does not match expectedPlacement; no write performed");
		release_group(&group);
		return;
	}

	const int64_t item_id = obs_sceneitem_get_id(item);
	if (obs_scene_find_sceneitem_by_id(group.scene, item_id)) {
		response_failed(response, "destination_item_id_collision",
				"group already contains sceneItemId; movement refused before mutation");
		release_group(&group);
		return;
	}
	char *source_uuid = bstrdup(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(item))));
	obs_data_set_bool(response, "mutationAttempted", true);
	obs_sceneitem_defer_group_resize_begin(group.item);
	obs_sceneitem_group_add_item(group.item, item);
	apply_placement(item, &desired);
	obs_sceneitem_defer_group_resize_end(group.item);

	obs_sceneitem_t *group_item = obs_scene_find_sceneitem_by_id(group.scene, item_id);
	obs_sceneitem_t *parent_item = obs_scene_find_sceneitem_by_id(group.parent.scene, item_id);
	bool parent_absent = !parent_item ||
			     strcmp(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(parent_item))), source_uuid) != 0;
	bool placement_verified = false;
	obs_data_t *placement_receipt =
		group_item ? placement_postcondition(group_item, &desired, &placement_verified) : obs_data_create();
	bool verified = parent_absent && group_item && scene_item_id_count(group.scene, item_id) == 1 &&
			strcmp(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(group_item))), source_uuid) == 0 &&
			placement_verified;
	obs_data_t *result = group_item ? item_to_data(group_item) : obs_data_create();
	obs_data_set_string(result, "operationSemantics", "moveParentItemToGroup");
	obs_data_set_bool(result, "sourceDuplicated", false);
	obs_data_set_bool(result, "sceneItemIdentityPreserved", group_item && obs_sceneitem_get_id(group_item) == item_id);
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	obs_data_set_obj(result, "placementPostcondition", placement_receipt);
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok",
				"parent placement moved into group on unchanged source/item identities", true, true);
	else
		response_status(response, "uncertain", "move_to_group_postcondition_mismatch",
				"group-add returned but exact membership/placement postcondition was not observed", true, false);
	obs_data_release(result);
	obs_data_release(placement_receipt);
	bfree(source_uuid);
	release_group(&group);
}

static void remove_group_child_placement(obs_data_t *request, obs_data_t *response)
{
	struct placement expected;
	if (!parse_expected(request, &expected, response))
		return;
	struct resolved_group group;
	if (!resolve_group(request, &group, response))
		return;
	obs_sceneitem_t *child = resolve_child(request, &group, response);
	if (!child) {
		release_group(&group);
		return;
	}
	if (!item_matches_placement(child, &expected)) {
		response_failed(response, "stale_group_child_placement",
				"fresh child placement does not match expectedPlacement; no write performed");
		release_group(&group);
		return;
	}

	const int64_t item_id = obs_sceneitem_get_id(child);
	char *source_uuid = bstrdup(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(child))));
	obs_data_set_bool(response, "mutationAttempted", true);
	obs_sceneitem_defer_group_resize_begin(group.item);
	obs_sceneitem_remove(child);
	obs_sceneitem_defer_group_resize_end(group.item);

	obs_sceneitem_t *remaining = obs_scene_find_sceneitem_by_id(group.scene, item_id);
	bool absent = !remaining ||
		      strcmp(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(remaining))), source_uuid) != 0;
	obs_source_t *remaining_source = obs_get_source_by_uuid(source_uuid);
	bool source_still_exists = remaining_source != NULL;
	if (remaining_source)
		obs_source_release(remaining_source);

	obs_data_t *result = obs_data_create();
	obs_data_set_int(result, "removedSceneItemId", item_id);
	obs_data_set_string(result, "sourceUuid", source_uuid);
	obs_data_set_bool(result, "sourceStillExists", source_still_exists);
	obs_data_set_string(result, "operationSemantics", "placementRemoval");
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	response_result(response, result);
	if (absent)
		response_status(response, "completed", "ok", "group child placement removed and absence observed", true,
				true);
	else
		response_status(response, "uncertain", "group_child_removal_postcondition_mismatch",
				"remove returned but exact group child tuple remains", true, false);
	obs_data_release(result);
	bfree(source_uuid);
	release_group(&group);
}

static void move_group_child_to_parent(obs_data_t *request, obs_data_t *response)
{
	struct placement expected;
	if (!parse_expected(request, &expected, response))
		return;
	struct resolved_group group;
	if (!resolve_group(request, &group, response))
		return;
	obs_sceneitem_t *child = resolve_child(request, &group, response);
	if (!child) {
		release_group(&group);
		return;
	}
	if (!item_matches_placement(child, &expected)) {
		response_failed(response, "stale_group_child_placement",
				"fresh child placement does not match expectedPlacement; no write performed");
		release_group(&group);
		return;
	}

	const int64_t item_id = obs_sceneitem_get_id(child);
	if (obs_scene_find_sceneitem_by_id(group.parent.scene, item_id)) {
		response_failed(response, "destination_item_id_collision",
				"parent scene already contains sceneItemId; movement refused before mutation");
		release_group(&group);
		return;
	}
	char *source_uuid = bstrdup(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(child))));
	obs_data_set_bool(response, "mutationAttempted", true);
	obs_sceneitem_group_remove_item(group.item, child);
	obs_sceneitem_t *parent_item = obs_scene_find_sceneitem_by_id(group.parent.scene, item_id);
	bool verified = parent_item && scene_item_id_count(group.parent.scene, item_id) == 1 &&
			obs_sceneitem_get_scene(child) == group.parent.scene &&
			strcmp(safe_string(obs_source_get_uuid(obs_sceneitem_get_source(parent_item))), source_uuid) == 0;
	obs_data_t *result = parent_item ? item_to_data(parent_item) : obs_data_create();
	obs_data_set_string(result, "operationSemantics", "moveToParent");
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok",
				"child moved out of group on the same item/source identities", true, true);
	else
		response_status(response, "uncertain", "move_to_parent_postcondition_mismatch",
				"group remove-item returned but exact parent membership was not observed", true, false);
	obs_data_release(result);
	bfree(source_uuid);
	release_group(&group);
}

struct count_items_context {
	size_t count;
};

static bool count_item(obs_scene_t *scene, obs_sceneitem_t *item, void *parameter)
{
	UNUSED_PARAMETER(scene);
	UNUSED_PARAMETER(item);
	struct count_items_context *context = parameter;
	context->count++;
	return true;
}

struct collect_identity_context {
	struct child_identity *children;
	size_t capacity;
	size_t count;
};

static bool collect_identity(obs_scene_t *scene, obs_sceneitem_t *item, void *parameter)
{
	UNUSED_PARAMETER(scene);
	struct collect_identity_context *context = parameter;
	if (context->count >= context->capacity)
		return false;
	obs_source_t *source = obs_sceneitem_get_source(item);
	struct child_identity *identity = &context->children[context->count++];
	identity->source_uuid = bstrdup(safe_string(obs_source_get_uuid(source)));
	identity->source_name = bstrdup(safe_string(obs_source_get_name(source)));
	identity->scene_item_id = obs_sceneitem_get_id(item);
	return true;
}

static void free_child_identities(struct child_identity *children, size_t count)
{
	if (!children)
		return;
	for (size_t i = 0; i < count; i++) {
		bfree(children[i].source_uuid);
		bfree(children[i].source_name);
	}
	bfree(children);
}

static bool expected_children_match(obs_data_t *request, const struct resolved_group *group,
				    struct child_identity **children_out, size_t *count_out, obs_data_t *response)
{
	obs_data_array_t *expected = obs_data_get_array(request, "expectedChildren");
	if (!expected) {
		response_failed(response, "missing_expected_children", "expectedChildren array is required");
		return false;
	}

	struct count_items_context count_context = {0};
	obs_scene_enum_items(group->scene, count_item, &count_context);
	const size_t expected_count = obs_data_array_count(expected);
	if (expected_count != count_context.count) {
		response_failed(response, "stale_group_children", "expectedChildren count does not match fresh group");
		obs_data_array_release(expected);
		return false;
	}

	struct child_identity *children = bzalloc(sizeof(*children) * count_context.count);
	struct collect_identity_context collect = {.children = children,
						   .capacity = count_context.count,
						   .count = 0};
	obs_scene_enum_items(group->scene, collect_identity, &collect);
	for (size_t i = 0; i < collect.count; i++) {
		obs_data_t *entry = obs_data_array_item(expected, i);
		const char *uuid = obs_data_get_string(entry, "sourceUuid");
		int64_t item_id = -1;
		const char *name = obs_data_get_string(entry, "sourceName");
		bool match = user_value_has_type(entry, "sourceUuid", OBS_DATA_STRING) && uuid && *uuid &&
			     require_integer(entry, "sceneItemId", 0, INT64_MAX, &item_id, response) &&
			     strcmp(uuid, children[i].source_uuid) == 0 &&
			     item_id == children[i].scene_item_id;
		if (match && obs_data_has_user_value(entry, "sourceName"))
			match = user_value_has_type(entry, "sourceName", OBS_DATA_STRING) && name &&
				strcmp(name, children[i].source_name) == 0;
		obs_data_release(entry);
		if (!match) {
			response_failed(response, "stale_group_children",
					"expectedChildren order or identity does not match fresh group");
			free_child_identities(children, collect.count);
			obs_data_array_release(expected);
			return false;
		}
	}

	obs_data_array_release(expected);
	*children_out = children;
	*count_out = collect.count;
	return true;
}

static bool snapshot_scene_identities(obs_scene_t *scene, struct child_identity **items_out, size_t *count_out)
{
	struct count_items_context count_context = {0};
	obs_scene_enum_items(scene, count_item, &count_context);
	struct child_identity *items = count_context.count ? bzalloc(sizeof(*items) * count_context.count) : NULL;
	struct collect_identity_context collect = {
		.children = items,
		.capacity = count_context.count,
		.count = 0,
	};
	obs_scene_enum_items(scene, collect_identity, &collect);
	if (collect.count != count_context.count) {
		free_child_identities(items, collect.count);
		return false;
	}
	*items_out = items;
	*count_out = collect.count;
	return true;
}

static bool identity_tuple_present(const struct child_identity *items, size_t count, int64_t item_id,
				   const char *source_uuid)
{
	for (size_t i = 0; i < count; i++) {
		if (items[i].scene_item_id == item_id && strcmp(items[i].source_uuid, source_uuid) == 0)
			return true;
	}
	return false;
}

static bool numeric_item_ids_unique(const struct child_identity *items, size_t count)
{
	for (size_t i = 0; i < count; i++) {
		for (size_t j = i + 1; j < count; j++) {
			if (items[i].scene_item_id == items[j].scene_item_id)
				return false;
		}
	}
	return true;
}

struct ungroup_diff_context {
	const struct child_identity *before;
	size_t before_count;
	const struct child_identity *expected_children;
	size_t expected_count;
	size_t observed_new_count;
	bool order_and_sources_match;
	obs_data_array_t *moved;
};

static bool collect_ungrouped_item(obs_scene_t *scene, obs_sceneitem_t *item, void *parameter)
{
	UNUSED_PARAMETER(scene);
	struct ungroup_diff_context *context = parameter;
	obs_source_t *source = obs_sceneitem_get_source(item);
	const char *source_uuid = safe_string(obs_source_get_uuid(source));
	const int64_t item_id = obs_sceneitem_get_id(item);
	if (identity_tuple_present(context->before, context->before_count, item_id, source_uuid))
		return true;

	const size_t index = context->observed_new_count++;
	if (index >= context->expected_count ||
	    strcmp(source_uuid, context->expected_children[index].source_uuid) != 0) {
		context->order_and_sources_match = false;
		return true;
	}

	obs_data_t *data = item_to_data(item);
	obs_data_set_int(data, "previousGroupSceneItemId", context->expected_children[index].scene_item_id);
	obs_data_set_bool(data, "sceneItemIdentityPreserved", false);
	obs_data_array_push_back(context->moved, data);
	obs_data_release(data);
	return true;
}

static void ungroup(obs_data_t *request, obs_data_t *response)
{
	struct resolved_group group;
	if (!resolve_group(request, &group, response))
		return;
	struct child_identity *children = NULL;
	size_t child_count = 0;
	if (!expected_children_match(request, &group, &children, &child_count, response)) {
		release_group(&group);
		return;
	}
	if (!numeric_item_ids_unique(children, child_count)) {
		response_failed(response, "ambiguous_group_child_ids",
				"group has duplicate numeric child IDs; ungroup refused before mutation");
		free_child_identities(children, child_count);
		release_group(&group);
		return;
	}
	struct child_identity *parent_before = NULL;
	size_t parent_before_count = 0;
	if (!snapshot_scene_identities(group.parent.scene, &parent_before, &parent_before_count)) {
		response_failed(response, "parent_snapshot_failed", "could not snapshot parent scene before ungroup");
		free_child_identities(children, child_count);
		release_group(&group);
		return;
	}
	if (!numeric_item_ids_unique(parent_before, parent_before_count)) {
		response_failed(response, "ambiguous_parent_item_ids",
				"parent scene has duplicate numeric item IDs; ungroup refused before mutation");
		free_child_identities(parent_before, parent_before_count);
		free_child_identities(children, child_count);
		release_group(&group);
		return;
	}

	const int64_t group_item_id = obs_sceneitem_get_id(group.item);
	char *group_uuid = bstrdup(safe_string(obs_source_get_uuid(group.source)));
	obs_data_set_bool(response, "mutationAttempted", true);
	obs_sceneitem_group_ungroup2(group.item, true);

	obs_data_array_t *moved = obs_data_array_create();
	struct ungroup_diff_context diff = {
		.before = parent_before,
		.before_count = parent_before_count,
		.expected_children = children,
		.expected_count = child_count,
		.observed_new_count = 0,
		.order_and_sources_match = true,
		.moved = moved,
	};
	obs_scene_enum_items(group.parent.scene, collect_ungrouped_item, &diff);
	struct child_identity *parent_after = NULL;
	size_t parent_after_count = 0;
	bool after_snapshot = snapshot_scene_identities(group.parent.scene, &parent_after, &parent_after_count);
	bool group_absent = after_snapshot &&
			    !identity_tuple_present(parent_after, parent_after_count, group_item_id, group_uuid);
	bool count_matches = after_snapshot && parent_before_count > 0 &&
			     parent_after_count == parent_before_count - 1 + child_count;
	bool destination_ids_unique = after_snapshot && numeric_item_ids_unique(parent_after, parent_after_count);
	bool verified = group_absent && count_matches && destination_ids_unique && diff.order_and_sources_match &&
			diff.observed_new_count == child_count && obs_data_array_count(moved) == child_count;

	obs_data_t *result = obs_data_create();
	obs_data_set_string(result, "operationSemantics", "ungroupWholeGroup");
	obs_data_set_string(result, "removedGroupUuid", group_uuid);
	obs_data_set_int(result, "removedGroupSceneItemId", group_item_id);
	obs_data_set_array(result, "movedChildren", moved);
	obs_data_set_bool(result, "itemIdsReassigned", true);
	obs_data_set_bool(result, "destinationItemIdsUnique", destination_ids_unique);
	obs_data_set_bool(result, "layoutSettled", false);
	obs_data_set_bool(result, "postFrameReadRequired", true);
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok",
				"group removed and all exact children observed in the parent scene", true, true);
	else
		response_status(response, "uncertain", "ungroup_postcondition_mismatch",
				"ungroup returned but exact group/child postcondition was not established", true, false);

	obs_data_release(result);
	obs_data_array_release(moved);
	bfree(group_uuid);
	free_child_identities(parent_after, parent_after_count);
	free_child_identities(parent_before, parent_before_count);
	free_child_identities(children, child_count);
	release_group(&group);
}

static bool has_group_scope(obs_data_t *request)
{
	return obs_data_has_user_value(request, "scene") || obs_data_has_user_value(request, "group");
}

static void save_scene_collection_checkpoint(obs_data_t *request, obs_data_t *response)
{
	bool scoped = has_group_scope(request);
	if (scoped && !(obs_data_has_user_value(request, "scene") && obs_data_has_user_value(request, "group"))) {
		response_failed(response, "incomplete_save_scope", "scene and group must be supplied together");
		return;
	}

	if (scoped) {
		struct resolved_group before;
		if (!resolve_group(request, &before, response))
			return;
		release_group(&before);
	}

	obs_data_set_bool(response, "mutationAttempted", true);
	obs_frontend_save();

	obs_data_t *result = obs_data_create();
	obs_data_set_bool(result, "frontendSaveReturned", true);
	obs_data_set_bool(result, "diskDurabilityVerified", false);
	obs_data_set_bool(result, "reloadVerificationRequired", true);
	bool verified = true;
	if (scoped) {
		struct resolved_group after;
		verified = resolve_group(request, &after, response);
		if (verified) {
			obs_data_t *scope = group_to_data(&after);
			obs_data_set_obj(result, "scope", scope);
			obs_data_release(scope);
			release_group(&after);
		}
	}
	response_result(response, result);
	if (verified)
		response_status(response, "completed", "ok",
				"frontend save returned; filesystem and reload verification remain external", true, true);
	else
		response_status(response, "uncertain", "save_scope_postcondition_unavailable",
				"frontend save returned but the supplied in-memory scope could not be re-observed", true, false);
	obs_data_release(result);
}

static void describe_filter(obs_data_t *request, obs_data_t *response)
{
	obs_data_t *result = NULL;
	char error[512] = {0};
	obstf_descriptor_status_t status = obstf_describe_filter_request(request, &result, error, sizeof(error));
	if (status != OBSTF_DESCRIPTOR_OK) {
		response_status(response, "failed", obstf_descriptor_status_name(status), error, false, true);
		return;
	}
	response_result(response, result);
	response_status(response, "completed", "ok", "exact filter descriptor collected; see inspection receipts", false, true);
	obs_data_release(result);
}

static const struct request_registration requests[] = {
	{"DescribeFilter", describe_filter, false, false},
	{"GetCapabilities", get_capabilities, false, false},
	{"GetGroup", get_group, false, false},
	{"CreateGroup", create_group, true, true},
	{"CreateInputInGroup", create_input_in_group, true, true},
	{"AddSourceToGroup", add_source_to_group, true, true},
	{"MoveParentItemToGroup", move_parent_item_to_group, true, true},
	{"RemoveGroupChildPlacement", remove_group_child_placement, true, true},
	{"MoveGroupChildToParent", move_group_child_to_parent, true, true},
	{"Ungroup", ungroup, true, true},
	{"SaveSceneCollectionCheckpoint", save_scene_collection_checkpoint, true, true},
};

static void execute_request_task(void *parameter)
{
	struct request_task *task = parameter;
	if (!require_contract(task->request, task->response, task->registration->mutation))
		return;
	if (task->registration->requires_absolute_runtime && !require_absolute_runtime(task->response))
		return;
	task->registration->handler(task->request, task->response);
}

static void vendor_request_callback(obs_data_t *request, obs_data_t *response, void *private_data)
{
	const struct request_registration *registration = private_data;
	response_base(response, registration->name, request);
	struct request_task task = {.registration = registration, .request = request, .response = response};
	if (obs_in_task_thread(OBS_TASK_UI))
		execute_request_task(&task);
	else
		obs_queue_task(OBS_TASK_UI, execute_request_task, &task, true);
}

bool obs_module_load(void)
{
	blog(LOG_INFO, "[obstf-native-bridge] loaded bridge %s contract %d; awaiting post-load vendor registration",
	     BRIDGE_VERSION, CONTRACT_VERSION);
	return true;
}

void obs_module_post_load(void)
{
	const unsigned int api_version = obs_websocket_get_api_version();
	if (api_version < 3) {
		blog(LOG_ERROR, "[obstf-native-bridge] obs-websocket vendor API 3 required, found %u", api_version);
		return;
	}

	bridge_vendor = obs_websocket_register_vendor(VENDOR_NAME);
	if (!bridge_vendor) {
		blog(LOG_ERROR, "[obstf-native-bridge] could not register vendor '%s'", VENDOR_NAME);
		return;
	}

	size_t registered = 0;
	for (size_t i = 0; i < sizeof(requests) / sizeof(requests[0]); i++) {
		if (!obs_websocket_vendor_register_request(bridge_vendor, requests[i].name, vendor_request_callback,
						   (void *)&requests[i])) {
			blog(LOG_ERROR, "[obstf-native-bridge] could not register request '%s'", requests[i].name);
			for (size_t j = 0; j < registered; j++)
				obs_websocket_vendor_unregister_request(bridge_vendor, requests[j].name);
			bridge_vendor = NULL;
			return;
		}
		registered++;
	}

	blog(LOG_INFO, "[obstf-native-bridge] registered vendor '%s' with %zu contract-v%d requests", VENDOR_NAME,
	     registered, CONTRACT_VERSION);
}

void obs_module_unload(void)
{
	if (bridge_vendor) {
		for (size_t i = 0; i < sizeof(requests) / sizeof(requests[0]); i++)
			obs_websocket_vendor_unregister_request(bridge_vendor, requests[i].name);
		bridge_vendor = NULL;
	}
	blog(LOG_INFO, "[obstf-native-bridge] unloaded");
}
