# obstf native bridge contract

Contract version: `1`  
Bridge version: `0.1.4`  
obs-websocket vendor: `obstf`  
Target used for the first build: OBS Studio `32.2.2`, obs-websocket API `3`, macOS arm64

## Boundary

This plugin is a narrow native graph bridge. Terraform remains the desired-state,
planning, dependency, state and reconciliation owner. The bridge does not plan, adopt,
install plugins, start OBS, or duplicate obs-websocket filter/media requests.

Version 1 exposes the native group operations and scoped observation that the
built-in obs-websocket protocol cannot perform:

- create a real group in one real scene;
- create a new input directly in that group's child scene, without a Terraform-owned
  temporary anchor placement;
- place an existing source directly in that group;
- move an existing exact parent-scene placement into that group without changing its
  source or scene-item identity;
- observe an exact bound group child;
- remove one child placement, move one child out of the group, or ungroup the whole
  group as three distinct operations; and
- ask the OBS frontend to save the current scene collection at an explicit checkpoint.

Source deletion is deliberately not exposed. Use obs-websocket `RemoveInput` when the
desired operation is deletion of a source, with its existing reference safeguards.
Group-child transform, enabled, locked and index updates are also deliberately not
exposed: pinned obs-websocket 5.7.4 already supports groups in the corresponding
`GetSceneItem*` and `SetSceneItem*` requests. The provider owns their fresh-read,
precondition and postcondition recipe.

## Transport and versioning

Clients call obs-websocket `CallVendorRequest` with `vendorName: "obstf"`. Every
request data object MUST contain `contractVersion: 1`. Mutation requests MUST also
contain a non-empty `clientOperationId`; it is echoed for journaling and is not a
deduplication key.

A successful outer `CallVendorRequest` means only that obs-websocket found and invoked
the vendor callback. The client MUST inspect the bridge envelope:

```json
{
  "contractVersion": 1,
  "bridgeVersion": "0.1.4",
  "clientOperationId": "apply-42/op-3",
  "outcome": "completed",
  "code": "ok",
  "message": "group created and raw-exact postcondition observed",
  "mutationAttempted": true,
  "postconditionVerified": true,
  "result": {}
}
```

`outcome` has exactly three values:

- `completed`: the requested in-memory effect and its request-specific authoritative
  postcondition were observed before the response;
- `failed`: no mutation occurred, or a requested operation returned a definite failure
  and authoritative observation proved the requested effect absent; and
- `uncertain`: a mutation may have occurred but the authoritative postcondition could
  not be established. The client MUST re-observe and replan before retrying.

Validation, missing capabilities and identity/precondition mismatches fail before a
write. A vendor request is synchronous, but a lost WebSocket response is still
client-side `uncertain` regardless of what the bridge completed.

## Exact identity scope

All group and child mutations use UUID plus scene-item ID tuples; names are optional
diagnostics and, when supplied, MUST match. Name-only adoption is forbidden.

```json
{
  "scene": {"sourceUuid": "...", "sourceName": "Office"},
  "group": {
    "sourceUuid": "...",
    "sourceName": "Party Lights",
    "sceneItemId": 17
  },
  "child": {
    "sourceUuid": "...",
    "sourceName": "Party Light Left",
    "sceneItemId": 3
  }
}
```

The bridge freshly resolves the scene UUID, verifies it is a scene, finds the direct
group placement by both group UUID and item ID, verifies that source is a group, then
resolves the child inside that exact group. Every addressed source-container numeric item
ID must occur exactly once; duplicate IDs are a no-write ambiguity failure. Any identity
disagreement is also a no-write failure.

## Placement shape

Creation and update requests use a complete placement object. `sceneItemIndex` is the
bottom-to-top zero-based order reported by libobs.

```json
{
  "sceneItemEnabled": true,
  "sceneItemLocked": false,
  "sceneItemIndex": 0,
  "sceneItemTransform": {
    "positionX": 0.0,
    "positionY": 0.0,
    "rotation": 0.0,
    "scaleX": 1.0,
    "scaleY": 1.0,
    "alignment": 5,
    "boundsType": "OBS_BOUNDS_NONE",
    "boundsAlignment": 0,
    "boundsWidth": 0.0,
    "boundsHeight": 0.0,
    "cropToBounds": false,
    "cropLeft": 0,
    "cropTop": 0,
    "cropRight": 0,
    "cropBottom": 0
  }
}
```

The bridge validates JSON types before reading values: booleans must be JSON booleans;
IDs, indexes, alignments and crop fields must be JSON integers in their declared ranges;
and transform numbers must be finite and representable as libobs floats. It rejects
fractional integers, double-to-float overflow and wrong-type values before mutation.
Supported `boundsType` values are the libobs names
`OBS_BOUNDS_NONE`, `OBS_BOUNDS_STRETCH`, `OBS_BOUNDS_SCALE_INNER`,
`OBS_BOUNDS_SCALE_OUTER`, `OBS_BOUNDS_SCALE_TO_WIDTH`,
`OBS_BOUNDS_SCALE_TO_HEIGHT` and `OBS_BOUNDS_MAX_ONLY`.

Observation returns the same placement shape plus source `inputKind`, source dimensions,
group membership and the exact resolved identity tuple. It does not normalize floats.

### Absolute-only mutation context

Bridge 0.1.2 proved that an immediate relative-coordinate projection is not a persistent
representation. Its fresh group and child receipts passed, but the second
setter/save/reload cycle moved an untouched right child from X `960` to
`960.0000610351562`. That is accumulating sibling/world geometry, not a harmless read
gauge. Relative mutation support and all projection/canonicalization behavior are
withdrawn; there is no epsilon or ULP allowance.

Before every structural mutation and `SaveSceneCollectionCheckpoint`, bridge 0.1.3
freshly requires all of:

- OBS version exactly `32.2.2`;
- private `AbsoluteCoordinates` present as a boolean and true;
- a readable live main base canvas exactly 1920x1080; and
- the normal request contract/client-operation validation.

The shared guard runs in the central UI-task dispatcher before the request handler. A
refusal is `failed`, returns a precise `absolute_runtime_*` code and `runtimeGuard`
receipt, and guarantees `mutationAttempted: false`, including requests with no placement
object such as remove, move-to-parent, ungroup and save. `GetCapabilities` and `GetGroup`
remain read-only and available in an unsupported context.

Supported placement postconditions are raw-exact across the complete placement. The
receipt contains `requested`, `observed`, `comparisonMode: "absolute-raw-exact"`, a fresh
nested runtime guard, `rawExactMatch`, and `verified`. Any changed float or non-position
field makes a post-mutation result `uncertain`. If the runtime context becomes ineligible
during a mutation, the postcondition cannot verify.

The bridge does not hot-convert a collection or call `obs_set_private_data`. The
supported bootstrap is an explicit cold version-1 shell with no `resolution`,
`migration_resolution`, relative transform fields, managed graph, or more than one exact
empty unmanaged bootstrap scene. Terraform plus this bridge must still create the group
and children from zero. See `ABSOLUTE-MODE-PROPOSAL.md` for the source-backed seed and
post-save `scale_ref` invariants. Existing relative collections are unsupported until a
separately authorized cold reset; they are never silently migrated.

## Requests

### `GetCapabilities`

Read-only. Requires only `contractVersion`. Returns bridge/libobs/API versions, compiled
platform and architecture, active scene-collection name, frontend streaming/recording/
replay-buffer/virtual-camera state, registered request names, execution model, and
boolean capabilities. The client should also call ordinary obs-websocket `GetVersion`
to pin the obs-websocket product and RPC versions. A caller that cannot obtain either
probe has unknown safety state and must refuse mutation.

The probe also returns the fresh `absoluteRuntimeGuard`, current coordinate-mode
knowledge, absolute/relative state and main base-canvas dimensions. Its
`absoluteGroupGraphMutations` flag is true only in the supported runtime context;
`relativeGroupGraphMutations` and `placementCanonicalization` are always false. Every
mutation evaluates the guard again rather than trusting an earlier probe.

### `GetGroup`

Read-only. Requires exact `scene` and `group`. Returns the group placement and every
direct child in bottom-to-top order. Each child includes its UUID/item-ID membership
tuple and complete placement shape. This is the authoritative bridge read for native
membership, transform and order; a later reload is required to prove persistence.

### `CreateGroup`

Mutation. Requires exact `scene`, a unique `groupName`, complete `placement`, and
`clientOperationId`. It refuses any existing source with that name and any same-scene
group with that name. It calls `obs_scene_add_group2(..., true)` on the OBS UI task,
applies the placement, then proves the returned group UUID/item-ID tuple exists in the
requested scene with the raw-exact absolute placement postcondition described above.

### `CreateInputInGroup`

Mutation. Requires exact `scene` and `group`, unique `inputName`, installed `inputKind`,
an `inputSettings` object, complete `placement`, and `clientOperationId`. It creates the
source with `obs_source_create`, adds it directly to the group's child scene, and releases
only the creator's source reference after the child owns a reference. No parent-scene
anchor placement is created or owned. Completion requires exact source UUID/item-ID,
membership, kind/name identity, raw-exact placement/order readback plus a default-aware fresh
`obs_source_get_settings` comparison of every key declared in `inputSettings`. The result
always retains observed source/item identities and a `settingsPostcondition` receipt for
recovery. Any unknown or mismatching declared value makes the outcome `uncertain`; the
bridge never calls the full request completed from graph acknowledgement alone.

This request is not safe for blind replay after an uncertain client outcome. Re-observe
by intended binding and refuse ambiguous same-name/same-kind candidates.

### `AddSourceToGroup`

Mutation. Requires exact `scene`, `group`, an existing exact `source` UUID/name, complete
`placement`, and `clientOperationId`. It adds a new placement directly to the group's
child scene. Completion requires exact new item ID, source UUID, membership, transform
and order readback. It never duplicates the source.

### `MoveParentItemToGroup`

Mutation. Requires exact `scene`, `group`, exact `parentItem` UUID/name/item ID, complete
`expectedPlacement`, complete desired group-local `placement`, and `clientOperationId`.
The bridge rechecks the parent placement, calls `obs_sceneitem_group_add_item`, then
applies the desired group-local placement. Completion requires absence from the parent
scene and presence in the exact group on the same source UUID and scene-item ID, with the
desired transform/order. This is the symmetric restore for `MoveGroupChildToParent` and
can consume a freshly created WebSocket input anchor without creating a second placement.
It refuses before mutation if the destination group already contains the numeric item ID.
Another placement of the same source under a different item ID is natively valid and is
not deleted or adopted by the bridge; the provider must require an explicit declared
binding or refuse that logical-address ambiguity before calling the bridge.

### `RemoveGroupChildPlacement`

Mutation. Requires exact `scene`, `group`, `child`, complete `expectedPlacement`, and
`clientOperationId`. It calls `obs_sceneitem_remove` for that group child. Completion
requires that item ID/source UUID tuple to be absent from that group. The response also
reports `sourceStillExists` after the placement reference is released. If this was the
source's last strong reference, the source may be destroyed; that observed consequence
does not change this operation into an explicit source-delete request.

### `MoveGroupChildToParent`

Mutation. Requires exact `scene`, `group`, `child`, complete `expectedPlacement`, and
`clientOperationId`. It calls `obs_sceneitem_group_remove_item`, preserving the child
placement/source while moving it immediately before the group in the parent scene.
Completion requires absence from the group and presence in the parent scene on the same
item/source identities. It refuses before mutation if the parent already contains that
numeric item ID. This is not placement removal.

### `Ungroup`

Mutation. Requires exact `scene`, `group`, the ordered exact `expectedChildren` identity
list, and `clientOperationId`. It calls `obs_sceneitem_group_ungroup2(..., true)`.
Completion requires the group tuple to be absent and every expected child source to be
present directly in the parent scene. OBS creates new parent scene items with new IDs
during ungroup; the response maps each prior child ID to its authoritative new parent ID
and explicitly reports `sceneItemIdentityPreserved: false`. The bridge derives that map
from exact parent before/after tuples and ordered expected children, so repeated
placements of one source are not guessed by source UUID alone. Any ambiguous/colliding
diff yields `uncertain`. This is not equivalent to deleting each child or deleting an
input.

### `SaveSceneCollectionCheckpoint`

Mutation/checkpoint. Requires `clientOperationId` and an optional exact `scene`/`group`
scope to re-observe after the call. It invokes `obs_frontend_save()` synchronously on the
OBS UI task. A `completed` response means the frontend save call returned and any supplied
in-memory scope still matched. Because this API returns no filesystem receipt, the
response always includes:

```json
{
  "frontendSaveReturned": true,
  "diskDurabilityVerified": false,
  "reloadVerificationRequired": true
}
```

The live-test owner must hash/read the saved collection and reload OBS before claiming
save/reload retention. The bridge never labels callback return as disk verification.

## Execution and lifetime rules

- Vendor registration occurs only from `obs_module_post_load`, as required by the
  obs-websocket vendor API. Registered callbacks are removed during unload when the
  obs-websocket API is still present.
- obs-websocket invokes vendor callbacks inline on its request-processing thread. Every
  libobs/frontend graph operation is synchronously queued to `OBS_TASK_UI`; the response
  is produced only after that task returns.
- Strong references returned by `obs_get_source_by_uuid` and `obs_source_create` are
  paired with `obs_source_release`. Borrowed scene/item pointers are never retained past
  the UI task. Enumeration callbacks copy data before returning and retain nothing.
- Every structural membership operation can leave transforms or group bounds awaiting a
  render update. Its immediate result therefore carries `layoutSettled: false` and
  `postFrameReadRequired: true`. Structural/membership postconditions are synchronous;
  final geometry and group bounds require a subsequent `GetGroup` after OBS processes at
  least one frame, before save/reload acceptance or another geometry-dependent write.
- Operations are individually synchronous, not atomic as a multi-request transaction.
  Terraform/provider journaling and fresh observation own recovery across requests.
- Native movement changes OBS membership only. It never transfers Terraform resource
  ownership or state automatically; the caller must agree and migrate ownership before
  issuing a movement request.
- For group-child value updates through ordinary obs-websocket requests there is no
  cross-client compare-and-swap primitive; another writer can race after the provider's
  last precondition read.

## First native acceptance sequence

The sole live-OBS owner installs the built plugin explicitly and runs this sequence in an
isolated collection:

1. `GetCapabilities`; verify OBS `32.2.2`, API `3`, contract `1`.
2. Create a real group, then create two distinct inputs directly in it.
3. After at least one rendered frame, `GetGroup`; verify exact UUIDs, item IDs,
   membership, raw local/world geometry, group bounds, sibling separation and order.
4. Save checkpoint, hash the collection, restart/reload, and verify the same graph and
   raw transforms. Run at least three no-manual-rewrite save/reload cycles; every cycle
   must remain version 1 with no migration or relative transform fields, satisfy the
   exact persisted `scale_ref` topology, retain exact local/world/sibling geometry, and
   produce a true zero-write Terraform no-op.
5. Remove one child placement; verify absence and source survival/destruction honestly,
   then wait a rendered frame and verify the remaining layout.
6. Restore it through `AddSourceToGroup` when the source survives, otherwise
   `CreateInputInGroup`; verify the expected identity consequence.
7. Run a Terraform no-op with zero mutating vendor calls.
8. Exercise `MoveGroupChildToParent`, wait/read, restore the same placement identity with
   `MoveParentItemToGroup`, wait/read again, and separately exercise `Ungroup` only in
   the disposable cleanup phase. Verify its old-to-new item-ID mapping rather than
   expecting child IDs to survive.
9. Save/reload once more, independently read back, and clean up through declared APIs.

Every phase records completed, failed and uncertain operations separately. API/source
feasibility and a successful offline build are not runtime evidence.

## Upstream basis

- OBS 32.2.2 scene API: <https://docs.obsproject.com/reference-scenes>
- OBS 32.2.2 frontend API: <https://docs.obsproject.com/reference-frontend-api>
- OBS 32.2.2 module API: <https://docs.obsproject.com/reference-modules>
- obs-websocket 5.7.4 vendor API header:
  <https://github.com/obsproject/obs-websocket/blob/5.7.4/lib/obs-websocket-api.h>
- obs-websocket 5.7.4 `CallVendorRequest` implementation:
  <https://github.com/obsproject/obs-websocket/blob/5.7.4/src/requesthandler/RequestHandler_General.cpp>
- obs-websocket 5.7.4 generated protocol:
  <https://github.com/obsproject/obs-websocket/blob/5.7.4/docs/generated/protocol.json>
- OBS 32.2.2 group implementation:
  <https://github.com/obsproject/obs-studio/blob/32.2.2/libobs/obs-scene.c>


## DescribeFilter (bridge 0.1.4)

Additive request, synchronously dispatched through the same `OBS_TASK_UI` wrapper
as graph requests. No absolute-canvas guard or client operation ID is required.
`GetCapabilities.supports` advertises `describeFilter`,
`filterDescriptorMetadata`, and `filterDescriptorInspectProperties`.

```json
{
  "vendorName": "obstf",
  "requestType": "DescribeFilter",
  "requestData": {
    "contractVersion": 1,
    "source": {"sourceUuid": "exact-parent-uuid", "sourceName": "optional-exact-name"},
    "filter": {"filterUuid": "exact-filter-uuid", "filterName": "required-exact-name"},
    "inspectionMode": "metadata",
    "dynamicRefresh": "none",
    "includeCurrentValues": false
  }
}
```

Supply this to obs-websocket `CallVendorRequest`. Its `responseData` is the
standard bridge envelope; the descriptor is `responseData.result`. UUID is
primary and a mismatched name is refused, never used as a fallback selector.
The descriptor's own `formatVersion` is 1 and schema completeness is not claimed.

`inspectionMode` defaults to `metadata`: native property/default builders are
not called, defaults/properties are excluded, and effective settings are
excluded. An explicit current-values read returns a single opaque marker,
without enumerating private settings. `inspectProperties` evaluates the exact
instance's native property/default builders. libobs applies current settings
while building properties, so modified callbacks may run and may have side
effects. The response reports this distinction and does not claim callback
purity, observed callback execution, or a read-only guarantee.

`dynamicRefresh` accepts only `none`, meaning no additional bridge-requested
refresh. Neither mode directly invokes buttons, modified callbacks, setters,
or synthetic settings permutations. `includeCurrentValues` defaults to false;
inspection with it enabled can return safely classified property-backed values
and the diagnostic defaults/current overlay. Password, text, path, editable
list, dynamic string choice, and unknown/private values remain opaque as
defined in `descriptor/HANDOFF.md` and `coordination/DESCRIPTOR-REVIEW.md`.

The descriptor always has explicit `inspection`, `qualification`, `suppression`,
`defaults`, `currentSettings`, `effectiveSettings`, and `propertyMetadata`
objects. The legacy `dynamicRefreshApplied` and auxiliary private-key arrays
are absent. A property snapshot is discovery for one instance and runtime;
it is not a complete provider schema or portable plugin behavior guarantee.

The envelope's `mutationAttempted: false` records no bridge-issued write. It
must not be interpreted as proof that third-party property callbacks had no
side effects. Before/after native qualification is external evidence, separately
owned by the live operator. Windows packaging/runtime remains unqualified.

Adapter failures retain precise codes: `invalid_request`, `ui_thread_required`,
`parent_not_found`, `parent_identity_mismatch`, `filter_not_found`,
`filter_identity_mismatch`, `filter_identity_ambiguous`, and `native_failure`.
