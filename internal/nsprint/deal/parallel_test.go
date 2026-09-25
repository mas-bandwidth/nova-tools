package deal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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

// TestPassWedgedBenchDoesNotDelayOthers: one bench's sshd accepts and never
// answers. The five healthy benches beside it (1.5 s sessions on the fake
// ssh) are dealt and their rows are written while the wedged session is
// still open: the test releases the wedged bench only after the five rows
// arrive, so a pass that waited for its slowest bench before writing any row
// never gets there. The wedged bench's row then reads timeout and its batch
// is back in the pool.
func TestPassWedgedBenchDoesNotDelayOthers(t *testing.T) {
	f := newFixture(t)
	const wedged = "ctl-hulk"
	for _, b := range sixBenches {
		f.set(t, b, "sleep", "1.5")
	}
	in := twoEach(sixBenches)
	lease := "lease-" + randHex()
	st := newFakeStore(lease, in)
	row := signalRow{st, make(chan string, len(sixBenches))}
	release := make(chan struct{})
	p := &Pass{Source: staticSource{in}, Fence: fence(lease), Reserver: st, Row: row,
		Dialer: wedgeDialer{inner: f.remote(), wedged: wedged, release: release}}

	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	start := time.Now()
	go func() {
		res, err := p.Run(context.Background())
		done <- out{res, err}
	}()
	for healthy := 0; healthy < len(sixBenches)-1; {
		select {
		case b := <-row.wrote:
			if b == wedged {
				t.Fatalf("%s row written before its session was released", wedged)
			}
			healthy++
		case o := <-done:
			t.Fatalf("pass ended before the healthy rows: %+v %v", o.res, o.err)
		case <-time.After(guard):
			close(release)
			t.Fatalf("%d of %d healthy rows written while %s was wedged: the wedged bench delayed the others", healthy, len(sixBenches)-1, wedged)
		}
	}
	t.Logf("five healthy rows written %s after the start, with %s still wedged", time.Since(start).Round(time.Millisecond), wedged)
	for _, b := range sixBenches {
		if b == wedged {
			continue
		}
		if got := st.cell(b); got != "ssh: ok" {
			t.Errorf("%s row %q, want ssh: ok", b, got)
		}
		if got := st.dealtOn(b); got != 2 {
			t.Errorf("%s dealt %d, want 2", b, got)
		}
	}
	close(release)
	var o out
	select {
	case o = <-done:
	case <-time.After(guard):
		t.Fatal("the pass did not end once the wedged session was released")
	}
	if o.err != nil {
		t.Fatal(o.err)
	}
	if got := st.cell(wedged); got != "ssh: "+SSHTimeout {
		t.Fatalf("%s row %q, want ssh: timeout", wedged, got)
	}
	if got := st.dealtOn(wedged); got != 0 {
		t.Fatalf("%s holds %d reservations, want its batch back in the pool", wedged, got)
	}
	if got := len(f.lines(wedged, "launched")); got != 0 {
		t.Fatalf("%s launch lines %d, want 0", wedged, got)
	}
}

// TestPassMaxSessionsBoundsWorkers: cfg:deal max_sessions (Input), else the
// pass's MaxSessions, else DefaultMaxSessions bounds the sessions open at
// once; every bench is still dealt. Each wave of want sessions must be open
// together before any of them ends, so a bound below want never fills a wave.
func TestPassMaxSessionsBoundsWorkers(t *testing.T) {
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

// TestRedisSourceMaxSessions reads cfg:deal max_sessions in the first round;
// unset, not a positive number, or denied by the ACL, it reads 0 and the read
// still succeeds.
func TestRedisSourceMaxSessions(t *testing.T) {
	c := throwawayRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "ctl-a")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "2")

	read := func(c *redis.Client) (int, error) {
		in, err := RedisSource{Client: c}.Read(ctx)
		return in.MaxSessions, err
	}
	if n, err := read(c); err != nil || n != 0 {
		t.Fatalf("unset: %d %v, want 0", n, err)
	}
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "3")
	if n, err := read(c); err != nil || n != 3 {
		t.Fatalf("set 3: %d %v", n, err)
	}
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "lots")
	if n, err := read(c); err != nil || n != 0 {
		t.Fatalf("garbage: %d %v, want 0", n, err)
	}
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "3")
	// A user whose ACL does not cover cfg:*: the read succeeds, default.
	if err := c.Do(ctx, "ACL", "SETUSER", "nocfg", "on", ">pw-3706", "~bench*", "~sprint*", "~s:*", "+@all").Err(); err != nil {
		t.Fatal(err)
	}
	limited := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: "nocfg", Password: "pw-3706"})
	t.Cleanup(func() { _ = limited.Close() })
	if err := limited.HGet(ctx, MaxSessionsKey, "max_sessions").Err(); err == nil || errors.Is(err, redis.Nil) {
		t.Fatalf("the ACL fixture reads cfg:deal (%v): vacuous", err)
	}
	in, err := RedisSource{Client: limited}.Read(ctx)
	if err != nil || in.MaxSessions != 0 || len(in.Benches) != 1 {
		t.Fatalf("ACL without cfg:*: max %d benches %d err %v, want 0, 1, nil", in.MaxSessions, len(in.Benches), err)
	}
}

// TestRedisSourceRoundErrorsStillFail: forgiving cfg:deal's own error does
// not hide any other command's. A user that can read cfg:deal and the
// registries but not the pools fails the read with the pool round's NOPERM,
// one that cannot read the registries fails the first round, and a lost
// connection fails the read; none returns an empty Input as if there were
// nothing to deal.
func TestRedisSourceRoundErrorsStillFail(t *testing.T) {
	c := throwawayRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "ctl-a")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "2")
	c.SAdd(ctx, "sprints", parallelSprint)
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: parallelSprint})
	c.HSet(ctx, "s:"+parallelSprint, "status", "open")
	c.ZAdd(ctx, "s:"+parallelSprint+":pool", redis.Z{Score: 1, Member: "card-00"})
	c.HSet(ctx, "s:"+parallelSprint+":card:card-00", "state", "queued")
	c.HSet(ctx, MaxSessionsKey, "max_sessions", "3")

	user := func(name string, keys ...string) *redis.Client {
		t.Helper()
		args := []any{"ACL", "SETUSER", name, "on", ">pw-3706"}
		for _, k := range keys {
			args = append(args, "~"+k)
		}
		args = append(args, "+@all")
		if err := c.Do(ctx, args...).Err(); err != nil {
			t.Fatal(err)
		}
		u := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: name, Password: "pw-3706"})
		t.Cleanup(func() { _ = u.Close() })
		return u
	}
	// Every key but the pools: the pool round is refused.
	nopool := user("nopool", "cfg:*", "bench*", "sprint*", "s:"+parallelSprint, "s:"+parallelSprint+":policy",
		"s:"+parallelSprint+":backpressure", "s:"+parallelSprint+":pitstop", "s:"+parallelSprint+":card:*", "s:"+parallelSprint+":waiting")
	if err := nopool.ZCard(ctx, "s:"+parallelSprint+":pool").Err(); err == nil {
		t.Fatal("the ACL fixture reads the pool: vacuous")
	}
	if in, err := (RedisSource{Client: nopool}).Read(ctx); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("pool round refused: err %v input %+v, want the NOPERM", err, in)
	}
	// cfg:deal readable, the registries not: the first round fails even
	// though max_sessions read fine.
	noreg := user("noreg", "cfg:*", "s:*")
	if in, err := (RedisSource{Client: noreg}).Read(ctx); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("registry round refused: err %v input %+v, want the NOPERM", err, in)
	}
	// A lost connection.
	gone := redis.NewClient(&redis.Options{Addr: c.Options().Addr})
	_ = gone.Close()
	if _, err := (RedisSource{Client: gone}).Read(ctx); err == nil {
		t.Fatal("a closed client read an Input, want the error")
	}
	// The full user still reads it all, max_sessions included.
	in, err := RedisSource{Client: c}.Read(ctx)
	if err != nil || in.MaxSessions != 3 || len(in.Sprints) != 1 || len(in.Sprints[0].Pool) != 1 {
		t.Fatalf("full read: %+v %v", in, err)
	}
}
