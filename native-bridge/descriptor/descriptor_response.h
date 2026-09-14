#ifndef OBSTF_DESCRIPTOR_RESPONSE_H
#define OBSTF_DESCRIPTOR_RESPONSE_H
#include "descriptor_model.h"
/* Takes ownership of scope, provenance, instance_state and property_metadata; borrows published values. */
obstf_value_t *obstf_assemble_descriptor_response(bool inspect_properties, bool include_current_values, bool defaults_available, const char *obs_version, obstf_value_t *scope, obstf_value_t *provenance, obstf_value_t *instance_state, obstf_value_t *property_metadata, const obstf_value_t *published_defaults, const obstf_value_t *published_current, const obstf_value_t *published_effective);
#endif
