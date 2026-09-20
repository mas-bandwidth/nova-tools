package pulse

// Two independent fill dealers that both observe one free seat must not both launch.
// Stella's HOLD on #2029: rename protects the same card, not shared capacity. A barrier
// forces both ticks to see free=1 before either claims; the failing control is two
// different cards launched. A losing dealer stands down. Reservations stay until owned
// execution or lease reconciliation; UNKNOWN is not freed.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// dealerCap is the barrier control: every Capacity call answers n, then waits until the
// test has seen both dealers observe, so both ticks go on with the same stale free=1.
type dealerCap struct {
	n        int
	observed chan struct{}
	release  chan struct{}
}

func (c *dealerCap) Capacity(bench string) (int, error) {
	c.observed <- struct{}{}
	<-c.release
	return c.n, nil
}

// dealerLauncher records every Launch call, including concurrent ones.
type dealerLauncher struct {
	mu    sync.Mutex
	calls []string
}

func (l *dealerLauncher) Launch(bench, seat, card string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, bench+" "+filepath.Base(card))
	return nil
}

func (l *dealerLauncher) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.calls))
	copy(out, l.calls)
	return out
}

// TestFillTwoDealersObservingOneFreeSeatDoNotOverDispatch is the original deterministic
// two-dealer control: two Fill ticks, one bench, free=1, two ready cards. Both observe
// free=1 before either claims. Exactly one card may launch; the loser stands down.
func TestFillTwoDealersObservingOneFreeSeatDoNotOverDispatch(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)

	observed := make(chan struct{}, 2)
	release := make(chan struct{})
	cap := &dealerCap{n: 1, observed: observed, release: release}
	l := &dealerLauncher{}

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			codes[i] = Fill(FillInput{
				Ready: ready, Launched: launched,
				Machines: machines,
				Benches:  []string{"bench-a"},
				Once:     true,
				Stdout:   &out,
				Stderr:   &errb,
				Capacity: cap,
				Launcher: l,
			})
		}(i)
	}

	waitObserved(t, observed, 2)
	close(release)
	wg.Wait()

	calls := l.snapshot()
	if len(calls) != 1 {
		t.Fatalf("two dealers observing free=1 launched %d cards, want 1 (the losing dealer must stand down): %q",
			len(calls), calls)
	}
	if got := len(readyCards(launched)); got != 1 {
		t.Fatalf("launched holds %d cards, want 1", got)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Fatalf("ready holds %d cards, want 1 (the card the loser did not take)", got)
	}
	for i, code := range codes {
		if code != 0 && code != 1 {
			t.Fatalf("dealer %d exit = %d, want 0 or 1", i, code)
		}
	}
}

// waitObserved waits until n dealers have called Capacity, up to NOVA_TEST_WAIT.
func waitObserved(t *testing.T, observed <-chan struct{}, n int) {
	t.Helper()
	deadline := time.Now().Add(testWait())
	for i := 0; i < n; i++ {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("timed out waiting for %d dealers to observe free capacity; saw %d", n, i)
		}
		select {
		case <-observed:
		case <-time.After(remain):
			t.Fatalf("timed out waiting for %d dealers to observe free capacity; saw %d", n, i)
		}
	}
}

func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// unknownSeatLauncher reports a launch as UNKNOWN: started, but not owned execution.
type unknownSeatLauncher struct {
	mu    sync.Mutex
	calls []string
}

func (l *unknownSeatLauncher) Launch(bench, seat, card string) error {
	_, err := l.LaunchSeat(bench, seat, card)
	return err
}

func (l *unknownSeatLauncher) LaunchSeat(bench, seat, card string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, bench+" "+filepath.Base(card))
	return true, nil
}

func (l *unknownSeatLauncher) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.calls))
	copy(out, l.calls)
	return out
}

// TestFillDoesNotFreeAnUNKNOWNLaunch: a dealer whose launch is UNKNOWN keeps the
// reservation. A second dealer observing the same free=1 stands down, and the seat file
// is still there — UNKNOWN is not freed.
func TestFillDoesNotFreeAnUNKNOWNLaunch(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)

	observed := make(chan struct{}, 2)
	release := make(chan struct{})
	cap := &dealerCap{n: 1, observed: observed, release: release}
	l := &unknownSeatLauncher{}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errb bytes.Buffer
			_ = Fill(FillInput{
				Ready: ready, Launched: launched,
				Machines: machines,
				Benches:  []string{"bench-a"},
				Once:     true,
				Stdout:   &out,
				Stderr:   &errb,
				Capacity: cap,
				Launcher: l,
			})
		}()
	}
	waitObserved(t, observed, 2)
	close(release)
	wg.Wait()

	if got := len(l.snapshot()); got != 1 {
		t.Fatalf("UNKNOWN launch over-dispatched: %d launches, want 1: %q", got, l.snapshot())
	}
	seats, err := filepath.Glob(filepath.Join(launched, ".fill-seats", "bench-a", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(seats) != 1 {
		t.Fatalf("UNKNOWN launch freed its reservation; seat files = %v, want 1", seats)
	}
	body, err := os.ReadFile(seats[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "outcome=unknown") {
		t.Fatalf("the kept reservation is not marked UNKNOWN: %q", body)
	}
}

// TestFillReservationPersistsUntilOwnedLeaseReconciliation: a successful launch keeps the
// reservation through the rest of the tick (no lock-release-after-dispatch). The same
// process drops an owned reservation at the next tick once the launched card is the owned
// execution; a live UNKNOWN reservation is not dropped then.
func TestFillReservationPersistsUntilOwnedLeaseReconciliation(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	stop := filepath.Join(dir, "STOP")
	p := &storeProbe{lines: map[string]string{"bench-a": storeLine(1, 0, 8, 0.1)}}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	slept := 0
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Stop:     stop,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: StoreCapacity{Probe: p.probe, Owner: testOwner, Stderr: &errb},
		Launcher: l,
		Sleep: func(d time.Duration) {
			slept++
			seats, err := filepath.Glob(filepath.Join(launched, ".fill-seats", "bench-a", "*"))
			if err != nil {
				t.Error(err)
				return
			}
			if slept == 1 && len(seats) != 1 {
				t.Errorf("after dispatch, before the next tick, seat files = %v, want 1 (the reservation must still be visible)", seats)
			}
			p.lines["bench-a"] = storeLine(1, 1, 8, 0.1)
			if err := os.WriteFile(stop, nil, 0o644); err != nil {
				t.Error(err)
			}
		},
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1: %q", len(l.calls), l.calls)
	}
}

// TestFillCountsUNKNOWNLeaseStateAsHeld: a lease whose state is UNKNOWN is not a free
// seat. The probe used to refuse the whole reading; UNKNOWN stays accounted as held so
// the seat is not handed out again.
func TestFillCountsUNKNOWNLeaseStateAsHeld(t *testing.T) {
	listing := "SLOT 1 owner=" + testOwner + " pid=9 label=card-001 until=2026-09-20T00:00:00Z state=UNKNOWN\n"
	n, _, err := countLeases(listing, testOwner)
	if err != nil {
		t.Fatalf("UNKNOWN lease was refused rather than held: %v", err)
	}
	if n != 1 {
		t.Fatalf("UNKNOWN held = %d, want 1 (UNKNOWN is not freed)", n)
	}
	a, err := ParseCapacityAnswer("store share=2 cores=8 load1=0.10\nleases\n"+listing, testOwner)
	if err != nil {
		t.Fatalf("a share with an UNKNOWN lease: %v", err)
	}
	if a.Held != 1 || a.Free() != 1 {
		t.Fatalf("share=2 UNKNOWN held=%d free=%d, want held=1 free=1", a.Held, a.Free())
	}
}

// TestReviewOwnedReservationBridgesDelayedLeaseVisibility is Stella's HOLD control
// on #2124: ReconcileOwned must not drop a successful launch just because a later
// tick ran. Capacity still answers free=1 (lease publication delayed). Exactly one
// card may be dispatched into that seat.
func TestReviewOwnedReservationBridgesDelayedLeaseVisibility(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)
	seats := fileSeats(filepath.Join(launched, ".fill-seats"))
	l := &dealerLauncher{}
	in := FillInput{
		Ready: ready, Launched: launched, Machines: machines,
		Benches:  []string{"bench-a"},
		Capacity: laneCap{"bench-a": 1},
		Launcher: l,
		Seats:    seats,
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	}
	registrySeats := map[string]string{"bench-a": "swarm-bench-a"}
	fillTick(in, registrySeats, 1)
	fillTick(in, registrySeats, 2)
	if got := len(l.snapshot()); got != 1 {
		t.Fatalf("delayed lease visibility dispatched %d cards into one observed seat, want 1: %q",
			got, l.snapshot())
	}
}

// reviewLeaseCap answers free counts and per-bench lease labels, so a test can
// feed the production ownedLeaseLabels construction without a slot store.
type reviewLeaseCap struct {
	free   map[string]int
	labels map[string][]string
}

func (c reviewLeaseCap) Capacity(bench string) (int, error) { return c.free[bench], nil }

func (c reviewLeaseCap) OwnedLeaseLabels(bench string) []string { return c.labels[bench] }

// TestReviewCrossBenchLabelMustNotReleaseReservation is Stella's HOLD at f05833e7:
// ownedLeaseLabels must not flatten every bench into one card-name set. A
// card-001 lease observed only on bench-b must not free bench-a's UNKNOWN
// reservation for the same card basename.
func TestReviewCrossBenchLabelMustNotReleaseReservation(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".fill-seats")
	seats := fileSeats(root)
	hold, ok, err := seats.Reserve("bench-a", 1, "card-001.md")
	if err != nil || !ok || hold == nil {
		t.Fatalf("reserve bench-a: ok=%v err=%v", ok, err)
	}
	hold.KeepUnknown()
	path := filepath.Join(root, "bench-a", "0")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reservation file missing after reserve: %v", err)
	}

	cap := reviewLeaseCap{
		free:   map[string]int{"bench-a": 1, "bench-b": 1},
		labels: map[string][]string{"bench-b": {"card-001"}},
	}
	seats.ReconcileOwned(ownedLeaseLabels(cap, []string{"bench-a", "bench-b"}))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bench-b label released bench-a reservation: %v", err)
	}
}

// TestFillRestartKeepsUNKNOWNReservationFromDisk: a new fileSeats on the same
// directory must load persisted UNKNOWN and not free the seat.
func TestFillRestartKeepsUNKNOWNReservationFromDisk(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	if err := os.MkdirAll(launched, 0o755); err != nil {
		t.Fatal(err)
	}
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)
	root := filepath.Join(launched, ".fill-seats")
	l := &unknownSeatLauncher{}
	in := FillInput{
		Ready: ready, Launched: launched, Machines: machines,
		Benches:  []string{"bench-a"},
		Capacity: laneCap{"bench-a": 1},
		Launcher: l,
		Seats:    fileSeats(root),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	}
	registrySeats := map[string]string{"bench-a": "swarm-bench-a"}
	fillTick(in, registrySeats, 1)
	in.Seats = fileSeats(root)
	writeCard(t, ready, "card-002.md", "a card\n")
	fillTick(in, registrySeats, 2)
	if got := len(l.snapshot()); got != 1 {
		t.Fatalf("restart freed an UNKNOWN reservation: %d launches, want 1: %q", got, l.snapshot())
	}
	seats, err := filepath.Glob(filepath.Join(root, "bench-a", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(seats) != 1 {
		t.Fatalf("UNKNOWN reservation files after restart = %v, want 1", seats)
	}
	body, err := os.ReadFile(seats[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "outcome=unknown") {
		t.Fatalf("restart did not keep UNKNOWN: %q", body)
	}
}

func TestFillFailedLaunchReleasesItsReservation(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	writeCard(t, ready, "card-002.md", "a card\n")
	l := &failingLauncher{err: fmt.Errorf("exit status 7")}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 1},
		Launcher: l,
	})
	if code != 1 {
		t.Fatalf("fill exit = %d, want 1; stderr=%q", code, errb.String())
	}
	seats, err := filepath.Glob(filepath.Join(launched, ".fill-seats", "bench-a", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(seats) != 0 {
		t.Fatalf("a known-failed launch left seat files %v; a known failure frees the reservation", seats)
	}
}
