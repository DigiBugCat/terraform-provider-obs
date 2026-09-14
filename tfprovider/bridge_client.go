package tfprovider

import (
	"context"
	"fmt"
	"os"
	"time"
)

const bridgeContractVersion int64 = 1

// The OBS build the bridge contract and the absolute-coordinate preflight are qualified
// against. Bumping OBS means re-qualifying both, so the values live in one place.
const (
	qualifiedOBSVersion  = "32.2.2"
	qualifiedWSVersion   = "5.7.4"
	requiredCanvasWidth  = 1920
	requiredCanvasHeight = 1080
)

func supportedBridgeVersion(version string) bool {
	return version == "0.1.3" || version == "0.1.4"
}

type bridgeEnvelope struct {
	ContractVersion       int64
	BridgeVersion         string
	RequestType           string
	ClientOperationID     string
	Outcome               string
	Code                  string
	Message               string
	MutationAttempted     bool
	PostconditionVerified bool
	Result                map[string]any
}

func parseBridgeEnvelope(outer map[string]any, requestType, operationID string) (bridgeEnvelope, error) {
	raw, ok := outer["responseData"].(map[string]any)
	if !ok {
		return bridgeEnvelope{}, fmt.Errorf("CallVendorRequest omitted responseData bridge envelope")
	}
	contract, cok := numberValue(raw["contractVersion"])
	mutationAttempted, mok := raw["mutationAttempted"].(bool)
	postconditionVerified, pok := raw["postconditionVerified"].(bool)
	e := bridgeEnvelope{
		ContractVersion: int64(contract), BridgeVersion: stringValue(raw["bridgeVersion"]),
		RequestType: stringValue(raw["requestType"]), ClientOperationID: stringValue(raw["clientOperationId"]),
		Outcome: stringValue(raw["outcome"]), Code: stringValue(raw["code"]), Message: stringValue(raw["message"]),
		MutationAttempted: mutationAttempted, PostconditionVerified: postconditionVerified, Result: mapValue(raw["result"]),
	}
	if !cok || contract != float64(bridgeContractVersion) || !supportedBridgeVersion(e.BridgeVersion) || e.RequestType != requestType || !mok || !pok || e.Code == "" || e.Message == "" {
		return e, fmt.Errorf("bridge envelope identity/status fields are incomplete or mismatched")
	}
	if e.ClientOperationID != operationID {
		return e, fmt.Errorf("bridge echoed clientOperationId=%q, want %q", e.ClientOperationID, operationID)
	}
	switch e.Outcome {
	case "completed":
		if !e.PostconditionVerified || e.Code != "ok" || e.Result == nil || (operationID != "" && !e.MutationAttempted) || (operationID == "" && e.MutationAttempted) {
			return e, fmt.Errorf("bridge reported completed without verified postcondition")
		}
	case "failed":
		if !e.PostconditionVerified || e.Code == "ok" {
			return e, fmt.Errorf("bridge reported failed without a verified non-ok result")
		}
	case "uncertain":
		if e.PostconditionVerified || !e.MutationAttempted || operationID == "" || e.Code == "ok" {
			return e, fmt.Errorf("bridge reported uncertain with verified postcondition")
		}
	default:
		return e, fmt.Errorf("bridge outcome=%q is invalid", e.Outcome)
	}
	return e, nil
}

func bridgeRequestData(data map[string]any, operationID string) map[string]any {
	out := make(map[string]any, len(data)+2)
	for key, value := range data {
		out[key] = value
	}
	out["contractVersion"] = bridgeContractVersion
	if operationID != "" {
		out["clientOperationId"] = operationID
	}
	return out
}

func bridgeRead(ctx context.Context, cl requester, requestType string, data map[string]any) (bridgeEnvelope, error) {
	outer, err := request(ctx, cl, "CallVendorRequest", map[string]any{"vendorName": "obstf", "requestType": requestType, "requestData": bridgeRequestData(data, "")})
	if err != nil {
		return bridgeEnvelope{}, err
	}
	e, err := parseBridgeEnvelope(outer, requestType, "")
	if traceErr := recordTrace(map[string]any{"event": "bridge-envelope", "request_type": requestType, "outcome": e.Outcome, "code": e.Code, "mutation_attempted": e.MutationAttempted, "postcondition_verified": e.PostconditionVerified, "bridge_version": e.BridgeVersion}); traceErr != nil {
		return e, fmt.Errorf("bridge read receipt failed: %w", traceErr)
	}
	return e, err
}

func bridgeMutation(ctx context.Context, cl requester, collection, requestType, operationID string, data map[string]any) (bridgeEnvelope, error) {
	if operationID == "" {
		return bridgeEnvelope{}, fmt.Errorf("bridge mutation operation ID is required")
	}
	if _, err := bridgeCapabilities(ctx, cl, collection, requestType, true); err != nil {
		return bridgeEnvelope{}, err
	}
	requestData := bridgeRequestData(data, operationID)
	outerData := map[string]any{"vendorName": "obstf", "requestType": requestType, "requestData": requestData}
	guard, guardErr := scopedPreflight(ctx, cl, collection, "CallVendorRequest")
	entry := map[string]any{"event": "mutation", "at": time.Now().UTC(), "request_type": "CallVendorRequest/obstf:" + requestType, "transport_request_type": "CallVendorRequest", "request_data": outerData, "preflight": guard, "native_sent": false}
	if guardErr != nil {
		entry["status"], entry["error"] = "refused", guardErr.Error()
		return bridgeEnvelope{}, recordReceipt(entry, guardErr)
	}
	outer, transportErr := request(ctx, cl, "CallVendorRequest", outerData)
	entry["native_sent"], entry["outer_response"] = true, outer
	if transportErr != nil {
		entry["status"], entry["error"] = "failed-or-uncertain", transportErr.Error()
		return bridgeEnvelope{}, recordReceipt(entry, transportErr)
	}
	e, parseErr := parseBridgeEnvelope(outer, requestType, operationID)
	entry["bridge_envelope"] = map[string]any{"request_type": requestType, "client_operation_id": operationID, "outcome": e.Outcome, "code": e.Code, "message": e.Message, "mutation_attempted": e.MutationAttempted, "postcondition_verified": e.PostconditionVerified, "bridge_version": e.BridgeVersion, "result": e.Result}
	if parseErr != nil {
		entry["status"], entry["error"] = "failed-or-uncertain", parseErr.Error()
	} else {
		switch e.Outcome {
		case "completed":
			entry["status"] = "completed"
		case "failed":
			if e.MutationAttempted {
				entry["status"] = "failed-verified"
			} else {
				entry["status"] = "failed-no-write"
			}
		case "uncertain":
			entry["status"] = "uncertain"
		}
	}
	if traceErr := recordTrace(entry); traceErr != nil {
		return e, fmt.Errorf("bridge mutation receipt failed: %w", traceErr)
	}
	if parseErr == nil && e.Outcome == "completed" && (os.Getenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE") == "CallVendorRequest" || os.Getenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE") == "obstf:"+requestType) {
		return e, &authoritativeNativeInterruptionError{requestType: "obstf:" + requestType}
	}
	return e, parseErr
}

// bridgeOutcome folds a bridge mutation's transport error, envelope outcome and result
// parsing into one verdict. trusted is true only for a completed envelope whose result
// parsed, the only state that may seed identity into Terraform state; err is what the
// caller returns: nil, the outcome or parse error, or the injected interruption after a
// completed write (which callers must still surface after checkpointing).
func bridgeOutcome[T any](requestType string, envelope bridgeEnvelope, callErr error, parse func(map[string]any) (T, error)) (result T, trusted bool, err error) {
	if callErr != nil && !isAuthoritativeNativeInterruption(callErr) {
		return result, false, callErr
	}
	if envelope.Outcome != "completed" {
		return result, false, fmt.Errorf("bridge %s %s (%s): %s; re-observe before retry", requestType, envelope.Outcome, envelope.Code, envelope.Message)
	}
	result, parseErr := parse(envelope.Result)
	if parseErr != nil {
		return result, false, parseErr
	}
	return result, true, callErr
}

func bridgeCapabilities(ctx context.Context, cl requester, collection, requiredRequest string, requireAbsoluteMutation bool) (bridgeEnvelope, error) {
	ordinary, err := request(ctx, cl, "GetVersion", nil)
	if err != nil {
		return bridgeEnvelope{}, err
	}
	rpc, rpcOK := integerNumber(ordinary["rpcVersion"])
	if stringValue(ordinary["obsVersion"]) != qualifiedOBSVersion || stringValue(ordinary["obsWebSocketVersion"]) != qualifiedWSVersion || !rpcOK || rpc != 1 {
		return bridgeEnvelope{}, fmt.Errorf("ordinary obs-websocket version identity mismatch")
	}
	callVendorAvailable := false
	for _, raw := range sliceValue(ordinary["availableRequests"]) {
		if stringValue(raw) == "CallVendorRequest" {
			callVendorAvailable = true
		}
	}
	if !callVendorAvailable {
		return bridgeEnvelope{}, fmt.Errorf("ordinary obs-websocket does not advertise CallVendorRequest")
	}
	e, err := bridgeRead(ctx, cl, "GetCapabilities", nil)
	if err != nil {
		return e, err
	}
	if e.Outcome != "completed" {
		return e, fmt.Errorf("bridge GetCapabilities outcome=%s code=%s: %s", e.Outcome, e.Code, e.Message)
	}
	result := e.Result
	api, aok := numberValue(result["obsWebSocketApiVersion"])
	contract, cok := integerNumber(result["contractVersion"])
	baseWidth, bwok := integerNumber(result["baseWidth"])
	baseHeight, bhok := integerNumber(result["baseHeight"])
	coordinateModeKnown, cmok := result["coordinateModeKnown"].(bool)
	absoluteCoordinates, acok := result["absoluteCoordinates"].(bool)
	mainCanvasKnown, mcok := result["mainCanvasKnown"].(bool)
	// Checked one field at a time so a bridge upgrade or a wrong collection names the
	// field that disagreed instead of one opaque mismatch.
	switch {
	case stringValue(result["vendorName"]) != "obstf":
		return e, fmt.Errorf("bridge capability vendorName=%q, want obstf", stringValue(result["vendorName"]))
	case !cok || contract != bridgeContractVersion:
		return e, fmt.Errorf("bridge capability contractVersion=%v, want %d", result["contractVersion"], bridgeContractVersion)
	case !supportedBridgeVersion(stringValue(result["bridgeVersion"])):
		return e, fmt.Errorf("bridge capability bridgeVersion=%q is not a supported bridge", stringValue(result["bridgeVersion"]))
	case stringValue(result["currentSceneCollectionName"]) != collection:
		return e, fmt.Errorf("bridge capability currentSceneCollectionName=%q, want %q", stringValue(result["currentSceneCollectionName"]), collection)
	case stringValue(result["obsVersion"]) != qualifiedOBSVersion:
		return e, fmt.Errorf("bridge capability obsVersion=%q, want %s", stringValue(result["obsVersion"]), qualifiedOBSVersion)
	case !aok || api != 3:
		return e, fmt.Errorf("bridge capability apiVersion=%v, want 3", result["apiVersion"])
	case stringValue(result["platform"]) == "" || stringValue(result["architecture"]) == "":
		return e, fmt.Errorf("bridge capability omitted platform or architecture")
	case !boolValue(result["outputStateObserved"]):
		return e, fmt.Errorf("bridge capability did not observe output state")
	case !bwok || !bhok || !cmok || !acok || !mcok:
		return e, fmt.Errorf("bridge capability omitted canvas or coordinate-mode fields")
	}
	for _, key := range []string{"streamingActive", "recordingActive", "replayBufferActive", "virtualCameraActive"} {
		value, ok := result[key].(bool)
		if !ok || value {
			return e, fmt.Errorf("bridge capability %s is active or unknown", key)
		}
	}
	guard := mapValue(result["absoluteRuntimeGuard"])
	if guard == nil {
		return e, fmt.Errorf("bridge capability omitted absolute runtime guard")
	}
	guardSupported, gsok := guard["supported"].(bool)
	guardCoordinateKnown, gcok := guard["coordinateModeKnown"].(bool)
	guardAbsolute, gaok := guard["absoluteCoordinates"].(bool)
	guardCanvasKnown, gmok := guard["mainCanvasKnown"].(bool)
	guardWidth, gwok := integerNumber(guard["baseWidth"])
	guardHeight, ghok := integerNumber(guard["baseHeight"])
	guardStatus := stringValue(guard["status"])
	guardComparison := stringValue(guard["comparisonMode"])
	if !gsok || !gcok || !gaok || !gmok || !gwok || !ghok || stringValue(guard["obsVersion"]) != qualifiedOBSVersion ||
		guardCoordinateKnown != coordinateModeKnown || guardAbsolute != absoluteCoordinates || guardCanvasKnown != mainCanvasKnown ||
		guardWidth != baseWidth || guardHeight != baseHeight {
		return e, fmt.Errorf("bridge absolute runtime guard is malformed or disagrees with capability fields")
	}
	allowedGuardStatus := map[string]bool{"supported": true, "wrong_obs_version": true, "coordinate_mode_unknown": true, "relative_mode_unsupported": true, "main_canvas_unknown": true, "wrong_main_canvas": true}
	if !allowedGuardStatus[guardStatus] || (guardSupported && (guardStatus != "supported" || guardComparison != "absolute-raw-exact")) || (!guardSupported && (guardStatus == "supported" || guardComparison != "ineligible")) {
		return e, fmt.Errorf("bridge absolute runtime guard status/comparison is inconsistent")
	}
	found := false
	for _, raw := range sliceValue(result["requests"]) {
		entry := mapValue(raw)
		if stringValue(entry["requestType"]) == requiredRequest {
			mutation, mok := entry["mutation"].(bool)
			requiresAbsolute, raok := entry["requiresAbsoluteRuntime"].(bool)
			wantMutation := bridgeRequestRequiresAbsoluteRuntime(requiredRequest)
			if !mok || !raok || mutation != wantMutation || requiresAbsolute != wantMutation {
				return e, fmt.Errorf("bridge request descriptor for %s is inconsistent", requiredRequest)
			}
			found = true
		}
	}
	if !found {
		return e, fmt.Errorf("bridge does not advertise %s", requiredRequest)
	}
	supports := mapValue(result["supports"])
	absoluteGraph, agok := supports["absoluteGroupGraphMutations"].(bool)
	relativeGraph, rgok := supports["relativeGroupGraphMutations"].(bool)
	canonicalization, pcok := supports["placementCanonicalization"].(bool)
	if !agok || !rgok || !pcok || relativeGraph || canonicalization || absoluteGraph != guardSupported {
		return e, fmt.Errorf("bridge absolute group policy capabilities are incomplete or inconsistent")
	}
	if requireAbsoluteMutation && (!absoluteGraph || !coordinateModeKnown || !absoluteCoordinates || !mainCanvasKnown || baseWidth != 1920 || baseHeight != 1080) {
		return e, fmt.Errorf("bridge absolute mutation runtime is unsupported: %s", guardStatus)
	}
	requiredSupport := map[string]bool{
		"realGroupCreate": true, "strictJsonRequestTypes": true, "directInputCreateInGroup": true,
		"declaredInputSettingsPostcondition": true, "directExistingSourcePlacementInGroup": true, "moveParentItemToGroup": true,
		"exactGroupChildObservation": true, "groupChildUpdate": false, "groupChildUpdateViaCoreWebSocket": true,
		"groupChildPlacementRemoval": true, "moveChildToParent": true, "ungroup": true,
		"ungroupReturnsReassignedItemIds": true, "sourceDelete": false, "frontendSaveCheckpoint": true, "diskDurabilityReceipt": false,
	}
	for key, want := range requiredSupport {
		got, ok := supports[key].(bool)
		if !ok || got != want {
			return e, fmt.Errorf("bridge capability %s is missing or mismatched", key)
		}
	}
	execution := mapValue(result["execution"])
	requestSynchronous, rsok := execution["requestSynchronous"].(bool)
	multiRequestAtomic, maok := execution["multiRequestAtomic"].(bool)
	compareAndSwap, caok := execution["compareAndSwap"].(bool)
	if stringValue(execution["vendorCallbackThread"]) == "" || stringValue(execution["graphOperationThread"]) != "OBS_TASK_UI" || !rsok || !requestSynchronous || !maok || multiRequestAtomic || !caok || compareAndSwap {
		return e, fmt.Errorf("bridge execution model is missing or unsupported")
	}
	return e, nil
}

func bridgeRequestRequiresAbsoluteRuntime(requestType string) bool {
	switch requestType {
	case "CreateGroup", "CreateInputInGroup", "AddSourceToGroup", "MoveParentItemToGroup", "RemoveGroupChildPlacement", "MoveGroupChildToParent", "Ungroup", "SaveSceneCollectionCheckpoint":
		return true
	case "GetCapabilities", "GetGroup":
		return false
	default:
		return false
	}
}
