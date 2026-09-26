package deal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The width of one deal pass (#3706): sprint quack-0925b dealt 12 cards to
// six benches and the benches planned last read `WEDGED: no session opened`,
// because the lease the sessions are bounded by was spent before they
// opened. These tests hold the pass to one worker per bench, all at once
// (bounded by cfg:deal max_sessions), each renewing the lease before its
// session and writing its row when its own session ends. They assert events,
// never elapsed time; the pass time is logged.

const parallelSprint = "control-00003706"

var sixBenches = []string{"ctl-batman", "ctl-hetzner", "ctl-hulk", "ctl-space", "ctl-superman", "ctl-vision"}

// guard is the generous bound on an event that must come.
const guard = 30 * time.Second

// set writes one fixture control file into a bench's dir.
func (f *fixture) set(t *testing.T, bench, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// twoEach is an input of the given benches, two slots each, and two queued
// cards per bench in one open sprint.
func twoEach(benches []string) Input {
	in := Input{Now: time.Now()}
	for _, b := range benches {
		in.Benches = append(in.Benches, upBench(b, 2))
	}
	cards := make([]Card, 2*len(benches))
	for i := range cards {
		cards[i] = Card{Sprint: parallelSprint, Label: fmt.Sprintf("card-%02d", i), Priority: float64(i)}
	}
	in.Sprints = []Sprint{{Name: parallelSprint, Share: 1, Pool: cards}}
	return in
}

// barrierDialer holds every session it opens until size of them are open at
// once, then lets that wave run its inner session. If the pass opened its
// sessions one after another, no wave would fill and each session fails at
// the guard. max is the most sessions ever open at once.
type barrierDialer struct {
	inner   Dialer // nil: the session succeeds once released
	size    int
	mu      sync.Mutex
	open    int
	max     int
	total   int
	waiting int
	wave    chan struct{}
	events  *eventLog
}

func newBarrier(inner Dialer, size int) *barrierDialer {
	return &barrierDialer{inner: inner, size: size, wave: make(chan struct{})}
}

func (d *barrierDialer) Dial(b Bench) Session {
	s := &barrierSession{d: d}
	if d.inner != nil {
		s.inner = d.inner.Dial(b)
	}
	return s
}

type barrierSession struct {
	d     *barrierDialer
	inner Session
}

func (s *barrierSession) Run(ctx context.Context, stdin []byte) error {
	d := s.d
	d.mu.Lock()
	d.open++
	d.total++
	if d.open > d.max {
		d.max = d.open
	}
	d.waiting++
	wave := d.wave
	if d.waiting == d.size {
		close(d.wave)
		d.wave, d.waiting = make(chan struct{}), 0
	}
	d.mu.Unlock()
	d.events.add("session")
	defer func() {
		d.mu.Lock()
		d.open--
		d.mu.Unlock()
	}()
	select {
	case <-wave:
	case <-time.After(guard):
		return fmt.Errorf("barrier: %d sessions never open at once", d.size)
	}
	if s.inner == nil {
		return nil
	}
	return s.inner.Run(ctx, stdin)
}

func (d *barrierDialer) stats() (maxOpen, total int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.max, d.total
}

// eventLog is the order things happened in; a nil log records nothing.
type eventLog struct {
	mu sync.Mutex
	ev []string
}

func (l *eventLog) add(e string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ev = append(l.ev, e)
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ev...)
}

// TestPassSixBenchesOneWindow is the DONE-WHEN of #3706: six benches whose
// sessions each take 1.5 s on the fake ssh are all dealt in ONE pass, and all
// six sessions are open at the same moment (the barrier fills), so the pass
// takes the slowest session, not the sum (9 s). The pass time is logged.
func TestPassSixBenchesOneWindow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for _, b := range sixBenches {
		f.set(t, b, "sleep", "1.5")
	}
	in := twoEach(sixBenches)
	lease := "lease-" + randHex()
	st := newFakeStore(lease, in)
	d := newBarrier(f.remote(), len(sixBenches))
	p := &Pass{Source: staticSource{in}, Fence: fence(lease), Reserver: st, Row: st, Dialer: d}

	start := time.Now()
	res, err := p.Run(context.Background())
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	var sum time.Duration
	for _, br := range res.Benches {
		sum += br.Took
	}
	t.Logf("six benches, 1.5 s sessions: one pass took %s (bench workers summed %s)", took.Round(time.Millisecond), sum.Round(time.Millisecond))
	if maxOpen, total := d.stats(); maxOpen != len(sixBenches) || total != len(sixBenches) {
		t.Fatalf("sessions open at once %d of %d, want all %d: the pass did not open them together", maxOpen, total, len(sixBenches))
	}
	if res.Rounds != 1 || res.Launched() != 12 {
		t.Fatalf("rounds %d launched %d, want 1 round and 12 cards", res.Rounds, res.Launched())
	}
	for _, b := range sixBenches {
		if got := st.cell(b); got != "ssh: ok" {
			t.Errorf("%s row %q, want ssh: ok", b, got)
		}
		if got := st.dealtOn(b); got != 2 {
			t.Errorf("%s dealt %d, want 2", b, got)
		}
		if got := len(f.lines(b, "sessions.log")); got != 1 {
			t.Errorf("%s sessions %d, want exactly 1", b, got)
		}
		if got := len(f.lines(b, "launched")); got != 2 {
			t.Errorf("%s launch lines %d, want 2", b, got)
		}
	}
}

// wedgeDialer sends one bench's session to a wedged sshd that answers only
// when the test releases it (then as the lease bound reports a wedged
// session: timeout, nothing ran); every other bench goes to inner.
type wedgeDialer struct {
	inner   Dialer
	wedged  string
	release chan struct{}
}

func (d wedgeDialer) Dial(b Bench) Session {
	if b.Name == d.wedged {
		return wedgedSession{bench: b.Name, release: d.release}
	}
	return d.inner.Dial(b)
}

type wedgedSession struct {
	bench   string
	release chan struct{}
}

func (s wedgedSession) Run(ctx context.Context, _ []byte) error {
	select {
	case <-s.release:
	case <-ctx.Done():
	case <-time.After(guard):
	}
	return &SessionError{Bench: s.bench, State: SSHTimeout, Exit: 255, Stderr: "WEDGED: sshd sent no banner inside the lease budget"}
}

// signalRow is the fake store's Row that announces every row write.
type signalRow struct {
	*fakeStore
	wrote chan string
}

func (r signalRow) SSH(ctx context.Context, fence, bench, state, why string) error {
	err := r.fakeStore.SSH(ctx, fence, bench, state, why)
	r.wrote <- bench
	return err
}

// TestPassMaxSessionsBoundsWorkers: cfg:deal max_sessions (Input), else the
// pass's MaxSessions, else DefaultMaxSessions bounds the sessions open at
// once; every bench is still dealt. Each wave of want sessions must be open
// together before any of them ends, so a bound below want never fills a wave.
func TestPassMaxSessionsBoundsWorkers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cfg, field int
		want       int
	}{
		{"cfg:deal max_sessions", 2, 5, 2},
		{"pass MaxSessions", 0, 3, 3},
		{"default", 0, 0, len(sixBenches)}, // six benches, under the default 8
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := twoEach(sixBenches)
			in.MaxSessions = tc.cfg
			lease := "lease-" + randHex()
			st := newFakeStore(lease, in)
			d := newBarrier(nil, tc.want)
			p := &Pass{Source: staticSource{in}, Fence: fence(lease), Reserver: st, Row: st, Dialer: d, MaxSessions: tc.field}
			res, err := p.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if maxOpen, total := d.stats(); maxOpen != tc.want || total != len(sixBenches) {
				t.Fatalf("most sessions open at once %d of %d, want %d", maxOpen, total, tc.want)
			}
			if res.Launched() != 12 {
				t.Fatalf("launched %d, want 12", res.Launched())
			}
		})
	}
	if DefaultMaxSessions != 8 {
		t.Fatalf("DefaultMaxSessions %d, want 8", DefaultMaxSessions)
	}
}

// renewingFence is a Fence and Renewer over memory.
type renewingFence struct {
	token  string
	fenced bool
	events *eventLog
}

func (f *renewingFence) Token(context.Context) (string, error) { return f.token, nil }

func (f *renewingFence) Renew(context.Context) error {
	if f.fenced {
		return fmt.Errorf("lease held by another instance: %w", ErrFenced)
	}
	f.events.add("renew")
	return nil
}

// TestPassRenewsLeaseBeforeSessions: a Renewer fence is renewed before each
// session opens (the coalescing of those renewals is the reconciler lease's,
// #3737, tested there), and a fenced renewal opens no session and fences the
// pass.
func TestPassRenewsLeaseBeforeSessions(t *testing.T) {
	t.Parallel()

	t.Run("renewed before the sessions", func(t *testing.T) {
		in := twoEach(sixBenches)
		lease := "lease-" + randHex()
		st := newFakeStore(lease, in)
		ev := &eventLog{}
		fe := &renewingFence{token: lease, events: ev}
		d := newBarrier(nil, len(sixBenches))
		d.events = ev
		p := &Pass{Source: staticSource{in}, Fence: fe, Reserver: st, Row: st, Dialer: d}
		if _, err := p.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := ev.all()
		renewals := 0
		for _, e := range got {
			if e == "renew" {
				renewals++
			}
		}
		if len(got) == 0 || got[0] != "renew" || renewals != len(sixBenches) || len(got) != 2*len(sixBenches) {
			t.Fatalf("events %v, want a renewal before each of the six sessions", got)
		}
	})
	t.Run("fenced renewal opens nothing", func(t *testing.T) {
		in := twoEach(sixBenches)
		lease := "lease-" + randHex()
		st := newFakeStore(lease, in)
		fe := &renewingFence{token: lease, fenced: true}
		d := newBarrier(nil, 1)
		p := &Pass{Source: staticSource{in}, Fence: fe, Reserver: st, Row: st, Dialer: d}
		_, err := p.Run(context.Background())
		if !errors.Is(err, ErrFenced) {
			t.Fatalf("pass err %v, want ErrFenced", err)
		}
		if _, total := d.stats(); total != 0 {
			t.Fatalf("%d sessions opened under a fenced lease, want 0", total)
		}
	})
}
