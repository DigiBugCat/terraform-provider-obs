package tfprovider

import (
	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalCredentialDiscoveryPreservesExplicitEndpoint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ws.json")
	os.WriteFile(p, []byte(`{"server_enabled":true,"server_port":4455,"server_password":"canary","auth_required":true}`), 0600)
	c := client{cfg: ws.Config{Host: "localhost", Port: 4555, ConfigPath: p}, discoverConfig: true, portExplicit: true}
	cfg, err := c.connectionConfig()
	if err != nil || cfg.Host != "localhost" || cfg.Port != 4555 {
		t.Fatalf("endpoint %v err%v", cfg.Host, err)
	}
	c.cfg.Host = "remote.invalid"
	if _, err = c.connectionConfig(); err == nil {
		t.Fatal("local credentials sent to remote")
	}
	c.credentialsUnknown = true
	c.discoverConfig = false
	if _, err = c.connectionConfig(); err == nil {
		t.Fatal("unknown endpoint accepted")
	}
}
