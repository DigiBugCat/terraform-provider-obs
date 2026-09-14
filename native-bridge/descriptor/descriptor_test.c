#include "descriptor_response.h"
#include "descriptor_model.h"
#include "descriptor_identity.h"

#include <assert.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void require_put(obstf_value_t *object, const char *key, obstf_value_t *value)
{
	assert(obstf_object_put(object, key, value));
}

static void test_typed_nested_json(void)
{
	obstf_value_t *root = obstf_value_object();
	obstf_value_t *nested = obstf_value_object();
	obstf_value_t *items = obstf_value_array();
	assert(root && nested && items);
	require_put(root, "contractVersion", obstf_value_int(1));
	require_put(root, "enabled", obstf_value_bool(true));
	require_put(nested, "text", obstf_value_string("line 1\n\"line 2\""));
	require_put(nested, "ratio", obstf_value_double(0.125));
	assert(obstf_array_push(items, obstf_value_null()));
	assert(obstf_array_push(items, obstf_value_int(-7)));
	require_put(nested, "items", items);
	require_put(root, "nested", nested);

	char *json = obstf_value_json(root);
	assert(json);
	assert(strcmp(json,
		       "{\"contractVersion\":1,\"enabled\":true,\"nested\":{\"text\":\"line 1\\n\\\"line 2\\\"\",\"ratio\":0.125,\"items\":[null,-7]}}") ==
	       0);
	free(json);
	obstf_value_free(root);
}

static void test_redaction_shape(void)
{
	obstf_value_t *secret = obstf_value_string("super-secret");
	obstf_value_t *redacted = obstf_redacted_descriptor(secret, "password");
	assert(secret && redacted);
	char *json = obstf_value_json(redacted);
	assert(json);
	assert(strstr(json, "super-secret") == NULL);
	assert(strstr(json, "\"$redacted\":true") != NULL);
	assert(strstr(json, "\"reason\":\"password\"") != NULL);
	assert(strstr(json, "valueType") == NULL);
	assert(strstr(json, "present") == NULL);
	assert(strstr(json, "serializedLength") == NULL);
	assert(strstr(json, "sha256") == NULL);
	free(json);
	obstf_value_free(redacted);
	obstf_value_free(secret);
}

static obstf_value_t *make_nested_object(bool reverse)
{
	obstf_value_t *outer = obstf_value_object();
	obstf_value_t *inner = obstf_value_object();
	assert(outer && inner);
	if (reverse) {
		require_put(inner, "b", obstf_value_int(2));
		require_put(inner, "a", obstf_value_int(1));
		require_put(outer, "z", inner);
		require_put(outer, "a", obstf_value_string("same"));
	} else {
		require_put(outer, "a", obstf_value_string("same"));
		require_put(inner, "a", obstf_value_int(1));
		require_put(inner, "b", obstf_value_int(2));
		require_put(outer, "z", inner);
	}
	return outer;
}

static void test_canonical_hash_sorts_nested_objects(void)
{
	obstf_value_t *first = make_nested_object(false);
	obstf_value_t *second = make_nested_object(true);
	char *first_json = obstf_value_json(first);
	char *second_json = obstf_value_json(second);
	char *first_hash = obstf_value_sha256(first);
	char *second_hash = obstf_value_sha256(second);
	assert(first_json && second_json && first_hash && second_hash);
	assert(strcmp(first_json, second_json) != 0);
	assert(strcmp(first_hash, second_hash) == 0);
	assert(strlen(first_hash) == 64);
	free(first_json);
	free(second_json);
	free(first_hash);
	free(second_hash);
	obstf_value_free(first);
	obstf_value_free(second);
}

static void test_effective_overlay_keeps_layers_distinct(void)
{
	obstf_value_t *defaults = obstf_value_object();
	obstf_value_t *current = obstf_value_object();
	assert(defaults && current);
	require_put(defaults, "opacity", obstf_value_double(1.0));
	require_put(defaults, "mode", obstf_value_string("default"));
	require_put(current, "opacity", obstf_value_double(0.0));
	obstf_value_t *effective = obstf_value_clone(defaults);
	assert(effective);
	for (size_t index = 0; index < obstf_object_count(current); index++) {
		obstf_value_t *copy = obstf_value_clone(obstf_object_value(current, index));
		assert(copy);
		assert(obstf_object_put(effective, obstf_object_key(current, index), copy));
	}
	char *defaults_json = obstf_value_json(defaults);
	char *current_json = obstf_value_json(current);
	char *effective_json = obstf_value_json(effective);
	assert(defaults_json && current_json && effective_json);
	assert(strcmp(defaults_json, "{\"opacity\":1.0,\"mode\":\"default\"}") == 0);
	assert(strcmp(current_json, "{\"opacity\":0.0}") == 0);
	assert(strcmp(effective_json, "{\"opacity\":0.0,\"mode\":\"default\"}") == 0);
	free(defaults_json);
	free(current_json);
	free(effective_json);
	obstf_value_free(defaults);
	obstf_value_free(current);
	obstf_value_free(effective);
}

static void test_unknown_value_is_not_silently_flattened(void)
{
	obstf_value_t *opaque = obstf_value_object();
	assert(opaque);
	require_put(opaque, "private", obstf_value_string("payload"));
	obstf_value_t *descriptor = obstf_redacted_descriptor(opaque, "opaque");
	assert(descriptor);
	const obstf_value_t *type = obstf_object_get(descriptor, "valueType");
	const obstf_value_t *reason = obstf_object_get(descriptor, "reason");
	assert(!type && reason);
	char *reason_json = obstf_value_json(reason);
	assert(reason_json);
	assert(strcmp(reason_json, "\"opaque\"") == 0);
	free(reason_json);
	obstf_value_free(descriptor);
	obstf_value_free(opaque);
}

static void test_uuid_first_identity_statuses(void)
{
	assert(obstf_filter_match_status(1, 0, 0, false) == OBSTF_DESCRIPTOR_FILTER_IDENTITY_MISMATCH);
	assert(obstf_filter_match_status(0, 2, 0, false) == OBSTF_DESCRIPTOR_FILTER_IDENTITY_AMBIGUOUS);
	assert(obstf_filter_match_status(2, 2, 2, true) == OBSTF_DESCRIPTOR_FILTER_IDENTITY_AMBIGUOUS);
	assert(obstf_filter_match_status(1, 1, 1, true) == OBSTF_DESCRIPTOR_OK);
	assert(obstf_filter_match_status(1, 1, 1, false) == OBSTF_DESCRIPTOR_NATIVE_FAILURE);
}

/* Exercises the production assembler used by descriptor.c, not a hand-built response. */
static void expect_json(const obstf_value_t *value, const char *expected)
{
	char *json = obstf_value_json(value);
	assert(json && strcmp(json, expected) == 0);
	free(json);
}

static void test_production_response_assembler(void)
{
	for (int inspect = 0; inspect < 2; inspect++) {
		for (int current = 0; current < 2; current++) {
			for (int available = 0; available < 2; available++) {
				obstf_value_t *values = obstf_value_object();
				require_put(values, "safe_numeric", obstf_value_int(42));
				obstf_value_t *response = obstf_assemble_descriptor_response(
					inspect, current, available, "32.2.2-test",
					obstf_value_object(), obstf_value_object(), obstf_value_object(),
					obstf_value_object(), values, values, values);
				assert(response);
				const char *objects[] = {"scope", "provenance", "instanceState", "inspection", "qualification", "suppression", "defaults", "currentSettings", "effectiveSettings", "propertyMetadata"};
				for (size_t i=0; i<sizeof(objects)/sizeof(objects[0]); i++)
					assert(obstf_value_type(obstf_object_get(response, objects[i])) == OBSTF_VALUE_OBJECT);
				assert(!obstf_object_get(response, "currentKeysWithoutProperty"));
				assert(!obstf_object_get(response, "propertiesWithoutCurrentKey"));
				assert(!obstf_object_get(response, "dynamicRefreshApplied"));
				expect_json(obstf_object_get(response, "formatVersion"), "1");
				const obstf_value_t *receipt = obstf_object_get(response, "inspection");
				const char *false_fields[] = {"bridgeRequestedDynamicRefresh", "actionButtonsInvoked", "explicitModifiedCallbacksInvoked", "settingsPermutationsApplied", "settersInvoked", "callbackExecutionObserved"};
				for (size_t i=0; i<sizeof(false_fields)/sizeof(false_fields[0]); i++)
					expect_json(obstf_object_get(receipt, false_fields[i]), "false");
				const char *mode_fields[] = {"propertyBuilderExecuted", "defaultsBuilderExecuted", "propertiesApplySettingsCalled", "modifiedCallbacksMayHaveExecuted", "callbacksMayHaveSideEffects"};
				for (size_t i=0; i<sizeof(mode_fields)/sizeof(mode_fields[0]); i++)
					expect_json(obstf_object_get(receipt, mode_fields[i]), inspect ? "true" : "false");
				expect_json(obstf_object_get(receipt, "readOnlyGuarantee"), inspect ? "\"not_claimed\"" : "\"callback_free_bounded\"");
				const obstf_value_t *qualification = obstf_object_get(response, "qualification");
				expect_json(obstf_object_get(qualification, "obsVersion"), "\"32.2.2-test\"");
				expect_json(obstf_object_get(qualification, "providerSchema"), "\"not_claimed\"");
				expect_json(obstf_object_get(qualification, "nativeBeforeAfterQualification"), "\"external_evidence_required\"");
				const char *layers[] = {"defaults", "currentSettings", "effectiveSettings"};
				const bool included[] = {inspect && available, current, inspect && current && available};
				for (size_t i=0; i<3; i++) {
					const obstf_value_t *layer = obstf_object_get(response, layers[i]);
					expect_json(obstf_object_get(layer, "included"), included[i] ? "true" : "false");
					expect_json(obstf_object_get(layer, "value"), included[i] ? "{\"safe_numeric\":42}" : "{}");
				}
				expect_json(obstf_object_get(obstf_object_get(response,"suppression"), "fingerprintsIncluded"), "false");
				expect_json(obstf_object_get(obstf_object_get(response,"suppression"), "privateKeyInventorySuppressed"), "true");
				obstf_value_free(response);
				obstf_value_free(values);
			}
		}
	}
}

int main(void)
{
	test_typed_nested_json();
	test_redaction_shape();
	test_canonical_hash_sorts_nested_objects();
	test_effective_overlay_keeps_layers_distinct();
	test_unknown_value_is_not_silently_flattened();
	test_uuid_first_identity_statuses();
	test_production_response_assembler();
	puts("descriptor pure-model tests: PASS");
	return 0;
}
