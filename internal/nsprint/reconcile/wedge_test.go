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
)

// fakeClock is the lease clock of #3322: it moves only when the test
// advances it, and fires every timer that falls due.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	asked  []time.Duration
	armed  chan struct{} // one send per timer, never blocking
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

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(c.now) {
			tm.ch <- c.now
			continue
		}
		keep = append(keep, tm)
	}
	c.timers = keep
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
// at once, like fakeDialer.
type bannerDialer struct {
	fakeDialer
	wedged map[string]string // bench -> fixture sshd address
}

func (d *bannerDialer) Dial(b deal.Bench) deal.Session {
	if addr, ok := d.wedged[b.Name]; ok {
		return bannerSession{bench: b.Name, addr: addr}
	}
	return d.fakeDialer.Dial(b)
}

type bannerSession struct{ bench, addr string }

// Run connects and reads the banner until its context ends. A banner that
// never came is the client's pre-exec timeout ("Connection timed out during
// banner exchange", exit 255): nothing reached the remote command.
func (s bannerSession) Run(ctx context.Context, _ []byte) error {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return &deal.SessionError{Bench: s.bench, State: deal.SSHRefused, Exit: 255, Stderr: "ssh: connect to host: Connection refused"}
	}
	defer conn.Close()
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

// TestDealPassWedgedSshdDoesNotFenceOrStrand (#3322, smoke outcome 3): a
// bench whose sshd accepts TCP and never sends a banner is bounded by the
// lease. The pass cuts its session inside the lease deadline (on the injected
// lease clock), writes its ssh row as timeout with a WEDGED why and an at,
// returns its reservations to the pool, is not fenced, and the next pass
// runs and deals. The healthy bench beside it is dealt and its row written in
// the same pass.
func TestDealPassWedgedSshdDoesNotFenceOrStrand(t *testing.T) {
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
	dialer := &bannerDialer{wedged: map[string]string{wedge: addr}}
	rf := &reconcile.Refill{Client: c, Deal: &deal.Pass{Dialer: dialer}, Now: clk.Now}
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

	// The wedge fixture accepted the session and the pass armed its bound.
	select {
	case <-accepted:
	case out := <-done:
		t.Fatalf("pass ended before the wedged sshd saw a session: %+v", out)
	case <-time.After(eventWait()):
		t.Fatal("the wedged sshd never saw a session")
	}
	select {
	case <-clk.armed:
	case <-time.After(eventWait()):
		t.Fatal("no bench session was bounded by the lease: a wedged sshd holds the pass past the lease TTL")
	}
	for _, b := range clk.bounds() {
		if b <= 0 || b > reconcile.DefaultTTL-reconcile.DefaultWriteMargin {
			t.Fatalf("session bound %s, want inside the lease TTL %s less the write margin %s", b, reconcile.DefaultTTL, reconcile.DefaultWriteMargin)
		}
	}
	// The lease clock reaches the lease TTL: every bound is due.
	clk.Advance(reconcile.DefaultTTL)

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
}
