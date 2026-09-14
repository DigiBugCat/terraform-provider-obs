package tfprovider

// Shared harness for the Terraform CLI regression tests: they run the real terraform
// binary against this provider built from the working tree, talking to a scripted
// obs-websocket fake served over a local websocket.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// pinnedTerraform returns the repository root and the pinned Terraform binary, skipping
// the test when the binary has not been fetched into .tools.
func pinnedTerraform(t *testing.T) (root, tf string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	root = filepath.Clean(filepath.Join(filepath.Dir(thisFile), ".."))
	tf = filepath.Join(root, ".tools", "terraform", "1.15.7", "terraform")
	if _, err := os.Stat(tf); err != nil {
		t.Skipf("pinned Terraform CLI unavailable: %v", err)
	}
	return root, tf
}

var (
	providerBuildOnce sync.Once
	providerBuildDir  string
	providerBuildErr  error
)

// builtProvider builds the provider binary once per test process and returns the
// directory holding it, ready for a dev_overrides entry.
func builtProvider(t *testing.T, root string) string {
	t.Helper()
	providerBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "terraform-provider-obs-test-")
		if err != nil {
			providerBuildErr = err
			return
		}
		build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "terraform-provider-obs"), ".")
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			providerBuildErr = fmt.Errorf("build provider: %v\n%s", err, out)
			return
		}
		providerBuildDir = dir
	})
	if providerBuildErr != nil {
		t.Fatal(providerBuildErr)
	}
	return providerBuildDir
}

// writeDevOverrides writes a CLI config pointing digibugcat/obs at providersDir and
// returns its path for TF_CLI_CONFIG_FILE.
func writeDevOverrides(t *testing.T, work, providersDir string) string {
	t.Helper()
	rc := filepath.Join(work, "terraform.rc")
	body := fmt.Sprintf("provider_installation {\n  dev_overrides {\n    %q = %q\n  }\n  direct {}\n}\n", "registry.terraform.io/digibugcat/obs", providersDir)
	if err := os.WriteFile(rc, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return rc
}

func newFakeOBSWebsocketServer(t *testing.T, fake requester) *httptest.Server {
	var serveMu sync.Mutex
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := context.Background()
		if err := writeWebsocketEnvelope(ctx, conn, 0, map[string]any{"obsWebSocketVersion": "5.7.4", "rpcVersion": 1}); err != nil {
			return
		}
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var identify struct {
			Op int `json:"op"`
		}
		if err := json.Unmarshal(payload, &identify); err != nil || identify.Op != 1 {
			return
		}
		if err := writeWebsocketEnvelope(ctx, conn, 2, map[string]any{"negotiatedRpcVersion": 1}); err != nil {
			return
		}
		for {
			_, payload, err = conn.Read(ctx)
			if err != nil {
				return
			}
			var envelope struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				return
			}
			serve := func(typ string, data map[string]any) (map[string]any, map[string]any) {
				serveMu.Lock()
				responseData, requestErr := fake.Req(ctx, typ, data)
				serveMu.Unlock()
				status := map[string]any{"result": requestErr == nil, "code": 100}
				if requestErr != nil {
					status["code"], status["comment"] = 500, requestErr.Error()
				}
				return status, responseData
			}
			switch envelope.Op {
			case 6:
				var request struct {
					Type string         `json:"requestType"`
					ID   string         `json:"requestId"`
					Data map[string]any `json:"requestData"`
				}
				if err := json.Unmarshal(envelope.D, &request); err != nil {
					return
				}
				status, responseData := serve(request.Type, request.Data)
				if err := writeWebsocketEnvelope(ctx, conn, 7, map[string]any{"requestType": request.Type, "requestId": request.ID, "requestStatus": status, "responseData": responseData}); err != nil {
					return
				}
			case 8: // RequestBatch: positional results, optional halt on first failure
				var batch struct {
					ID       string `json:"requestId"`
					Halt     bool   `json:"haltOnFailure"`
					Requests []struct {
						Type string         `json:"requestType"`
						Data map[string]any `json:"requestData"`
					} `json:"requests"`
				}
				if err := json.Unmarshal(envelope.D, &batch); err != nil {
					return
				}
				results := []any{}
				for _, item := range batch.Requests {
					status, responseData := serve(item.Type, item.Data)
					results = append(results, map[string]any{"requestType": item.Type, "requestStatus": status, "responseData": responseData})
					if batch.Halt && status["result"] == false {
						break
					}
				}
				if err := writeWebsocketEnvelope(ctx, conn, 9, map[string]any{"requestId": batch.ID, "results": results}); err != nil {
					return
				}
			default:
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeWebsocketEnvelope(ctx context.Context, conn *websocket.Conn, op int, data any) error {
	payload, err := json.Marshal(map[string]any{"op": op, "d": data})
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}
