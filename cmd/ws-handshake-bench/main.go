// ws-handshake-bench: read-only timing of the obs-websocket transport strategies the
// provider could use. Sends only Get* requests; never mutates OBS and never prints
// credentials.
//
//	A  dial-per-call        what tfprovider client.with() does today (1 request each)
//	B  persistent, serial   one connection, N sequential requests
//	C  persistent, batch    one connection, one RequestBatch of N
//	D  handshake only       Connect+Identify+Close, no request
//	E  K connections, concurrent reads   does OBS overlap reads across clients?
//	F  realistic Read replay             GetSceneCollectionList+GetSceneList+GetSceneItemList
//	                                     per Read, i.e. what a scene_item Read sends
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/DigiBugCat/terraform-provider-obs/internal/ws"
)

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return v
}

func arrOf(v any) []any { a, _ := v.([]any); return a }

func main() {
	n, k := 100, 8
	if len(os.Args) > 1 {
		n = must(strconv.Atoi(os.Args[1]))
	}
	if len(os.Args) > 2 {
		k = must(strconv.Atoi(os.Args[2]))
	}
	cfg, path, err := ws.DiscoverConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "discover:", err)
		os.Exit(1)
	}
	fmt.Printf("config=%s host=%s port=%d N=%d K=%d\n", path, cfg.Host, cfg.Port, n, k)
	ctx := context.Background()

	// Pick the scene with the most items: the realistic worst case for a Read.
	cl := must(ws.Connect(ctx, cfg))
	sl := must(cl.Req(ctx, "GetSceneList", nil))
	scenes := arrOf(sl["scenes"])
	if len(scenes) == 0 {
		fmt.Fprintln(os.Stderr, "no scenes")
		os.Exit(1)
	}
	scene, most := "", -1
	for _, s := range scenes {
		name, _ := s.(map[string]any)["sceneName"].(string)
		x := must(cl.Req(ctx, "GetSceneItemList", map[string]any{"sceneName": name}))
		if c := len(arrOf(x["sceneItems"])); c > most {
			scene, most = name, c
		}
	}
	cl.Close()
	read := map[string]any{"sceneName": scene}
	fmt.Printf("scene=%q items=%d\n", scene, most)
	per := func(d time.Duration) time.Duration { return d / time.Duration(n) }

	// A: dial per call (current provider behaviour).
	t := time.Now()
	for i := 0; i < n; i++ {
		c := must(ws.Connect(ctx, cfg))
		must(c.Req(ctx, "GetSceneItemList", read))
		c.Close()
	}
	a := time.Since(t)

	// B: one persistent connection, serial requests.
	c := must(ws.Connect(ctx, cfg))
	t = time.Now()
	for i := 0; i < n; i++ {
		must(c.Req(ctx, "GetSceneItemList", read))
	}
	b := time.Since(t)

	// C: one persistent connection, one RequestBatch.
	items := make([]ws.BatchItem, n)
	for i := range items {
		items[i] = ws.BatchItem{Type: "GetSceneItemList", Data: read}
	}
	t = time.Now()
	res := must(c.Batch(ctx, items, 200, false))
	cc := time.Since(t)
	ok := 0
	for _, r := range res {
		if r.OK {
			ok++
		}
	}

	// F: realistic scene_item Read replay, serial on the persistent connection.
	t = time.Now()
	for i := 0; i < n; i++ {
		must(c.Req(ctx, "GetSceneCollectionList", nil))
		must(c.Req(ctx, "GetSceneList", nil))
		must(c.Req(ctx, "GetSceneItemList", read))
	}
	f := time.Since(t)
	c.Close()

	// D: handshake only.
	t = time.Now()
	for i := 0; i < n; i++ {
		must(ws.Connect(ctx, cfg)).Close()
	}
	d := time.Since(t)

	// E: K persistent connections, N reads spread across them concurrently.
	conns := make([]*ws.Client, k)
	for i := range conns {
		conns[i] = must(ws.Connect(ctx, cfg))
	}
	var wg sync.WaitGroup
	t = time.Now()
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(c *ws.Client, share int) {
			defer wg.Done()
			for j := 0; j < share; j++ {
				must(c.Req(ctx, "GetSceneItemList", read))
			}
		}(conns[i], n/k)
	}
	wg.Wait()
	e := time.Since(t)
	for _, c := range conns {
		c.Close()
	}

	fmt.Printf("A dial-per-call, 1 req        : %9v  (%v/op)\n", a, per(a))
	fmt.Printf("B persistent serial, 1 req    : %9v  (%v/op)\n", b, per(b))
	fmt.Printf("C persistent batch, 1 req     : %9v  (%v/op, %d/%d ok)\n", cc, per(cc), ok, n)
	fmt.Printf("D handshake only              : %9v  (%v/op)\n", d, per(d))
	fmt.Printf("E %d conns concurrent, 1 req   : %9v  (%v/op)\n", k, e, per(e))
	fmt.Printf("F realistic Read (3 req) serial: %9v  (%v/Read)\n", f, per(f))
	fmt.Printf("=> handshake share of A: %.0f%%   B->C batch: %.1fx   B->E concurrency(K=%d): %.1fx   A->C: %.1fx\n",
		100*float64(d)/float64(a), float64(b)/float64(cc), k, float64(b)/float64(e), float64(a)/float64(cc))
}
