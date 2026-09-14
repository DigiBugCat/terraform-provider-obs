package tfprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

type client struct {
	cfg                ws.Config
	discoverConfig     bool
	portExplicit       bool
	credentialsUnknown bool
	// mu keeps mutations exclusive against everything, as before; Reads take the shared
	// side so Terraform's parallel refresh actually reaches OBS concurrently. Measured on
	// the reference collection: a Read is ~2ms of OBS work but spent ~70ms queued behind the
	// old exclusive mutex, and OBS overlaps reads across connections ~6x.
	gate          *lockGate
	testRequester requester

	// pool holds persistent identified connections, dialed lazily. A nil slot means "dial
	// on next acquire". The old dial-per-call also re-ran credential discovery (a config
	// file read) every call; resolved caches it for the process.
	poolOnce sync.Once
	pool     chan *ws.Client
	cfgMu    sync.Mutex
	resolved *ws.Config

	cacheOnce sync.Once
	cache     *readCache

	// Provider-block settings; -1 means unset (fall back to env, then default).
	connectionsCfg int
	readCacheMS    int
	// concurrentMutations enables per-container mutation locks instead of one
	// exclusive mutation at a time. EXPERIMENTAL and off by default: OBS 32.2.2 with
	// obs-websocket 5.7.4 wedged (accepted TCP, never completed Identify) under the
	// provider's multi-session write mix on 2026-09-14, although a single-request
	// microbenchmark overlapped 3.6x. Keep off unless testing against a fixed OBS.
	concurrentMutations int

	// keyed serialises mutations per container instead of globally. OBS overlaps
	// mutations across connections (measured 3.6x on CreateInput), and Terraform's
	// dependency graph already orders the mutations that must not overlap; what remains
	// is two writers touching the same scene, group or source, which these locks cover.
	gateOnce sync.Once
}

func (c *client) locks() *lockGate {
	c.gateOnce.Do(func() { c.gate = newLockGate() })
	return c.gate
}

// withKeyed runs a mutation holding the locks for the containers it touches, in sorted
// order so two operations can never deadlock on each other. It takes the shared side of
// the global lock, so a keyless (global-exclusive) operation still excludes it. With no
// usable key it degrades to the global-exclusive path. The read cache is dropped on
// entry and exit so a concurrent Read cannot leave a mid-write list behind.
func (c *client) withKeyed(ctx context.Context, keys []string, fn func(requester) error) error {
	if !c.concurrentMutationsEnabled() {
		return c.withRequester(ctx, fn)
	}
	uniq := map[string]bool{}
	var sorted []string
	for _, k := range keys {
		if k != "" && !uniq[k] {
			uniq[k] = true
			sorted = append(sorted, k)
		}
	}
	if len(sorted) == 0 {
		return c.withRequester(ctx, fn)
	}
	sort.Strings(sorted)
	op := opLabel()
	release, err := c.locks().shared(ctx, op)
	if err != nil {
		return err
	}
	defer release()
	for _, k := range sorted {
		unlock, err := c.locks().key(ctx, k, op)
		if err != nil {
			return err
		}
		defer unlock()
	}
	c.readCache().clear()
	defer c.readCache().clear()
	if c.testRequester != nil {
		return fn(c.testRequester)
	}
	return c.run(ctx, func(cl *ws.Client) error { return fn(exclusiveRequester{cl, &preflightScope{}}) })
}

// defaultConnections matches Terraform's default -parallelism closely enough that a
// refresh rarely waits for a slot. Provider attribute connections or OBS_CONNECTIONS
// overrides it.
const defaultConnections = 10

func (c *client) connections() int {
	if c.connectionsCfg > 0 {
		return c.connectionsCfg
	}
	if v, err := strconv.Atoi(os.Getenv("OBS_CONNECTIONS")); err == nil && v > 0 {
		return v
	}
	return defaultConnections
}

func (c *client) concurrentMutationsEnabled() bool {
	if c.concurrentMutations >= 0 {
		return c.concurrentMutations > 0
	}
	return os.Getenv("OBS_CONCURRENT_MUTATIONS") == "1"
}

func (c *client) resolvedConfig() (ws.Config, error) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	if c.resolved != nil {
		return *c.resolved, nil
	}
	cfg, err := c.connectionConfig()
	if err != nil {
		return cfg, err
	}
	c.resolved = &cfg
	return cfg, nil
}

// acquire takes a pool slot and returns its connection, dialing if the slot is empty.
// The dial uses its own timeout rather than the request context: the connection outlives
// the request, and coder/websocket only scopes the context to the handshake anyway.
func (c *client) acquire(ctx context.Context) (*ws.Client, error) {
	c.poolOnce.Do(func() {
		n := c.connections()
		c.pool = make(chan *ws.Client, n)
		for i := 0; i < n; i++ {
			c.pool <- nil
		}
	})
	var cl *ws.Client
	select {
	case cl = <-c.pool:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if cl != nil {
		return cl, nil
	}
	cfg, err := c.resolvedConfig()
	if err != nil {
		c.pool <- nil
		return nil, err
	}
	dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err = ws.Connect(dctx, cfg)
	if err != nil {
		c.pool <- nil
		return nil, err
	}
	return cl, nil
}

// release returns the connection to the pool unless the call failed below the request
// level (dial, socket, or a cancelled context mid-read leaves an unread response on the
// wire). Those close the connection and leave the slot empty for a fresh dial. An
// obs-websocket request failure (*ws.Error) is an application answer on a healthy
// connection and keeps it.
func (c *client) release(cl *ws.Client, err error) {
	if err != nil && !isRequestError(err) {
		cl.Close()
		c.pool <- nil
		return
	}
	c.pool <- cl
}

func isRequestError(err error) bool {
	var e *ws.Error
	return errors.As(err, &e)
}

func (c *client) run(ctx context.Context, fn func(*ws.Client) error) error {
	cl, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	err = fn(cl)
	c.release(cl, err)
	return err
}

// clampCreateIndex bounds a declared sibling index to what OBS can honour right now.
// Terraform creates siblings in arbitrary order, so an item declared at index 5 may be
// created while only two siblings exist; OBS silently places it last and a raw-exact
// postcondition against 5 would fail. Requesting and verifying the clamped index lets
// creation succeed in any order; obs_scene_contents (or the item's own next Update)
// enforces the declared order once every sibling exists. State keeps the declared value.
func clampCreateIndex(declared, maxIndex int64) int64 {
	if maxIndex < 0 {
		maxIndex = 0
	}
	if declared < 0 {
		return 0
	}
	if declared > maxIndex {
		return maxIndex
	}
	return declared
}

// containerItemCount is the number of placements currently in a scene or group. It only
// counts the array, so incomplete list entries do not matter; it exists for
// clampCreateIndex, which needs a bound, not identities.
func containerItemCount(ctx context.Context, cl requester, container string, group bool) (int64, error) {
	typ := "GetSceneItemList"
	if group {
		typ = "GetGroupSceneItemList"
	}
	x, err := request(ctx, cl, typ, map[string]any{"sceneName": container})
	if err != nil {
		return 0, err
	}
	items, _ := x["sceneItems"].([]any)
	return int64(len(items)), nil
}

type requester interface {
	Req(context.Context, string, map[string]any) (map[string]any, error)
}

type authoritativeNativeInterruptionError struct{ requestType string }

func (e *authoritativeNativeInterruptionError) Error() string {
	return fmt.Sprintf("injected interruption after authoritative %s response; refresh before retry", e.requestType)
}

func isAuthoritativeNativeInterruption(err error) bool {
	var interruption *authoritativeNativeInterruptionError
	return errors.As(err, &interruption)
}

// withRequester runs fn exclusively: no other call, read or write, overlaps it.
// Mutations use it; it drops the read cache so later Reads re-observe after this
// provider's own write, and hands out a requester carrying a fresh preflight scope.
func (c *client) withRequester(ctx context.Context, fn func(requester) error) error {
	op := opLabel()
	release, err := c.locks().exclusive(ctx, op)
	if err != nil {
		return err
	}
	defer release()
	c.readCache().clear()
	if c.testRequester != nil {
		return fn(c.testRequester)
	}
	return c.run(ctx, func(cl *ws.Client) error { return fn(exclusiveRequester{cl, &preflightScope{}}) })
}

// withReadRequester runs fn on the shared side of the lock: Reads overlap each other on
// separate pooled connections but never overlap a mutation, so a Read cannot observe a
// half-applied write from this provider. Only Read methods should use it. The requester
// it hands out memoises list requests (see readCache); the test requester is handed out
// bare so request-sequence tests stay exact.
func (c *client) withReadRequester(ctx context.Context, fn func(requester) error) error {
	release, err := c.locks().shared(ctx, opLabel())
	if err != nil {
		return err
	}
	defer release()
	if c.testRequester != nil {
		return fn(c.testRequester)
	}
	return c.run(ctx, func(cl *ws.Client) error { return fn(cachingRequester{cl, c.readCache()}) })
}

// readCache is an EXPERIMENTAL, default-off memo of idempotent list requests across
// concurrent Reads. Enabled it took the reference plan from 8.8s to 4.0s, at the cost of a
// TTL-long window in which an external OBS edit is not observed; simplicity/correctness
// wins by default. Terraform
// refreshes every resource independently, so on the reference collection a plan pulled each
// container's whole item list once per item in it (204 scene_item Reads) and the whole
// input list once per group_source Read (381). Entries live for the TTL and are dropped
// on every exclusive (mutating) acquisition, so a Read never sees a list older than this
// provider's last write. Against another writer the staleness window is the TTL, where
// before it was a single request.
type readCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]readCacheEntry
}

type readCacheEntry struct {
	at   time.Time
	data map[string]any
}

const defaultReadCacheTTL time.Duration = 0 // off: experimental, opt in via read_cache_ms or OBS_READ_CACHE_MS

// cacheableReads are the list requests that repeat across Reads. Per-resource reads
// (GetInputSettings, GetSceneItemTransform) do not repeat and are never cached.
var cacheableReads = map[string]bool{
	"GetSceneCollectionList": true, "GetSceneList": true, "GetGroupList": true, "GetInputList": true,
	"GetSceneItemList": true, "GetGroupSceneItemList": true, "GetSourceFilterList": true,
}

func (c *client) readCache() *readCache {
	c.cacheOnce.Do(func() {
		ttl := defaultReadCacheTTL
		if c.readCacheMS >= 0 {
			ttl = time.Duration(c.readCacheMS) * time.Millisecond
		} else if v, err := strconv.Atoi(os.Getenv("OBS_READ_CACHE_MS")); err == nil && v >= 0 {
			ttl = time.Duration(v) * time.Millisecond
		}
		c.cache = &readCache{ttl: ttl, m: map[string]readCacheEntry{}}
	})
	return c.cache
}

func (rc *readCache) clear() {
	rc.mu.Lock()
	rc.m = map[string]readCacheEntry{}
	rc.mu.Unlock()
}

type cachingRequester struct {
	requester
	rc *readCache
}

func (q cachingRequester) Req(ctx context.Context, typ string, data map[string]any) (map[string]any, error) {
	if q.rc.ttl <= 0 || !cacheableReads[typ] {
		return q.requester.Req(ctx, typ, data)
	}
	key := typ
	if len(data) > 0 {
		b, _ := json.Marshal(data)
		key += string(b)
	}
	q.rc.mu.Lock()
	e, ok := q.rc.m[key]
	q.rc.mu.Unlock()
	if ok && time.Since(e.at) < q.rc.ttl {
		return deepCopyMap(e.data), nil
	}
	x, err := q.requester.Req(ctx, typ, data)
	if err != nil {
		return x, err
	}
	q.rc.mu.Lock()
	q.rc.m[key] = readCacheEntry{at: time.Now(), data: deepCopyMap(x)}
	q.rc.mu.Unlock()
	return x, nil
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopyValue(x)
		}
		return out
	default:
		return v
	}
}

func (c *client) connectionConfig() (ws.Config, error) {
	if c.credentialsUnknown {
		return c.cfg, fmt.Errorf("OBS endpoint or credentials remain unknown")
	}
	if !c.discoverConfig {
		return c.cfg, nil
	}
	if c.cfg.Host != "127.0.0.1" && c.cfg.Host != "localhost" && c.cfg.Host != "::1" {
		return c.cfg, fmt.Errorf("remote OBS requires explicitly configured credentials; refusing local credential discovery")
	}
	cfg, _, err := ws.DiscoverConfigWith(c.cfg)
	if err != nil {
		return cfg, fmt.Errorf("OBS WebSocket configuration unavailable: %w", err)
	}
	cfg.Host = c.cfg.Host
	if c.portExplicit {
		cfg.Port = c.cfg.Port
	}
	return cfg, nil
}

func request(ctx context.Context, cl requester, typ string, data map[string]any) (map[string]any, error) {
	x, err := cl.Req(ctx, typ, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", typ, err)
	}
	return x, nil
}

func ensureCollection(ctx context.Context, cl requester, want string) (string, error) {
	x, err := request(ctx, cl, "GetSceneCollectionList", nil)
	if err != nil {
		return "", err
	}
	got := stringValue(x["currentSceneCollectionName"])
	if got == "" {
		return "", fmt.Errorf("GetSceneCollectionList omitted currentSceneCollectionName")
	}
	if got != want {
		return "", fmt.Errorf("refusing OBS write/read: current collection %q, want %q", got, want)
	}
	return got, nil
}

// preflightScope caches the safety preflight (version, collection, canvas, output
// status) for one exclusive section. Nothing this provider does inside the section can
// change those answers, and one Create issues four or more mutations; re-reading seven
// values before each of them was most of a resource's apply time. Only the request-type
// capability check is repeated per mutation, from the cached availableRequests.
type preflightScope struct {
	collection string
	guard      map[string]any
	available  map[string]bool
}

type preflightScoper interface{ preflightScope() *preflightScope }

// exclusiveRequester is what exclusive sections hand out: the pooled connection plus
// the section's preflight scope.
type exclusiveRequester struct {
	requester
	scope *preflightScope
}

func (q exclusiveRequester) preflightScope() *preflightScope { return q.scope }

func scopedPreflight(ctx context.Context, cl requester, collection, typ string) (map[string]any, error) {
	sc, ok := cl.(preflightScoper)
	var scope *preflightScope
	if ok {
		scope = sc.preflightScope()
	}
	if scope != nil && scope.guard != nil && scope.collection == collection {
		if !scope.available[typ] {
			return scope.guard, fmt.Errorf("OBS %v / WebSocket %v does not advertise %s", scope.guard["obs_version"], scope.guard["obs_websocket_version"], typ)
		}
		out := make(map[string]any, len(scope.guard)+1)
		for k, v := range scope.guard {
			out[k] = v
		}
		out["request_type"] = typ
		out["reused"] = true
		return out, nil
	}
	guard, err := mutationPreflight(ctx, cl, collection, typ)
	if err == nil && scope != nil {
		avail := map[string]bool{}
		if xs, ok := guard["available_requests"].([]any); ok {
			for _, x := range xs {
				avail[stringValue(x)] = true
			}
		}
		scope.collection, scope.guard, scope.available = collection, guard, avail
	}
	return guard, err
}

// batchRequester is implemented by requesters that can send an obs-websocket
// RequestBatch; the pooled *ws.Client does, and the wrappers forward it.
type batchRequester interface {
	Batch(ctx context.Context, items []ws.BatchItem, chunk int, halt bool) ([]ws.BatchResult, error)
}

func (q exclusiveRequester) Batch(ctx context.Context, items []ws.BatchItem, chunk int, halt bool) ([]ws.BatchResult, error) {
	b, ok := q.requester.(batchRequester)
	if !ok {
		return nil, errNoBatch
	}
	return b.Batch(ctx, items, chunk, halt)
}

var errNoBatch = errors.New("requester cannot batch")

// mutationBatch sends several mutations as one RequestBatch (one socket round trip
// instead of one per request; on this Mac half of apply's provider CPU was kernel time
// in socket writes). Semantics match a sequence of mutation calls: one preflight per
// operation, every request type capability-checked, haltOnFailure so nothing after a
// failed request runs, one trace receipt carrying every request and result. Falls back
// to sequential mutation calls when the requester cannot batch or when a test injects
// an interruption after one of the types.
func mutationBatch(ctx context.Context, cl requester, collection string, items []ws.BatchItem) ([]map[string]any, error) {
	b, ok := cl.(batchRequester)
	inject := os.Getenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE")
	for _, it := range items {
		if it.Type == inject {
			ok = false
		}
	}
	if !ok {
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			x, err := mutation(ctx, cl, collection, it.Type, it.Data)
			out = append(out, x)
			if err != nil {
				return out, err
			}
		}
		return out, nil
	}
	var guard map[string]any
	for _, it := range items {
		g, err := scopedPreflight(ctx, cl, collection, it.Type)
		if err != nil {
			if traceErr := recordTrace(map[string]any{"event": "mutation", "at": time.Now().UTC(), "request_type": it.Type, "request_data": it.Data, "native_sent": false, "status": "refused", "preflight": g, "error": err.Error()}); traceErr != nil {
				return nil, fmt.Errorf("%w; refusal receipt write also failed: %v", err, traceErr)
			}
			return nil, err
		}
		guard = g
	}
	results, err := b.Batch(ctx, items, 0, true)
	if errors.Is(err, errNoBatch) {
		return mutationBatch(ctx, plainRequester{cl}, collection, items)
	}
	types := make([]string, len(items))
	for i, it := range items {
		types[i] = it.Type
	}
	entry := map[string]any{"event": "mutation", "at": time.Now().UTC(), "request_type": "RequestBatch", "batch_request_types": types, "request_data": items, "native_sent": true, "status": "completed", "preflight": guard}
	out := make([]map[string]any, 0, len(items))
	if err == nil {
		for i, r := range results {
			if !r.OK {
				err = &ws.Error{Request: items[i].Type, Code: r.Code, Comment: r.Comment}
				break
			}
			out = append(out, r.Data)
		}
		if err == nil && len(results) != len(items) {
			err = fmt.Errorf("RequestBatch returned %d results for %d requests", len(results), len(items))
		}
	}
	if err != nil {
		entry["status"] = "failed-or-uncertain"
		entry["error"] = err.Error()
	}
	if traceErr := recordTrace(entry); traceErr != nil {
		if err != nil {
			return out, fmt.Errorf("%w; receipt write also failed: %v", err, traceErr)
		}
		return out, fmt.Errorf("RequestBatch native result recorded as %q but receipt write failed: %w", entry["status"], traceErr)
	}
	return out, err
}

// plainRequester hides batching so mutationBatch can fall back to sequential calls.
type plainRequester struct{ requester }

func (q plainRequester) preflightScope() *preflightScope {
	if p, ok := q.requester.(preflightScoper); ok {
		return p.preflightScope()
	}
	return nil
}

func mutation(ctx context.Context, cl requester, collection, typ string, data map[string]any) (map[string]any, error) {
	guard, guardErr := scopedPreflight(ctx, cl, collection, typ)
	if guardErr != nil {
		if traceErr := recordTrace(map[string]any{"event": "mutation", "at": time.Now().UTC(), "request_type": typ, "request_data": data, "native_sent": false, "status": "refused", "preflight": guard, "error": guardErr.Error()}); traceErr != nil {
			return nil, fmt.Errorf("%w; refusal receipt write also failed: %v", guardErr, traceErr)
		}
		return nil, guardErr
	}
	x, err := request(ctx, cl, typ, data)
	entry := map[string]any{"at": time.Now().UTC(), "request_type": typ, "request_data": data, "native_sent": true, "status": "completed", "preflight": guard, "response": x}
	if err != nil {
		entry["status"] = "failed-or-uncertain"
		entry["error"] = err.Error()
	}
	entry["event"] = "mutation"
	if traceErr := recordTrace(entry); traceErr != nil {
		if err != nil {
			return x, fmt.Errorf("%w; receipt write also failed: %v", err, traceErr)
		}
		return x, fmt.Errorf("%s native result recorded as %q but receipt write failed: %w", typ, entry["status"], traceErr)
	}
	if err == nil && os.Getenv("OBSTF_TF_INJECT_AFTER_NATIVE_TYPE") == typ {
		return x, &authoritativeNativeInterruptionError{requestType: typ}
	}
	return x, err
}

var traceMu sync.Mutex

func recordTrace(v map[string]any) error {
	path := os.Getenv("OBSTF_TF_TRACE_PATH")
	if path == "" {
		return nil
	}
	traceMu.Lock()
	defer traceMu.Unlock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(redactTraceSettings(v)); err != nil {
		return err
	}
	return f.Sync()
}

// Plugin settings can contain credentials under arbitrary native key names.
// Redact whole settings payloads in receipts; Terraform state remains sensitive.
// Return a copy so evidence handling cannot change the actual native request.
func redactTraceSettings(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			switch key {
			case "filterSettings", "inputSettings", "defaultFilterSettings", "defaultInputSettings", "settings_json", "password":
				out[key] = map[string]any{"$redacted": true}
			default:
				out[key] = redactTraceSettings(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactTraceSettings(item)
		}
		return out
	default:
		return value
	}
}

func mutationPreflight(ctx context.Context, cl requester, collection, mutationType string) (map[string]any, error) {
	ev := map[string]any{"requested_collection": collection, "request_type": mutationType, "outputs": map[string]any{}}
	v, err := request(ctx, cl, "GetVersion", nil)
	if err != nil {
		return ev, err
	}
	obsVersion, wsVersion := stringValue(v["obsVersion"]), stringValue(v["obsWebSocketVersion"])
	ev["obs_version"], ev["obs_websocket_version"] = obsVersion, wsVersion
	if obsVersion == "" || wsVersion == "" {
		return ev, fmt.Errorf("GetVersion omitted OBS/WebSocket version")
	}
	available, err := requiredSlice(v, "availableRequests")
	if err != nil {
		return ev, err
	}
	ev["available_requests"] = available
	supports := func(q string) bool {
		for _, x := range available {
			if stringValue(x) == q {
				return true
			}
		}
		return false
	}
	if !supports(mutationType) {
		return ev, fmt.Errorf("OBS %s / WebSocket %s does not advertise %s", obsVersion, wsVersion, mutationType)
	}
	current, err := ensureCollection(ctx, cl, collection)
	if err != nil {
		return ev, err
	}
	ev["current_collection"] = current
	video, err := request(ctx, cl, "GetVideoSettings", nil)
	if err != nil {
		return ev, err
	}
	w, wok := numberValue(video["baseWidth"])
	h, hok := numberValue(video["baseHeight"])
	ev["base_width"], ev["base_height"] = w, h
	if !wok || !hok {
		return ev, fmt.Errorf("GetVideoSettings omitted numeric base canvas")
	}
	if int(w) != 1920 || int(h) != 1080 {
		return ev, fmt.Errorf("refusing mutation on unsupported live canvas %.0fx%.0f; pilot requires 1920x1080", w, h)
	}
	outputs := ev["outputs"].(map[string]any)
	for _, q := range []string{"GetStreamStatus", "GetRecordStatus", "GetVirtualCamStatus", "GetReplayBufferStatus"} {
		if !supports(q) {
			continue
		}
		x, err := request(ctx, cl, q, nil)
		if err != nil {
			var we *ws.Error
			if q == "GetReplayBufferStatus" && errors.As(err, &we) && we.Code == 604 {
				outputs[q] = "unavailable-604"
				continue
			}
			return ev, err
		}
		active, ok := x["outputActive"].(bool)
		if !ok {
			return ev, fmt.Errorf("%s omitted boolean outputActive", q)
		}
		outputs[q] = active
		if active {
			return ev, fmt.Errorf("refusing %s while %s reports active output", mutationType, q)
		}
	}
	return ev, nil
}

type sceneObservation struct {
	Name string
	UUID string
}

func scenes(ctx context.Context, cl requester) ([]sceneObservation, error) {
	x, err := request(ctx, cl, "GetSceneList", nil)
	if err != nil {
		return nil, err
	}
	values, err := requiredSlice(x, "scenes")
	if err != nil {
		return nil, err
	}
	var out []sceneObservation
	for _, v := range values {
		o := mapValue(v)
		name, uuid := stringValue(o["sceneName"]), stringValue(o["sceneUuid"])
		if name == "" || uuid == "" {
			return nil, fmt.Errorf("GetSceneList returned scene without name/UUID")
		}
		out = append(out, sceneObservation{Name: name, UUID: uuid})
	}
	return out, nil
}

func sceneByUUID(ctx context.Context, cl requester, uuid string) (sceneObservation, bool, error) {
	all, err := scenes(ctx, cl)
	if err != nil {
		return sceneObservation{}, false, err
	}
	for _, s := range all {
		if s.UUID == uuid {
			return s, true, nil
		}
	}
	return sceneObservation{}, false, nil
}

func sceneByName(ctx context.Context, cl requester, name string) (sceneObservation, bool, error) {
	all, err := scenes(ctx, cl)
	if err != nil {
		return sceneObservation{}, false, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, true, nil
		}
	}
	return sceneObservation{}, false, nil
}

type inputObservation struct {
	Name     string
	UUID     string
	Kind     string
	File     string
	Unload   bool
	Settings map[string]any
}

func inputs(ctx context.Context, cl requester) ([]inputObservation, error) {
	x, err := request(ctx, cl, "GetInputList", nil)
	if err != nil {
		return nil, err
	}
	values, err := requiredSlice(x, "inputs")
	if err != nil {
		return nil, err
	}
	var out []inputObservation
	for _, v := range values {
		o := mapValue(v)
		name, uuid, kind := stringValue(o["inputName"]), stringValue(o["inputUuid"]), stringValue(o["inputKind"])
		if name == "" || uuid == "" || kind == "" {
			return nil, fmt.Errorf("GetInputList returned input without name/UUID/kind")
		}
		out = append(out, inputObservation{Name: name, UUID: uuid, Kind: kind})
	}
	return out, nil
}

func inputByUUID(ctx context.Context, cl requester, uuid string) (inputObservation, bool, error) {
	all, err := inputs(ctx, cl)
	if err != nil {
		return inputObservation{}, false, err
	}
	for _, in := range all {
		if in.UUID != uuid {
			continue
		}
		x, err := request(ctx, cl, "GetInputSettings", map[string]any{"inputUuid": uuid})
		if err != nil {
			if inputSettingsNotFound(err) {
				return inputObservation{}, false, nil
			}
			return inputObservation{}, false, err
		}
		settings, ok := x["inputSettings"].(map[string]any)
		if !ok {
			return inputObservation{}, false, fmt.Errorf("GetInputSettings omitted settings object")
		}
		settingsKind, ok := x["inputKind"].(string)
		if !ok || settingsKind == "" {
			return inputObservation{}, false, fmt.Errorf("GetInputSettings omitted string inputKind")
		}
		if settingsKind != in.Kind {
			return inputObservation{}, false, fmt.Errorf("GetInputSettings kind=%q disagrees with GetInputList kind=%q", settingsKind, in.Kind)
		}
		in.Settings = settings
		if in.Kind == "image_source" {
			file, unload, err := observedImageSettings(ctx, cl, in)
			if err != nil {
				return inputObservation{}, false, err
			}
			in.File, in.Unload = file, unload
		}
		return in, true, nil
	}
	return inputObservation{}, false, nil
}

func observedImageSettings(ctx context.Context, cl requester, in inputObservation) (string, bool, error) {
	x, err := request(ctx, cl, "GetInputDefaultSettings", map[string]any{"inputKind": "image_source"})
	if err != nil {
		return "", false, err
	}
	defaults, ok := x["defaultInputSettings"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("GetInputDefaultSettings omitted object")
	}
	fileValue, fileLive := in.Settings["file"]
	if !fileLive {
		fileValue, ok = defaults["file"]
		if !ok {
			return "", false, fmt.Errorf("image setting file absent from live and defaults")
		}
	}
	file, ok := fileValue.(string)
	if !ok {
		return "", false, fmt.Errorf("image setting file has type %T, want string", fileValue)
	}
	unloadValue, unloadLive := in.Settings["unload"]
	if !unloadLive {
		unloadValue, ok = defaults["unload"]
		if !ok {
			return "", false, fmt.Errorf("image setting unload absent from live and defaults")
		}
	}
	unload, ok := unloadValue.(bool)
	if !ok {
		return "", false, fmt.Errorf("image setting unload has type %T, want bool", unloadValue)
	}
	if err := recordTrace(map[string]any{"event": "image-settings-observation", "input_uuid": in.UUID, "origins": map[string]string{"file": map[bool]string{true: "live", false: "default"}[fileLive], "unload": map[bool]string{true: "live", false: "default"}[unloadLive]}}); err != nil {
		return "", false, err
	}
	return file, unload, nil
}

func inputByName(ctx context.Context, cl requester, name string) (inputObservation, bool, error) {
	all, err := inputs(ctx, cl)
	if err != nil {
		return inputObservation{}, false, err
	}
	for _, in := range all {
		if in.Name == name {
			return in, true, nil
		}
	}
	return inputObservation{}, false, nil
}

func existingSourceIdentity(ctx context.Context, cl requester, name, uuid string) (bool, error) {
	all, err := inputs(ctx, cl)
	if err != nil {
		return false, err
	}
	for _, in := range all {
		if in.UUID == uuid || in.Name == name {
			if in.UUID == uuid && in.Name == name {
				return true, nil
			}
			return false, fmt.Errorf("source identity/name collision: observed %s/%s want %s/%s", in.Name, in.UUID, name, uuid)
		}
	}
	ss, err := scenes(ctx, cl)
	if err != nil {
		return false, err
	}
	found := false
	for _, s := range ss {
		if s.UUID == uuid || s.Name == name {
			if s.UUID != uuid || s.Name != name {
				return false, fmt.Errorf("scene source identity/name collision: observed %s/%s want %s/%s", s.Name, s.UUID, name, uuid)
			}
			found = true
		}
		items, err := sceneItems(ctx, cl, s.Name)
		if err != nil {
			return false, err
		}
		for _, it := range items {
			if it.SourceUUID == uuid || it.SourceName == name {
				if it.SourceUUID != uuid || it.SourceName != name {
					return false, fmt.Errorf("source identity/name collision: observed %s/%s want %s/%s", it.SourceName, it.SourceUUID, name, uuid)
				}
				found = true
			}
		}
	}
	return found, nil
}

func deleteAnchoredInput(ctx context.Context, cl requester, collection, inputUUID, anchorSceneUUID string, anchorItemID int64) error {
	in, ok, err := inputByUUID(ctx, cl, inputUUID)
	if err != nil || !ok {
		return err
	}
	allScenes, err := scenes(ctx, cl)
	if err != nil {
		return err
	}
	var extras []string
	for _, s := range allScenes {
		items, err := sceneItems(ctx, cl, s.Name)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.SourceUUID == in.UUID && !(s.UUID == anchorSceneUUID && it.ItemID == anchorItemID) {
				extras = append(extras, fmt.Sprintf("%s/%d", s.Name, it.ItemID))
			}
		}
	}
	if len(extras) > 0 {
		return fmt.Errorf("refusing input destruction before mutation: extra source references %v", extras)
	}
	scene, ok, err := sceneByUUID(ctx, cl, anchorSceneUUID)
	if err != nil {
		return err
	}
	if ok {
		it, exists, err := itemByID(ctx, cl, scene.Name, anchorItemID)
		if err != nil {
			return err
		}
		if exists {
			if it.SourceUUID != in.UUID {
				return fmt.Errorf("anchor item rebound; refusing deletion")
			}
			if _, err := mutation(ctx, cl, collection, "RemoveSceneItem", map[string]any{"sceneUuid": scene.UUID, "sceneItemId": it.ItemID}); err != nil {
				return err
			}
		}
	}
	return waitUntil(ctx, func() (bool, error) { _, exists, e := inputByUUID(ctx, cl, in.UUID); return !exists, e })
}

type itemObservation struct {
	SceneName  string
	SceneUUID  string
	ItemID     int64
	Index      int64
	SourceName string
	SourceUUID string
	Enabled    bool
	Transform  transformModel
}

func sceneItems(ctx context.Context, cl requester, sceneName string) ([]itemObservation, error) {
	x, err := request(ctx, cl, "GetSceneItemList", map[string]any{"sceneName": sceneName})
	if err != nil {
		return nil, err
	}
	values, err := requiredSlice(x, "sceneItems")
	if err != nil {
		return nil, err
	}
	var out []itemObservation
	for _, v := range values {
		o := mapValue(v)
		tr := mapValue(o["sceneItemTransform"])
		if tr == nil || int64Value(o["sceneItemId"]) <= 0 || stringValue(o["sourceUuid"]) == "" {
			return nil, fmt.Errorf("GetSceneItemList returned incomplete item identity/transform")
		}
		out = append(out, itemObservation{
			SceneName: sceneName, ItemID: int64Value(o["sceneItemId"]), Index: int64Value(o["sceneItemIndex"]),
			SourceName: stringValue(o["sourceName"]), SourceUUID: stringValue(o["sourceUuid"]), Enabled: boolValue(o["sceneItemEnabled"]),
			Transform: transformFromNative(tr),
		})
	}
	return out, nil
}

func itemByID(ctx context.Context, cl requester, sceneName string, id int64) (itemObservation, bool, error) {
	all, err := sceneItems(ctx, cl, sceneName)
	if err != nil {
		return itemObservation{}, false, err
	}
	for _, item := range all {
		if item.ItemID == id {
			return item, true, nil
		}
	}
	return itemObservation{}, false, nil
}

func waitUntil(ctx context.Context, check func() (bool, error)) error {
	for i := 0; i < 40; i++ {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("native postcondition did not converge within 4s")
}

func itemMismatch(got itemObservation, sourceUUID string, enabled bool, index int64, tr transformModel) string {
	if got.SourceUUID != sourceUUID {
		return fmt.Sprintf("source UUID=%s want %s", got.SourceUUID, sourceUUID)
	}
	if got.Enabled != enabled {
		return fmt.Sprintf("enabled=%t want %t", got.Enabled, enabled)
	}
	if got.Index != index {
		return fmt.Sprintf("index=%d want %d", got.Index, index)
	}
	if !transformEqual(got.Transform, tr) {
		return "transform: " + transformDifference(got.Transform, tr)
	}
	return ""
}

func transformEqual(a, b transformModel) bool {
	if a.X.IsNull() || a.X.IsUnknown() || b.X.IsNull() || b.X.IsUnknown() || a.Y.IsNull() || a.Y.IsUnknown() || b.Y.IsNull() || b.Y.IsUnknown() || a.ScaleX.IsNull() || a.ScaleX.IsUnknown() || b.ScaleX.IsNull() || b.ScaleX.IsUnknown() || a.ScaleY.IsNull() || a.ScaleY.IsUnknown() || b.ScaleY.IsNull() || b.ScaleY.IsUnknown() || a.Rotation.IsNull() || a.Rotation.IsUnknown() || b.Rotation.IsNull() || b.Rotation.IsUnknown() {
		return false
	}
	return transformBoundsDifference(a, b) == "" && a.X.ValueFloat64() == b.X.ValueFloat64() && a.Y.ValueFloat64() == b.Y.ValueFloat64() && a.ScaleX.ValueFloat64() == b.ScaleX.ValueFloat64() && a.ScaleY.ValueFloat64() == b.ScaleY.ValueFloat64() && rotationMatches(a.Rotation.ValueFloat64(), b.Rotation.ValueFloat64()) && a.CropLeft.ValueInt64() == b.CropLeft.ValueInt64() && a.CropTop.ValueInt64() == b.CropTop.ValueInt64() && a.CropRight.ValueInt64() == b.CropRight.ValueInt64() && a.CropBottom.ValueInt64() == b.CropBottom.ValueInt64() && a.Alignment.ValueInt64() == b.Alignment.ValueInt64() && a.BoundsType.ValueString() == b.BoundsType.ValueString() && a.BoundsAlignment.ValueInt64() == b.BoundsAlignment.ValueInt64()
}

func transformDifference(a, b transformModel) string {
	if d := transformBoundsDifference(a, b); d != "" {
		return d
	}
	checks := []struct {
		name string
		a, b float64
	}{{"x", a.X.ValueFloat64(), b.X.ValueFloat64()}, {"y", a.Y.ValueFloat64(), b.Y.ValueFloat64()}, {"scale_x", a.ScaleX.ValueFloat64(), b.ScaleX.ValueFloat64()}, {"scale_y", a.ScaleY.ValueFloat64(), b.ScaleY.ValueFloat64()}, {"rotation", a.Rotation.ValueFloat64(), b.Rotation.ValueFloat64()}}
	for _, c := range checks {
		if c.name == "rotation" && rotationMatches(c.a, c.b) {
			continue
		}
		if c.a != c.b {
			return fmt.Sprintf("%s=%v want %v", c.name, c.a, c.b)
		}
	}
	ints := []struct {
		name string
		a, b int64
	}{{"crop_left", a.CropLeft.ValueInt64(), b.CropLeft.ValueInt64()}, {"crop_top", a.CropTop.ValueInt64(), b.CropTop.ValueInt64()}, {"crop_right", a.CropRight.ValueInt64(), b.CropRight.ValueInt64()}, {"crop_bottom", a.CropBottom.ValueInt64(), b.CropBottom.ValueInt64()}, {"alignment", a.Alignment.ValueInt64(), b.Alignment.ValueInt64()}, {"bounds_alignment", a.BoundsAlignment.ValueInt64(), b.BoundsAlignment.ValueInt64()}}
	for _, c := range ints {
		if c.a != c.b {
			return fmt.Sprintf("%s=%d want %d", c.name, c.a, c.b)
		}
	}
	if a.BoundsType.ValueString() != b.BoundsType.ValueString() {
		return fmt.Sprintf("bounds_type=%s want %s", a.BoundsType.ValueString(), b.BoundsType.ValueString())
	}
	if !transformEqual(a, b) {
		return "unknown-or-null transform value"
	}
	return ""
}

func waitItemExpected(ctx context.Context, cl requester, scene string, id int64, sourceUUID string, enabled bool, index int64, tr transformModel, attempts int, delay time.Duration) (itemObservation, error) {
	var last string
	for i := 0; i < attempts; i++ {
		got, ok, err := itemByID(ctx, cl, scene, id)
		if err != nil {
			traceErr := recordTrace(map[string]any{"event": "postcondition", "object": "scene-item", "status": "failed-or-uncertain", "scene": scene, "item_id": id, "source_uuid": sourceUUID, "error": err.Error()})
			if traceErr != nil {
				return itemObservation{}, fmt.Errorf("%w; receipt write also failed: %v", err, traceErr)
			}
			return itemObservation{}, err
		}
		if !ok {
			last = "item absent"
		} else if mismatch := itemMismatch(got, sourceUUID, enabled, index, tr); mismatch == "" {
			if err := recordTrace(map[string]any{"event": "postcondition", "object": "scene-item", "status": "passed", "scene": scene, "item_id": id, "source_uuid": sourceUUID}); err != nil {
				return itemObservation{}, fmt.Errorf("scene item converged but receipt write failed: %w", err)
			}
			return got, nil
		} else {
			last = mismatch
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return itemObservation{}, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	err := fmt.Errorf("scene item postcondition did not converge: %s", last)
	if traceErr := recordTrace(map[string]any{"event": "postcondition", "object": "scene-item", "status": "failed-or-uncertain", "scene": scene, "item_id": id, "source_uuid": sourceUUID, "error": err.Error()}); traceErr != nil {
		return itemObservation{}, fmt.Errorf("%w; receipt write also failed: %v", err, traceErr)
	}
	return itemObservation{}, err
}

func waitInputExpected(ctx context.Context, cl requester, uuid, name, kind, file string, unload bool, attempts int, delay time.Duration) (inputObservation, error) {
	var last string
	for i := 0; i < attempts; i++ {
		got, ok, err := inputByUUID(ctx, cl, uuid)
		if err != nil {
			traceErr := recordTrace(map[string]any{"event": "postcondition", "object": "input", "status": "failed-or-uncertain", "input_uuid": uuid, "error": err.Error()})
			if traceErr != nil {
				return inputObservation{}, fmt.Errorf("%w; receipt write also failed: %v", err, traceErr)
			}
			return inputObservation{}, err
		}
		switch {
		case !ok:
			last = "input absent"
		case got.Name != name:
			last = fmt.Sprintf("name=%q want %q", got.Name, name)
		case got.Kind != kind:
			last = fmt.Sprintf("kind=%q want %q", got.Kind, kind)
		case got.File != file:
			last = fmt.Sprintf("file=%q want %q", got.File, file)
		case got.Unload != unload:
			last = fmt.Sprintf("unload=%t want %t", got.Unload, unload)
		default:
			if err := recordTrace(map[string]any{"event": "postcondition", "object": "input", "status": "passed", "input_uuid": uuid}); err != nil {
				return inputObservation{}, fmt.Errorf("input converged but receipt write failed: %w", err)
			}
			return got, nil
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return inputObservation{}, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	err := fmt.Errorf("input postcondition did not converge: %s", last)
	if traceErr := recordTrace(map[string]any{"event": "postcondition", "object": "input", "status": "failed-or-uncertain", "input_uuid": uuid, "error": err.Error()}); traceErr != nil {
		return inputObservation{}, fmt.Errorf("%w; receipt write also failed: %v", err, traceErr)
	}
	return inputObservation{}, err
}

func sliceValue(v any) []any        { x, _ := v.([]any); return x }
func mapValue(v any) map[string]any { x, _ := v.(map[string]any); return x }
func stringValue(v any) string      { x, _ := v.(string); return x }
func boolValue(v any) bool          { x, _ := v.(bool); return x }
func int64Value(v any) int64        { x, _ := numberValue(v); return int64(x) }
func numberValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func requiredSlice(m map[string]any, key string) ([]any, error) {
	v, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("response omitted %s", key)
	}
	x, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("response %s has type %T, want array", key, v)
	}
	return x, nil
}
