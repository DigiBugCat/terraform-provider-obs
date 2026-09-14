# Absolute-mode bridge proposal after the 0.1.2 persistence failure

Status: source-backed proposal plus pure policy tests; not implemented in the plugin  
Recorded: 2026-09-12 15:36 PDT

## Decision

Withdraw relative-coordinate mutation support from the native bridge. Bridge `0.1.3`
should perform group graph mutations only when all of the following are freshly known:

- OBS is exactly `32.2.2`;
- `AbsoluteCoordinates` is present in libobs private data and true;
- the live main base canvas is exactly 1920x1080; and
- the full request-specific identity, precondition and raw placement postcondition pass.

There is no relative projection, epsilon, ULP allowance, observed-value rewrite or
"normalized no-op" path. `GetCapabilities` and `GetGroup` remain available for diagnosis,
but `CreateGroup`, `CreateInputInGroup`, `AddSourceToGroup`,
`MoveParentItemToGroup`, `RemoveGroupChildPlacement`, `MoveGroupChildToParent`,
`Ungroup`, and `SaveSceneCollectionCheckpoint` must fail before mutation/save when the
absolute runtime predicate is not satisfied. The provider must apply the same
precondition before ordinary obs-websocket group-child setters, which the bridge cannot
intercept.

The bridge does not create, convert or adopt an absolute collection. Terraform's
explicit cold foundation/reset must publish only a fresh absent minimal collection shell
while OBS is stopped, then activate it and prove the runtime predicate before graph
mutation. The shell may carry one exact documented unmanaged **empty** bootstrap scene
when OBS requires one; it must not precreate the Party Lights group, its sources, its
children, filters, or any bootstrap-scene item. Terraform
plus the native bridge must still create that declared graph from zero. This keeps mode
initialization and destructive scope outside the companion plugin without quietly
reintroducing cold group ownership.

## Why 0.1.2 is rejected

The reviewed `0.1.2` candidate correctly modeled one immediate relative setter/getter
round trip. Its first native group and two direct group children completed with exact
receipts, rendered as a pixel-exact 1920x1080 half split, and survived the first
save/reload. That was not persistence convergence.

On the second authored-set/save/reload cycle, the untouched right child retained the
same source UUID, scene-item ID and order but moved from exactly `960` to
`960.0000610351562`. Before final quit, disk still stored absolute `pos.x: 960` with
`pos_rel.x: 5.298190686175985e-8`; the reload reconstructed the larger live value. Clean
quit then persisted the drift. This is real sibling/world geometry movement, explicitly
forbidden by `CONTRACT.md`, so the live owner stopped before add/move/remove/ungroup.

Pinned failure evidence outside this ownership lane:

| artifact | SHA-256 |
|---|---|
| prior reload `v012-get-group-after-reload.json` | `9b0c48c46063f108e2d5888ef3aee8fdc107106d187bc488d84315436d84d56d` |
| second pre-save read `v012-repeat2-after-left-frame.json` | `fc57351423a10eeac580dff69be97fa4ecb86b15c411ec59ae428828e48d23b8` |
| failing reload `v012-get-group-after-repeat2-reload.json` | `26852cf47278703cc825df68f96c034d62032dde6c13aa72cf24e98a4a345b41` |
| disk before final quit `v012-accumulation-before-stop.collection.json` | `fbc3e8215f139717fd727096d7d622c948456195d12ed708605dfc5bb0a8c1ab` |
| disk after final quit `v012-accumulation-after-stop.collection.json` | `84f5bb06d899b531b997a80f652bd33c7df41e3149b573b4ceb77a84d9705d4f` |
| run log `v012-accumulation-run.log` | `ff03fa134b10c1f2194e5e7ef9190719ae0ae5ba678985bc44215cb607d3e0f2` |

## OBS 32.2.2 mechanism

The frontend's `SceneCollection` model maps relative mode to saved version 2 and
absolute mode to version 1. New collections default to relative mode. During load,
version 1 without a top-level `resolution` object selects absolute mode; version 1 with
`resolution` is a migration input and becomes relative. Before sources load, the
frontend publishes its choice through libobs private `AbsoluteCoordinates`.

Each scene copies that private flag when created and each scene item copies its parent
scene's flag. In relative mode, item load prefers `pos_rel`/`scale_rel`, and save writes
both the relative values and reconstructed absolute compatibility values. In absolute
mode, load and save use raw `pos`, `scale` and `bounds` directly. The frontend also
removes relative transform fields when saving an absolute collection and writes version
1.

The sibling recurrence is source-derived and reproduces the live numbers without a
tolerance. `resize_scene_base` finds the absolute child minimum, converts that minimum
through `size_from_absolute`, subtracts it from every child's stored relative position,
then updates the group. At 1920x1080, authored left X zero is stored as
`-1.7777777910232544` and reconstructs as
`-0.000007152557373046875`. Converting that minimum as a size produces
`-1.3245476715439963e-8`. Each resize therefore adds that amount to the untouched
right child's relative X. The fourth step is the captured
`5.298190686175985e-8` and still reconstructs as `960`; the fifth is the captured
`6.622738624173508e-8` and reconstructs as `960.0000610351562`. This is the exact
recurrence seam in `proposal/absolute_mode_policy.h` and its test, and it proves why an
immediate projection cannot define persisted convergence.

The public APIs do not expose the frontend model's `SceneCoordinateMode` setter. Calling
`obs_set_private_data` after collection load is therefore not a supported conversion:
existing scenes/items have already copied their mode, while the frontend would still
save according to its current collection model. A plugin-created mixed state could save
as version 2 and reload relative. The OBS UI's remigration path also does not provide a
persistent reverse conversion: it temporarily saves version 1, restores the original
mode, and reactivates the collection so OBS migrates it to relative again.

Relevant pinned OBS 32.2.2 source:

- `frontend/models/SceneCollection.hpp`: coordinate enum, relative default, and
  relative/version-2 versus absolute/version-1 mapping;
- `frontend/widgets/OBSBasic_SceneCollections.cpp`: private mode publication,
  version/resolution load decision, absolute save cleanup, and remigration behavior;
- `libobs/obs-scene.c`: scene/item mode capture plus relative and absolute load/save
  branches; and
- `libobs/obs.c`: private-data APIs, which do not update already-created scene/item
  flags or the frontend collection model.

## Cold baseline contract

The supported seed is exact and intentionally boring:

- top-level `version` is exactly `1`;
- `resolution` and `migration_resolution` are absent;
- no invented top-level `AbsoluteCoordinates` field is present;
- the shell contains no managed group/input/placement/filter graph;
- zero bootstrap scenes, or one exact named unmanaged empty bootstrap scene, is allowed;
- `pos_rel`, `scale_rel`, `scale_ref` and `bounds_rel` are absent at every nesting
  level; and
- the profile/live base canvas is exactly 1920x1080 before load and mutation.

An existing relative collection is not converted in place. The safe migration is an
explicit cold reset: preserve a backup, publish only the minimal version-1 shell under
an absent target (or replace only within separately authorized whole-collection scope),
atomically publish it while OBS and the WebSocket port are stopped, activate it, verify
runtime mode/canvas/identity, then let Terraform and the bridge create the declared
group graph. Imported relative-only metadata is not silently preserved. This is
acceptable for the authorized disposable Mac lab; it is not production/Windows
authorization.

Manual OBS migration, a base-canvas change, a saved version change, new migration
metadata, unknown mode, or relative mode makes group mutation unsupported until another
explicit reset. Filter/media operations that remain ordinary obs-websocket requests are
separate, but a Terraform plan containing group layout must fail closed rather than
partially apply around an unsupported graph.

The seed rule above is pre-load hygiene, not the post-save file invariant. libobs writes
`scale_ref` before its absolute/relative save branch for every scene item. The frontend's
absolute cleanup walks the top-level `sources` array while group sources are serialized
separately under `groups`; valid absolute group children can therefore retain
`scale_ref`. Absolute load ignores it. After OBS has created and saved the graph, the
persisted-artifact policy requires **zero** `scale_ref` fields on ordinary scene items
and derived group-backup records under `sources[*].settings.items`. It requires exactly
one `scale_ref` per true group child under `groups[*].settings.items`, with every value
equal to the fixed 1920x1080 canvas. It rejects a missing/extra/misplaced/different
`scale_ref`, including any top-level or otherwise unclassified occurrence, and continues
to reject `pos_rel`, `scale_rel` and `bounds_rel` everywhere.
This models OBS's post-save cleanup rather than the precompiled fixture's pre-save
shape.

## Prior evidence and remaining proof

The existing absolute fixture is evidence that the representation is viable, not the
permitted bootstrap shape and not acceptance of a new bridge build. It already contains
the group graph and therefore cannot prove bridge create-from-zero. Four prior native
runs used that version-1/no-resolution artifact and reported exact group/child geometry,
no numeric normalization, empty A2 plans/traces, and a consecutive saved-file hash
chain. Independent review accepted the representation within its recorded limits. The
fixture hash is
`77950bf05c6ebccb5bbb8211e454f7821c2779835f11540edc45b3cc3e47d737`;
the independent report is `coordination/ABSOLUTE-RECHECK.md`, hash
`ca67c5ad164dff2ec74f6e60c3b8a32a1be2ee5ab9b2b010b608c42f94e2e13c`.

After proposal review, the smallest bridge implementation is:

1. Replace the relative projection verifier with the pure absolute runtime predicate in
   `proposal/absolute_mode_policy.h` and raw-exact placement comparison.
2. Return the exact runtime decision/reason in `GetCapabilities` and every placement
   receipt; remove projection/canonicalization claims.
3. Apply the absolute precondition before every structural mutation and save checkpoint.
4. Keep all existing strict types, identity checks, uncertainty semantics, UI-task
   execution, post-frame requirements and default-aware settings comparison.
5. Rebuild and obtain a fresh offline review before any load.
6. In a fresh absent absolute lab seeded with only the minimal shell/bootstrap, use
   Terraform plus the bridge to create the group and children from zero. Prove exact
   create/children/render and at least three no-manual-rewrite save/reload cycles, each
   with version 1, absent migration metadata and absent `pos_rel`/`scale_rel`/
   `bounds_rel`, plus the exact post-save `scale_ref` rule: none under `sources`, one
   fixed-canvas value per true child under `groups`.
   Require live 1920x1080, exact local/world/sibling geometry, stable identities and a
   true Terraform zero-write second apply after the provider contract blockers are
   closed. Then exercise the remaining lifecycle callbacks under their existing
   authorization.
7. Negative-test a version-1 seed with `resolution`, a relative runtime, unknown mode,
   wrong canvas and another OBS version; every case must refuse before mutation.

Any raw numeric drift, automatic version-2 save, migration metadata, nonempty
normalization, unexpected write on A2, identity loss or widened policy rejects the design.

## Pure proposal tests

`proposal/absolute_mode_policy.h` is not wired into the plugin. It exists so the policy
can be reviewed and falsified before implementation. `tests/absolute_mode_policy_test.c`
first reproduces the exact relative sibling recurrence and one-ULP failure, then executes
the absolute seed, runtime and completion truth tables. Seed cases distinguish an empty
shell/one exact empty unmanaged bootstrap from a precompiled managed graph, populated or
unrecognized bootstrap, multiple bootstraps and every forbidden metadata family including
seed-time `scale_ref`. Separate persisted-artifact cases accept exact 1920x1080
`scale_ref` values only for true group children, model four ordinary/backup items plus
two true children, and reject all-items, missing, extra or wrong-value shapes while
also rejecting top-level/unclassified locations and every actual relative transform
field. The runtime cases include the critical rule that a raw-equal placement in
relative mode cannot verify.

Run without rebuilding the plugin:

```sh
mkdir -p build
xcrun clang -std=c11 -Wall -Wextra -Werror -Wpedantic \
  tests/absolute_mode_policy_test.c -o build/absolute-mode-policy-test
./build/absolute-mode-policy-test
```
