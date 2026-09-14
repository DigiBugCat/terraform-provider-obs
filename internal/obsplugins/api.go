package obsplugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Spec is one pinned plugin bundle. All files and executable modes are pinned.
type Spec = plugin
type Manifest = manifest
type Receipt = receipt

// Load reads and validates an explicitly pinned manifest. Relative sources resolve
// against the returned absolute manifest directory, not the caller's cwd.
func Load(path, expectedSHA256, id string) (Spec, string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Spec{}, "", e
	}
	if expectedSHA256 != "" && sum(b) != expectedSHA256 {
		return Spec{}, "", fmt.Errorf("manifest checksum mismatch")
	}
	var m manifest
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return Spec{}, "", e
	}
	if e = validate(m); e != nil {
		return Spec{}, "", e
	}
	if runtime.GOOS != m.OS || runtime.GOARCH != m.Arch {
		return Spec{}, "", fmt.Errorf("unsupported host %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return Spec{}, "", e
	}
	for _, p := range m.Plugins {
		if p.ID == id {
			return p, filepath.Dir(abs), nil
		}
	}
	return Spec{}, "", fmt.Errorf("unknown plugin ID %q", id)
}

// Inspect is read-only and can run while OBS is running. Status is matching,
// missing, or different. It compares actual files and modes, not old receipts.
func Inspect(root string, p Spec) Receipt { return inspect(root, p) }

// Install refuses running or ambiguously running OBS, verifies all staged files,
// and requires replace for differing existing contents. Prior content is retained.
func Install(ctx context.Context, root, manifestBase string, p Spec, replace bool) (Receipt, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return Receipt{}, fmt.Errorf("only macOS arm64 installation supported")
	}
	if e := validate(manifest{Version: 1, OS: "darwin", Arch: "arm64", Plugins: []plugin{p}}); e != nil {
		return Receipt{}, e
	}
	return install(ctx, root, manifestBase, p, replace, defaults())
}

// Digest identifies the entire pinned file set including relative paths and modes.
func Digest(p Spec) string { b, _ := json.Marshal(p.Files); return sum(b) }

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
