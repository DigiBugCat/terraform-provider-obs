# Integration update — 2026-09-13

The frozen packet below is retained as historical review evidence. Bridge 0.1.4
now registers `DescribeFilter` through synchronous UI-thread dispatch. Its
production response assembly is extracted into `descriptor_response.c` and is
covered by mode/current/default-availability regression tests, including
ASan/UBSan. Native lookup, builders, settings/property classification and
redaction remain unchanged. See `../CONTRACT.md` and
`../../coordination/DESCRIPTOR-INTEGRATION-HANDOFF.md` for the current build and
qualification boundary. This update does not itself claim native execution.

---

# Read-only filter descriptor handoff

Checkpoint: 2026-09-12 16:52 PDT

Owner: native-bridge/descriptor lane

Receiving owner: bridge integration task `01a09763-7f6e-7bd2-a5e4-fd13a899273b`.
Operations receipt: `01a097ce-46d3-7e80-b8c7-2f9ac80aa2c9`.

Mode/schema review pin: `coordination/DESCRIPTOR-REVIEW.md`
`f658eba86f6a274e488de161e9ef74945f02bd674a0a7e1dab10d3b45a06035c`.

## Frozen integration API

`descriptor.h` exposes one additive, unregistered handler:

```c
obstf_descriptor_status_t obstf_describe_filter_request(
    obs_data_t *request,
    obs_data_t **out_result,
    char *error_message,
    size_t error_message_size);
```

The vendor wrapper must invoke it on `OBS_TASK_UI`. The handler does not queue
or dispatch internally. It accepts `contractVersion: 1`, exact parent
`sourceUuid`, optional parent `sourceName`, exact `filterName` plus
`filterUuid`, optional `includeCurrentValues` (default `false`),
`inspectionMode` (`metadata` by default or explicit `inspectProperties`), and
only `dynamicRefresh: "none"`. It does not create a vendor envelope or
register a request. On success the caller owns a new `obs_data_t` and releases
it with `obs_data_release()`. Every non-OK path sets `*out_result = NULL` and
writes a best-effort NUL-terminated error when a buffer is supplied.

UUID is primary identity. A UUID match with a different name is an identity
mismatch; a name match with a different or multiple UUIDs never selects a
filter. Duplicate exact matches return `filter_identity_ambiguous`.

## Result boundary

The response is instance-scoped and keeps these layers separate:

- `scope.mode` is `instance`, with exact parent and filter UUID/name tuples.
- Top-level `inspection`, `qualification`, and `suppression` objects carry the
  mode receipts, action/setter/provider boundary, exact-instance/native
  qualification status, and all redaction/key-inventory suppression facts.
- `provenance` reports versioned/unversioned kind and module id/name/file/
  version. Missing native strings remain `null`; missing module version is
  explicitly `versionStatus: "unknown"`.
- `metadata` mode never calls `obs_source_properties()` or
  `obs_get_source_defaults()`. It reports `defaultsAvailable: false` and
  `defaultsStatus: "not_collected_callback_free"`; property metadata reports
  `propertyEvaluation: "not_collected_callback_free"` and an empty property
  array. An opted-in current read may call only
  `obs_source_get_settings()`, then returns one opaque marker without
  enumerating private key names. Effective settings are explicitly excluded
  with `status: "defaults_not_collected"`.
- `defaults` is always an explicit `{included, status, value}` layer: excluded
  with an empty value in metadata mode, and included after native default
  evaluation in inspect-properties mode.
- `inspectProperties` mode deliberately calls the native property/default
  builders and returns typed defaults/property metadata. It reports
  `propertyBuilderExecuted`, `defaultsBuilderExecuted`,
  `propertiesApplySettingsCalled`, `modifiedCallbacksMayHaveExecuted`,
  `callbacksMayHaveSideEffects`, `callbackExecutionObserved: false`, and
  `readOnlyGuarantee: "not_claimed"`. The pinned source shows native property
  evaluation transitively applies settings; the bridge does not call modified
  callbacks directly and does not claim to have observed one. The ambiguous
  legacy `dynamicRefreshApplied` field is omitted; `bridgeRequestedDynamicRefresh`
  and `propertiesApplySettingsCalled` carry the distinct facts.
- `currentSettings` and `effectiveSettings` are explicit `{included, status,
  value}` layers. Current values default to excluded. Effective values are
  merged only in `inspectProperties` mode when current values were requested
  and defaults were actually collected.
- No `currentKeysWithoutProperty` or `propertiesWithoutCurrentKey` arrays are
  emitted; the suppression receipt records that the auxiliary inventory and
  presence oracle are intentionally absent.
- Property metadata covers the pinned native types in `inspectProperties`,
  including nested groups, typed list choices, frame-rate ranges, native font
  shape, paths, editable lists, colors, and button metadata. No action button,
  setter, synthetic setting permutation, or direct modified callback is used.
- Password, path, editable-list, text, unknown, and opaque setting values are
  redacted without value-derived type, presence, length, digest, or fingerprint
  fields. Sensitive path/default URL/property metadata is redacted as well.
  The pure model keeps a recursively key-sorted SHA helper for internal tests,
  but no redaction response emits it. `schemaCompleteness` remains
  `"not_claimed"`.

## Files and ownership

Implementation and pure model:

- `descriptor.h` — public adapter/status API and lifetime preconditions.
- `descriptor.c` — native lookup, provenance, settings/property snapshot and
  conversion to caller-owned `obs_data_t`.
- `descriptor_identity.h` / `descriptor_identity.c` — UUID-first mismatch/
  ambiguity classification with pure adapter-level cases.
- `descriptor_model.h` / `descriptor_model.c` — dependency-free typed value
  tree, deterministic JSON, recursively canonical internal SHA-256, and safe
  redaction helpers.
- `descriptor_test.c` — deterministic nested-type, escaping, secret-redaction,
  canonical-hash, effective-overlay, opaque-value, and identity-status tests.
- `build.sh` — isolated compile/static/test helper; all generated outputs stay
  under `native-bridge/descriptor/build/`.

No `plugin.c`, `Makefile`, contract, Go code, existing tests, or live OBS state
was changed. The source-derived sample is reference-only; no live response is
claimed.

## Verification

Command:

```sh
cd /Users/andrew.sulistio/aviary/products/obstf
native-bridge/descriptor/build.sh
```

Passes in this checkpoint:

- native adapter compile-only against the pinned OBS 32.2.2 headers;
- Clang static analyzer for native adapter and pure model;
- pure-model tests: `descriptor pure-model tests: PASS`;
- forbidden read-only-boundary and provider-schema-claim scans;
- manual ASan/UBSan pure-model run:
  `ASAN_OPTIONS=halt_on_error=1 native-bridge/descriptor/build/descriptor-test-sanitize-final3`.

The build never links or launches OBS. It does not prove plugin registration,
UI-thread dispatch by the wrapper, a live filter response, callback behavior of
third-party property builders, or Windows compatibility. The inspect-properties
mode is source/compile-qualified only; its callback receipts are not live
observations.

## SHA-256 pins

```text
descriptor/descriptor.h             2e710df9edbd97a357e3cbd4a87e007c5ae619b6f3a7b7077decf881ac1f43e7
descriptor/descriptor.c             7572a118976c2cbabbc9f4088e75fee5a4c333b2828c436c4407a45ff2f7b36b
descriptor/descriptor_model.h       008d5224c943ef3150bef92215f762a4690ef00603d5ef4a7e0abf0eec5c1290
descriptor/descriptor_model.c       3303be8a1ffe2f5ad75ac40e9db95e02cf344b880b56e2b5425e3ae3700a4869
descriptor/descriptor_identity.h    8dccbcbf1dab85b48721866311e5968c052401064c2670d3ab3c7905146136ad
descriptor/descriptor_identity.c    13a0c7638fb5d46b321e06d16dea6370b619d6adedb0d09a77f1807bcfaab9e0
descriptor/descriptor_test.c        35461a7daf7d67fc2310beb6c4c94265ef3e3005d936eee813532dcfcd149c84
descriptor/build.sh                 252236ab7f7201ca344592b9bd0bb09d6968d2372d02a2981703abe081c1d2ef
descriptor/build/descriptor.o       9aa899865dfc2c8e20f2b69351c366d9c3ea80d6348a243d8357b2577a843361
descriptor/build/descriptor_model.o bfcfd5b5eb00edb0192d62242e7911241d212018a3a75510c10ce7943760aed3
descriptor/build/descriptor_identity.o 555ade00902ac9b908eb3fd9e5c351cfc62117a9cf6741e8b305a6a3a385baaa
descriptor/build/descriptor-test    7ca44774aec6dbf10bcc824804d9dc39843f920b07b6d0b60a53546a69cf262c
```

These are compile/static/pure-model artifacts only. Native/runtime qualification
and later `plugin.c` integration remain separate gates owned by the receiver.
