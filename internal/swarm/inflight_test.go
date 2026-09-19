package swarm

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A ROUTE AT ITS CAP LAUNCHES NOTHING FURTHER until one in flight finishes (#917). This is
// the whole mechanism: with the cap at 2 and five cards wanting the same route, exactly two
// are ever running, and the third starts only when one of the first two has ended.
func TestARouteAtItsCapHoldsTheRestBack(t *testing.T) {
	f := newInflight(2)
	const route = "opencode/muse-spark-1.3-contributor-free@muse"
	var running, peak atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	finish := make(chan struct{})
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if !f.acquire(route) {
				return
			}
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-finish
			running.Add(-1)
			f.release(route)
		}()
	}
	close(start)
	// Let every goroutine reach its acquire. Two get in; three block on the condition
	// variable, which is the state this test is about.
	waitFor(t, func() bool { return running.Load() == 2 })
	if got := running.Load(); got != 2 {
		t.Fatalf("a cap of 2 admits 2 at once, got %d", got)
	}
	close(finish)
	wg.Wait()
	if got := peak.Load(); got != 2 {
		t.Fatalf("no more than the cap is ever in flight at once; peak was %d", got)
	}
	lines := f.statusLines()
	if len(lines) != 1 || !strings.Contains(lines[0], "cap=2 peak=2 held-back=3") {
		t.Fatalf("the STATUS line says what the cap did: %q", lines)
	}
}

// TWO KEYS ARE TWO QUEUES, and two models on one key are one. The measurement is per KEY --
// the tier queues on the credential -- so the route is the provider, the model and the key,
// and neither the model alone nor the key alone is the unit.
func TestTheCapIsPerRouteAndNotGlobal(t *testing.T) {
	f := newInflight(1)
	a := RouteKey("opencode/muse-spark", "key-one")
	b := RouteKey("opencode/muse-spark", "key-two")
	if a == b {
		t.Fatal("the same model on two keys is two routes")
	}
	if !f.acquire(a) {
		t.Fatal("the first route's only slot")
	}
	// The other key's queue is untouched: this must not block, and a cap that was global
	// would deadlock the test here.
	done := make(chan bool, 1)
	go func() { done <- f.acquire(b) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("a second route's slot is its own")
		}
	case <-time.After(30 * time.Second): // wall-ok: a deadlock's give-up, never a product bound
		t.Fatal("a cap on one route blocked another: the cap is global, and it must not be")
	}
	// And the same key with a different model is also its own queue only if the model
	// differs -- the key is not the whole unit either.
	if RouteKey("opencode/one", "k") == RouteKey("opencode/two", "k") {
		t.Fatal("two models are two routes")
	}
}

// NO CAP IS TODAY'S BEHAVIOUR, BYTE FOR BYTE. A batch that asked for none launches every
// card at once and prints no ROUTE line at all.
func TestNoCapHoldsNothingBack(t *testing.T) {
	f := newInflight(0)
	const route = "m@k"
	for i := 0; i < 50; i++ {
		if !f.acquire(route) {
			t.Fatalf("an uncapped route never waits, and acquire %d did", i)
		}
	}
	if lines := f.statusLines(); len(lines) != 0 {
		t.Fatalf("a batch with no cap prints no cap line: %q", lines)
	}
}

// A CLOSED GATE RELEASES EVERY WAITER AND LAUNCHES NONE OF THEM. The batch closes it when
// its deadline has passed: a card still held then is never going to run, and a launcher
// goroutine parked on a condition variable is a batch that does not return.
func TestClosingTheGateWakesEveryWaiterAndLaunchesNone(t *testing.T) {
	f := newInflight(1)
	const route = "m@k"
	if !f.acquire(route) {
		t.Fatal("the only slot")
	}
	var launched atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.acquire(route) {
				launched.Add(1)
			}
		}()
	}
	f.close()
	wg.Wait() // without close this never returns, which is the assertion
	if got := launched.Load(); got != 0 {
		t.Fatalf("a card held when the batch gave up is never launched, and %d were", got)
	}
}

// A ROUTE NAMES ITS KEY BY PROFILE AND NEVER BY VALUE. This string reaches a STATUS line and
// a log; a secret that can reach a log is a secret that will.
func TestARouteNamesTheProfileNeverTheSecret(t *testing.T) {
	got := RouteKey("opencode/muse", "muse-contributor-free")
	if !strings.Contains(got, "muse-contributor-free") {
		t.Fatalf("the route names the auth PROFILE: %q", got)
	}
	// An empty model or profile is a dash and never an empty field a reader could misread
	// as a missing column.
	if got := RouteKey("", ""); got != "-@-" {
		t.Fatalf("an unnamed route is dashes, got %q", got)
	}
}

// waitFor polls a condition until it holds or the test gives up.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // wall-ok: a test's give-up, not a product bound
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond) // wall-ok: polling a condition in a test
	}
	t.Fatal("the condition never held")
}

// THE ROUTE IS ONE FIELD ON THE LINE, whatever the auth profile is called. The profile half
// of a route is a PATH a person typed on `--auth`, and a path may carry a space, a tab or a
// newline; the model half comes out of a cards TSV. `BATCH ROUTE <route> cap=… peak=…
// held-back=…` is read by splitting on spaces, so a route printed raw turns one line into
// two and every field after it into a stranger -- and a newline in it forges a whole line.
func TestTheRouteIsOneFieldOnTheStatusLine(t *testing.T) {
	f := newInflight(1)
	route := RouteKey("prov/model", "/tmp/my keys/auth.json")
	if !f.acquire(route) {
		t.Fatal("the first acquire under a cap of one is granted")
	}
	lines := f.statusLines()
	if len(lines) != 1 {
		t.Fatalf("one route gives one line, got %d: %q", len(lines), lines)
	}
	got := lines[0]
	fields := strings.Fields(got)
	if len(fields) != 6 {
		t.Fatalf("BATCH ROUTE <route> cap= peak= held-back= is six fields, got %d in %q", len(fields), got)
	}
	if fields[2] != oneline.Field(route) {
		t.Fatalf("the route field is the escaped route %q, got %q (line %q)", oneline.Field(route), fields[2], got)
	}
	// And a newline in the profile can never end the line early.
	g := newInflight(1)
	forged := RouteKey("prov/model", "a\nBATCH ROUTE forged cap=99 peak=99 held-back=99")
	if !g.acquire(forged) {
		t.Fatal("acquire granted")
	}
	out := g.statusLines()
	if len(out) != 1 || strings.Contains(out[0], "\n") {
		t.Fatalf("a newline in the auth profile must not forge a second line, got %q", out)
	}
}
