// Package ws: obs-websocket v5 client. Verified against 5.7.4.
//
// Streamer.bot speaks exactly this protocol (decoded from its C# actions), so obstf
// and the show's automation are two clients of the same daemon with identical
// semantics. Events are unsubscribed at Identify: this client only ever does requests.
package ws

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Config locates obs-websocket. Password is never logged by anything in this package.
type Config struct {
	Host     string
	Port     int
	Password string

	// ConfigPath, when set, is the obs-websocket config.json to read -- highest
	// precedence, above any discovery. Set by DiscoverConfig to the file it used.
	ConfigPath string
	// OBSDir, when set, is an OBS install directory to read as a PORTABLE install:
	// <dir>/config/obs-studio/plugin_config/obs-websocket/config.json.
	OBSDir string
	// AuthRequired mirrors the config's auth_required, so a caller can explain an empty
	// password before it fails at Identify. Never carries the password itself.
	AuthRequired bool
}

// DisabledError is returned when the config that was found exists and parses but has
// server_enabled=false. It is distinct from "no config" and from a connection failure:
// the fix is a checkbox in OBS, or a different config file (see the portable note below).
type DisabledError struct {
	Path string
	Port int
}

func (e *DisabledError) Error() string {
	return fmt.Sprintf("obs-websocket is disabled in this config (%s, server_port %d)", e.Path, e.Port)
}

// ErrDisabled matches any DisabledError under errors.Is.
var ErrDisabled = errors.New("obs-websocket is disabled in this config")

func (e *DisabledError) Is(target error) bool { return target == ErrDisabled }

// Portable installs keep their whole config tree inside the install directory, marked by
// portable_mode.txt (obs_portable_mode.txt on older builds) next to bin/ and config/. The
// rig is one: OBS lives under the streaming volume with obs-websocket on 4545, while the
// %APPDATA% copy is a stale, disabled 4455 config. Reading %APPDATA% first is how you get
// "obs-websocket server is disabled" on a machine whose server is running fine.
const (
	portableMarker    = "portable_mode.txt"
	portableMarkerOld = "obs_portable_mode.txt"
	wsConfigRel       = "plugin_config/obs-websocket/config.json"
)

// portableConfigPath is the obs-websocket config inside an OBS install directory.
func portableConfigPath(dir string) string {
	return filepath.Join(dir, "config", "obs-studio", filepath.FromSlash(wsConfigRel))
}

// defaultUserConfigPath is the per-OS obs-studio config location for a normal (non-portable)
// install.
func defaultUserConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		h, _ := os.UserHomeDir()
		return filepath.Join(h, "Library", "Application Support", "obs-studio", filepath.FromSlash(wsConfigRel))
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "obs-studio", filepath.FromSlash(wsConfigRel))
	default:
		h, _ := os.UserHomeDir()
		return filepath.Join(h, ".config", "obs-studio", filepath.FromSlash(wsConfigRel))
	}
}

// installDirs lists OBS install directories worth checking for a portable marker. A var
// so tests can point it at a temp tree.
var installDirs = func() []string {
	var dirs []string
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd, filepath.Join(wd, "obs-studio"))
	}
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			if p := os.Getenv(env); p != "" {
				dirs = append(dirs, filepath.Join(p, "obs-studio"))
			}
		}
		dirs = append(dirs, `C:\Program Files\obs-studio`)
	case "darwin":
		dirs = append(dirs, "/Applications/OBS.app/Contents/Resources")
	default:
		dirs = append(dirs, "/usr/share/obs", "/opt/obs-studio")
	}
	return dirs
}

// userConfigPath is defaultUserConfigPath behind a var, for the same reason.
var userConfigPath = defaultUserConfigPath

// isPortableDir reports whether an OBS install directory carries a portable marker.
func isPortableDir(dir string) bool {
	for _, m := range []string{portableMarker, portableMarkerOld} {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// configCandidates is OBS's own resolution order, most specific first:
//
//  1. an explicit file (Config.ConfigPath, or $OBSTF_OBS_CONFIG)
//  2. a named install directory ($OBS_DIR / $OBSTF_OBS_DIR / Config.OBSDir), read as portable
//  3. any discoverable OBS install carrying a portable marker
//  4. the per-OS user config
func configCandidates(hint Config) []string {
	var out []string
	if hint.ConfigPath != "" {
		out = append(out, hint.ConfigPath)
	}
	if p := os.Getenv("OBSTF_OBS_CONFIG"); p != "" {
		out = append(out, p)
	}
	for _, d := range []string{hint.OBSDir, os.Getenv("OBSTF_OBS_DIR"), os.Getenv("OBS_DIR")} {
		if d != "" {
			out = append(out, portableConfigPath(d))
		}
	}
	for _, d := range installDirs() {
		if d != "" && isPortableDir(d) {
			out = append(out, portableConfigPath(d))
		}
	}
	return append(out, userConfigPath())
}

// DiscoverConfig reads OBS's own obs-websocket config.json for the current user.
func DiscoverConfig() (Config, string, error) { return DiscoverConfigWith(Config{}) }

// DiscoverConfigWith is DiscoverConfig with explicit overrides (ConfigPath / OBSDir).
// The second return is always the path that was read (or the last one tried), so callers
// can say WHICH config they believed -- the portable/AppData split is invisible otherwise.
func DiscoverConfigWith(hint Config) (Config, string, error) {
	cands := configCandidates(hint)
	explicit := hint.ConfigPath != "" || os.Getenv("OBSTF_OBS_CONFIG") != ""
	var p string
	var b []byte
	var err error
	for _, c := range cands {
		p = c
		b, err = os.ReadFile(c)
		if err == nil {
			break
		}
		if explicit {
			return Config{}, p, err // an explicitly named file must exist
		}
	}
	if err != nil {
		return Config{}, p, fmt.Errorf("no obs-websocket config found (tried %s): %w",
			strings.Join(cands, ", "), err)
	}
	var raw struct {
		Port     int    `json:"server_port"`
		Password string `json:"server_password"`
		Enabled  bool   `json:"server_enabled"`
		Auth     bool   `json:"auth_required"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Config{}, p, fmt.Errorf("%s: %w", p, err)
	}
	if raw.Port == 0 {
		raw.Port = 4455
	}
	if !raw.Enabled {
		return Config{}, p, &DisabledError{Path: p, Port: raw.Port}
	}
	return Config{Host: "127.0.0.1", Port: raw.Port, Password: raw.Password,
		ConfigPath: p, OBSDir: hint.OBSDir, AuthRequired: raw.Auth}, p, nil
}

// Error is a failed request; Code follows obs-websocket RequestStatus.
type Error struct {
	Request string
	Code    int
	Comment string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s failed: %d %s", e.Request, e.Code, e.Comment)
}

type Client struct {
	c       *websocket.Conn
	mu      sync.Mutex
	n       int
	broken  atomic.Bool // set once a read or write failed; the connection must not be reused
	Version string      // obsWebSocketVersion from Hello
	RPC     int
}

// Broken reports whether a read or write on this connection has failed. A failed
// request/response exchange can leave frames unread, so callers must not reuse the
// connection; obs-websocket request failures (*Error) do not mark it.
func (cl *Client) Broken() bool { return cl.broken.Load() }

type envelope struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	url := fmt.Sprintf("ws://%s:%d", cfg.Host, cfg.Port)
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	c.SetReadLimit(64 << 20)
	cl := &Client{c: c}
	identified := false
	defer func() {
		if !identified {
			c.Close(websocket.StatusPolicyViolation, "handshake failed")
		}
	}()

	var hello struct {
		Version string `json:"obsWebSocketVersion"`
		RPC     int    `json:"rpcVersion"`
		Auth    *struct {
			Challenge string `json:"challenge"`
			Salt      string `json:"salt"`
		} `json:"authentication"`
	}
	env, err := cl.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("hello: %w", err)
	}
	if env.Op != 0 {
		return nil, fmt.Errorf("expected Hello, got op %d", env.Op)
	}
	if err := json.Unmarshal(env.D, &hello); err != nil {
		return nil, err
	}
	cl.Version, cl.RPC = hello.Version, hello.RPC

	ident := map[string]any{"rpcVersion": hello.RPC, "eventSubscriptions": 0}
	if hello.Auth != nil {
		if cfg.Password == "" {
			return nil, errors.New("obs-websocket requires a password and none was provided")
		}
		ident["authentication"] = auth(cfg.Password, hello.Auth.Salt, hello.Auth.Challenge)
	}
	if err := cl.write(ctx, 1, ident); err != nil {
		return nil, err
	}
	env, err = cl.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("identify: %w (wrong password?)", err)
	}
	if env.Op != 2 {
		return nil, fmt.Errorf("identify rejected (op %d)", env.Op)
	}
	identified = true
	return cl, nil
}

func auth(password, salt, challenge string) string {
	s := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(s[:])
	a := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(a[:])
}

func (cl *Client) Close() error { return cl.c.Close(websocket.StatusNormalClosure, "") }

func (cl *Client) write(ctx context.Context, op int, d any) error {
	b, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return err
	}
	if err := cl.c.Write(ctx, websocket.MessageText, b); err != nil {
		cl.broken.Store(true)
		return err
	}
	return nil
}

func (cl *Client) read(ctx context.Context) (envelope, error) {
	_, b, err := cl.c.Read(ctx)
	if err != nil {
		cl.broken.Store(true)
		return envelope{}, err
	}
	var env envelope
	return env, json.Unmarshal(b, &env)
}

type status struct {
	Result  bool   `json:"result"`
	Code    int    `json:"code"`
	Comment string `json:"comment"`
}

// Req performs one request and returns responseData.
func (cl *Client) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.n++
	rid := fmt.Sprint(cl.n)
	if data == nil {
		data = map[string]any{}
	}
	if err := cl.write(ctx, 6, map[string]any{"requestType": typ, "requestId": rid, "requestData": data}); err != nil {
		return nil, err
	}
	for {
		env, err := cl.read(ctx)
		if err != nil {
			return nil, err
		}
		if env.Op != 7 {
			continue
		}
		var r struct {
			ID     string         `json:"requestId"`
			Status status         `json:"requestStatus"`
			Data   map[string]any `json:"responseData"`
		}
		if err := json.Unmarshal(env.D, &r); err != nil {
			if !frameIsFor(env.D, rid) {
				continue
			}
			return nil, err
		}
		if r.ID != rid {
			continue
		}
		if !r.Status.Result {
			return nil, &Error{typ, r.Status.Code, r.Status.Comment}
		}
		if r.Data == nil {
			r.Data = map[string]any{}
		}
		return r.Data, nil
	}
}

// BatchItem is one request inside a RequestBatch.
type BatchItem struct {
	Type string         `json:"requestType"`
	Data map[string]any `json:"requestData"`
}

// BatchResult is the per-request outcome.
type BatchResult struct {
	OK      bool
	Code    int
	Comment string
	Data    map[string]any
}

// Batch sends requests in chunks via RequestBatch (op 8). Results are positional.
func (cl *Client) Batch(ctx context.Context, items []BatchItem, chunk int, halt bool) ([]BatchResult, error) {
	if chunk <= 0 {
		chunk = 200
	}
	out := make([]BatchResult, 0, len(items))
	for i := 0; i < len(items); i += chunk {
		end := min(i+chunk, len(items))
		res, err := cl.batchOne(ctx, items[i:end], halt)
		if err != nil {
			return nil, fmt.Errorf("batch chunk %d: %w", i/chunk, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

func (cl *Client) batchOne(ctx context.Context, items []BatchItem, halt bool) ([]BatchResult, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.n++
	rid := fmt.Sprintf("b%d", cl.n)
	reqs := make([]map[string]any, len(items))
	for j, it := range items {
		d := it.Data
		if d == nil {
			d = map[string]any{}
		}
		reqs[j] = map[string]any{"requestType": it.Type, "requestId": fmt.Sprint(j), "requestData": d}
	}
	if err := cl.write(ctx, 8, map[string]any{"requestId": rid, "haltOnFailure": halt,
		"executionType": 0, "requests": reqs}); err != nil {
		return nil, err
	}
	for {
		env, err := cl.read(ctx)
		if err != nil {
			return nil, err
		}
		if env.Op != 9 {
			continue
		}
		var r struct {
			ID      string `json:"requestId"`
			Results []struct {
				Status status         `json:"requestStatus"`
				Data   map[string]any `json:"responseData"`
			} `json:"results"`
		}
		if err := json.Unmarshal(env.D, &r); err != nil {
			if !frameIsFor(env.D, rid) {
				continue
			}
			return nil, err
		}
		if r.ID != rid {
			continue
		}
		out := make([]BatchResult, len(r.Results))
		for j, x := range r.Results {
			out[j] = BatchResult{OK: x.Status.Result, Code: x.Status.Code, Comment: x.Status.Comment, Data: x.Data}
		}
		return out, nil
	}
}

// ListNames extracts the names from a list response such as GetSceneList ("scenes",
// "sceneName"), GetInputList ("inputs", "inputName") or GetGroupList ("groups", ""),
// whose elements are objects with a name key or plain strings.
func ListNames(x map[string]any, list, key string) []string {
	arr, _ := x[list].([]any)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		switch t := v.(type) {
		case map[string]any:
			if n, _ := t[key].(string); n != "" {
				out = append(out, n)
			}
		case string:
			out = append(out, t)
		}
	}
	return out
}

// frameIsFor reports whether a response frame carries our requestId, so a malformed
// frame that belongs to someone else can be skipped instead of failing this request.
func frameIsFor(d json.RawMessage, rid string) bool {
	var id struct {
		ID string `json:"requestId"`
	}
	return json.Unmarshal(d, &id) == nil && id.ID == rid
}

// Timeout is a convenience for CLI contexts.
func Timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
