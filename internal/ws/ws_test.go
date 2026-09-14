package ws

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeWSConfig writes an obs-websocket config.json. The password here is a test literal,
// never printed by these tests -- only ports and flags are asserted.
func writeWSConfig(t *testing.T, path string, port int, enabled bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"server_enabled": enabled, "server_port": port,
		"auth_required": true, "server_password": "test-only-not-a-real-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// portableTree builds <dir>/portable_mode.txt + the portable websocket config.
func portableTree(t *testing.T, port int, enabled bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, portableMarker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeWSConfig(t, portableConfigPath(dir), port, enabled)
	return dir
}

// isolate clears the discovery environment and points the user path at an empty temp dir
// so a developer's real OBS config can never be read by these tests.
func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("OBSTF_OBS_CONFIG", "")
	t.Setenv("OBSTF_OBS_DIR", "")
	t.Setenv("OBS_DIR", "")
	user := filepath.Join(t.TempDir(), "user", "obs-studio", "plugin_config", "obs-websocket", "config.json")
	oldUser, oldDirs := userConfigPath, installDirs
	userConfigPath = func() string { return user }
	installDirs = func() []string { return nil }
	t.Cleanup(func() { userConfigPath, installDirs = oldUser, oldDirs })
	return user
}

func TestDiscoverExplicitConfigPath(t *testing.T) {
	isolate(t)
	p := filepath.Join(t.TempDir(), "explicit.json")
	writeWSConfig(t, p, 4599, true)
	cfg, used, err := DiscoverConfigWith(Config{ConfigPath: p})
	if err != nil {
		t.Fatal(err)
	}
	if used != p || cfg.Port != 4599 || cfg.ConfigPath != p {
		t.Fatalf("used %q port %d", used, cfg.Port)
	}
	if !cfg.AuthRequired {
		t.Error("auth_required not surfaced")
	}
	if _, _, err := DiscoverConfigWith(Config{ConfigPath: p + ".missing"}); err == nil {
		t.Error("an explicitly named config that does not exist must be an error")
	}
}

func TestDiscoverEnvConfig(t *testing.T) {
	isolate(t)
	p := filepath.Join(t.TempDir(), "env.json")
	writeWSConfig(t, p, 4601, true)
	t.Setenv("OBSTF_OBS_CONFIG", p)
	cfg, used, err := DiscoverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if used != p || cfg.Port != 4601 {
		t.Fatalf("used %q port %d", used, cfg.Port)
	}
}

func TestDiscoverPortableDirWins(t *testing.T) {
	// The rig: a portable install on 4545 plus a stale, disabled AppData copy on 4455.
	user := isolate(t)
	writeWSConfig(t, user, 4455, false)
	dir := portableTree(t, 4545, true)

	cfg, used, err := DiscoverConfigWith(Config{OBSDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 4545 || used != portableConfigPath(dir) {
		t.Fatalf("OBSDir: port %d used %q", cfg.Port, used)
	}

	t.Setenv("OBS_DIR", dir)
	cfg, used, err = DiscoverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 4545 || used != portableConfigPath(dir) {
		t.Fatalf("OBS_DIR: port %d used %q", cfg.Port, used)
	}
	t.Setenv("OBS_DIR", "")

	// And without any env at all, via the portable marker beside a discoverable install.
	old := installDirs
	installDirs = func() []string { return []string{dir} }
	t.Cleanup(func() { installDirs = old })
	if !isPortableDir(dir) {
		t.Fatal("portable marker not detected")
	}
	cfg, used, err = DiscoverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 4545 || used != portableConfigPath(dir) {
		t.Fatalf("marker discovery: port %d used %q", cfg.Port, used)
	}
}

func TestDiscoverUserPathFallback(t *testing.T) {
	user := isolate(t)
	writeWSConfig(t, user, 0, true) // no server_port -> 4455
	cfg, used, err := DiscoverConfig()
	if err != nil {
		t.Fatal(err)
	}
	if used != user || cfg.Port != 4455 {
		t.Fatalf("used %q port %d", used, cfg.Port)
	}
}

func TestDiscoverDisabledIsItsOwnError(t *testing.T) {
	user := isolate(t)
	writeWSConfig(t, user, 4455, false)
	_, used, err := DiscoverConfig()
	if err == nil {
		t.Fatal("expected an error for server_enabled=false")
	}
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("not a DisabledError: %v", err)
	}
	var de *DisabledError
	if !errors.As(err, &de) || de.Port != 4455 || de.Path != used {
		t.Fatalf("DisabledError: %+v (used %q)", de, used)
	}
	if used != user {
		t.Errorf("the path that was read must be reported: %q", used)
	}
}

func TestDiscoverNoConfigAnywhere(t *testing.T) {
	isolate(t)
	_, _, err := DiscoverConfig()
	if err == nil {
		t.Fatal("expected an error when no config exists")
	}
	if errors.Is(err, ErrDisabled) {
		t.Error("a missing config must not read as disabled")
	}
}

func TestConfigCandidateOrder(t *testing.T) {
	user := isolate(t)
	dir := portableTree(t, 4545, true)
	t.Setenv("OBS_DIR", dir)
	got := configCandidates(Config{ConfigPath: "/x/explicit.json"})
	want := []string{"/x/explicit.json", portableConfigPath(dir), user}
	if len(got) != len(want) {
		t.Fatalf("candidates: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d: %q want %q", i, got[i], want[i])
		}
	}
}
