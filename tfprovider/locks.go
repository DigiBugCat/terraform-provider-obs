package tfprovider

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Locking that fails loudly. A plain sync.Mutex turns a lock-ordering bug into a silent
// hang inside a Terraform apply; every wait here is bounded by the request context and
// by lockWaitTimeout, and a timeout reports which operation holds the lock and for how
// long, so a deadlock becomes a diagnosable error instead of a stuck plan.
//
// Two levels: a global gate (shared for reads and keyed mutations, exclusive for
// keyless mutations) and one lock per container key.

const defaultLockWait = 60 * time.Second

func lockWaitTimeout() time.Duration {
	if v, err := strconv.Atoi(os.Getenv("OBS_LOCK_TIMEOUT_MS")); err == nil && v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return defaultLockWait
}

type lockHolder struct {
	op    string
	since time.Time
}

type lockGate struct {
	mu      sync.Mutex
	writer  *lockHolder            // exclusive holder, or nil
	readers map[string]int         // op label -> count of shared holders
	pending []string               // exclusive waiters, oldest first
	keys    map[string]*lockHolder // container key -> holder
	changed chan struct{}          // closed and replaced whenever state changes
}

func newLockGate() *lockGate {
	return &lockGate{readers: map[string]int{}, keys: map[string]*lockHolder{}, changed: make(chan struct{})}
}

func (g *lockGate) notify() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// wait blocks until try succeeds, the context ends, or the timeout passes; describe
// renders the current holders for the error.
func (g *lockGate) wait(ctx context.Context, what, op string, try func() bool, describe func() string) error {
	deadline := time.NewTimer(lockWaitTimeout())
	defer deadline.Stop()
	for {
		g.mu.Lock()
		if try() {
			g.mu.Unlock()
			return nil
		}
		ch := g.changed
		holders := describe()
		g.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("%s: %s gave up waiting for %s (%s): %w", op, op, what, holders, ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("lock wait timeout: %s waited %v for %s; %s", op, lockWaitTimeout(), what, holders)
		}
	}
}

func (g *lockGate) describe() string {
	var parts []string
	if g.writer != nil {
		parts = append(parts, fmt.Sprintf("exclusive held by %s for %v", g.writer.op, time.Since(g.writer.since).Round(time.Millisecond)))
	}
	for op, n := range g.readers {
		parts = append(parts, fmt.Sprintf("shared x%d by %s", n, op))
	}
	if len(g.pending) > 0 {
		parts = append(parts, "exclusive pending: "+strings.Join(g.pending, ", "))
	}
	if len(parts) == 0 {
		return "no holders"
	}
	return strings.Join(parts, "; ")
}

// shared admits readers and keyed mutations together; a pending exclusive request
// blocks new shared entries so it cannot starve.
func (g *lockGate) shared(ctx context.Context, op string) (func(), error) {
	err := g.wait(ctx, "the shared gate", op, func() bool {
		if g.writer != nil || len(g.pending) > 0 {
			return false
		}
		g.readers[op]++
		return true
	}, g.describe)
	if err != nil {
		return nil, err
	}
	return func() {
		g.mu.Lock()
		g.readers[op]--
		if g.readers[op] == 0 {
			delete(g.readers, op)
		}
		g.notify()
		g.mu.Unlock()
	}, nil
}

func (g *lockGate) exclusive(ctx context.Context, op string) (func(), error) {
	g.mu.Lock()
	g.pending = append(g.pending, op)
	g.mu.Unlock()
	unpend := func() {
		for i, p := range g.pending {
			if p == op {
				g.pending = append(g.pending[:i], g.pending[i+1:]...)
				break
			}
		}
	}
	err := g.wait(ctx, "the exclusive gate", op, func() bool {
		if g.writer != nil || len(g.readers) > 0 || g.pending[0] != op {
			return false
		}
		unpend()
		g.writer = &lockHolder{op: op, since: time.Now()}
		return true
	}, g.describe)
	if err != nil {
		g.mu.Lock()
		unpend()
		g.notify()
		g.mu.Unlock()
		return nil, err
	}
	return func() {
		g.mu.Lock()
		g.writer = nil
		g.notify()
		g.mu.Unlock()
	}, nil
}

// key locks one container. Callers take keys in sorted order, so waits never cycle.
func (g *lockGate) key(ctx context.Context, key, op string) (func(), error) {
	err := g.wait(ctx, "container "+key, op, func() bool {
		if g.keys[key] != nil {
			return false
		}
		g.keys[key] = &lockHolder{op: op, since: time.Now()}
		return true
	}, func() string {
		if h := g.keys[key]; h != nil {
			return fmt.Sprintf("held by %s for %v", h.op, time.Since(h.since).Round(time.Millisecond))
		}
		return "free"
	})
	if err != nil {
		return nil, err
	}
	return func() {
		g.mu.Lock()
		delete(g.keys, key)
		g.notify()
		g.mu.Unlock()
	}, nil
}

// opLabel names the calling resource operation ("sceneItemResource.Create") from the
// stack so lock diagnostics need no call-site plumbing.
func opLabel() string {
	pcs := make([]uintptr, 24)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		name := f.Function
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		name = strings.TrimPrefix(name, "tfprovider.")
		for _, m := range []string{".Create", ".Read", ".Update", ".Delete", ".ImportState"} {
			if i := strings.Index(name, m); i >= 0 {
				return strings.Trim(name[:i+len(m)], "(*)")
			}
		}
		if !more {
			return "unknown-op"
		}
	}
}
