package tfprovider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

// batchingFake answers RequestBatch positionally and never sees the plain Req path for
// the batched types, so the receipt written by mutationBatch is the one under test.
type batchingFake struct{ calls []string }

func (f *batchingFake) Req(_ context.Context, typ string, _ map[string]any) (map[string]any, error) {
	f.calls = append(f.calls, typ)
	switch typ {
	case "GetVersion":
		return map[string]any{"obsVersion": "32.2.2", "obsWebSocketVersion": "5.7.4", "availableRequests": []any{"SetSourceFilterSettings", "SetSourceFilterEnabled", "GetStreamStatus", "GetRecordStatus", "GetVirtualCamStatus", "GetReplayBufferStatus"}}, nil
	case "GetSceneCollectionList":
		return map[string]any{"currentSceneCollectionName": "Lab"}, nil
	case "GetVideoSettings":
		return map[string]any{"baseWidth": 1920.0, "baseHeight": 1080.0}, nil
	case "GetStreamStatus", "GetRecordStatus", "GetVirtualCamStatus", "GetReplayBufferStatus":
		return map[string]any{"outputActive": false}, nil
	}
	return map[string]any{}, nil
}

func (f *batchingFake) Batch(_ context.Context, items []ws.BatchItem, _ int, _ bool) ([]ws.BatchResult, error) {
	out := make([]ws.BatchResult, len(items))
	for i := range items {
		f.calls = append(f.calls, "batch:"+items[i].Type)
		out[i] = ws.BatchResult{OK: true, Data: map[string]any{}}
	}
	return out, nil
}

func (f *batchingFake) preflightScope() *preflightScope { return &preflightScope{} }

func TestMutationBatchReceiptRedactsSettings(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("OBSTF_TF_TRACE_PATH", trace)
	t.Setenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE", "")
	fake := &batchingFake{}
	const canary = "hunter2-super-secret-key-value"
	_, err := mutationBatch(context.Background(), fake, "Lab", []ws.BatchItem{
		{Type: "SetSourceFilterSettings", Data: map[string]any{"sourceUuid": "u", "filterName": "f", "filterSettings": map[string]any{"api_key": canary}}},
		{Type: "SetSourceFilterEnabled", Data: map[string]any{"sourceUuid": "u", "filterName": "f", "filterEnabled": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), canary) {
		t.Fatalf("batch receipt leaked filter settings:\n%s", b)
	}
	if !strings.Contains(string(b), `"request_type":"RequestBatch"`) || !strings.Contains(string(b), `"$redacted":true`) {
		t.Fatalf("receipt missing batch marker or redaction:\n%s", b)
	}
	if got := strings.Join(fake.calls, ","); !strings.Contains(got, "batch:SetSourceFilterSettings,batch:SetSourceFilterEnabled") {
		t.Fatalf("expected one RequestBatch with both items, got %s", got)
	}
}

func TestMutationBatchRejectsOversizedReply(t *testing.T) {
	t.Setenv("OBSTF_TF_TRACE_PATH", "")
	over := &oversizedBatchFake{batchingFake: &batchingFake{}}
	_, err := mutationBatch(context.Background(), over, "Lab", []ws.BatchItem{{Type: "SetSourceFilterEnabled", Data: map[string]any{}}})
	if err == nil || !strings.Contains(err.Error(), "returned 2 results for 1 requests") {
		t.Fatalf("want oversized-reply error, got %v", err)
	}
}

type oversizedBatchFake struct{ *batchingFake }

func (f *oversizedBatchFake) Batch(_ context.Context, items []ws.BatchItem, _ int, _ bool) ([]ws.BatchResult, error) {
	return []ws.BatchResult{{OK: true}, {OK: false, Code: 600, Comment: "extra"}}, nil
}
