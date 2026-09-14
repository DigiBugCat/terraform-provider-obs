package tfprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTerraformCLIGenericFilterNoopAndDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI regression")
	}
	root, _ := filepath.Abs("../..")
	tf := filepath.Join(root, ".tools/terraform/1.15.7/terraform")
	if _, e := os.Stat(tf); e != nil {
		t.Skip(e)
	}
	f := &genericFilterFake{}
	server := newGenericFilterWebsocketServer(t, f)
	u, _ := url.Parse(server.URL)
	work := t.TempDir()
	providers := filepath.Join(work, "providers")
	os.Mkdir(providers, 0700)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(providers, "terraform-provider-obs"), ".")
	cmd.Dir = root
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build %v %s", e, out)
	}
	rc := filepath.Join(work, "terraform.rc")
	os.WriteFile(rc, []byte(fmt.Sprintf(`provider_installation { dev_overrides { "registry.terraform.io/digibugcat/obs" = %q } direct {} }`, providers)), 0600)
	config := fmt.Sprintf(`terraform {
 required_providers { obs = { source = "digibugcat/obs" } }
}
provider "obs" {
 host = "127.0.0.1"
 port = %s
 password = "fake"
}
resource "obs_filter" "test" {
 collection = "lab"
 parent_name = "parent"
 parent_uuid = "p"
 name = "fx"
 kind = "plugin_filter"
 settings_json = "{ \"gain\": 2.0 }"
 enabled = false
 index = 0
}
`, u.Port())
	os.WriteFile(filepath.Join(work, "main.tf"), []byte(config), 0600)
	run := func(args ...string) {
		t.Helper()
		c := exec.Command(tf, args...)
		c.Dir = work
		c.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1")
		if out, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%v: %v\n%s", args, e, out)
		}
	}
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	f.filter.Settings["gain"] = 9.
	f.filter.Settings["unmanaged"] = "keep"
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	if f.filter.Settings["unmanaged"] != "keep" {
		t.Fatal("unmanaged setting lost")
	}
}
func newGenericFilterWebsocketServer(t *testing.T, fake requester) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := context.Background()
		if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 0, map[string]any{"obsWebSocketVersion": "5.7.4", "rpcVersion": 1}); err != nil {
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
		if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 2, map[string]any{"negotiatedRpcVersion": 1}); err != nil {
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
				responseData, requestErr := fake.Req(ctx, typ, data)
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
				if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 7, map[string]any{"requestType": request.Type, "requestId": request.ID, "requestStatus": status, "responseData": responseData}); err != nil {
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
				if err := writeGroupGraphWebsocketEnvelope(ctx, conn, 9, map[string]any{"requestId": batch.ID, "results": results}); err != nil {
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
