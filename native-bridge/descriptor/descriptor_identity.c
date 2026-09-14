#include "descriptor_identity.h"

obstf_descriptor_status_t obstf_filter_match_status(size_t uuid_count, size_t name_count,
						      size_t exact_count, bool exact_reference_available)
{
	if (uuid_count == 0) {
		if (name_count == 0)
			return OBSTF_DESCRIPTOR_FILTER_NOT_FOUND;
		return name_count == 1 ? OBSTF_DESCRIPTOR_FILTER_IDENTITY_MISMATCH
				       : OBSTF_DESCRIPTOR_FILTER_IDENTITY_AMBIGUOUS;
	}

	/* UUID is the selector: one UUID plus a stale/wrong name is a mismatch. */
	if (uuid_count == 1 && exact_count == 0)
		return OBSTF_DESCRIPTOR_FILTER_IDENTITY_MISMATCH;
	if (uuid_count != 1 || exact_count != 1)
		return OBSTF_DESCRIPTOR_FILTER_IDENTITY_AMBIGUOUS;
	return exact_reference_available ? OBSTF_DESCRIPTOR_OK : OBSTF_DESCRIPTOR_NATIVE_FAILURE;
}
