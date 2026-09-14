#ifndef OBSTF_DESCRIPTOR_MODEL_H
#define OBSTF_DESCRIPTOR_MODEL_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/*
 * A deliberately small JSON value tree used by the descriptor adapter.
 * It keeps the pure serialization/redaction rules independent from libobs so
 * they can be tested without launching OBS or linking an OBS runtime.
 */
enum obstf_value_type {
	OBSTF_VALUE_NULL,
	OBSTF_VALUE_BOOL,
	OBSTF_VALUE_INT,
	OBSTF_VALUE_DOUBLE,
	OBSTF_VALUE_STRING,
	OBSTF_VALUE_OBJECT,
	OBSTF_VALUE_ARRAY,
};

typedef struct obstf_value obstf_value_t;

obstf_value_t *obstf_value_null(void);
obstf_value_t *obstf_value_bool(bool value);
obstf_value_t *obstf_value_int(int64_t value);
obstf_value_t *obstf_value_double(double value);
obstf_value_t *obstf_value_string(const char *value);
obstf_value_t *obstf_value_object(void);
obstf_value_t *obstf_value_array(void);

void obstf_value_free(obstf_value_t *value);
obstf_value_t *obstf_value_clone(const obstf_value_t *value);
enum obstf_value_type obstf_value_type(const obstf_value_t *value);

/* Object/array insertion takes ownership, including when allocation fails. */
/* Object insertion replaces an existing key while retaining its position. */
bool obstf_object_put(obstf_value_t *object, const char *key, obstf_value_t *value);
const obstf_value_t *obstf_object_get(const obstf_value_t *object, const char *key);
size_t obstf_object_count(const obstf_value_t *object);
const char *obstf_object_key(const obstf_value_t *object, size_t index);
const obstf_value_t *obstf_object_value(const obstf_value_t *object, size_t index);

bool obstf_array_push(obstf_value_t *array, obstf_value_t *value);
size_t obstf_array_count(const obstf_value_t *array);
const obstf_value_t *obstf_array_value(const obstf_value_t *array, size_t index);

/* The returned string is heap-owned by the caller and must be free()d. */
char *obstf_value_json(const obstf_value_t *value);
char *obstf_value_sha256(const obstf_value_t *value);
size_t obstf_value_json_length(const obstf_value_t *value);

/*
 * Turn a value into the non-sensitive diagnostic shape used for an unknown
 * or password setting. The returned object is owned by the caller.
 */
obstf_value_t *obstf_redacted_descriptor(const obstf_value_t *value, const char *reason);

#ifdef __cplusplus
}
#endif

#endif
