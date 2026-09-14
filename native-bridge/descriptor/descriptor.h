#ifndef OBSTF_DESCRIPTOR_H
#define OBSTF_DESCRIPTOR_H

#include <obs-data.h>

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

enum obstf_descriptor_status {
	OBSTF_DESCRIPTOR_OK = 0,
	OBSTF_DESCRIPTOR_INVALID_REQUEST,
	OBSTF_DESCRIPTOR_UI_THREAD_REQUIRED,
	OBSTF_DESCRIPTOR_PARENT_NOT_FOUND,
	OBSTF_DESCRIPTOR_PARENT_IDENTITY_MISMATCH,
	OBSTF_DESCRIPTOR_FILTER_NOT_FOUND,
	OBSTF_DESCRIPTOR_FILTER_IDENTITY_MISMATCH,
	OBSTF_DESCRIPTOR_FILTER_IDENTITY_AMBIGUOUS,
	OBSTF_DESCRIPTOR_NATIVE_FAILURE,
};

typedef enum obstf_descriptor_status obstf_descriptor_status_t;

/*
 * Build a read-only instance descriptor for one exact filter.
 *
 * Preconditions and ownership:
 * - The caller must invoke this on OBS_TASK_UI. This function does not queue
 *   work or dispatch internally; the vendor wrapper owns that boundary.
 * - request is borrowed for the duration of the call and is never modified.
 * - On success, *out_result is a new obs_data_t owned by the caller. Release
 *   it with obs_data_release().
 * - On every non-OK return, *out_result is set to NULL. error_message is
 *   best-effort and NUL-terminated when its size is non-zero.
 *
 * The accepted request is:
 * {
 *   "contractVersion": 1,
 *   "source": {"sourceUuid": "...", "sourceName": "optional"},
 *   "filter": {"filterName": "...", "filterUuid": "..."},
 *   "includeCurrentValues": false,
 *   "dynamicRefresh": "none",
 *   "inspectionMode": "metadata"
 * }
 *
 * includeCurrentValues defaults to false. dynamicRefresh defaults to "none"
 * and is rejected when set to any other value. inspectionMode defaults to
 * "metadata": it does not evaluate native property/default builders. The
 * explicit "inspectProperties" mode evaluates those native builders and
 * reports that transitive apply-settings/modified callbacks may have run;
 * it still never invokes an action button, direct modified callback, setter,
 * or synthetic settings permutation.
 */
obstf_descriptor_status_t obstf_describe_filter_request(obs_data_t *request, obs_data_t **out_result,
							 char *error_message, size_t error_message_size);

const char *obstf_descriptor_status_name(obstf_descriptor_status_t status);

#ifdef __cplusplus
}
#endif

#endif
