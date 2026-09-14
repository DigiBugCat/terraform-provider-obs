#include "descriptor_model.h"

#include <inttypes.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

struct obstf_object_entry {
	char *key;
	obstf_value_t *value;
};

struct obstf_value {
	enum obstf_value_type type;
	union {
		bool boolean;
		int64_t integer;
		double number;
		char *string;
		struct {
			struct obstf_object_entry *entries;
			size_t count;
			size_t capacity;
		} object;
		struct {
			obstf_value_t **items;
			size_t count;
			size_t capacity;
		} array;
	} as;
};

static char *duplicate_string(const char *value)
{
	if (!value)
		return NULL;

	size_t length = strlen(value);
	if (length == SIZE_MAX)
		return NULL;
	char *copy = malloc(length + 1);
	if (!copy)
		return NULL;
	memcpy(copy, value, length + 1);
	return copy;
}

static obstf_value_t *allocate_value(enum obstf_value_type type)
{
	obstf_value_t *value = calloc(1, sizeof(*value));
	if (value)
		value->type = type;
	return value;
}

obstf_value_t *obstf_value_null(void)
{
	return allocate_value(OBSTF_VALUE_NULL);
}

obstf_value_t *obstf_value_bool(bool value)
{
	obstf_value_t *result = allocate_value(OBSTF_VALUE_BOOL);
	if (result)
		result->as.boolean = value;
	return result;
}

obstf_value_t *obstf_value_int(int64_t value)
{
	obstf_value_t *result = allocate_value(OBSTF_VALUE_INT);
	if (result)
		result->as.integer = value;
	return result;
}

obstf_value_t *obstf_value_double(double value)
{
	obstf_value_t *result = allocate_value(OBSTF_VALUE_DOUBLE);
	if (result)
		result->as.number = value;
	return result;
}

obstf_value_t *obstf_value_string(const char *value)
{
	obstf_value_t *result = allocate_value(OBSTF_VALUE_STRING);
	if (!result)
		return NULL;

	result->as.string = duplicate_string(value ? value : "");
	if (!result->as.string) {
		free(result);
		return NULL;
	}
	return result;
}

obstf_value_t *obstf_value_object(void)
{
	return allocate_value(OBSTF_VALUE_OBJECT);
}

obstf_value_t *obstf_value_array(void)
{
	return allocate_value(OBSTF_VALUE_ARRAY);
}

void obstf_value_free(obstf_value_t *value)
{
	if (!value)
		return;

	switch (value->type) {
	case OBSTF_VALUE_STRING:
		free(value->as.string);
		break;
	case OBSTF_VALUE_OBJECT:
		for (size_t index = 0; index < value->as.object.count; index++) {
			free(value->as.object.entries[index].key);
			obstf_value_free(value->as.object.entries[index].value);
		}
		free(value->as.object.entries);
		break;
	case OBSTF_VALUE_ARRAY:
		for (size_t index = 0; index < value->as.array.count; index++)
			obstf_value_free(value->as.array.items[index]);
		free(value->as.array.items);
		break;
	case OBSTF_VALUE_NULL:
	case OBSTF_VALUE_BOOL:
	case OBSTF_VALUE_INT:
		break;
	case OBSTF_VALUE_DOUBLE:
		break;
	}
	free(value);
}

enum obstf_value_type obstf_value_type(const obstf_value_t *value)
{
	return value ? value->type : OBSTF_VALUE_NULL;
}

static bool grow_array(void **storage, size_t element_size, size_t *capacity, size_t required)
{
	if (required <= *capacity)
		return true;

	size_t next = *capacity ? *capacity : 8;
	while (next < required) {
		if (next > SIZE_MAX / 2)
			next = required;
		else
			next *= 2;
		if (next < required && next == SIZE_MAX)
			return false;
	}
	if (next > SIZE_MAX / element_size)
		return false;
	size_t previous = *capacity;
	void *grown = realloc(*storage, next * element_size);
	if (!grown)
		return false;
	if (next > previous)
		memset((unsigned char *)grown + previous * element_size, 0, (next - previous) * element_size);
	*storage = grown;
	*capacity = next;
	return true;
}

static ssize_t object_index(const obstf_value_t *object, const char *key)
{
	if (!object || object->type != OBSTF_VALUE_OBJECT || !key)
		return -1;
	for (size_t index = 0; index < object->as.object.count; index++) {
		if (object->as.object.entries[index].key && strcmp(object->as.object.entries[index].key, key) == 0)
			return (ssize_t)index;
	}
	return -1;
}

bool obstf_object_put(obstf_value_t *object, const char *key, obstf_value_t *value)
{
	if (!value)
		return false;
	if (!object || object->type != OBSTF_VALUE_OBJECT || !key || !*key) {
		obstf_value_free(value);
		return false;
	}

	ssize_t existing = object_index(object, key);
	if (existing >= 0 && (size_t)existing < object->as.object.count && object->as.object.entries) {
		obstf_value_free(object->as.object.entries[existing].value);
		object->as.object.entries[existing].value = value;
		return true;
	}

	if (object->as.object.count == SIZE_MAX ||
	    !grow_array((void **)&object->as.object.entries, sizeof(*object->as.object.entries),
			&object->as.object.capacity, object->as.object.count + 1)) {
		obstf_value_free(value);
		return false;
	}
	char *key_copy = duplicate_string(key);
	if (!key_copy) {
		obstf_value_free(value);
		return false;
	}
	object->as.object.entries[object->as.object.count].key = key_copy;
	object->as.object.entries[object->as.object.count].value = value;
	object->as.object.count++;
	return true;
}

const obstf_value_t *obstf_object_get(const obstf_value_t *object, const char *key)
{
	ssize_t index = object_index(object, key);
	return index >= 0 ? object->as.object.entries[index].value : NULL;
}

size_t obstf_object_count(const obstf_value_t *object)
{
	return object && object->type == OBSTF_VALUE_OBJECT ? object->as.object.count : 0;
}

const char *obstf_object_key(const obstf_value_t *object, size_t index)
{
	if (!object || object->type != OBSTF_VALUE_OBJECT || index >= object->as.object.count)
		return NULL;
	return object->as.object.entries[index].key;
}

const obstf_value_t *obstf_object_value(const obstf_value_t *object, size_t index)
{
	if (!object || object->type != OBSTF_VALUE_OBJECT || index >= object->as.object.count)
		return NULL;
	return object->as.object.entries[index].value;
}

bool obstf_array_push(obstf_value_t *array, obstf_value_t *value)
{
	if (!value)
		return false;
	if (!array || array->type != OBSTF_VALUE_ARRAY || array->as.array.count == SIZE_MAX ||
	    !grow_array((void **)&array->as.array.items, sizeof(*array->as.array.items), &array->as.array.capacity,
			array->as.array.count + 1)) {
		obstf_value_free(value);
		return false;
	}
	array->as.array.items[array->as.array.count++] = value;
	return true;
}

size_t obstf_array_count(const obstf_value_t *array)
{
	return array && array->type == OBSTF_VALUE_ARRAY ? array->as.array.count : 0;
}

const obstf_value_t *obstf_array_value(const obstf_value_t *array, size_t index)
{
	if (!array || array->type != OBSTF_VALUE_ARRAY || index >= array->as.array.count)
		return NULL;
	return array->as.array.items[index];
}

obstf_value_t *obstf_value_clone(const obstf_value_t *value)
{
	if (!value)
		return NULL;

	obstf_value_t *copy = NULL;
	switch (value->type) {
	case OBSTF_VALUE_NULL:
		copy = obstf_value_null();
		break;
	case OBSTF_VALUE_BOOL:
		copy = obstf_value_bool(value->as.boolean);
		break;
	case OBSTF_VALUE_INT:
		copy = obstf_value_int(value->as.integer);
		break;
	case OBSTF_VALUE_DOUBLE: {
		copy = obstf_value_double(value->as.number);
		break;
	}
	case OBSTF_VALUE_STRING:
		copy = obstf_value_string(value->as.string);
		break;
	case OBSTF_VALUE_OBJECT:
		copy = obstf_value_object();
		if (copy) {
			for (size_t index = 0; index < value->as.object.count; index++) {
				obstf_value_t *child = obstf_value_clone(value->as.object.entries[index].value);
				if (!child || !obstf_object_put(copy, value->as.object.entries[index].key, child)) {
					/* obstf_object_put takes ownership even on failure. */
					if (!child)
						obstf_value_free(child);
					obstf_value_free(copy);
					return NULL;
				}
			}
		}
		break;
	case OBSTF_VALUE_ARRAY:
		copy = obstf_value_array();
		if (copy) {
			for (size_t index = 0; index < value->as.array.count; index++) {
				obstf_value_t *child = obstf_value_clone(value->as.array.items[index]);
				if (!child || !obstf_array_push(copy, child)) {
					/* obstf_array_push takes ownership even on failure. */
					if (!child)
						obstf_value_free(child);
					obstf_value_free(copy);
					return NULL;
				}
			}
		}
		break;
	}
	return copy;
}

struct string_builder {
	char *data;
	size_t length;
	size_t capacity;
};

static bool builder_reserve(struct string_builder *builder, size_t additional)
{
	if (additional > SIZE_MAX - builder->length - 1)
		return false;
	return grow_array((void **)&builder->data, sizeof(*builder->data), &builder->capacity,
			  builder->length + additional + 1);
}

static bool builder_append_bytes(struct string_builder *builder, const char *data, size_t length)
{
	if (!builder_reserve(builder, length))
		return false;
	memcpy(builder->data + builder->length, data, length);
	builder->length += length;
	builder->data[builder->length] = '\0';
	return true;
}

static bool builder_append_text(struct string_builder *builder, const char *text)
{
	return builder_append_bytes(builder, text, strlen(text));
}

static bool builder_append_format(struct string_builder *builder, const char *format, ...)
{
	va_list args;
	va_start(args, format);
	va_list copy;
	va_copy(copy, args);
	int required = vsnprintf(NULL, 0, format, args);
	va_end(args);
	if (required < 0 || !builder_reserve(builder, (size_t)required)) {
		va_end(copy);
		return false;
	}
	int written = vsnprintf(builder->data + builder->length, builder->capacity - builder->length, format, copy);
	va_end(copy);
	if (written < 0 || written != required)
		return false;
	builder->length += (size_t)written;
	return true;
}

static bool builder_append_json_string(struct string_builder *builder, const char *text)
{
	if (!builder_append_text(builder, "\""))
		return false;
	for (const unsigned char *cursor = (const unsigned char *)(text ? text : ""); *cursor; cursor++) {
		const char *escape = NULL;
		switch (*cursor) {
		case '\\':
			escape = "\\\\";
			break;
		case '"':
			escape = "\\\"";
			break;
		case '\b':
			escape = "\\b";
			break;
		case '\f':
			escape = "\\f";
			break;
		case '\n':
			escape = "\\n";
			break;
		case '\r':
			escape = "\\r";
			break;
		case '\t':
			escape = "\\t";
			break;
		default:
			break;
		}
		if (escape) {
			if (!builder_append_text(builder, escape))
				return false;
		} else if (*cursor < 0x20) {
			if (!builder_append_format(builder, "\\u%04x", *cursor))
				return false;
		} else if (!builder_append_bytes(builder, (const char *)cursor, 1)) {
			return false;
		}
	}
	return builder_append_text(builder, "\"");
}

static bool builder_append_json_number(struct string_builder *builder, double number)
{
	if (!(number == number) || number > 1.7976931348623157e308 || number < -1.7976931348623157e308)
		return builder_append_text(builder, "null");
	char formatted[64];
	int written = snprintf(formatted, sizeof(formatted), "%.17g", number);
	if (written < 0 || (size_t)written >= sizeof(formatted))
		return false;
	if (!strchr(formatted, '.') && !strchr(formatted, 'e') && !strchr(formatted, 'E')) {
		if ((size_t)written + 2 >= sizeof(formatted))
			return false;
		formatted[written++] = '.';
		formatted[written++] = '0';
		formatted[written] = '\0';
	}
	return builder_append_text(builder, formatted);
}

static bool append_json_value(struct string_builder *builder, const obstf_value_t *value)
{
	if (!value)
		return builder_append_text(builder, "null");

	switch (value->type) {
	case OBSTF_VALUE_NULL:
		return builder_append_text(builder, "null");
	case OBSTF_VALUE_BOOL:
		return builder_append_text(builder, value->as.boolean ? "true" : "false");
	case OBSTF_VALUE_INT:
		return builder_append_format(builder, "%" PRId64, value->as.integer);
	case OBSTF_VALUE_DOUBLE:
		return builder_append_json_number(builder, value->as.number);
	case OBSTF_VALUE_STRING:
		return builder_append_json_string(builder, value->as.string);
	case OBSTF_VALUE_OBJECT:
		if (!builder_append_text(builder, "{"))
			return false;
		for (size_t index = 0; index < value->as.object.count; index++) {
			if (index && !builder_append_text(builder, ","))
				return false;
			if (!builder_append_json_string(builder, value->as.object.entries[index].key) ||
			    !builder_append_text(builder, ":") ||
			    !append_json_value(builder, value->as.object.entries[index].value))
				return false;
		}
		return builder_append_text(builder, "}");
	case OBSTF_VALUE_ARRAY:
		if (!builder_append_text(builder, "["))
			return false;
		for (size_t index = 0; index < value->as.array.count; index++) {
			if (index && !builder_append_text(builder, ","))
				return false;
			if (!append_json_value(builder, value->as.array.items[index]))
				return false;
		}
		return builder_append_text(builder, "]");
	}
	return false;
}

struct canonical_entry {
	const char *key;
	const obstf_value_t *value;
};

static int canonical_entry_compare(const void *left, const void *right)
{
	const struct canonical_entry *left_entry = left;
	const struct canonical_entry *right_entry = right;
	return strcmp(left_entry->key, right_entry->key);
}

static bool append_canonical_json_value(struct string_builder *builder, const obstf_value_t *value)
{
	if (!value)
		return builder_append_text(builder, "null");

	switch (value->type) {
	case OBSTF_VALUE_NULL:
		return builder_append_text(builder, "null");
	case OBSTF_VALUE_BOOL:
		return builder_append_text(builder, value->as.boolean ? "true" : "false");
	case OBSTF_VALUE_INT:
		return builder_append_format(builder, "%" PRId64, value->as.integer);
	case OBSTF_VALUE_DOUBLE:
		return builder_append_json_number(builder, value->as.number);
	case OBSTF_VALUE_STRING:
		return builder_append_json_string(builder, value->as.string);
	case OBSTF_VALUE_OBJECT: {
		const size_t count = value->as.object.count;
		struct canonical_entry *entries = NULL;
		if (count) {
			if (count > SIZE_MAX / sizeof(*entries))
				return false;
			entries = malloc(count * sizeof(*entries));
			if (!entries)
				return false;
			for (size_t index = 0; index < count; index++) {
				entries[index].key = value->as.object.entries[index].key;
				entries[index].value = value->as.object.entries[index].value;
			}
			qsort(entries, count, sizeof(*entries), canonical_entry_compare);
		}
		bool ok = builder_append_text(builder, "{");
		for (size_t index = 0; ok && index < count; index++) {
			if (index)
				ok = builder_append_text(builder, ",");
			if (ok)
				ok = builder_append_json_string(builder, entries[index].key) &&
				     builder_append_text(builder, ":") &&
				     append_canonical_json_value(builder, entries[index].value);
		}
		if (ok)
			ok = builder_append_text(builder, "}");
		free(entries);
		return ok;
	}
	case OBSTF_VALUE_ARRAY:
		if (!builder_append_text(builder, "["))
			return false;
		for (size_t index = 0; index < value->as.array.count; index++) {
			if (index && !builder_append_text(builder, ","))
				return false;
			if (!append_canonical_json_value(builder, value->as.array.items[index]))
				return false;
		}
		return builder_append_text(builder, "]");
	}
	return false;
}

static char *obstf_value_canonical_json(const obstf_value_t *value)
{
	struct string_builder builder = {0};
	if (!append_canonical_json_value(&builder, value)) {
		free(builder.data);
		return NULL;
	}
	if (!builder.data)
		builder.data = duplicate_string("");
	return builder.data;
}

char *obstf_value_json(const obstf_value_t *value)
{
	struct string_builder builder = {0};
	if (!append_json_value(&builder, value)) {
		free(builder.data);
		return NULL;
	}
	if (!builder.data) {
		builder.data = duplicate_string("");
	}
	return builder.data;
}

size_t obstf_value_json_length(const obstf_value_t *value)
{
	char *json = obstf_value_json(value);
	if (!json)
		return 0;
	size_t length = strlen(json);
	free(json);
	return length;
}

/* Compact SHA-256 implementation for stable opaque-value fingerprints. */
struct sha256_context {
	uint8_t data[64];
	uint32_t datalen;
	uint64_t bitlen;
	uint32_t state[8];
};

static const uint32_t sha256_round_constants[64] = {
	0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU, 0x59f111f1U, 0x923f82a4U,
	0xab1c5ed5U, 0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU,
	0x9bdc06a7U, 0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU, 0x2de92c6fU,
	0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
	0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
	0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U, 0xa2bfe8a1U, 0xa81a664bU,
	0xc24b8b70U, 0xc76c51a3U, 0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U,
	0x1e376c08U, 0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
	0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U, 0x90befffaU, 0xa4506cebU, 0xbef9a3f7U,
	0xc67178f2U,
};

static uint32_t rotate_right(uint32_t value, uint32_t amount)
{
	return (value >> amount) | (value << (32U - amount));
}

static void sha256_transform(struct sha256_context *context, const uint8_t data[64])
{
	uint32_t words[64];
	for (uint32_t index = 0; index < 16; index++) {
		words[index] = ((uint32_t)data[index * 4] << 24) | ((uint32_t)data[index * 4 + 1] << 16) |
			       ((uint32_t)data[index * 4 + 2] << 8) | (uint32_t)data[index * 4 + 3];
	}
	for (uint32_t index = 16; index < 64; index++) {
		uint32_t s0 = rotate_right(words[index - 15], 7) ^ rotate_right(words[index - 15], 18) ^
				(words[index - 15] >> 3);
		uint32_t s1 = rotate_right(words[index - 2], 17) ^ rotate_right(words[index - 2], 19) ^
				(words[index - 2] >> 10);
		words[index] = words[index - 16] + s0 + words[index - 7] + s1;
	}

	uint32_t a = context->state[0];
	uint32_t b = context->state[1];
	uint32_t c = context->state[2];
	uint32_t d = context->state[3];
	uint32_t e = context->state[4];
	uint32_t f = context->state[5];
	uint32_t g = context->state[6];
	uint32_t h = context->state[7];
	for (uint32_t index = 0; index < 64; index++) {
		uint32_t s1 = rotate_right(e, 6) ^ rotate_right(e, 11) ^ rotate_right(e, 25);
		uint32_t choose = (e & f) ^ ((~e) & g);
		uint32_t temporary1 = h + s1 + choose + sha256_round_constants[index] + words[index];
		uint32_t s0 = rotate_right(a, 2) ^ rotate_right(a, 13) ^ rotate_right(a, 22);
		uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
		uint32_t temporary2 = s0 + majority;
		h = g;
		g = f;
		f = e;
		e = d + temporary1;
		d = c;
		c = b;
		b = a;
		a = temporary1 + temporary2;
	}

	context->state[0] += a;
	context->state[1] += b;
	context->state[2] += c;
	context->state[3] += d;
	context->state[4] += e;
	context->state[5] += f;
	context->state[6] += g;
	context->state[7] += h;
}

static void sha256_init(struct sha256_context *context)
{
	context->datalen = 0;
	context->bitlen = 0;
	context->state[0] = 0x6a09e667U;
	context->state[1] = 0xbb67ae85U;
	context->state[2] = 0x3c6ef372U;
	context->state[3] = 0xa54ff53aU;
	context->state[4] = 0x510e527fU;
	context->state[5] = 0x9b05688cU;
	context->state[6] = 0x1f83d9abU;
	context->state[7] = 0x5be0cd19U;
}

static void sha256_update(struct sha256_context *context, const uint8_t *data, size_t length)
{
	for (size_t index = 0; index < length; index++) {
		context->data[context->datalen++] = data[index];
		if (context->datalen == 64) {
			sha256_transform(context, context->data);
			context->bitlen += 512;
			context->datalen = 0;
		}
	}
}

static void sha256_final(struct sha256_context *context, uint8_t digest[32])
{
	uint32_t index = context->datalen;
	context->data[index++] = 0x80;
	if (index > 56) {
		while (index < 64)
			context->data[index++] = 0;
		sha256_transform(context, context->data);
		index = 0;
	}
	while (index < 56)
		context->data[index++] = 0;
	context->bitlen += (uint64_t)context->datalen * 8;
	for (int offset = 0; offset < 8; offset++)
		context->data[63 - offset] = (uint8_t)(context->bitlen >> (offset * 8));
	sha256_transform(context, context->data);

	for (uint32_t word = 0; word < 8; word++) {
		digest[word * 4] = (uint8_t)(context->state[word] >> 24);
		digest[word * 4 + 1] = (uint8_t)(context->state[word] >> 16);
		digest[word * 4 + 2] = (uint8_t)(context->state[word] >> 8);
		digest[word * 4 + 3] = (uint8_t)context->state[word];
	}
}

char *obstf_value_sha256(const obstf_value_t *value)
{
	char *json = obstf_value_canonical_json(value);
	if (!json)
		return NULL;

	struct sha256_context context;
	uint8_t digest[32];
	sha256_init(&context);
	sha256_update(&context, (const uint8_t *)json, strlen(json));
	sha256_final(&context, digest);
	free(json);

	char *hex = malloc(65);
	if (!hex)
		return NULL;
	for (size_t index = 0; index < 32; index++)
		(void)snprintf(hex + index * 2, 3, "%02x", digest[index]);
	hex[64] = '\0';
	return hex;
}

obstf_value_t *obstf_redacted_descriptor(const obstf_value_t *value, const char *reason)
{
	(void)value;
	obstf_value_t *descriptor = obstf_value_object();
	if (!descriptor ||
	    !obstf_object_put(descriptor, "$redacted", obstf_value_bool(true)) ||
    !obstf_object_put(descriptor, "reason", obstf_value_string(reason ? reason : "opaque"))) {
		obstf_value_free(descriptor);
		return NULL;
	}
	return descriptor;
}
