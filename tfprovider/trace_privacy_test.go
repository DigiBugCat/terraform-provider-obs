package tfprovider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSceneCanBeExactFilterParent(t *testing.T) {
	f := &fakeRequester{responses: map[string]map[string]any{
		"GetInputList":     {"inputs": []any{}},
		"GetSceneList":     {"scenes": []any{map[string]any{"sceneName": "Office", "sceneUuid": "office"}}},
		"GetSceneItemList": {"sceneItems": []any{}},
	}}
	if found, err := existingSourceIdentity(context.Background(), f, "Office", "office"); err != nil || !found {
		t.Fatalf("scene parent: %v %v", found, err)
	}
	if _, err := existingSourceIdentity(context.Background(), f, "Office", "wrong"); err == nil {
		t.Fatal("scene identity collision accepted")
	}
}

func TestTraceSettingsAreRedactedWithoutChangingRequest(t *testing.T) {
	settings := map[string]any{"arbitrary_plugin_key": "secret-canary"}
	request := map[string]any{"request_type": "SetSourceFilterSettings", "request_data": map[string]any{"filterName": "Plugin", "filterSettings": settings}, "response": map[string]any{"inputSettings": settings}}
	b, err := json.Marshal(redactTraceSettings(request))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-canary") || strings.Contains(string(b), "arbitrary_plugin_key") {
		t.Fatal("private settings leaked into trace")
	}
	if !strings.Contains(string(b), "SetSourceFilterSettings") || !strings.Contains(string(b), "Plugin") {
		t.Fatal("operation identity lost")
	}
	if settings["arbitrary_plugin_key"] != "secret-canary" {
		t.Fatal("trace redaction changed native request")
	}
	nested, _ := json.Marshal(redactTraceSettings(map[string]any{"entries": []any{map[string]any{"password": "secret-canary"}}}))
	if strings.Contains(string(nested), "secret-canary") {
		t.Fatal("nested password leaked")
	}
}

func TestExplicitBridgeVersionCompatibility(t *testing.T) {
	for _, version := range []string{"0.1.3", "0.1.4"} {
		if !supportedBridgeVersion(version) {
			t.Fatalf("compatible bridge %s rejected", version)
		}
	}
	for _, version := range []string{"", "0.1.2", "0.1.5", "0.2.0"} {
		if supportedBridgeVersion(version) {
			t.Fatalf("unqualified bridge %s accepted", version)
		}
	}
}
