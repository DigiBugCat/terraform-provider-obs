// obs-plugins is an explicit, bounded macOS plugin installer. It never runs
// package scripts and is intentionally separate from Terraform resource effects.
package obsplugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

type filePin struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}
type plugin struct {
	ID            string             `json:"id"`
	Version       string             `json:"version"`
	Bundle        string             `json:"bundle"`
	Format        string             `json:"format"`
	Source        string             `json:"source"`
	ArchiveSHA256 string             `json:"archive_sha256,omitempty"`
	Payload       string             `json:"payload,omitempty"`
	Files         map[string]filePin `json:"files"`
}
type manifest struct {
	Version int      `json:"version"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Plugins []plugin `json:"plugins"`
}
type receipt struct {
	ID      string             `json:"id"`
	Version string             `json:"version"`
	Status  string             `json:"status"`
	Bundle  string             `json:"bundle"`
	Backup  string             `json:"backup,omitempty"`
	Files   map[string]filePin `json:"files"`
}
type operations struct {
	stopped func() error
	fetch   func(context.Context, string, string) error
	expand  func(context.Context, string, string) error
	rename  func(string, string) error
}

func defaults() operations {
	return operations{stopped: obsStopped, fetch: download, expand: func(ctx context.Context, a, b string) error {
		out, e := exec.CommandContext(ctx, "/usr/sbin/pkgutil", "--expand-full", a, b).CombinedOutput()
		if e != nil {
			return fmt.Errorf("pkgutil extraction: %w: %s", e, out)
		}
		return nil
	}, rename: os.Rename}
}
func safeRel(s string) bool {
	return s != "" && !filepath.IsAbs(s) && filepath.Clean(s) == s && s != "." && s != ".." && !strings.HasPrefix(s, ".."+string(os.PathSeparator))
}
func hashFile(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func inventory(root string) (map[string]filePin, error) {
	out := map[string]filePin{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		i, e := d.Info()
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		if !i.Mode().IsRegular() {
			return fmt.Errorf("nonregular file refused: %s", p)
		}
		r, e := filepath.Rel(root, p)
		if e != nil || !safeRel(r) {
			return fmt.Errorf("unsafe bundle path")
		}
		h, e := hashFile(p)
		if e != nil {
			return e
		}
		out[filepath.ToSlash(r)] = filePin{h, uint32(i.Mode().Perm())}
		return nil
	})
	return out, e
}
func verify(root string, p plugin) error {
	x, e := inventory(root)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(x, p.Files) {
		return fmt.Errorf("bundle file hashes/modes or file set differ from manifest")
	}
	return nil
}
func validate(m manifest) error {
	if m.Version != 1 || m.OS != "darwin" || m.Arch != "arm64" {
		return fmt.Errorf("only manifest v1 darwin/arm64 is supported")
	}
	seen := map[string]bool{}
	for _, p := range m.Plugins {
		if p.ID == "" || seen[p.ID] || p.Version == "" || !safeRel(p.Bundle) || filepath.Base(p.Bundle) != p.Bundle || !strings.HasSuffix(p.Bundle, ".plugin") {
			return fmt.Errorf("invalid/duplicate plugin identity")
		}
		seen[p.ID] = true
		if p.Format != "bundle" && p.Format != "pkg" {
			return fmt.Errorf("unsupported archive format")
		}
		if p.Source == "" || len(p.Files) == 0 {
			return fmt.Errorf("source and full file pins required")
		}
		if p.Format == "pkg" && (!safeRel(p.Payload) || len(p.ArchiveSHA256) != 64) {
			return fmt.Errorf("pkg requires safe payload and SHA256")
		}
		for f, pin := range p.Files {
			if !safeRel(f) || len(pin.SHA256) != 64 || pin.Mode > 0777 {
				return fmt.Errorf("invalid pinned file %s", f)
			}
			if _, e := hex.DecodeString(pin.SHA256); e != nil {
				return e
			}
		}
	}
	return nil
}
func obsStopped() error {
	out, e := exec.Command("/bin/ps", "-axo", "comm=").Output()
	if e != nil || len(out) == 0 {
		return fmt.Errorf("cannot establish OBS process status; refusing installation")
	}
	for _, line := range strings.Split(string(out), "\n") {
		s := strings.TrimSpace(line)
		if strings.EqualFold(filepath.Base(s), "obs") || strings.Contains(strings.ToLower(s), "obs.app/contents/macos/") {
			return fmt.Errorf("OBS is running; quit it before installing")
		}
	}
	return nil
}
func download(ctx context.Context, url, dst string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("download requires HTTPS")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return e
	}
	c := http.Client{Timeout: 2 * time.Minute}
	resp, e := c.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, ce := io.Copy(f, io.LimitReader(resp.Body, 1<<30))
	e = f.Close()
	if ce != nil {
		return ce
	}
	return e
}
func copyBundle(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		i, e := d.Info()
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused")
		}
		r, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, r)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !i.Mode().IsRegular() {
			return fmt.Errorf("nonregular file")
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, i.Mode().Perm())
		if e != nil {
			return e
		}
		_, ce := io.Copy(out, in)
		e = out.Close()
		if ce != nil {
			return ce
		}
		return e
	})
}
func noSymlinkAncestors(p string) error {
	p, e := filepath.Abs(p)
	if e != nil {
		return e
	}
	for {
		st, e := os.Lstat(p)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink ancestor refused: %s", p)
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		next := filepath.Dir(p)
		if next == p {
			return nil
		}
		p = next
	}
}
func inspect(root string, p plugin) receipt {
	r := receipt{ID: p.ID, Version: p.Version, Bundle: p.Bundle, Files: p.Files, Status: "different"}
	e := verify(filepath.Join(root, p.Bundle), p)
	if e == nil {
		r.Status = "matching"
	} else if os.IsNotExist(e) {
		r.Status = "missing"
	}
	return r
}
func install(ctx context.Context, root, base string, p plugin, replace bool, op operations) (receipt, error) {
	r := inspect(root, p)
	if e := op.stopped(); e != nil {
		return r, e
	}
	if e := noSymlinkAncestors(root); e != nil {
		return r, e
	}
	if r.Status == "matching" {
		r.Status = "unchanged"
		return r, nil
	}
	target := filepath.Join(root, p.Bundle)
	if _, e := os.Lstat(target); e == nil && !replace {
		return r, fmt.Errorf("different existing bundle requires --replace")
	} else if e != nil && !os.IsNotExist(e) {
		return r, e
	}
	if e := os.MkdirAll(root, 0755); e != nil {
		return r, e
	}
	lock := filepath.Join(root, ".obs-plugin-install.lock")
	if e := os.Mkdir(lock, 0700); e != nil {
		return r, fmt.Errorf("installation lock unavailable (another install or interrupted transaction): %w", e)
	}
	defer os.Remove(lock)
	work, e := os.MkdirTemp(root, ".obs-plugin-stage-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(work)
	source := p.Source
	if !filepath.IsAbs(source) && !strings.HasPrefix(source, "https://") {
		source = filepath.Join(base, source)
	}
	if p.Format == "pkg" {
		archive := filepath.Join(work, "archive.pkg")
		if strings.HasPrefix(source, "https://") {
			e = op.fetch(ctx, source, archive)
		} else {
			e = copyFile(source, archive)
		}
		if e != nil {
			return r, e
		}
		h, e := hashFile(archive)
		if e != nil {
			return r, e
		}
		if h != p.ArchiveSHA256 {
			return r, fmt.Errorf("archive checksum mismatch")
		}
		expanded := filepath.Join(work, "expanded")
		if e = op.expand(ctx, archive, expanded); e != nil {
			return r, e
		}
		source = filepath.Join(expanded, p.Payload)
	}
	if e = noSymlinkAncestors(source); e != nil {
		return r, e
	}
	if e = verify(source, p); e != nil {
		return r, e
	}
	stage := filepath.Join(work, p.Bundle)
	if e = copyBundle(source, stage); e != nil {
		return r, e
	}
	if e = verify(stage, p); e != nil {
		return r, e
	}
	if e = op.stopped(); e != nil {
		return r, e
	}
	// Recheck immediately before publishing. Keep the old bundle on the same filesystem.
	if e = noSymlinkAncestors(target); e != nil {
		return r, e
	}
	backup := ""
	if _, e = os.Lstat(target); e == nil {
		if !replace {
			return r, fmt.Errorf("bundle appeared during staging; --replace required")
		}
		backup, e = os.MkdirTemp(root, ".obs-plugin-backup-")
		if e != nil {
			return r, e
		}
		old := filepath.Join(backup, p.Bundle)
		if e = op.rename(target, old); e != nil {
			return r, e
		}
		r.Backup = old
	} else if !os.IsNotExist(e) {
		return r, e
	}
	if e = op.rename(stage, target); e != nil {
		if backup != "" {
			if re := op.rename(r.Backup, target); re != nil {
				return r, fmt.Errorf("publish failed: %v; rollback failed: %v; old bundle at %s", e, re, r.Backup)
			}
		}
		return r, fmt.Errorf("publish failed: %w", e)
	}
	r.Status = "installed"
	return r, nil
}
func copyFile(src, dst string) error {
	f, e := os.Open(src)
	if e != nil {
		return e
	}
	defer f.Close()
	o, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, ce := io.Copy(o, f)
	e = o.Close()
	if ce != nil {
		return ce
	}
	return e
}
func Run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: obs-plugins inspect|check|install --manifest PATH --id ID [--replace] [--plugin-root PATH] [--receipt PATH]")
	}
	cmd := args[0]
	if cmd != "inspect" && cmd != "check" && cmd != "install" {
		return fmt.Errorf("unknown command %q", cmd)
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	mf := fs.String("manifest", "", "versioned manifest path")
	id := fs.String("id", "", "plugin ID")
	replace := fs.Bool("replace", false, "preserve and replace differing bundle")
	root := fs.String("plugin-root", "", "plugin directory; defaults to current user's OBS plugins")
	rp := fs.String("receipt", "", "optional JSON evidence file")
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	if fs.NArg() != 0 || *mf == "" || *id == "" {
		return fmt.Errorf("--manifest and --id required, no positional arguments")
	}
	b, e := os.ReadFile(*mf)
	if e != nil {
		return e
	}
	var m manifest
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return e
	}
	if e = validate(m); e != nil {
		return e
	}
	if runtime.GOOS != m.OS || runtime.GOARCH != m.Arch {
		return fmt.Errorf("unsupported host %s/%s, requires %s/%s", runtime.GOOS, runtime.GOARCH, m.OS, m.Arch)
	}
	var p plugin
	for _, x := range m.Plugins {
		if x.ID == *id {
			p = x
		}
	}
	if p.ID == "" {
		return fmt.Errorf("unknown plugin ID")
	}
	if *root == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		*root = filepath.Join(home, "Library/Application Support/obs-studio/plugins")
	}
	*root, e = filepath.Abs(*root)
	if e != nil {
		return e
	}
	r := inspect(*root, p)
	if cmd == "install" {
		abs, e := filepath.Abs(*mf)
		if e != nil {
			return e
		}
		r, e = install(context.Background(), *root, filepath.Dir(abs), p, *replace, defaults())
		if e != nil {
			return e
		}
	}
	out, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	out = append(out, '\n')
	if *rp != "" {
		if e = os.WriteFile(*rp, out, 0600); e != nil {
			return e
		}
	}
	fmt.Print(string(out))
	if cmd == "check" && r.Status != "matching" {
		return fmt.Errorf("installed bundle does not match pinned manifest")
	}
	return nil
}
