//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package deal

import (
	"context"
	"strings"
	"testing"
	"time"
)

// SLOW: 1.5 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// TestPassWedgedBenchDoesNotDelayOthers: one bench's sshd accepts and never
// answers. The five healthy benches beside it (1.5 s sessions on the fake
// ssh) are dealt and their rows are written while the wedged session is
// still open: the test releases the wedged bench only after the five rows
// arrive, so a pass that waited for its slowest bench before writing any row
// never gets there. The wedged bench's row then reads timeout and its batch
// is back in the pool.
func TestPassWedgedBenchDoesNotDelayOthers(t *testing.T) {
	t.Parallel()

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

// SLOW: 7.0 s on hetzner at dev 64b9bec48, over the five-second line.
// TestWedgedBenchRowWithinTenSeconds is nova-tools #3322 on the pass alone:
// one bench's ssh client hangs for longer than the lease TTL (a session stuck
// after the banner, which no ConnectTimeout bounds), another's sshd refuses,
// a third is healthy. The hard deadline kills the hung ssh and its whole
// process group, the pass returns inside the deadline, both failed rows are
// written (refused, and timeout with a why that says so) well inside 10 s,
// each failure's row and return are ONE call, the refused and timed-out
// cards are redealt to the healthy bench in the same pass, and nothing is
// left dealt on a bench whose session failed before exec.
func TestWedgedBenchRowWithinTenSeconds(t *testing.T) {
	t.Parallel()

	const sprint = "control-00003322"
	const deadline = 1500 * time.Millisecond
	f := newFixture(t)
	f.hang(t, "ctl-hang")
	f.hangNoisy(t, "ctl-noisy")
	f.wedge(t, "ctl-closed")
	in := Input{Now: time.Now(),
		Benches: []Bench{upBench("ctl-closed", 20), upBench("ctl-hang", 20), upBench("ctl-noisy", 10), upBench("ctl-ok", 64)},
		Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
	st := newFakeStore("lease-1", in)
	r := f.remote()
	r.RunTimeout = deadline
	p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: r}
	// The event is the pass returning: without the deadline the hung ssh
	// holds it forever. The bound is the generous event wait, never the
	// deadline itself; the why line and the dead grandchild prove the kill.
	start := time.Now()
	res, err := runPass(t, p)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("pass: %v (a hung ssh must not fail the pass)", err)
	}
	if c := st.cell("ctl-hang"); c != "ssh: timeout" {
		t.Fatalf("hung bench row %q (why %q), want ssh: timeout", c, st.row["ctl-hang"].why)
	}
	if why := st.row["ctl-hang"].why; !strings.Contains(why, "killed at the deadline") || !strings.Contains(why, "no start line") {
		t.Fatalf("hung bench why %q, want the deadline kill and the missing start line named", why)
	}
	if c := st.cell("ctl-noisy"); c != "ssh: timeout" {
		t.Fatalf("noisy hung bench row %q (why %q), want ssh: timeout: profile noise on stdout is not the launch verb's ack", c, st.row["ctl-noisy"].why)
	}
	if why := st.row["ctl-noisy"].why; !strings.Contains(why, "no start line") {
		t.Fatalf("noisy hung bench why %q, want the missing start line named", why)
	}
	if c := st.cell("ctl-closed"); c != "ssh: refused" {
		t.Fatalf("refused bench row %q, want ssh: refused", c)
	}
	for _, b := range []string{"ctl-hang", "ctl-noisy", "ctl-closed"} {
		if at := st.row[b].at.Sub(start); at > 10*time.Second {
			t.Fatalf("%s row written after %s, want within 10 s", b, at)
		}
		if st.fails[b] != 1 {
			t.Fatalf("Fail calls on %s = %d, want 1: the row and the return are one call", b, st.fails[b])
		}
		if n := st.dealtOn(b); n != 0 {
			t.Fatalf("%d reservations left dealt on %s, whose session failed before exec", n, b)
		}
	}
	if st.row["ctl-hang"].timeouts != 1 || st.row["ctl-closed"].timeouts != 0 {
		t.Fatalf("timeouts hang=%d closed=%d, want 1 and 0 (a refusal is not a timeout)", st.row["ctl-hang"].timeouts, st.row["ctl-closed"].timeouts)
	}
	if n := st.dealtOn("ctl-ok"); n != 50 {
		t.Fatalf("%d cards on ctl-ok, want all 50 redealt in the same pass", n)
	}
	if res.Launched() != 50 || res.Rounds != 2 {
		t.Fatalf("launched %d in %d rounds, want 50 in 2", res.Launched(), res.Rounds)
	}
	for _, br := range res.Benches {
		if br.Bench == "ctl-hang" && br.SSH == SSHTimeout && br.Returned == 0 {
			t.Fatalf("the hung bench's result returned 0 reservations: %+v", br)
		}
	}
	// The whole process group died with the deadline: the grandchild the
	// hung client started is gone.
	pid := f.grandchild("ctl-hang")
	if pid <= 0 {
		t.Fatal("the hung ssh client wrote no grandchild pid")
	}
	if !processGone(pid) {
		t.Fatalf("grandchild %d of the hung ssh client is still alive after the deadline: the process group was not killed", pid)
	}
	t.Logf("pass took %s with a %s deadline", took.Round(time.Millisecond), deadline)

	// Three timeouts in a row report the hold (the store's fail-after, the
	// function's default) that the fleet duty acts on. The
	// source reads the bench fresh each pass, as the Redis one does, so the
	// pass's refused-hold does not skip it.
	fresh := func() Input {
		return Input{Now: time.Now(), Benches: []Bench{upBench("ctl-hang", 20)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)[:3]}}}
	}
	st2 := newFakeStore("lease-1", fresh())
	p2 := &Pass{Source: freshSource(fresh), Fence: fence("lease-1"), Reserver: st2, Row: st2, Dialer: r}
	for i := 1; i <= 3; i++ {
		res, err := runPass(t, p2)
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if len(res.Benches) != 1 {
			t.Fatalf("pass %d: benches = %+v, want one (rounds %d, waiting %+v)", i, res.Benches, res.Rounds, res.Waiting)
		}
		br := res.Benches[0]
		if br.SSH != SSHTimeout || br.Timeouts != i || br.Held != (i == 3) {
			t.Fatalf("pass %d: ssh=%s timeouts=%d held=%t, want timeout, %d, %t", i, br.SSH, br.Timeouts, br.Held, i, i == 3)
		}
		if n := st2.dealtOn("ctl-hang"); n != 0 {
			t.Fatalf("pass %d left %d reservations on the hung bench", i, n)
		}
	}
	if !st2.held["ctl-hang"] {
		t.Fatal("three consecutive timeouts did not report the hold")
	}
}
