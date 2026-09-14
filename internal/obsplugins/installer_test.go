package obsplugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, string, plugin) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	src := filepath.Join(base, "test.plugin")
	if e := os.MkdirAll(filepath.Join(src, "Contents/MacOS"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src, "Contents/MacOS/test"), []byte("pinned executable"), 0755); e != nil {
		t.Fatal(e)
	}
	files, e := inventory(src)
	if e != nil {
		t.Fatal(e)
	}
	return base, filepath.Join(base, "plugins"), plugin{ID: "test", Version: "1", Bundle: "test.plugin", Format: "bundle", Source: "test.plugin", Files: files}
}
func offlineOps() operations {
	o := defaults()
	o.stopped = func() error { return nil }
	o.fetch = func(context.Context, string, string) error { return errors.New("unexpected download") }
	o.expand = func(context.Context, string, string) error { return errors.New("unexpected extraction") }
	return o
}
func TestInstallRepeatReplaceAndBackup(t *testing.T) {
	base, root, p := fixture(t)
	r, e := install(context.Background(), root, base, p, false, offlineOps())
	if e != nil || r.Status != "installed" {
		t.Fatalf("%+v %v", r, e)
	}
	r, e = install(context.Background(), root, base, p, false, offlineOps())
	if e != nil || r.Status != "unchanged" {
		t.Fatalf("%+v %v", r, e)
	}
	target := filepath.Join(root, p.Bundle, "Contents/MacOS/test")
	os.WriteFile(target, []byte("old local content"), 0755)
	if _, e = install(context.Background(), root, base, p, false, offlineOps()); e == nil {
		t.Fatal("replacement without flag")
	}
	r, e = install(context.Background(), root, base, p, true, offlineOps())
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(r.Backup, "Contents/MacOS/test"))
	if e != nil || string(b) != "old local content" {
		t.Fatalf("backup lost: %s %v", b, e)
	}
	if e = verify(filepath.Join(root, p.Bundle), p); e != nil {
		t.Fatal(e)
	}
}
func TestRunningAndChecksumRefusalLeaveTargetAlone(t *testing.T) {
	base, root, p := fixture(t)
	o := offlineOps()
	o.stopped = func() error { return errors.New("running") }
	if _, e := install(context.Background(), root, base, p, false, o); e == nil {
		t.Fatal("running allowed")
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("running check changed target")
	}
	os.WriteFile(filepath.Join(base, p.Bundle, "Contents/MacOS/test"), []byte("tampered"), 0755)
	if _, e := install(context.Background(), root, base, p, false, offlineOps()); e == nil {
		t.Fatal("checksum allowed")
	}
	if _, e := os.Stat(filepath.Join(root, p.Bundle)); !os.IsNotExist(e) {
		t.Fatal("tamper published")
	}
}
func TestSymlinkRefusal(t *testing.T) {
	base, root, p := fixture(t)
	os.Symlink("/etc/passwd", filepath.Join(base, p.Bundle, "secret"))
	if _, e := install(context.Background(), root, base, p, false, offlineOps()); e == nil {
		t.Fatal("symlink allowed")
	}
}
func TestRollbackOnPublishFailure(t *testing.T) {
	base, root, p := fixture(t)
	_, e := install(context.Background(), root, base, p, false, offlineOps())
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(root, p.Bundle, "Contents/MacOS/test")
	os.WriteFile(target, []byte("old"), 0755)
	o := offlineOps()
	calls := 0
	o.rename = func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("publish failure")
		}
		return os.Rename(a, b)
	}
	if _, e = install(context.Background(), root, base, p, true, o); e == nil {
		t.Fatal("expected publish failure")
	}
	b, e := os.ReadFile(target)
	if e != nil || string(b) != "old" {
		t.Fatalf("rollback lost old: %s %v", b, e)
	}
}
func TestPkgPinBeforeExtraction(t *testing.T) {
	base, root, p := fixture(t)
	p.Format = "pkg"
	p.Source = "https://example.invalid/pinned.pkg"
	p.Payload = "safe/test.plugin"
	p.ArchiveSHA256 = "wrong"
	o := offlineOps()
	o.fetch = func(_ context.Context, _ string, dst string) error { return os.WriteFile(dst, []byte("archive"), 0600) }
	called := false
	o.expand = func(context.Context, string, string) error { called = true; return nil }
	if _, e := install(context.Background(), root, base, p, false, o); e == nil || called {
		t.Fatal("unpinned archive extracted")
	}
}
func TestManifestTraversalRejected(t *testing.T) {
	_, _, p := fixture(t)
	p.Bundle = "../escape.plugin"
	if e := validate(manifest{Version: 1, OS: "darwin", Arch: "arm64", Plugins: []plugin{p}}); e == nil {
		t.Fatal("traversal accepted")
	}
}
