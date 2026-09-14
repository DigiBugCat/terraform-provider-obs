#include "../src/plugin.c"

static int failures;

static void expect(bool condition, const char *name)
{
	if (!condition) {
		fprintf(stderr, "FAIL: %s\n", name);
		failures++;
	}
}

static bool parse_placement_json(const char *json)
{
	obs_data_t *root = obs_data_create_from_json(json);
	obs_data_t *response = obs_data_create();
	obs_data_t *placement_data = obs_data_get_obj(root, "placement");
	struct placement placement;
	const bool result = parse_placement(placement_data, &placement, response);
	if (placement_data)
		obs_data_release(placement_data);
	obs_data_release(response);
	obs_data_release(root);
	return result;
}

static const char *valid_json =
	"{\"placement\":{"
	"\"sceneItemEnabled\":true,\"sceneItemLocked\":false,\"sceneItemIndex\":0,"
	"\"sceneItemTransform\":{"
	"\"positionX\":0.0,\"positionY\":0.0,\"rotation\":0.0,"
	"\"scaleX\":1.0,\"scaleY\":1.0,\"alignment\":5,"
	"\"boundsType\":\"OBS_BOUNDS_NONE\",\"boundsAlignment\":0,"
	"\"boundsWidth\":0.0,\"boundsHeight\":0.0,\"cropToBounds\":false,"
	"\"cropLeft\":0,\"cropTop\":0,\"cropRight\":0,\"cropBottom\":0}}}";

static void expect_rejected_replacement(const char *needle, const char *replacement, const char *name)
{
	char json[2048];
	const char *location = strstr(valid_json, needle);
	expect(location != NULL, "test fixture contains replacement needle");
	if (!location)
		return;
	const size_t prefix = (size_t)(location - valid_json);
	snprintf(json, sizeof(json), "%.*s%s%s", (int)prefix, valid_json, replacement, location + strlen(needle));
	expect(!parse_placement_json(json), name);
}

int main(void)
{
	expect(parse_placement_json(valid_json), "valid placement accepted");
	expect_rejected_replacement("\"sceneItemLocked\":false", "\"sceneItemLocked\":\"false\"",
				    "string boolean rejected");
	expect_rejected_replacement("\"sceneItemIndex\":0", "\"sceneItemIndex\":0.5",
				    "fractional index rejected");
	expect_rejected_replacement("\"positionX\":0.0", "\"positionX\":\"0\"",
				    "string number rejected");
	expect_rejected_replacement("\"positionX\":0.0", "\"positionX\":1e300",
				    "double to float overflow rejected");
	expect_rejected_replacement("\"alignment\":5", "\"alignment\":-1", "negative alignment rejected");
	expect_rejected_replacement("\"cropLeft\":0", "\"cropLeft\":1.5", "fractional crop rejected");

	struct placement requested_placement = {0};
	struct placement observed_placement = requested_placement;
	expect(placement_equal(&requested_placement, &observed_placement) &&
		       obstf_absolute_postcondition_verified(OBSTF_ABSOLUTE_RUNTIME_SUPPORTED, true),
	       "supported absolute runtime and raw-exact placement verifies");
	observed_placement.transform.pos.x = nextafterf(requested_placement.transform.pos.x, INFINITY);
	expect(!placement_equal(&requested_placement, &observed_placement) &&
		       !obstf_absolute_postcondition_verified(OBSTF_ABSOLUTE_RUNTIME_SUPPORTED, false),
	       "adjacent placement float fails raw-exact verification");
	expect(!obstf_absolute_postcondition_verified(OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE, true),
	       "raw-equal placement in relative mode cannot verify");

	expect(strcmp(absolute_runtime_status_name(OBSTF_ABSOLUTE_RUNTIME_SUPPORTED), "supported") == 0 &&
		       strcmp(absolute_runtime_error_code(OBSTF_ABSOLUTE_RUNTIME_SUPPORTED), "ok") == 0,
	       "supported runtime has stable status and code");
	expect(strcmp(absolute_runtime_error_code(OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION),
		      "absolute_runtime_wrong_obs_version") == 0,
	       "wrong OBS version has precise code");
	expect(strcmp(absolute_runtime_error_code(OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN),
		      "absolute_runtime_coordinate_mode_unknown") == 0,
	       "unknown coordinate mode has precise code");
	expect(strcmp(absolute_runtime_error_code(OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE),
		      "absolute_runtime_relative_mode_unsupported") == 0,
	       "relative mode has precise code");
	expect(strcmp(absolute_runtime_error_code(OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN),
		      "absolute_runtime_main_canvas_unknown") == 0,
	       "unknown main canvas has precise code");
	expect(strcmp(absolute_runtime_error_code(OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS),
		      "absolute_runtime_wrong_main_canvas") == 0,
	       "wrong main canvas has precise code");
	struct absolute_runtime_observation refused_runtime = {
		.obs_version = "32.2.2",
		.coordinate_mode_known = true,
		.absolute_coordinates = false,
		.canvas_known = true,
		.base_width = 1920,
		.base_height = 1080,
		.status = OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE,
	};
	obs_data_t *guard_response = obs_data_create();
	expect(!apply_absolute_runtime_guard(&refused_runtime, guard_response),
	       "relative runtime is refused by the shared guard");
	obs_data_t *guard_receipt = obs_data_get_obj(guard_response, "runtimeGuard");
	expect(strcmp(obs_data_get_string(guard_receipt, "status"), "relative_mode_unsupported") == 0 &&
		       !obs_data_get_bool(guard_receipt, "supported") &&
		       strcmp(obs_data_get_string(guard_receipt, "comparisonMode"), "ineligible") == 0 &&
		       obs_data_get_int(guard_receipt, "baseWidth") == 1920 &&
		       obs_data_get_int(guard_receipt, "baseHeight") == 1080,
	       "no-placement guard receipt preserves the precise refusal context");
	expect(strcmp(obs_data_get_string(guard_response, "outcome"), "failed") == 0 &&
		       !obs_data_get_bool(guard_response, "mutationAttempted") &&
		       obs_data_get_bool(guard_response, "postconditionVerified"),
	       "guard refusal is a verified no-write failure");
	obs_data_release(guard_response);
	obs_data_release(guard_receipt);
	const enum obstf_absolute_runtime_status refused_statuses[] = {
		OBSTF_ABSOLUTE_RUNTIME_WRONG_OBS_VERSION,
		OBSTF_ABSOLUTE_RUNTIME_MODE_UNKNOWN,
		OBSTF_ABSOLUTE_RUNTIME_RELATIVE_MODE,
		OBSTF_ABSOLUTE_RUNTIME_CANVAS_UNKNOWN,
		OBSTF_ABSOLUTE_RUNTIME_WRONG_CANVAS,
	};
	for (size_t i = 0; i < sizeof(refused_statuses) / sizeof(refused_statuses[0]); i++) {
		refused_runtime.status = refused_statuses[i];
		guard_response = obs_data_create();
		expect(!apply_absolute_runtime_guard(&refused_runtime, guard_response),
		       "every unsupported runtime status is refused");
		guard_receipt = obs_data_get_obj(guard_response, "runtimeGuard");
		expect(strcmp(obs_data_get_string(guard_response, "code"),
			      absolute_runtime_error_code(refused_statuses[i])) == 0 &&
			       strcmp(obs_data_get_string(guard_receipt, "status"),
				      absolute_runtime_status_name(refused_statuses[i])) == 0 &&
			       !obs_data_get_bool(guard_response, "mutationAttempted"),
		       "every guard refusal carries its precise status/code and no-write flag");
		obs_data_release(guard_receipt);
		obs_data_release(guard_response);
	}

	const size_t request_count = sizeof(requests) / sizeof(requests[0]);
	size_t guarded_mutation_count = 0;
	expect(request_count == 11, "ten graph requests plus DescribeFilter remain registered");
	for (size_t i = 0; i < request_count; i++) {
		const bool read_only = strcmp(requests[i].name, "GetCapabilities") == 0 ||
				       strcmp(requests[i].name, "GetGroup") == 0 ||
				       strcmp(requests[i].name, "DescribeFilter") == 0;
		expect(requests[i].mutation == !read_only, "registration mutation classification is exact");
		expect(requests[i].requires_absolute_runtime == !read_only,
		       "every mutation/save registration uses the shared absolute guard");
		if (requests[i].requires_absolute_runtime)
			guarded_mutation_count++;
	}
	expect(guarded_mutation_count == 8,
	       "seven structural mutations plus SaveSceneCollectionCheckpoint are guarded");

	obs_data_t *request = obs_data_create_from_json("{\"contractVersion\":\"1\"}");
	obs_data_t *response = obs_data_create();
	expect(!require_contract(request, response, false), "string contract version rejected");
	obs_data_release(response);
	obs_data_release(request);

	request = obs_data_create_from_json("{\"contractVersion\":1,\"clientOperationId\":7}");
	response = obs_data_create();
	expect(!require_contract(request, response, true), "non-string operation id rejected");
	obs_data_release(response);
	obs_data_release(request);

	request = obs_data_create_from_json("{\"sceneItemId\":2.25}");
	response = obs_data_create();
	int64_t item_id = -1;
	expect(!require_integer(request, "sceneItemId", 0, INT64_MAX, &item_id, response),
	       "fractional scene item id rejected");
	obs_data_release(response);
	obs_data_release(request);

	obs_data_t *declared = obs_data_create_from_json("{\"file\":\"light.png\",\"unload\":false}");
	obs_data_t *actual = obs_data_create_from_json(
		"{\"file\":\"light.png\",\"unload\":false,\"plugin_private\":\"preserved\"}");
	expect(declared_object_matches(declared, actual), "declared settings match with unknown actual key preserved");
	obs_data_release(actual);
	obs_data_release(declared);

	declared = obs_data_create_from_json("{\"opacity\":1.0}");
	actual = obs_data_create();
	obs_data_set_default_double(actual, "opacity", 1.0);
	expect(declared_object_matches(declared, actual), "declared settings compare against effective default");
	obs_data_set_double(actual, "opacity", 0.5);
	expect(!declared_object_matches(declared, actual), "declared settings mismatch is detected");
	obs_data_release(actual);
	obs_data_release(declared);

	const long allocations_before_nested_mismatches = bnum_allocs();
	for (int i = 0; i < 1000; i++) {
		declared = obs_data_create_from_json("{\"nested\":{\"value\":1},\"items\":[{\"value\":1}]}");
		actual = obs_data_create_from_json("{\"nested\":{\"value\":2},\"items\":[{\"value\":2}]}");
		expect(!declared_object_matches(declared, actual), "nested settings mismatch is detected");
		obs_data_release(actual);
		obs_data_release(declared);

		declared = obs_data_create_from_json("{\"nested\":{\"value\":1},\"items\":[{\"value\":1}]}");
		actual = obs_data_create_from_json("{\"nested\":{\"value\":1},\"items\":[{\"value\":2}]}");
		expect(!declared_object_matches(declared, actual), "nested array mismatch is detected");
		obs_data_release(actual);
		obs_data_release(declared);
	}
	const long allocations_after_nested_mismatches = bnum_allocs();
	expect(allocations_before_nested_mismatches == allocations_after_nested_mismatches,
	       "nested mismatch stress leaves libobs allocation count balanced");

	if (failures) {
		fprintf(stderr, "%d validation test(s) failed\n", failures);
		return 1;
	}
	puts("strict request validation tests: PASS");
	return 0;
}
