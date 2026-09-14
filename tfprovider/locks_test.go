package tfprovider

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLockGateKeyedOverlapAndExclusion(t *testing.T) {
	g := newLockGate()
	ctx := context.Background()
	relA, err := g.shared(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	unlockX, err := g.key(ctx, "x", "a")
	if err != nil {
		t.Fatal(err)
	}
	// Different key: proceeds while x is held.
	relB, err := g.shared(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	unlockY, err := g.key(ctx, "y", "b")
	if err != nil {
		t.Fatalf("different key should not wait: %v", err)
	}
	unlockY()
	relB()
	// Same key: waits until released.
	done := make(chan struct{})
	go func() {
		defer close(done)
		rel, err := g.shared(ctx, "c")
		if err != nil {
			t.Error(err)
			return
		}
		defer rel()
		u, err := g.key(ctx, "x", "c")
		if err != nil {
			t.Error(err)
			return
		}
		u()
	}()
	select {
	case <-done:
		t.Fatal("same key overlapped")
	case <-time.After(50 * time.Millisecond):
	}
	unlockX()
	relA()
	<-done
}

func TestLockGateTimeoutNamesHolder(t *testing.T) {
	t.Setenv("OBS_LOCK_TIMEOUT_MS", "100")
	g := newLockGate()
	ctx := context.Background()
	rel, _ := g.shared(ctx, "sceneItemResource.Create")
	defer rel()
	unlock, _ := g.key(ctx, "scene-1", "sceneItemResource.Create")
	defer unlock()
	_, err := g.key(ctx, "scene-1", "sceneContentsResource.Create")
	if err == nil || !strings.Contains(err.Error(), "held by sceneItemResource.Create") || !strings.Contains(err.Error(), "sceneContentsResource.Create waited") {
		t.Fatalf("timeout should name holder and waiter: %v", err)
	}
	// Exclusive waits for shared holders and blocks new shared entries meanwhile.
	_, err = g.exclusive(ctx, "pluginResource.Create")
	if err == nil || !strings.Contains(err.Error(), "shared x1 by sceneItemResource.Create") {
		t.Fatalf("exclusive timeout should list shared holders: %v", err)
	}
}

func TestLockGateExclusiveExcludesEverything(t *testing.T) {
	g := newLockGate()
	ctx := context.Background()
	rel, err := g.exclusive(ctx, "w")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	entered := make(chan string, 2)
	for _, op := range []string{"r", "k"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			r, err := g.shared(ctx, op)
			if err != nil {
				t.Error(err)
				return
			}
			entered <- op
			r()
		}(op)
	}
	select {
	case op := <-entered:
		t.Fatalf("%s entered during exclusive", op)
	case <-time.After(50 * time.Millisecond):
	}
	rel()
	wg.Wait()
}
