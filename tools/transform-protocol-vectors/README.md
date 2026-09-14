# OBS transform protocol vectors

This directory pins the small set of OBS transform facts needed by the Luna
wire regression. It is deliberately not a copy of the generated OBS protocol
file and it is not generated from the Go encoder. The Go test drives the real
`groupResource.Update` callback through a capture requester, then checks the
captured request against the intended wire semantics independently.

The vectors target obs-websocket 5.7.4:

- [generated protocol](https://github.com/obsproject/obs-websocket/blob/5.7.4/docs/generated/protocol.json)
- [scene-item request handler](https://github.com/obsproject/obs-websocket/blob/5.7.4/src/requesthandler/RequestHandler_SceneItems.cpp)
- [request validation helpers](https://github.com/obsproject/obs-websocket/blob/5.7.4/src/requesthandler/rpc/Request.cpp)

The repository-local protocol pin is
`native-bridge/build/deps/obs-websocket-api-5.7.4.h`, SHA-256
`c29e8e38ee66c36db79cae86217ab0f270aad39f97e82090809d087c281b7948`.
The local contract used alongside that header is
`native-bridge/CONTRACT.md`, SHA-256
`2cd75b5478759f7b16cc99d43f5ff4be362e7cc2b8ac37ce5e5093f769917911`.

The source-derived rules represented here are:

- `SetSceneItemTransform` has optional scene identity fields, a required
  numeric `sceneItemId` (protocol minimum 0), and a required transform object.
- Transform properties are JSON numbers, strings, or booleans as listed in
  `set-scene-item-transform.vectors.json`.
- OBS accepts position values from -90001 through 90001, rotation from -360
  through 360, alignment values through the uint32 maximum, bounds dimensions
  from 1 through 90001, and crop values from 0 through 100000, inclusive.
  Scale acceptance depends on the source dimensions and resulting geometry.
- `OBS_BOUNDS_NONE` is a valid bounds type, but obs-websocket validates
  explicitly supplied bounds dimensions before applying the type. The native
  readback contract reports zero width and height for NONE, so the core
  WebSocket request omits each zero dimension individually while retaining
  any positive dimension and the raw model values.
- Omitted transform/crop fields preserve the corresponding native value.
  Enabled, locked, and index are separate scene-item properties and are not
  members of the transform object.

The numeric boundary table distinguishes protocol acceptance from the current
provider guard. The provider currently narrows alignment and
`boundsAlignment` to 0 through 10, permits crop values through the signed
int32 maximum, and validates finite float32 values without reproducing every
OBS position/rotation endpoint. The table must not be read as silently
changing that provider policy.

2026-09-13: contract pin refreshed for additive bridge 0.1.4 DescribeFilter; diff review confirmed group/transform semantics unchanged. Original vector/contract remain in the demo003 frozen workspace.
