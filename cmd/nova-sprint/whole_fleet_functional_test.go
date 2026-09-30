//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/redis/go-redis/v9"
)

// lockedBuffer is a buffer the loop writes to while the test runs.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// loopDeals is the members each printed tick of run dealt to, in order, one
// entry a tick that dealt.
func loopDeals(out string) [][]string {
	var ticks [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			ticks = append(ticks, cur)
		}
		cur = nil
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, " tick") {
			flush()
			continue
		}
		if !strings.HasPrefix(l, "MOVED deal: ") {
			continue
		}
		if _, m, ok := strings.Cut(l, " member="); ok {
			m, _, _ = strings.Cut(m, " ")
			cur = append(cur, m)
		}
	}
	flush()
	return ticks
}

// The owner's run on the store (the dogfood finding of 2026-09-30, "ticking
// halves"; his words: "we need to not do this. whole fleet table, one
// update."): eight machines of width 64, three streams of 1,000, the loop
// woken by the log, the world played at 10 ms. Every tick that deals reaches
// every machine (the whole fleet in one update), never part of the fleet one
// tick and the rest the next, and the deal's index is a counter
// (errata 3 amendment 5, the owner's form).
func TestTheWholeFleetMovesInOneTickOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world := newApp(func(k string) string { return env[k] })
	defer world.close()
	do := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := world.run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: %d %s", args, code, errb.String())
		}
		return out.String()
	}
	var members, spec []string
	for i := 1; i <= 8; i++ {
		members = append(members, "m"+strconv.Itoa(i))
		spec = append(spec, members[i-1]+":64")
	}
	do("init", "--readers", "reader-a,reader-b,reader-c", "--members", strings.Join(spec, ","))
	do("add", "--stream", "a,b,c", "--count", "1000")
	for _, m := range members {
		do("fleet", "beat", m)
	}
	do("start")

	loop := newApp(func(k string) string { return env[k] })
	defer loop.close()
	loop.checkTwin = func(twin, fresh *sprint.Snapshot) error {
		if d := store.TwinDiff(twin, fresh); d != "" {
			return errors.New(d)
		}
		return nil
	}
	st, _, code := loop.machineVerb("run", nil, &bytes.Buffer{})
	if st == nil {
		t.Fatalf("run: %d", code)
	}
	var out, errb lockedBuffer
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.runLoop(lctx, st, 0, 0, &out, &errb)
	}()
	var pout, perr bytes.Buffer
	world.run([]string{"play", "--every", "10ms", "--ticks", "40"}, &pout, &perr)
	cancel()
	<-done

	// the loop's twin never disagreed with the store's own counts
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "TIMES ") && !strings.Contains(l, " mismatch=0:") {
			t.Errorf("the loop's twin did not add up to the store's counts: %s", l)
		}
	}
	ticks := loopDeals(out.String())
	if len(ticks) < 3 {
		t.Fatalf("%d ticks dealt, want at least 3: %s", len(ticks), out.String())
	}
	dealt := 0
	for i, d := range ticks {
		var set []string
		for _, m := range d {
			if !slices.Contains(set, m) {
				set = append(set, m)
			}
		}
		slices.Sort(set)
		t.Logf("tick %d: %d dealt to %v", i+1, len(d), set)
		dealt += len(d)
		if len(d) >= len(members) && !slices.Equal(set, members) {
			t.Fatalf("tick %d dealt %d cards to %v only: every tick's deal reaches the whole fleet", i+1, len(d), set)
		}
	}
	snap, err := st.Load(ctx, store.All, nil)
	if err != nil {
		t.Fatal(err)
	}
	at, _ := snap.Fleet.Prop(sprint.PropDealIndex)
	n, err := strconv.ParseUint(at, 10, 64)
	if err != nil || n < uint64(dealt) {
		t.Fatalf("deal_index %q after %d cards dealt: want a counter, up by at least one a placement", at, dealt)
	}
	t.Logf("deal_index %s after %d cards dealt in %d ticks", at, dealt, len(ticks))
}
