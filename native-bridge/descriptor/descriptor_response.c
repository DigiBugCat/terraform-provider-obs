#include "descriptor_response.h"

static bool model_put_string(obstf_value_t *o, const char *k, const char *v) { return obstf_object_put(o,k,obstf_value_string(v ? v : "")); }
static bool model_put_bool(obstf_value_t *o, const char *k, bool v) { return obstf_object_put(o,k,obstf_value_bool(v)); }
static bool model_put_int(obstf_value_t *o, const char *k, int64_t v) { return obstf_object_put(o,k,obstf_value_int(v)); }
static bool model_take_value(obstf_value_t *o, const char *k, obstf_value_t **v) { if (!v || !*v) return false; bool ok=obstf_object_put(o,k,*v); *v=NULL; return ok; }

static obstf_value_t *build_settings_layer(const obstf_value_t *value, bool included, const char *status)
{
	obstf_value_t *layer = obstf_value_object();
	obstf_value_t *published = included ? obstf_value_clone(value) : obstf_value_object();
	if (!layer || !published) {
		obstf_value_free(layer);
		obstf_value_free(published);
		return NULL;
	}
	if (!model_put_bool(layer, "included", included) || !model_put_string(layer, "status", status)) {
		obstf_value_free(layer);
		obstf_value_free(published);
		return NULL;
	}
	bool published_added = model_take_value(layer, "value", &published);
	if (!published_added) {
		obstf_value_free(layer);
		return NULL;
	}
	return layer;
}

static obstf_value_t *build_inspection_receipt(bool inspect_properties)
{
	obstf_value_t *inspection = obstf_value_object();
	if (!inspection || !model_put_string(inspection, "inspectionMode", inspect_properties ? "inspectProperties" : "metadata") ||
	    !model_put_bool(inspection, "bridgeRequestedDynamicRefresh", false) ||
	    !model_put_bool(inspection, "actionButtonsInvoked", false) ||
	    !model_put_bool(inspection, "explicitModifiedCallbacksInvoked", false) ||
	    !model_put_bool(inspection, "settingsPermutationsApplied", false) ||
	    !model_put_bool(inspection, "settersInvoked", false) ||
	    !model_put_bool(inspection, "propertyBuilderExecuted", inspect_properties) ||
	    !model_put_bool(inspection, "defaultsBuilderExecuted", inspect_properties) ||
	    !model_put_bool(inspection, "propertiesApplySettingsCalled", inspect_properties) ||
	    !model_put_bool(inspection, "modifiedCallbacksMayHaveExecuted", inspect_properties) ||
	    !model_put_bool(inspection, "callbacksMayHaveSideEffects", inspect_properties) ||
	    !model_put_bool(inspection, "callbackExecutionObserved", false) ||
	    !model_put_string(inspection, "readOnlyGuarantee",
					inspect_properties ? "not_claimed" : "callback_free_bounded")) {
		obstf_value_free(inspection);
		return NULL;
	}
	return inspection;
}

static obstf_value_t *build_qualification_receipt(bool inspect_properties, const char *obs_version)
{
	obstf_value_t *qualification = obstf_value_object();
	if (!qualification || !model_put_string(qualification, "scope", "exact_filter_instance") ||
	    !model_put_string(qualification, "obsVersion", obs_version) ||
	    !model_put_string(qualification, "providerSchema", "not_claimed") ||
	    !model_put_string(qualification, "nativeBeforeAfterQualification", "external_evidence_required") ||
	    !model_put_string(qualification, "propertySnapshot",
					inspect_properties ? "after_native_builder_and_libobs_current_settings_application" :
							"not_collected_callback_free")) {
		obstf_value_free(qualification);
		return NULL;
	}
	return qualification;
}

static obstf_value_t *build_suppression_receipt(void)
{
	obstf_value_t *suppression = obstf_value_object();
	if (!suppression || !model_put_bool(suppression, "passwordValuesOmitted", true) ||
	    !model_put_bool(suppression, "unknownDefaultValuesOmitted", true) ||
	    !model_put_bool(suppression, "unknownCurrentValuesOmitted", true) ||
	    !model_put_bool(suppression, "fingerprintsIncluded", false) ||
	    !model_put_bool(suppression, "privateKeyInventorySuppressed", true) ||
	    !model_put_bool(suppression, "sensitivePropertyMetadataOmitted", true) ||
	    !model_put_bool(suppression, "dynamicChoiceLabelsOmitted", true)) {
		obstf_value_free(suppression);
		return NULL;
	}
	return suppression;
}

obstf_value_t *obstf_assemble_descriptor_response(bool inspect_properties, bool include_current_values, bool defaults_available, const char *obs_version, obstf_value_t *scope, obstf_value_t *provenance, obstf_value_t *instance_state, obstf_value_t *property_metadata, const obstf_value_t *published_defaults, const obstf_value_t *published_current, const obstf_value_t *published_effective)
{
	const bool effective_included = inspect_properties && include_current_values && defaults_available;
	obstf_value_t *current_layer = build_settings_layer(
		published_current, include_current_values,
		include_current_values ?
			(inspect_properties ? "collected_with_safe_redaction" : "collected_opaque_not_enumerated") :
			"not_requested");
	obstf_value_t *effective_layer = build_settings_layer(
		published_effective, effective_included,
		effective_included ? "mechanical_default_plus_current" : "defaults_not_collected");
	obstf_value_t *defaults_layer = build_settings_layer(
		published_defaults, inspect_properties && defaults_available,
		inspect_properties ? "native_builder_executed" : "not_collected_callback_free");
	obstf_value_t *inspection = build_inspection_receipt(inspect_properties);
	obstf_value_t *qualification = build_qualification_receipt(inspect_properties, obs_version);
	obstf_value_t *suppression = build_suppression_receipt();
	obstf_value_t *result = obstf_value_object();
	bool result_ok = current_layer && effective_layer && defaults_layer && inspection && qualification && suppression && result &&
			model_put_int(result, "formatVersion", 1);
	if (result_ok)
		result_ok = model_put_string(result, "inspectionMode", inspect_properties ? "inspectProperties" : "metadata");
	if (result_ok)
		result_ok = model_put_string(result, "dynamicRefresh", "none");
	if (result_ok)
		result_ok = model_put_bool(result, "bridgeRequestedDynamicRefresh", false);
	if (result_ok)
		result_ok = model_take_value(result, "scope", &scope);
	if (result_ok)
		result_ok = model_take_value(result, "provenance", &provenance);
	if (result_ok)
		result_ok = model_take_value(result, "instanceState", &instance_state);
	if (result_ok)
		result_ok = model_take_value(result, "inspection", &inspection);
	if (result_ok)
		result_ok = model_take_value(result, "qualification", &qualification);
	if (result_ok)
		result_ok = model_take_value(result, "suppression", &suppression);
	if (result_ok)
		result_ok = model_put_bool(result, "defaultsAvailable", defaults_available);
	if (result_ok)
		result_ok = model_put_string(result, "defaultsStatus",
					inspect_properties ? "native_builder_executed" : "not_collected_callback_free");
	if (result_ok)
		result_ok = model_take_value(result, "defaults", &defaults_layer);
	if (result_ok)
		result_ok = model_take_value(result, "currentSettings", &current_layer);
	if (result_ok)
		result_ok = model_take_value(result, "effectiveSettings", &effective_layer);
	if (result_ok)
		result_ok = model_put_bool(result, "currentValuesIncluded", include_current_values);
	if (result_ok)
		result_ok = model_take_value(result, "propertyMetadata", &property_metadata);
	if (!result_ok) {
		obstf_value_free(result);
		obstf_value_free(current_layer);
		obstf_value_free(effective_layer);
		obstf_value_free(scope);
		obstf_value_free(provenance);
		obstf_value_free(instance_state);
		obstf_value_free(defaults_layer);
		obstf_value_free(inspection);
		obstf_value_free(qualification);
		obstf_value_free(suppression);
		obstf_value_free(property_metadata);
		return NULL;
	}
	return result;
}
