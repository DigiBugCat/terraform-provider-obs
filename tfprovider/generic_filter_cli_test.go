package tfprovider

import (
	"fmt"
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
	root, tf := pinnedTerraform(t)
	f := &genericFilterFake{}
	server := newFakeOBSWebsocketServer(t, f)
	u, _ := url.Parse(server.URL)
	work := t.TempDir()
	providers := builtProvider(t, root)
	rc := writeDevOverrides(t, work, providers)
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
