//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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

// fleetReady is the ready cards on a member's queue, as the member sees them.
func fleetReady(t *testing.T, do func(args ...string) string, member string) int {
	t.Helper()
	var q struct {
		Cards []struct {
			Col string `json:"col"`
		} `json:"cards"`
	}
	require.NoError(t, json.Unmarshal([]byte(do("queue", "--as", member, "--json")), &q), "queue of %s", member)
	n := 0
	for _, c := range q.Cards {
		if c.Col == "ready" {
			n++
		}
	}
	return n
}

// The owner's run on the store (the dogfood finding of 2026-09-30, "ticking
// halves"; his words: "we need to not do this. whole fleet table, one
// update."): eight machines of width 64, three streams of 1,000. The machine
// ticks and the world plays in turn, so every deal sees every member's room
// freed by the world's whole batch before it: the tick deals, the world takes
// up to its running width (every member up takes in the one step after the
// deal reaches it), retaining the dealt-ahead backlog. The world finishes
// its running batch, and the tick deals again. Every
// tick that deals reaches every machine (the whole fleet in one update), never
// part of the fleet one tick and the rest the next; over the run no member is
// dealt more than one card more than another (the rolling index, errata 3
// amendment 5, goes round the fleet a card a member); and the deal's index is a
// counter (errata 3 amendment 5, the owner's form).
//
// The turns are the test's, not a sleep's: a machine ticking on the log while
// the world plays at 10 ms is not a run this can assert the whole fleet of. The
// world and the machine each write the store in steps of their own, and a deal
// that commits between two of the world's steps deals the room it finds: a run
// of it on a loaded machine dealt 448 cards, 64 to each of seven members, the
// eighth not yet having room. A deal reaches every member that has room. The
// wake on the log is run_wake_functional_test.go's.
func TestTheWholeFleetMovesInOneTickOnTheStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	require.NoError(t, fn.Load(ctx, c))
	env := map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}
	world := newApp(func(k string) string { return env[k] })
	defer world.close()
	do := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := world.run(args, &out, &errb)
		require.Equal(t, 0, code, "%v: %d %s", args, code, errb.String())
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
	require.NotNil(t, st, "run: %d", code)
	var out, errb lockedBuffer
	// Each round is one machine tick and one world tick; the world takes
	// its width (and, the round after, finishes it), so the next deal that
	// has cards to give has room across the whole fleet. A round is
	// 1 ms of world time, and the rounds are bounded, never timed.
	const rounds = 16
	dealing := 0
	for i := 0; i < rounds; i++ {
		loop.runLoop(ctx, st, 0, 1, &out, &errb)
		dealt := len(loopDeals(out.String())) > dealing
		dealing = len(loopDeals(out.String()))
		beforeReady := map[string]int{}
		if dealt {
			for _, m := range members {
				beforeReady[m] = fleetReady(t, do, m)
			}
		}
		var pout, perr bytes.Buffer
		code = world.run([]string{"play", "--every", "1ms", "--ticks", "1"}, &pout, &perr)
		require.Equal(t, 0, code, "play: %d %s%s", code, pout.String(), perr.String())
		if !dealt {
			continue
		}
		// Every member takes its running width; the dealt-ahead remainder
		// stays ready. The world finishes the previous batch before taking.
		snap, err := st.Load(ctx, store.All, nil)
		require.NoError(t, err)
		for _, m := range members {
			assert.Equal(t, max(0, beforeReady[m]-64), fleetReady(t, do, m), "round %d: %s takes its width in this tick", i+1, m)
			assert.Equal(t, min(64, beforeReady[m]), snap.Fleet.Count(m, sprint.Working), "round %d: %s fills its running width", i+1, m)
		}
	}

	// the loop's twin never disagreed with the store's own counts
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "TIMES ") {
			assert.Contains(t, l, " mismatch=0:", "the loop's twin did not add up to the store's counts: %s", l)
		}
	}
	ticks := loopDeals(out.String())
	require.GreaterOrEqual(t, len(ticks), 3, "%d ticks dealt in %d rounds, want at least 3: %s", len(ticks), rounds, out.String())
	dealt := 0
	perMember := map[string]int{}
	for i, d := range ticks {
		var set []string
		for _, m := range d {
			if !slices.Contains(set, m) {
				set = append(set, m)
			}
			perMember[m]++
		}
		slices.Sort(set)
		t.Logf("tick %d: %d dealt to %v", i+1, len(d), set)
		dealt += len(d)
		if len(d) >= len(members) && !slices.Equal(set, members) {
			t.Fatalf("tick %d dealt %d cards to %v only: every tick's deal reaches the whole fleet", i+1, len(d), set)
		}
	}
	least, most := dealt, 0
	for _, m := range members {
		least, most = min(least, perMember[m]), max(most, perMember[m])
	}
	require.LessOrEqual(t, most-least, 1, "members were dealt from %d to %d cards over the run (%v): the deal goes round the fleet a card a member, so no member is more than one card ahead", least, most, perMember)
	snap, err := st.Load(ctx, store.All, nil)
	require.NoError(t, err)
	at, _ := snap.Fleet.Prop(sprint.PropDealIndex)
	n, err := strconv.ParseUint(at, 10, 64)
	require.NoError(t, err, "deal_index %q after %d cards dealt: want a counter, up by at least one a placement", at, dealt)
	require.GreaterOrEqual(t, n, uint64(dealt), "deal_index %q after %d cards dealt: want a counter, up by at least one a placement", at, dealt)
	t.Logf("deal_index %s after %d cards dealt in %d ticks", at, dealt, len(ticks))
}
