#ifndef OBSTF_DESCRIPTOR_IDENTITY_H
#define OBSTF_DESCRIPTOR_IDENTITY_H

#include "descriptor.h"

#include <stdbool.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/*
 * Classify the result of UUID-first filter enumeration. The caller supplies
 * counts observed under one exact parent; no name-only fallback is performed.
 * exact_reference_available is true only when one exact child reference was
 * retained for the caller.
 */
obstf_descriptor_status_t obstf_filter_match_status(size_t uuid_count, size_t name_count,
						      size_t exact_count, bool exact_reference_available);

#ifdef __cplusplus
}
#endif

#endif
