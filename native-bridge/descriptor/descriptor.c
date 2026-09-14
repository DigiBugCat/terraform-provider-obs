#include "descriptor.h"
#include "descriptor_identity.h"
#include "descriptor_model.h"
#include "descriptor_response.h"

#include <obs-module.h>
#include <obs-properties.h>
#include <obs.h>

#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define DESCRIPTOR_FORMAT_VERSION 1
#define DESCRIPTOR_CONTRACT_VERSION 1

struct string_list {
	char **items;
	size_t count;
	size_t capacity;
};

struct parsed_request {
	char *source_uuid;
	char *source_name;
	char *filter_name;
	char *filter_uuid;
	bool include_current_values;
	bool inspect_properties;
};

struct filter_match {
	const char *requested_uuid;
	const char *requested_name;
	obs_source_t *match;
	size_t uuid_count;
	size_t name_count;
	size_t exact_count;
};

struct property_context {
	const obstf_value_t *defaults;
	const obstf_value_t *current;
	struct string_list property_names;
	struct string_list ui_only_names;
	struct string_list password_names;
	struct string_list sensitive_names;
};

static void set_error(char *buffer, size_t buffer_size, const char *format, ...)
{
	if (!buffer || buffer_size == 0)
		return;

	va_list args;
	va_start(args, format);
	(void)vsnprintf(buffer, buffer_size, format, args);
	va_end(args);
	buffer[buffer_size - 1] = '\0';
}

static void clear_error(char *buffer, size_t buffer_size)
{
	if (buffer && buffer_size)
		buffer[0] = '\0';
}

static char *duplicate_string(const char *value)
{
	const char *source = value ? value : "";
	size_t length = strlen(source);
	if (length == SIZE_MAX)
		return NULL;
	char *copy = malloc(length + 1);
	if (!copy)
		return NULL;
	memcpy(copy, source, length + 1);
	return copy;
}

static const char *safe_string(const char *value)
{
	return value ? value : "";
}

static bool string_list_grow(struct string_list *list, size_t required)
{
	if (required <= list->capacity)
		return true;
	size_t next = list->capacity ? list->capacity : 8;
	while (next < required) {
		if (next > SIZE_MAX / 2) {
			next = required;
			break;
		}
		next *= 2;
	}
	if (next > SIZE_MAX / sizeof(*list->items))
		return false;
	char **grown = realloc(list->items, next * sizeof(*list->items));
	if (!grown)
		return false;
	list->items = grown;
	list->capacity = next;
	return true;
}

static bool string_list_contains(const struct string_list *list, const char *value)
{
	if (!list || !value)
		return false;
	for (size_t index = 0; index < list->count; index++) {
		if (strcmp(list->items[index], value) == 0)
			return true;
	}
	return false;
}

static bool string_list_add_unique(struct string_list *list, const char *value)
{
	if (!list || !value || !*value)
		return false;
	if (string_list_contains(list, value))
		return true;
	if (list->count == SIZE_MAX || !string_list_grow(list, list->count + 1))
		return false;
	list->items[list->count] = duplicate_string(value);
	if (!list->items[list->count])
		return false;
	list->count++;
	return true;
}

static void string_list_free(struct string_list *list)
{
	if (!list)
		return;
	for (size_t index = 0; index < list->count; index++)
		free(list->items[index]);
	free(list->items);
	memset(list, 0, sizeof(*list));
}

static bool model_put_string(obstf_value_t *object, const char *key, const char *value)
{
	return obstf_object_put(object, key, obstf_value_string(value ? value : ""));
}

static bool model_put_nullable_string(obstf_value_t *object, const char *key, const char *value)
{
	obstf_value_t *item = value ? obstf_value_string(value) : obstf_value_null();
	return obstf_object_put(object, key, item);
}

static bool model_put_bool(obstf_value_t *object, const char *key, bool value)
{
	return obstf_object_put(object, key, obstf_value_bool(value));
}

static bool model_put_int(obstf_value_t *object, const char *key, int64_t value)
{
	return obstf_object_put(object, key, obstf_value_int(value));
}

static bool model_put_double(obstf_value_t *object, const char *key, double value)
{
	return obstf_object_put(object, key, obstf_value_double(value));
}

static bool model_put_value(obstf_value_t *object, const char *key, obstf_value_t *value)
{
	return obstf_object_put(object, key, value);
}

static bool model_put_redacted_string(obstf_value_t *object, const char *key, const char *value, const char *reason)
{
	if (!value)
		return obstf_object_put(object, key, obstf_value_null());
	obstf_value_t *raw = obstf_value_string(value ? value : "");
	obstf_value_t *redacted = raw ? obstf_redacted_descriptor(raw, reason) : NULL;
	obstf_value_free(raw);
	return obstf_object_put(object, key, redacted);
}

static bool model_take_value(obstf_value_t *object, const char *key, obstf_value_t **value)
{
	if (!value || !*value)
		return false;
	bool added = model_put_value(object, key, *value);
	*value = NULL; /* object_put owns the value on both success and failure */
	return added;
}

static bool data_item_is_type(obs_data_t *data, const char *key, enum obs_data_type type)
{
	if (!data || !key || !obs_data_has_user_value(data, key))
		return false;
	obs_data_item_t *item = obs_data_item_byname(data, key);
	if (!item)
		return false;
	bool matches = obs_data_item_gettype(item) == type;
	obs_data_item_release(&item);
	return matches;
}

static bool required_string_field(obs_data_t *data, const char *key, char **value, char *error, size_t error_size)
{
	if (!data_item_is_type(data, key, OBS_DATA_STRING)) {
		set_error(error, error_size, "%s must be a string", key);
		return false;
	}
	const char *raw = obs_data_get_string(data, key);
	if (!raw || !*raw) {
		set_error(error, error_size, "%s must be non-empty", key);
		return false;
	}
	*value = duplicate_string(raw);
	if (!*value) {
		set_error(error, error_size, "out of memory copying %s", key);
		return false;
	}
	return true;
}

static bool optional_string_field(obs_data_t *data, const char *key, char **value, char *error, size_t error_size)
{
	if (!obs_data_has_user_value(data, key))
		return true;
	return required_string_field(data, key, value, error, error_size);
}

static bool optional_bool_field(obs_data_t *data, const char *key, bool *value, char *error, size_t error_size)
{
	if (!obs_data_has_user_value(data, key))
		return true;
	if (!data_item_is_type(data, key, OBS_DATA_BOOLEAN)) {
		set_error(error, error_size, "%s must be a boolean", key);
		return false;
	}
	*value = obs_data_get_bool(data, key);
	return true;
}

static bool required_integer_field(obs_data_t *data, const char *key, int64_t *value, char *error, size_t error_size)
{
	if (!data_item_is_type(data, key, OBS_DATA_NUMBER)) {
		set_error(error, error_size, "%s must be an integer", key);
		return false;
	}
	obs_data_item_t *item = obs_data_item_byname(data, key);
	if (!item || obs_data_item_numtype(item) != OBS_DATA_NUM_INT) {
		if (item)
			obs_data_item_release(&item);
		set_error(error, error_size, "%s must be an integer", key);
		return false;
	}
	*value = obs_data_item_get_int(item);
	obs_data_item_release(&item);
	return true;
}

static bool parse_request(obs_data_t *request, struct parsed_request *parsed, char *error, size_t error_size)
{
	memset(parsed, 0, sizeof(*parsed));
	if (!request) {
		set_error(error, error_size, "request is required");
		return false;
	}

	int64_t contract_version;
	if (!required_integer_field(request, "contractVersion", &contract_version, error, error_size))
		return false;
	if (contract_version != DESCRIPTOR_CONTRACT_VERSION) {
		set_error(error, error_size, "contractVersion must be %d", DESCRIPTOR_CONTRACT_VERSION);
		return false;
	}

	if (!data_item_is_type(request, "source", OBS_DATA_OBJECT) || !data_item_is_type(request, "filter", OBS_DATA_OBJECT)) {
		set_error(error, error_size, "source and filter objects are required");
		return false;
	}
	obs_data_t *source = obs_data_get_obj(request, "source");
	obs_data_t *filter = obs_data_get_obj(request, "filter");
	if (!source || !filter) {
		if (source)
			obs_data_release(source);
		if (filter)
			obs_data_release(filter);
		set_error(error, error_size, "source and filter objects must be readable");
		return false;
	}
	bool valid = required_string_field(source, "sourceUuid", &parsed->source_uuid, error, error_size) &&
		     optional_string_field(source, "sourceName", &parsed->source_name, error, error_size) &&
		     required_string_field(filter, "filterName", &parsed->filter_name, error, error_size) &&
		     required_string_field(filter, "filterUuid", &parsed->filter_uuid, error, error_size);
	obs_data_release(source);
	obs_data_release(filter);
	if (!valid)
		return false;

	parsed->include_current_values = false;
	if (!optional_bool_field(request, "includeCurrentValues", &parsed->include_current_values, error, error_size))
		return false;

	parsed->inspect_properties = false;
	if (obs_data_has_user_value(request, "inspectionMode")) {
		if (!data_item_is_type(request, "inspectionMode", OBS_DATA_STRING)) {
			set_error(error, error_size, "inspectionMode must be metadata or inspectProperties");
			return false;
		}
		const char *inspection_mode = safe_string(obs_data_get_string(request, "inspectionMode"));
		if (strcmp(inspection_mode, "metadata") == 0) {
			parsed->inspect_properties = false;
		} else if (strcmp(inspection_mode, "inspectProperties") == 0) {
			parsed->inspect_properties = true;
		} else {
			set_error(error, error_size, "inspectionMode must be metadata or inspectProperties");
			return false;
		}
	}

	if (obs_data_has_user_value(request, "dynamicRefresh")) {
		if (!data_item_is_type(request, "dynamicRefresh", OBS_DATA_STRING) ||
		    strcmp(safe_string(obs_data_get_string(request, "dynamicRefresh")), "none") != 0) {
			set_error(error, error_size, "dynamicRefresh only supports none");
			return false;
		}
	}
	return true;
}

static void parsed_request_free(struct parsed_request *parsed)
{
	if (!parsed)
		return;
	free(parsed->source_uuid);
	free(parsed->source_name);
	free(parsed->filter_name);
	free(parsed->filter_uuid);
	memset(parsed, 0, sizeof(*parsed));
}

static obstf_value_t *value_from_data(obs_data_t *data, bool only_user_values);

static obstf_value_t *value_from_item(obs_data_item_t *item, bool only_user_values)
{
	if (!item)
		return NULL;

	switch (obs_data_item_gettype(item)) {
	case OBS_DATA_NULL:
		return obstf_value_null();
	case OBS_DATA_STRING:
		return obstf_value_string(obs_data_item_get_string(item));
	case OBS_DATA_BOOLEAN:
		return obstf_value_bool(obs_data_item_get_bool(item));
	case OBS_DATA_NUMBER:
		if (obs_data_item_numtype(item) == OBS_DATA_NUM_INT)
			return obstf_value_int((int64_t)obs_data_item_get_int(item));
		return obstf_value_double(obs_data_item_get_double(item));
	case OBS_DATA_OBJECT: {
		obs_data_t *child = obs_data_item_get_obj(item);
		if (!child)
			return obstf_value_null();
		obstf_value_t *result = value_from_data(child, only_user_values);
		obs_data_release(child);
		return result;
	}
	case OBS_DATA_ARRAY: {
		obs_data_array_t *array = obs_data_item_get_array(item);
		if (!array)
			return obstf_value_null();
		obstf_value_t *result = obstf_value_array();
		if (result) {
			for (size_t index = 0; index < obs_data_array_count(array); index++) {
				obs_data_t *child = obs_data_array_item(array, index);
				obstf_value_t *child_value = child ? value_from_data(child, only_user_values) : NULL;
				if (child)
					obs_data_release(child);
				if (!child_value || !obstf_array_push(result, child_value)) {
					if (!child_value)
						obstf_value_free(child_value);
					obstf_value_free(result);
					result = NULL;
					break;
				}
			}
		}
		obs_data_array_release(array);
		return result;
	}
	}
	return NULL;
}

static obstf_value_t *value_from_data(obs_data_t *data, bool only_user_values)
{
	obstf_value_t *result = obstf_value_object();
	if (!result)
		return NULL;
	if (!data)
		return result;

	for (obs_data_item_t *item = obs_data_first(data); item; obs_data_item_next(&item)) {
		if (only_user_values && !obs_data_item_has_user_value(item))
			continue;
		const char *name = obs_data_item_get_name(item);
		obstf_value_t *value = value_from_item(item, only_user_values);
		if (!name || !value || !obstf_object_put(result, name, value)) {
			if (!value)
				obstf_value_free(value);
			obs_data_item_release(&item);
			obstf_value_free(result);
			return NULL;
		}
	}
	return result;
}

static obstf_value_t *defaults_model(obs_source_t *filter, bool *available)
{
	*available = false;
	const char *kind = obs_source_get_id(filter);
	obs_data_t *raw = kind ? obs_get_source_defaults(kind) : NULL;
	if (!raw)
		return obstf_value_object();
	obs_data_t *with_defaults = obs_data_get_defaults(raw);
	obstf_value_t *result = value_from_data(with_defaults ? with_defaults : raw, false);
	if (with_defaults)
		obs_data_release(with_defaults);
	obs_data_release(raw);
	if (result)
		*available = true;
	return result;
}

static obstf_value_t *current_model(obs_source_t *filter)
{
	obs_data_t *raw = obs_source_get_settings(filter);
	if (!raw)
		return obstf_value_object();
	obstf_value_t *result = value_from_data(raw, true);
	obs_data_release(raw);
	return result;
}

static obstf_value_t *current_probe_model(obs_source_t *filter)
{
	obs_data_t *raw = obs_source_get_settings(filter);
	if (raw)
		obs_data_release(raw);
	return obstf_value_object();
}

static const char *property_type_name(enum obs_property_type type)
{
	switch (type) {
	case OBS_PROPERTY_BOOL:
		return "bool";
	case OBS_PROPERTY_INT:
		return "int";
	case OBS_PROPERTY_FLOAT:
		return "float";
	case OBS_PROPERTY_TEXT:
		return "text";
	case OBS_PROPERTY_PATH:
		return "path";
	case OBS_PROPERTY_LIST:
		return "list";
	case OBS_PROPERTY_COLOR:
		return "color";
	case OBS_PROPERTY_BUTTON:
		return "button";
	case OBS_PROPERTY_FONT:
		return "font";
	case OBS_PROPERTY_EDITABLE_LIST:
		return "editableList";
	case OBS_PROPERTY_FRAME_RATE:
		return "frameRate";
	case OBS_PROPERTY_GROUP:
		return "group";
	case OBS_PROPERTY_COLOR_ALPHA:
		return "colorAlpha";
	case OBS_PROPERTY_INVALID:
		return "invalid";
	}
	return "unknown";
}

static const char *number_control_name(enum obs_number_type type)
{
	return type == OBS_NUMBER_SLIDER ? "slider" : "scroller";
}

static const char *text_type_name(enum obs_text_type type)
{
	switch (type) {
	case OBS_TEXT_DEFAULT:
		return "default";
	case OBS_TEXT_PASSWORD:
		return "password";
	case OBS_TEXT_MULTILINE:
		return "multiline";
	case OBS_TEXT_INFO:
		return "info";
	}
	return "unknown";
}

static const char *text_info_type_name(enum obs_text_info_type type)
{
	switch (type) {
	case OBS_TEXT_INFO_NORMAL:
		return "normal";
	case OBS_TEXT_INFO_WARNING:
		return "warning";
	case OBS_TEXT_INFO_ERROR:
		return "error";
	}
	return "unknown";
}

static const char *path_type_name(enum obs_path_type type)
{
	switch (type) {
	case OBS_PATH_FILE:
		return "file";
	case OBS_PATH_FILE_SAVE:
		return "fileSave";
	case OBS_PATH_DIRECTORY:
		return "directory";
	}
	return "unknown";
}

static const char *combo_type_name(enum obs_combo_type type)
{
	switch (type) {
	case OBS_COMBO_TYPE_EDITABLE:
		return "editable";
	case OBS_COMBO_TYPE_LIST:
		return "list";
	case OBS_COMBO_TYPE_RADIO:
		return "radio";
	case OBS_COMBO_TYPE_INVALID:
		return "invalid";
	}
	return "unknown";
}

static const char *combo_format_name(enum obs_combo_format type)
{
	switch (type) {
	case OBS_COMBO_FORMAT_INT:
		return "int";
	case OBS_COMBO_FORMAT_FLOAT:
		return "float";
	case OBS_COMBO_FORMAT_STRING:
		return "string";
	case OBS_COMBO_FORMAT_BOOL:
		return "bool";
	case OBS_COMBO_FORMAT_INVALID:
		return "invalid";
	}
	return "unknown";
}

static const char *editable_list_type_name(enum obs_editable_list_type type)
{
	switch (type) {
	case OBS_EDITABLE_LIST_TYPE_STRINGS:
		return "strings";
	case OBS_EDITABLE_LIST_TYPE_FILES:
		return "files";
	case OBS_EDITABLE_LIST_TYPE_FILES_AND_URLS:
		return "files-and-URLs";
	}
	return "unknown";
}

static const char *group_type_name(enum obs_group_type type)
{
	switch (type) {
	case OBS_GROUP_NORMAL:
		return "normal";
	case OBS_GROUP_CHECKABLE:
		return "checkable";
	case OBS_COMBO_INVALID:
		return "invalid";
	}
	return "unknown";
}

static const char *button_type_name(enum obs_button_type type)
{
	switch (type) {
	case OBS_BUTTON_DEFAULT:
		return "default";
	case OBS_BUTTON_URL:
		return "url";
	}
	return "unknown";
}

static const char *setting_backed_name(obs_property_t *property, const struct property_context *context,
					      const char *name)
{
	enum obs_property_type type = obs_property_get_type(property);
	if ((type == OBS_PROPERTY_TEXT && obs_property_text_type(property) == OBS_TEXT_INFO) ||
	    type == OBS_PROPERTY_BUTTON)
		return "uiOnly";
	if (type == OBS_PROPERTY_GROUP)
		return "unknown";
	if (obstf_object_get(context->defaults, name) || obstf_object_get(context->current, name))
		return "likely";
	return "unknown";
}

static bool add_property_metadata(obs_property_t *property, obstf_value_t *metadata, char *error, size_t error_size)
{
	enum obs_property_type type = obs_property_get_type(property);
	switch (type) {
	case OBS_PROPERTY_BOOL:
		return true;
	case OBS_PROPERTY_INT:
		return model_put_int(metadata, "minimum", obs_property_int_min(property)) &&
		       model_put_int(metadata, "maximum", obs_property_int_max(property)) &&
		       model_put_int(metadata, "step", obs_property_int_step(property)) &&
		       model_put_string(metadata, "control", number_control_name(obs_property_int_type(property))) &&
		       model_put_nullable_string(metadata, "suffix", obs_property_int_suffix(property));
	case OBS_PROPERTY_FLOAT:
		return model_put_double(metadata, "minimum", obs_property_float_min(property)) &&
		       model_put_double(metadata, "maximum", obs_property_float_max(property)) &&
		       model_put_double(metadata, "step", obs_property_float_step(property)) &&
		       model_put_string(metadata, "control", number_control_name(obs_property_float_type(property))) &&
		       model_put_nullable_string(metadata, "suffix", obs_property_float_suffix(property));
	case OBS_PROPERTY_TEXT: {
		enum obs_text_type text_type = obs_property_text_type(property);
		if (!model_put_string(metadata, "textType", text_type_name(text_type)) ||
		    !model_put_bool(metadata, "monospace", obs_property_text_monospace(property)))
			return false;
		if (text_type == OBS_TEXT_INFO &&
		    (!model_put_string(metadata, "infoSeverity", text_info_type_name(obs_property_text_info_type(property))) ||
		     !model_put_bool(metadata, "infoWordWrap", obs_property_text_info_word_wrap(property))))
			return false;
		return true;
	}
	case OBS_PROPERTY_PATH:
		return model_put_string(metadata, "pathType", path_type_name(obs_property_path_type(property))) &&
		       model_put_nullable_string(metadata, "filter", obs_property_path_filter(property)) &&
		       model_put_redacted_string(metadata, "defaultPath", obs_property_path_default_path(property),
						 "sensitive-property-metadata");
	case OBS_PROPERTY_LIST: {
		enum obs_combo_format format = obs_property_list_format(property);
		if (!model_put_string(metadata, "comboType", combo_type_name(obs_property_list_type(property))) ||
		    !model_put_string(metadata, "valueFormat", combo_format_name(format)))
			return false;
		obstf_value_t *choices = obstf_value_array();
		if (!choices)
			return false;
		for (size_t index = 0; index < obs_property_list_item_count(property); index++) {
			obstf_value_t *choice = obstf_value_object();
			bool label_added = format == OBS_COMBO_FORMAT_STRING ?
				model_put_redacted_string(choice, "label", obs_property_list_item_name(property, index),
						 "dynamic-choice-label") :
				model_put_string(choice, "label", obs_property_list_item_name(property, index));
			if (!choice || !label_added ||
			    !model_put_bool(choice, "disabled", obs_property_list_item_disabled(property, index))) {
				obstf_value_free(choice);
				obstf_value_free(choices);
				return false;
			}
			obstf_value_t *choice_value = NULL;
			switch (format) {
			case OBS_COMBO_FORMAT_INT:
				choice_value = obstf_value_int((int64_t)obs_property_list_item_int(property, index));
				break;
			case OBS_COMBO_FORMAT_FLOAT:
				choice_value = obstf_value_double(obs_property_list_item_float(property, index));
				break;
			case OBS_COMBO_FORMAT_STRING:
				{
					obstf_value_t *raw_choice = obstf_value_string(obs_property_list_item_string(property, index));
					choice_value = raw_choice ? obstf_redacted_descriptor(raw_choice, "dynamic-choice") : NULL;
					obstf_value_free(raw_choice);
				}
				break;
			case OBS_COMBO_FORMAT_BOOL:
				choice_value = obstf_value_bool(obs_property_list_item_bool(property, index));
				break;
		case OBS_COMBO_FORMAT_INVALID:
				choice_value = obstf_value_null();
				break;
			}
			if (!choice_value) {
				obstf_value_free(choice);
				obstf_value_free(choices);
				return false;
			}
			bool value_added = model_put_value(choice, "value", choice_value);
			choice_value = NULL; /* object_put owns it on both success and failure */
			if (!value_added) {
				obstf_value_free(choice);
				obstf_value_free(choices);
				return false;
			}
			bool choice_added = obstf_array_push(choices, choice);
			choice = NULL; /* array_push owns it on both success and failure */
			if (!choice_added) {
				obstf_value_free(choices);
				return false;
			}
		}
		return model_put_value(metadata, "choices", choices);
	}
	case OBS_PROPERTY_COLOR:
	case OBS_PROPERTY_COLOR_ALPHA:
		return model_put_string(metadata, "valueEncoding", "nativeInteger");
	case OBS_PROPERTY_BUTTON:
		if (!model_put_string(metadata, "buttonType", button_type_name(obs_property_button_type(property))))
			return false;
		if (obs_property_button_type(property) == OBS_BUTTON_URL)
			return model_put_redacted_string(metadata, "url", obs_property_button_url(property),
						 "sensitive-property-metadata");
		return true;
	case OBS_PROPERTY_FONT: {
		obstf_value_t *shape = obstf_value_object();
		if (!shape || !model_put_string(shape, "face", "string") || !model_put_string(shape, "style", "string") ||
		    !model_put_string(shape, "size", "integer") || !model_put_string(shape, "flags", "integer")) {
			obstf_value_free(shape);
			return false;
		}
		bool shape_added = model_put_value(metadata, "nativeShape", shape);
		shape = NULL; /* object_put owns it on both success and failure */
		return shape_added;
	}
	case OBS_PROPERTY_EDITABLE_LIST:
		return model_put_string(metadata, "editableListType",
					editable_list_type_name(obs_property_editable_list_type(property))) &&
		       model_put_nullable_string(metadata, "filter", obs_property_editable_list_filter(property)) &&
		       model_put_redacted_string(metadata, "defaultPath", obs_property_editable_list_default_path(property),
						 "sensitive-property-metadata");
	case OBS_PROPERTY_FRAME_RATE: {
		obstf_value_t *options = obstf_value_array();
		obstf_value_t *ranges = obstf_value_array();
		if (!options || !ranges) {
			obstf_value_free(options);
			obstf_value_free(ranges);
			return false;
		}
		for (size_t index = 0; index < obs_property_frame_rate_options_count(property); index++) {
			obstf_value_t *option = obstf_value_object();
			if (!option || !model_put_string(option, "name", obs_property_frame_rate_option_name(property, index)) ||
			    !model_put_string(option, "description", obs_property_frame_rate_option_description(property, index))) {
				obstf_value_free(option);
				obstf_value_free(options);
				obstf_value_free(ranges);
				return false;
			}
			bool option_added = obstf_array_push(options, option);
			option = NULL; /* array_push owns it on both success and failure */
			if (!option_added) {
				obstf_value_free(options);
				obstf_value_free(ranges);
				return false;
			}
		}
		for (size_t index = 0; index < obs_property_frame_rate_fps_ranges_count(property); index++) {
			struct media_frames_per_second minimum = obs_property_frame_rate_fps_range_min(property, index);
			struct media_frames_per_second maximum = obs_property_frame_rate_fps_range_max(property, index);
			obstf_value_t *range = obstf_value_object();
			obstf_value_t *minimum_value = obstf_value_object();
			obstf_value_t *maximum_value = obstf_value_object();
			if (!range || !minimum_value || !maximum_value || !model_put_int(minimum_value, "numerator", minimum.numerator) ||
			    !model_put_int(minimum_value, "denominator", minimum.denominator) ||
			    !model_put_int(maximum_value, "numerator", maximum.numerator) ||
			    !model_put_int(maximum_value, "denominator", maximum.denominator)) {
				obstf_value_free(minimum_value);
				obstf_value_free(maximum_value);
				obstf_value_free(range);
				obstf_value_free(options);
				obstf_value_free(ranges);
				return false;
			}
			bool minimum_added = model_put_value(range, "minimum", minimum_value);
			minimum_value = NULL; /* object_put owns it on both success and failure */
			bool maximum_added = false;
			if (minimum_added) {
				maximum_added = model_put_value(range, "maximum", maximum_value);
				maximum_value = NULL; /* object_put owns it on both success and failure */
			} else {
				obstf_value_free(maximum_value);
				maximum_value = NULL;
			}
			bool range_added = false;
			if (maximum_added) {
				range_added = obstf_array_push(ranges, range);
				range = NULL; /* array_push owns it on both success and failure */
			} else {
				obstf_value_free(range);
				range = NULL;
			}
			if (!minimum_added || !maximum_added || !range_added) {
				obstf_value_free(options);
				obstf_value_free(ranges);
				return false;
			}
		}
		bool options_added = model_put_value(metadata, "options", options);
		options = NULL; /* object_put owns it on both success and failure */
		bool ranges_added = false;
		if (options_added) {
			ranges_added = model_put_value(metadata, "fpsRanges", ranges);
			ranges = NULL; /* object_put owns it on both success and failure */
		} else {
			obstf_value_free(ranges);
			ranges = NULL;
		}
		return options_added && ranges_added;
	}
	case OBS_PROPERTY_GROUP:
		return model_put_string(metadata, "groupType", group_type_name(obs_property_group_type(property)));
	case OBS_PROPERTY_INVALID:
		set_error(error, error_size, "unsupported invalid property");
		return false;
	}
	set_error(error, error_size, "unsupported property type");
	return false;
}

static bool build_property_list(obs_properties_t *properties, obstf_value_t *output,
					struct property_context *context, char *error, size_t error_size);

static bool build_property_entry(obs_property_t *property, size_t declared_order, obstf_value_t **out_entry,
					struct property_context *context, char *error, size_t error_size)
{
	*out_entry = NULL;
	const char *name = obs_property_name(property);
	if (!name || !*name) {
		set_error(error, error_size, "property has no name");
		return false;
	}

	enum obs_property_type type = obs_property_get_type(property);
	const bool ui_only = (type == OBS_PROPERTY_BUTTON) ||
			     (type == OBS_PROPERTY_TEXT && obs_property_text_type(property) == OBS_TEXT_INFO);
	if (!string_list_add_unique(&context->property_names, name) ||
	    (ui_only && !string_list_add_unique(&context->ui_only_names, name)) ||
	    (type == OBS_PROPERTY_TEXT && obs_property_text_type(property) == OBS_TEXT_PASSWORD &&
	     !string_list_add_unique(&context->password_names, name)) ||
	    (((type == OBS_PROPERTY_PATH || type == OBS_PROPERTY_EDITABLE_LIST) ||
	      (type == OBS_PROPERTY_TEXT && obs_property_text_type(property) != OBS_TEXT_INFO) ||
	      (type == OBS_PROPERTY_LIST && obs_property_list_format(property) == OBS_COMBO_FORMAT_STRING)) &&
	     !string_list_add_unique(&context->sensitive_names, name))) {
		set_error(error, error_size, "out of memory recording property identity");
		return false;
	}

	obstf_value_t *entry = obstf_value_object();
	obstf_value_t *metadata = obstf_value_object();
	if (!entry || !metadata || !model_put_string(entry, "name", name) ||
	    !model_put_string(entry, "label", safe_string(obs_property_description(property))) ||
	    !model_put_nullable_string(entry, "longDescription", obs_property_long_description(property)) ||
	    !model_put_string(entry, "propertyType", property_type_name(type)) ||
	    !model_put_bool(entry, "visible", obs_property_visible(property)) ||
	    !model_put_bool(entry, "enabled", obs_property_enabled(property)) ||
	    !model_put_int(entry, "declaredOrder", (int64_t)declared_order) ||
	    !model_put_string(entry, "settingBacked", setting_backed_name(property, context, name)) ||
	    !add_property_metadata(property, metadata, error, error_size)) {
		obstf_value_free(entry);
		obstf_value_free(metadata);
		return false;
	}
	bool metadata_added = model_put_value(entry, "metadata", metadata);
	metadata = NULL; /* object_put owns it on both success and failure */
	if (!metadata_added) {
		obstf_value_free(entry);
		return false;
	}

	if (type == OBS_PROPERTY_GROUP) {
		obstf_value_t *children = obstf_value_array();
		obs_properties_t *group = obs_property_group_content(property);
		if (!children) {
			obstf_value_free(entry);
			return false;
		}
		if (group && !build_property_list(group, children, context, error, error_size)) {
			obstf_value_free(children);
			obstf_value_free(entry);
			return false;
		}
		bool children_added = model_put_value(entry, "children", children);
		children = NULL; /* object_put owns it on both success and failure */
		if (!children_added) {
			obstf_value_free(entry);
			return false;
		}
	}

	*out_entry = entry;
	return true;
}

static bool build_property_list(obs_properties_t *properties, obstf_value_t *output,
					struct property_context *context, char *error, size_t error_size)
{
	if (!properties || !output || obstf_value_type(output) != OBSTF_VALUE_ARRAY)
		return false;
	size_t order = 0;
	for (obs_property_t *property = obs_properties_first(properties); property; obs_property_next(&property)) {
		obstf_value_t *entry = NULL;
		if (!build_property_entry(property, order++, &entry, context, error, error_size)) {
			obstf_value_free(entry);
			return false;
		}
		bool entry_added = obstf_array_push(output, entry);
		entry = NULL; /* array_push owns it on both success and failure */
		if (!entry_added) {
			set_error(error, error_size, "out of memory recording property metadata");
			return false;
		}
	}
	return true;
}

static obstf_value_t *safe_settings_model(const obstf_value_t *raw, const struct property_context *context,
						  bool defaults_layer)
{
	obstf_value_t *result = obstf_value_object();
	if (!result)
		return NULL;
	if (!raw)
		return result;

	for (size_t index = 0; index < obstf_object_count(raw); index++) {
		const char *key = obstf_object_key(raw, index);
		const obstf_value_t *value = obstf_object_value(raw, index);
		const bool known_property = string_list_contains(&context->property_names, key);
		const bool password = string_list_contains(&context->password_names, key);
		const bool sensitive = string_list_contains(&context->sensitive_names, key);
		obstf_value_t *published = NULL;
		if (password)
			published = obstf_redacted_descriptor(value, "password");
		else if (sensitive)
			published = obstf_redacted_descriptor(value, "sensitive-property");
		else if (!known_property)
			published = obstf_redacted_descriptor(value, defaults_layer ? "opaque-default" : "opaque");
		else
			published = obstf_value_clone(value);
		if (!published || !obstf_object_put(result, key, published)) {
			if (!published)
				obstf_value_free(published);
			obstf_value_free(result);
			return NULL;
		}
	}
	return result;
}

static obstf_value_t *effective_settings_model(const obstf_value_t *defaults, const obstf_value_t *current)
{
	obstf_value_t *result = obstf_value_clone(defaults);
	if (!result)
		return NULL;
	for (size_t index = 0; index < obstf_object_count(current); index++) {
		obstf_value_t *copy = obstf_value_clone(obstf_object_value(current, index));
		if (!copy || !obstf_object_put(result, obstf_object_key(current, index), copy)) {
			if (!copy)
				obstf_value_free(copy);
			obstf_value_free(result);
			return NULL;
		}
	}
	return result;
}

static void collect_filter_match(obs_source_t *parent, obs_source_t *child, void *param)
{
	(void)parent;
	struct filter_match *match = param;
	if (!match || !child)
		return;

	const bool uuid_matches = strcmp(safe_string(obs_source_get_uuid(child)), match->requested_uuid) == 0;
	const bool name_matches = strcmp(safe_string(obs_source_get_name(child)), match->requested_name) == 0;
	if (uuid_matches)
		match->uuid_count++;
	if (name_matches)
		match->name_count++;
	if (uuid_matches && name_matches) {
		match->exact_count++;
		if (!match->match)
			match->match = obs_source_get_ref(child);
	}
}

static obstf_value_t *build_provenance(const char *kind, const char *unversioned_kind)
{
	obstf_value_t *provenance = obstf_value_object();
	obstf_value_t *module = obstf_value_object();
	if (!provenance || !module || !model_put_string(provenance, "sourceType", "filter") ||
	    !model_put_nullable_string(provenance, "kind", kind) ||
	    !model_put_nullable_string(provenance, "unversionedKind", unversioned_kind)) {
		obstf_value_free(provenance);
		obstf_value_free(module);
		return NULL;
	}

	obs_module_t *native_module = kind ? obs_source_get_module(kind) : NULL;
	const char *module_id = native_module ? obs_get_module_id(native_module) : NULL;
	const char *module_name = native_module ? obs_get_module_name(native_module) : NULL;
	const char *module_file = native_module ? obs_get_module_file_name(native_module) : NULL;
	const char *module_version = native_module ? obs_get_module_version(native_module) : NULL;
	bool module_ok = model_put_nullable_string(module, "id", module_id) &&
			model_put_nullable_string(module, "name", module_name) &&
			model_put_nullable_string(module, "fileName", module_file) &&
			model_put_nullable_string(module, "version", module_version) &&
			model_put_string(module, "versionStatus", module_version ? "known" : "unknown");
	if (module_ok)
		module_ok = model_take_value(provenance, "module", &module);
	if (!module_ok) {
		obstf_value_free(provenance);
		obstf_value_free(module);
		return NULL;
	}
	return provenance;
}

static obstf_value_t *build_scope(obs_source_t *parent, obs_source_t *filter)
{
	obstf_value_t *scope = obstf_value_object();
	obstf_value_t *source = obstf_value_object();
	obstf_value_t *filter_identity = obstf_value_object();
	if (!scope || !source || !filter_identity || !model_put_string(scope, "mode", "instance") ||
	    !model_put_string(source, "sourceUuid", safe_string(obs_source_get_uuid(parent))) ||
	    !model_put_string(source, "sourceName", safe_string(obs_source_get_name(parent))) ||
	    !model_put_string(filter_identity, "filterUuid", safe_string(obs_source_get_uuid(filter))) ||
	    !model_put_string(filter_identity, "filterName", safe_string(obs_source_get_name(filter)))) {
		obstf_value_free(scope);
		obstf_value_free(source);
		obstf_value_free(filter_identity);
		return NULL;
	}
	bool source_added = model_take_value(scope, "source", &source);
	bool filter_added = false;
	if (source_added)
		filter_added = model_take_value(scope, "filter", &filter_identity);
	else
		obstf_value_free(filter_identity);
	if (!source_added || !filter_added) {
		obstf_value_free(scope);
		return NULL;
	}
	return scope;
}

static obstf_value_t *build_instance_state(obs_source_t *parent, obs_source_t *filter)
{
	obstf_value_t *state = obstf_value_object();
	if (!state || !model_put_string(state, "filterName", safe_string(obs_source_get_name(filter))) ||
	    !model_put_bool(state, "enabled", obs_source_enabled(filter)) ||
	    !model_put_int(state, "filterIndex", obs_source_filter_get_index(parent, filter))) {
		obstf_value_free(state);
		return NULL;
	}
	return state;
}

static obstf_value_t *build_descriptor_model(obs_source_t *parent, obs_source_t *filter,
						      const struct parsed_request *request, char *error,
						      size_t error_size)
{
	const char *kind = obs_source_get_id(filter);
	const char *unversioned_kind = obs_source_get_unversioned_id(filter);
	const bool inspect_properties = request->inspect_properties;
	bool defaults_available = false;
	obstf_value_t *raw_defaults = inspect_properties ? defaults_model(filter, &defaults_available) : obstf_value_object();
	obstf_value_t *raw_current = request->include_current_values ?
		(inspect_properties ? current_model(filter) : current_probe_model(filter)) : obstf_value_object();
	if (!raw_defaults || !raw_current) {
		obstf_value_free(raw_defaults);
		obstf_value_free(raw_current);
		set_error(error, error_size, "unable to copy native settings");
		return NULL;
	}

	struct property_context context = {
		.defaults = raw_defaults,
		.current = raw_current,
	};
	obstf_value_t *property_rows = obstf_value_array();
	bool properties_available = false;
	bool properties_ok = property_rows;
	if (inspect_properties) {
		obs_properties_t *properties = obs_source_properties(filter);
		properties_available = properties != NULL;
		properties_ok = properties_ok && (!properties || build_property_list(properties, property_rows, &context, error, error_size));
		if (properties)
			obs_properties_destroy(properties);
	}
	if (!properties_ok) {
		obstf_value_free(property_rows);
		obstf_value_free(raw_defaults);
		obstf_value_free(raw_current);
		string_list_free(&context.property_names);
		string_list_free(&context.ui_only_names);
		string_list_free(&context.password_names);
		string_list_free(&context.sensitive_names);
		if (!error || !*error)
			set_error(error, error_size, "unable to copy native property metadata");
		return NULL;
	}

	obstf_value_t *published_defaults = safe_settings_model(raw_defaults, &context, true);
	obstf_value_t *published_current = request->include_current_values ?
		(inspect_properties ? safe_settings_model(raw_current, &context, false) :
		 obstf_redacted_descriptor(NULL, "opaque-current-settings")) : obstf_value_object();
	const bool effective_included = inspect_properties && request->include_current_values && defaults_available;
	obstf_value_t *published_effective = effective_included ?
		effective_settings_model(published_defaults, published_current) : obstf_value_object();
	if (!published_defaults || !published_current || !published_effective) {
		obstf_value_free(property_rows);
		obstf_value_free(raw_defaults);
		obstf_value_free(raw_current);
		obstf_value_free(published_defaults);
		obstf_value_free(published_current);
		obstf_value_free(published_effective);
		string_list_free(&context.property_names);
		string_list_free(&context.ui_only_names);
		string_list_free(&context.password_names);
		string_list_free(&context.sensitive_names);
		set_error(error, error_size, "out of memory building descriptor");
		return NULL;
	}

	obstf_value_t *scope = build_scope(parent, filter);
	obstf_value_t *provenance = build_provenance(kind, unversioned_kind);
	obstf_value_t *instance_state = build_instance_state(parent, filter);
	obstf_value_t *property_metadata = obstf_value_object();
	bool property_metadata_ok = property_metadata &&
			model_put_string(property_metadata, "schemaCompleteness", "not_claimed") &&
			model_put_string(property_metadata, "inspectionMode", inspect_properties ? "inspectProperties" : "metadata") &&
			model_put_string(property_metadata, "propertyEvaluation",
					inspect_properties ? "native_builder_executed" : "not_collected_callback_free") &&
			model_put_string(property_metadata, "defaultEvaluation",
					inspect_properties ? "native_builder_executed" : "not_collected_callback_free") &&
			model_put_bool(property_metadata, "propertiesAvailable", properties_available);
	if (property_metadata_ok)
		property_metadata_ok = model_take_value(property_metadata, "properties", &property_rows);
	if (!scope || !provenance || !instance_state || !property_metadata_ok) {
		obstf_value_free(scope);
		obstf_value_free(provenance);
		obstf_value_free(instance_state);
		obstf_value_free(property_metadata);
		obstf_value_free(property_rows);
		obstf_value_free(raw_defaults);
		obstf_value_free(raw_current);
		obstf_value_free(published_defaults);
		obstf_value_free(published_current);
		obstf_value_free(published_effective);
		string_list_free(&context.property_names);
		string_list_free(&context.ui_only_names);
		string_list_free(&context.password_names);
		string_list_free(&context.sensitive_names);
		set_error(error, error_size, "out of memory assembling descriptor");
		return NULL;
	}

	obstf_value_t *result = obstf_assemble_descriptor_response(
		inspect_properties, request->include_current_values, defaults_available,
		safe_string(obs_get_version_string()), scope, provenance, instance_state,
		property_metadata, published_defaults, published_current, published_effective);
	if (!result)
		set_error(error, error_size, "out of memory assembling descriptor result");
	obstf_value_free(raw_defaults);
	obstf_value_free(raw_current);
	obstf_value_free(published_defaults);
	obstf_value_free(published_current);
	obstf_value_free(published_effective);
	string_list_free(&context.property_names);
	string_list_free(&context.ui_only_names);
	string_list_free(&context.password_names);
	string_list_free(&context.sensitive_names);
	return result;
}

const char *obstf_descriptor_status_name(obstf_descriptor_status_t status)
{
	switch (status) {
	case OBSTF_DESCRIPTOR_OK:
		return "ok";
	case OBSTF_DESCRIPTOR_INVALID_REQUEST:
		return "invalid_request";
	case OBSTF_DESCRIPTOR_UI_THREAD_REQUIRED:
		return "ui_thread_required";
	case OBSTF_DESCRIPTOR_PARENT_NOT_FOUND:
		return "parent_not_found";
	case OBSTF_DESCRIPTOR_PARENT_IDENTITY_MISMATCH:
		return "parent_identity_mismatch";
	case OBSTF_DESCRIPTOR_FILTER_NOT_FOUND:
		return "filter_not_found";
	case OBSTF_DESCRIPTOR_FILTER_IDENTITY_MISMATCH:
		return "filter_identity_mismatch";
	case OBSTF_DESCRIPTOR_FILTER_IDENTITY_AMBIGUOUS:
		return "filter_identity_ambiguous";
	case OBSTF_DESCRIPTOR_NATIVE_FAILURE:
		return "native_failure";
	}
	return "unknown";
}

obstf_descriptor_status_t obstf_describe_filter_request(obs_data_t *request, obs_data_t **out_result,
							 char *error_message, size_t error_message_size)
{
	if (out_result)
		*out_result = NULL;
	clear_error(error_message, error_message_size);
	if (!out_result) {
		set_error(error_message, error_message_size, "out_result is required");
		return OBSTF_DESCRIPTOR_INVALID_REQUEST;
	}
	if (!obs_in_task_thread(OBS_TASK_UI)) {
		set_error(error_message, error_message_size, "DescribeFilter must run on OBS_TASK_UI");
		return OBSTF_DESCRIPTOR_UI_THREAD_REQUIRED;
	}

	struct parsed_request parsed;
	if (!parse_request(request, &parsed, error_message, error_message_size)) {
		parsed_request_free(&parsed);
		return OBSTF_DESCRIPTOR_INVALID_REQUEST;
	}

	obs_source_t *parent = obs_get_source_by_uuid(parsed.source_uuid);
	if (!parent) {
		set_error(error_message, error_message_size, "parent source UUID was not found");
		parsed_request_free(&parsed);
		return OBSTF_DESCRIPTOR_PARENT_NOT_FOUND;
	}
	if (parsed.source_name && strcmp(parsed.source_name, safe_string(obs_source_get_name(parent))) != 0) {
		set_error(error_message, error_message_size, "parent source UUID/name identity mismatch");
		obs_source_release(parent);
		parsed_request_free(&parsed);
		return OBSTF_DESCRIPTOR_PARENT_IDENTITY_MISMATCH;
	}

	struct filter_match match = {
		.requested_uuid = parsed.filter_uuid,
		.requested_name = parsed.filter_name,
	};
	obs_source_enum_filters(parent, collect_filter_match, &match);
	obstf_descriptor_status_t match_status =
		obstf_filter_match_status(match.uuid_count, match.name_count, match.exact_count, match.match != NULL);
	if (match_status != OBSTF_DESCRIPTOR_OK) {
		if (match.match)
			obs_source_release(match.match);
		obs_source_release(parent);
		set_error(error_message, error_message_size, "filter UUID/name did not resolve to one exact child");
		parsed_request_free(&parsed);
		return match_status;
	}

	obstf_value_t *model = build_descriptor_model(parent, match.match, &parsed, error_message, error_message_size);
	obs_source_release(match.match);
	obs_source_release(parent);
	parsed_request_free(&parsed);
	if (!model) {
		if (!error_message || !*error_message)
			set_error(error_message, error_message_size, "unable to build descriptor");
		return OBSTF_DESCRIPTOR_NATIVE_FAILURE;
	}

	char *json = obstf_value_json(model);
	obstf_value_free(model);
	if (!json) {
		set_error(error_message, error_message_size, "unable to serialize descriptor");
		return OBSTF_DESCRIPTOR_NATIVE_FAILURE;
	}
	obs_data_t *result = obs_data_create_from_json(json);
	free(json);
	if (!result) {
		set_error(error_message, error_message_size, "unable to create OBS result data");
		return OBSTF_DESCRIPTOR_NATIVE_FAILURE;
	}
	*out_result = result;
	return OBSTF_DESCRIPTOR_OK;
}
