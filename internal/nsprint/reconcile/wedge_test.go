package reconcile_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/redis/go-redis/v9"
)

// fakeClock is the lease clock of #3322: it moves only when the test
// advances it, and fires every timer that falls due. onMove, when set, sees
// the clock after every Advance and every armed timer, outside the lock; the
// wedge test uses it to run the throwaway Redis's lease TTL on this clock.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	asked  []time.Duration
	armed  chan struct{} // one send per timer, never blocking
	onMove func(now time.Time)
}

func (c *fakeClock) setOnMove(f func(time.Time)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMove = f
}

func (c *fakeClock) moved() {
	c.mu.Lock()
	f, now := c.onMove, c.now
	c.mu.Unlock()
	if f != nil {
		f(now)
	}
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t, armed: make(chan struct{}, 64)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	defer c.moved()
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.asked = append(c.asked, d)
	if d <= 0 {
		ch <- c.now
	} else {
		c.timers = append(c.timers, fakeTimer{at: c.now.Add(d), ch: ch})
	}
	select {
	case c.armed <- struct{}{}:
	default:
	}
	return ch
}

// Advance moves the clock by d. onMove sees the new time before any timer
// falls due, so whatever the clock drives (the lease TTL) has moved before a
// bounded session is cut and the pass goes on to write.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now, f := c.now, c.onMove
	var due []chan time.Time
	keep := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(now) {
			due = append(due, tm.ch)
			continue
		}
		keep = append(keep, tm)
	}
	c.timers = keep
	c.mu.Unlock()
	if f != nil {
		f(now)
	}
	for _, ch := range due {
		ch <- now
	}
}

func (c *fakeClock) bounds() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.asked...)
}

// wedgedSSHD is the smoke's wedge fixture: a local listener that accepts TCP
// and never writes a byte, so no ssh banner ever arrives (CI-NET: loopback).
func wedgedSSHD(t *testing.T) (addr string, accepted <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	acc := make(chan struct{}, 16)
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			acc <- struct{}{}
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String(), acc
}

// bannerDialer opens a real TCP session to a bench named in wedged and waits
// for the ssh banner, as the ssh client does; any other bench takes its batch
// at once, like fakeDialer. connected, when set, gets one event per session
// once the client side holds the connection.
type bannerDialer struct {
	fakeDialer
	wedged    map[string]string // bench -> fixture sshd address
	connected chan<- struct{}
}

func (d *bannerDialer) Dial(b deal.Bench) deal.Session {
	if addr, ok := d.wedged[b.Name]; ok {
		return bannerSession{bench: b.Name, addr: addr, connected: d.connected}
	}
	return d.fakeDialer.Dial(b)
}

type bannerSession struct {
	bench, addr string
	connected   chan<- struct{}
}

// Run connects and reads the banner until its context ends. A banner that
// never came is the client's pre-exec timeout ("Connection timed out during
// banner exchange", exit 255): nothing reached the remote command.
func (s bannerSession) Run(ctx context.Context, _ []byte) error {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return &deal.SessionError{Bench: s.bench, State: deal.SSHRefused, Exit: 255, Stderr: "ssh: connect to host: Connection refused (" + err.Error() + ")"}
	}
	defer conn.Close()
	if s.connected != nil {
		s.connected <- struct{}{}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err == nil {
		return errors.New("fixture sshd sent a banner; it must never write")
	}
	if ctx.Err() != nil {
		return &deal.SessionError{Bench: s.bench, State: deal.SSHTimeout, Exit: 255, Stderr: "Connection timed out during banner exchange"}
	}
	return &deal.SessionError{Bench: s.bench, State: deal.SSHError, Exit: 255, Stderr: "banner read failed"}
}

// eventWait is the generous bound for an event, NOVA_TEST_WAIT or thirty
// seconds; the assertions are on events, never on elapsed time.
func eventWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// leaseOnClock runs the throwaway redis-server's lease:reconciler TTL on the
// lease clock (#3322 review): miniredis has no FUNCTION/FCALL, so the lease
// and deal functions need the real server, whose TTL runs on the wall clock.
// Each time the lease clock moves, the key's wall TTL is removed (PERSIST) so
// the wall clock can never lapse it, and once the lease clock reaches the
// deadline the lease keeps (the last renewal's send time plus the TTL, the
// earliest Redis can expire it) the key is deleted, as Redis would expire it.
// A fenced write the pass presents after that is refused FENCED for real.
func leaseOnClock(t *testing.T, c *redis.Client, clk *fakeClock, l *reconcile.Lease) (lapsed func() bool) {
	t.Helper()
	var mu sync.Mutex
	var gone bool
	clk.setOnMove(func(now time.Time) {
		ctx := context.Background()
		mu.Lock()
		defer mu.Unlock()
		if !now.Before(l.Deadline()) {
			if err := c.Del(ctx, reconcile.LeaseKey).Err(); err != nil {
				t.Errorf("expire %s on the lease clock: %v", reconcile.LeaseKey, err)
			}
			gone = true
			return
		}
		if err := c.Persist(ctx, reconcile.LeaseKey).Err(); err != nil {
			t.Errorf("persist %s: %v", reconcile.LeaseKey, err)
		}
	})
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gone
	}
}

// TestDealPassWedgedSshdDoesNotFenceOrStrand (#3322, smoke outcome 3): a
// bench whose sshd accepts TCP and never sends a banner is bounded by the
// lease. The pass cuts its session at the lease deadline less the write
// margin (on the injected lease clock, which also runs the Redis lease TTL),
// writes its ssh row as timeout with a WEDGED why and an at and returns its
// reservations to the pool while the lease still has time left, is not
// fenced, and the next pass runs and deals. The healthy bench beside it is
// dealt and its row written in the same pass.
func TestDealPassWedgedSshdDoesNotFenceOrStrand(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	const ok, wedge, S = "ctl-a-ok", "ctl-wedge", "control-5ca8ae22"
	seedBench(t, c, ok, 4)
	seedBench(t, c, wedge, 4)
	seedSprint(t, c, S, 1, 6) // ctl-a-ok plans card-00..03, ctl-wedge card-04..05

	addr, accepted := wedgedSSHD(t)
	clk := newFakeClock(time.Unix(1_800_000_000, 0))
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	lapsed := leaseOnClock(t, c, clk, l)
	// The client side's own event gates the clock (dev push run 35950929169,
	// ubuntu-latest shard 1): the listener's accept fires as soon as the kernel
	// completes the handshake, which can be before DialContext has returned to
	// the session; a clock advanced then cancels the dial itself, Go reports a
	// cancelled connect as a dial error, and the row read "refused" instead of
	// the banner timeout this test is about (20/100 on one loaded Linux core).
	connected := make(chan struct{}, 16)
	dialer := &bannerDialer{wedged: map[string]string{wedge: addr}, connected: connected}
	// When the deal pass has written every row and undealt the wedged batch,
	// the lease must still have time left on its clock, and Redis must still
	// hold it under this instance's token.
	type atWrites struct {
		left  time.Duration
		token string
		err   error
	}
	wrote := make(chan atWrites, 4)
	rf := &reconcile.Refill{Client: c, Deal: &deal.Pass{Dialer: dialer}, Now: clk.Now,
		AfterDeal: func(reconcile.Wake, deal.Result) {
			tok, err := c.HGet(ctx, reconcile.LeaseKey, "token").Result()
			wrote <- atWrites{left: l.Remaining(), token: tok, err: err}
		}}
	lp := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{rf.Run}}

	type passOut struct {
		res reconcile.PassResult
		err error
	}
	done := make(chan passOut, 1)
	go func() {
		res, err := lp.Pass(ctx)
		done <- passOut{res, err}
	}()

	// The wedge fixture accepted the session, the client holds it, and the
	// pass armed its bound.
	select {
	case <-accepted:
	case out := <-done:
		t.Fatalf("pass ended before the wedged sshd saw a session: %+v", out)
	case <-time.After(eventWait()):
		t.Fatal("the wedged sshd never saw a session")
	}
	select {
	case <-connected:
	case out := <-done:
		t.Fatalf("pass ended before the session's client held its connection: %+v", out)
	case <-time.After(eventWait()):
		t.Fatal("the session's client never held its connection to the wedged sshd")
	}
	// Both benches' sessions (ctl-a-ok and ctl-wedge) armed their bound before
	// the clock moves: the pass opens them concurrently, and a session that
	// first reads the lease after the advance finds no budget left and reports
	// itself wedged (the ok bench's row read timeout, 4/200 on one loaded Linux
	// core, once the connect race above was closed).
	for armed := 0; armed < 2; armed++ {
		select {
		case <-clk.armed:
		case <-time.After(eventWait()):
			t.Fatalf("%d of 2 bench sessions were bounded by the lease: a wedged sshd holds the pass past the lease TTL", armed)
		}
	}
	// The pass renewed at its start and the lease clock has not moved, so
	// every session's bound is exactly the lease deadline less the margin.
	bound := reconcile.DefaultTTL - reconcile.DefaultWriteMargin
	for _, b := range clk.bounds() {
		if b != bound {
			t.Fatalf("session bound %s, want the lease TTL %s less the write margin %s = %s", b, reconcile.DefaultTTL, reconcile.DefaultWriteMargin, bound)
		}
	}
	// The lease clock reaches the bound, and no further: the wedged session
	// is due to be cut, and the lease (in Redis too) has the margin left.
	clk.Advance(bound)

	var out passOut
	select {
	case out = <-done:
	case <-time.After(eventWait()):
		t.Fatal("the pass did not end once the lease bound was due: the wedged session was not cut")
	}
	if out.err != nil {
		t.Fatalf("pass: %v (a wedged bench must not fence the reconciler)", out.err)
	}
	if out.res.Err != "" {
		t.Fatalf("pass recorded an error: %s", out.res.Err)
	}
	if lapsed() {
		t.Fatal("the lease lapsed on the lease clock during the pass")
	}
	select {
	case w := <-wrote:
		if w.err != nil || w.token != l.Token() {
			t.Fatalf("after the deal pass's writes %s token = %q (%v), want this instance's", reconcile.LeaseKey, w.token, w.err)
		}
		if w.left <= 0 || w.left > reconcile.DefaultWriteMargin {
			t.Fatalf("lease left when the deal pass's writes were done = %s, want inside (0, %s]: the writes ran in the margin, before expiry", w.left, reconcile.DefaultWriteMargin)
		}
	default:
		t.Fatal("the deal pass ran no writes (AfterDeal never called)")
	}
	// The pass is timed on the lease clock: it recorded itself at the bound,
	// under the TTL, on the clock the lease deadline is kept on.
	took, err := c.HGet(ctx, reconcile.ProcKey, "took_ms").Int64()
	if err != nil || took != bound.Milliseconds() {
		t.Fatalf("%s took_ms = %d (%v), want %d: the pass timed on the lease clock", reconcile.ProcKey, took, err, bound.Milliseconds())
	}

	// The wedged bench's row: timeout, WEDGED, with at.
	row, err := c.HGetAll(ctx, deal.RowKey(wedge)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if row["state"] != deal.SSHTimeout || !strings.Contains(row["why"], reconcile.WedgedPrefix) || row["at"] == "" {
		t.Fatalf("%s row = %v, want state timeout, why with %s, and at", deal.RowKey(wedge), row, reconcile.WedgedPrefix)
	}
	okRow, err := c.HGetAll(ctx, deal.RowKey(ok)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if okRow["state"] != deal.SSHOK || okRow["at"] == "" {
		t.Fatalf("%s row = %v, want ok with at", deal.RowKey(ok), okRow)
	}

	// Nothing is stranded in starting on the wedged bench; its two cards are
	// back in the pool, queued.
	if m := starting(t, c, wedge); len(m) != 0 {
		t.Fatalf("%s starting = %v, want empty: reservations stranded on a bench that ran nothing", wedge, m)
	}
	if got := openCards(t, c, S); got != 2 {
		t.Fatalf("open %d, want the wedged bench's 2 cards back in the pool", got)
	}
	for _, label := range []string{"card-04", "card-05"} {
		state, err := c.HGet(ctx, "s:"+S+":card:"+label, "state").Result()
		if err != nil || state != "queued" {
			t.Fatalf("%s state %q (%v), want queued", label, state, err)
		}
	}
	if got := leased(t, c, ok); got != 4 {
		t.Fatalf("%s working %d, want 4", ok, got)
	}

	// The lease is held, and the next pass runs: one child ends on the
	// healthy bench, and its slot is refilled from the returned cards while
	// the wedged bench sits out its hold.
	if err := l.Renew(ctx); err != nil {
		t.Fatalf("lease after the wedged pass: %v", err)
	}
	childDone(t, c, ok, starting(t, c, ok)[0])
	p := mustPass(t, lp)
	if p.Counts.Dealt != 1 || leased(t, c, ok) != 4 || openCards(t, c, S) != 1 {
		t.Fatalf("next pass dealt %d, %s working %d, open %d; want 1, 4, 1",
			p.Counts.Dealt, ok, leased(t, c, ok), openCards(t, c, S))
	}
	if m := starting(t, c, wedge); len(m) != 0 {
		t.Fatalf("%s starting = %v after the next pass, want empty", wedge, m)
	}

	// The fixture is not vacuous: the lease clock reaching the deadline with
	// no renewal lapses the Redis lease, and the holder is fenced.
	clk.Advance(l.Remaining())
	if !lapsed() {
		t.Fatal("the lease clock reached the deadline and the Redis lease did not lapse")
	}
	if err := l.Renew(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("renew after the lease lapsed on its clock: %v, want ErrFenced", err)
	}
}
