package tfprovider

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTerraformCLIPluginPlanInstallDriftAndRetain(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI regression")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("installer deliberately supports macOS arm64 only")
	}
	if out, e := exec.Command("/bin/ps", "-axo", "comm=").Output(); e != nil {
		t.Skip("cannot establish stopped OBS for scratch install test")
	} else {
		for _, line := range strings.Split(string(out), "\n") {
			s := strings.TrimSpace(line)
			if strings.EqualFold(filepath.Base(s), "obs") || strings.Contains(strings.ToLower(s), "obs.app/contents/macos/") {
				t.Skip("OBS is running; scratch installer still requires stopped OBS")
			}
		}
	}
	root, tf := pinnedTerraform(t)
	work, _ := filepath.EvalSymlinks(t.TempDir())
	providers := builtProvider(t, root)
	rc := writeDevOverrides(t, work, providers)
	source := filepath.Join(work, "source.plugin")
	os.Mkdir(source, 0755)
	payload := []byte("synthetic plugin payload")
	os.WriteFile(filepath.Join(source, "payload"), payload, 0644)
	mf := filepath.Join(work, "manifest.json")
	manifest := fmt.Sprintf(`{"version":1,"os":"darwin","arch":"arm64","plugins":[{"id":"fixture","version":"1","bundle":"fixture.plugin","format":"bundle","source":%q,"files":{"payload":{"sha256":"%x","mode":420}}}]}`, source, sha256.Sum256(payload))
	os.WriteFile(mf, []byte(manifest), 0600)
	target := filepath.Join(work, "installation")
	config := fmt.Sprintf(`terraform {
 required_providers { obs = { source = "digibugcat/obs" } }
}
provider "obs" {}
resource "obs_plugin" "test" {
 manifest_path = %q
 manifest_sha256 = "%x"
 plugin_id = "fixture"
 version = "1"
 install_dir = %q
}
`, mf, sha256.Sum256([]byte(manifest)), target)
	main := filepath.Join(work, "main.tf")
	os.WriteFile(main, []byte(config), 0600)
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command(tf, args...)
		c.Dir = work
		c.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1")
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%v: %v\n%s", args, e, out)
		}
		return string(out)
	}
	run("plan", "-input=false", "-no-color", "-out=install.plan")
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("plan mutated installation directory", e)
	}
	run("apply", "-input=false", "-no-color", "install.plan")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	installed := filepath.Join(target, "fixture.plugin", "payload")
	os.WriteFile(installed, []byte("drift"), 0644)
	plan := run("plan", "-input=false", "-no-color", "-out=repair.plan")
	if !strings.Contains(plan, "false -> true") {
		t.Fatal("drift did not produce visible install repair", plan)
	}
	b, _ := os.ReadFile(installed)
	if string(b) != "drift" {
		t.Fatal("plan repaired files")
	}
	run("apply", "-input=false", "-no-color", "repair.plan")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	matches, _ := filepath.Glob(filepath.Join(target, ".obs-plugin-backup-*", "fixture.plugin", "payload"))
	if len(matches) != 1 {
		t.Fatal("replacement backup missing", matches)
	}
	// Old refresh must not read the now-edited manifest; pins are retained in state.
	updated := strings.Replace(manifest, `"version":"1"`, `"version":"2"`, 1)
	os.WriteFile(mf, []byte(updated), 0600)
	config = strings.Replace(config, fmt.Sprintf("%x", sha256.Sum256([]byte(manifest))), fmt.Sprintf("%x", sha256.Sum256([]byte(updated))), 1)
	config = strings.Replace(config, `version = "1"`, `version = "2"`, 1)
	os.WriteFile(main, []byte(config), 0600)
	run("apply", "-auto-approve", "-input=false", "-no-color")
	run("plan", "-detailed-exitcode", "-input=false", "-no-color")
	run("destroy", "-auto-approve", "-input=false", "-no-color")
	if _, e := os.Stat(installed); e != nil {
		t.Fatal("destroy removed retained plugin", e)
	}
}
