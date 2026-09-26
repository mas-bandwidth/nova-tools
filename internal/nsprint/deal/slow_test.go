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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// SLOW: 3.8 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// TestDealFailOneCallHoldsBenchAfterThreeTimeouts is nova-tools #3322 on the
// real functions: the deal pass runs against a fake ssh that hangs (a client
// stuck after the banner) with a hard deadline, and the fleet duty's step
// (ns_fleet_step) runs before each pass as the reconciler runs it. Each pass
// returns inside the deadline; ns_card_deal_fail writes bench:<b>:ssh as
// timeout with why and at, returns the batch to the pool in that same call
// (starting empty, the cards queued and pooled, one undeal receipt each, one
// slot-freed and one bench-ssh receipt on cap:log), and counts the bench's
// consecutive timeouts; at the third it reports the hold, and the next fleet
// step flips the bench UP -> PROBING with a fleet-state receipt, after which
// ns_card_deal refuses it (NONE probing) and the plan skips it. cfg:fleet
// ssh_fail_after is honoured through ns_fleet_config, an ok row clears the
// count, and a stale token is FENCED before any write.
func TestDealFailOneCallHoldsBenchAfterThreeTimeouts(t *testing.T) {
	t.Parallel()

	const sprint = "control-00003322"
	const deadline = 1200 * time.Millisecond
	ctx := context.Background()
	c := dealRedis(t)
	f := newFixture(t)
	f.hang(t, "ctl-hang")
	r := f.remote()
	r.RunTimeout = deadline

	seedFleet(t, c, sprint, 6, map[string]int{"ctl-hang": 4})
	seedLease(t, c, "live-token")
	st := newFnStore(c)
	// Each pass reads the fleet from Redis, as the reconciler's does; the
	// refused-hold is zero so the bench is retried on every pass here.
	p := &Pass{Source: RedisSource{Client: c}, Fence: fence("live-token"), Reserver: st, Row: st, Dialer: r, Hold: time.Nanosecond}
	step := func() {
		t.Helper()
		if reply, err := c.FCall(ctx, "ns_fleet_step", nil).StringSlice(); err != nil || len(reply) == 0 || reply[0] != "OK" {
			t.Fatalf("ns_fleet_step: %v (%v)", reply, err)
		}
	}
	benchState := func() (string, string) {
		t.Helper()
		v, err := c.HMGet(ctx, "bench:ctl-hang:state", "state", "reason").Result()
		if err != nil {
			t.Fatal(err)
		}
		return str(v, 0), str(v, 1)
	}
	for i := 1; i <= 3; i++ {
		step()
		if state, _ := benchState(); state != "UP" {
			t.Fatalf("before pass %d: bench state %q, want UP (%d timeouts do not hold it)", i, state, i-1)
		}
		// The pass returning is the event: a hung ssh the deadline did not
		// cut holds it past the event wait (runPass).
		res, err := runPass(t, p)
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if len(res.Benches) != 1 || res.Benches[0].SSH != SSHTimeout || res.Benches[0].Returned != 4 || res.Benches[0].Timeouts != i {
			t.Fatalf("pass %d benches = %+v, want one timeout with 4 returned and %d timeouts", i, res.Benches, i)
		}
		row, err := c.HGetAll(ctx, RowKey("ctl-hang")).Result()
		if err != nil {
			t.Fatal(err)
		}
		if row["state"] != SSHTimeout || row["at"] == "" || row["timeouts"] != strconv.Itoa(i) || !strings.Contains(row["why"], "no start line") {
			t.Fatalf("pass %d row = %v, want timeout, at, timeouts=%d, why naming the missing start line", i, row, i)
		}
		if n := zcard(t, c, "bench:ctl-hang:starting"); n != 0 {
			t.Fatalf("pass %d left %d reservations in starting", i, n)
		}
		if n := zcard(t, c, "s:"+sprint+":pool"); n != 6 {
			t.Fatalf("pass %d: pool = %d, want all 6 back", i, n)
		}
		for j := 0; j < 6; j++ {
			label := fmt.Sprintf("card-%02d", j)
			card, _ := c.HMGet(ctx, "s:"+sprint+":card:"+label, "state", "token", "reason", "retries").Result()
			if str(card, 0) != "queued" || str(card, 1) != "" {
				t.Fatalf("pass %d: %s = %v, want queued with the token cleared", i, label, card)
			}
			if j < 4 && (str(card, 2) != ReasonSSHTimeout || str(card, 3) != strconv.Itoa(i)) {
				t.Fatalf("pass %d: %s reason %q retries %q, want %s and %d", i, label, str(card, 2), str(card, 3), ReasonSSHTimeout, i)
			}
		}
		if held := res.Benches[0].Held; held != (i == 3) {
			t.Fatalf("pass %d: held %t, want %t", i, held, i == 3)
		}
	}
	// The fleet duty's next step holds the bench.
	step()
	state, reason := benchState()
	if state != "PROBING" || !strings.HasPrefix(reason, "ssh timeout 3 of 3") {
		t.Fatalf("after three timeouts and a fleet step: state %q reason %q, want PROBING with the count", state, reason)
	}
	if held, _ := c.HGet(ctx, "bench:ctl-hang:state", "ssh_held").Result(); held != "3" {
		t.Fatalf("ssh_held = %q, want 3: the count the hold was taken at", held)
	}
	// The receipts: one bench-ssh per fail, one slot-freed per fail, one
	// fleet-state flip, all on cap:log.
	kinds := map[string]int{}
	msgs, err := c.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var flip string
	for _, m := range msgs {
		k, _ := m.Values["kind"].(string)
		kinds[k]++
		if k == "fleet-state" {
			flip, _ = m.Values["reason"].(string)
		}
	}
	if kinds["bench-ssh"] != 3 || kinds["slot-freed"] != 3 || kinds["fleet-state"] != 1 {
		t.Fatalf("cap:log kinds = %v, want 3 bench-ssh, 3 slot-freed, 1 fleet-state", kinds)
	}
	if !strings.HasPrefix(flip, "UP->PROBING ssh timeout 3 of 3") {
		t.Fatalf("fleet-state receipt %q, want UP->PROBING with the count", flip)
	}
	// A further step neither flips again nor writes another receipt: the
	// hold was taken at this count.
	step()
	if state, _ := benchState(); state != "PROBING" {
		t.Fatalf("bench state after another step %q, want PROBING", state)
	}
	if msgs, _ := c.XRange(ctx, "cap:log", "-", "+").Result(); countKind(msgs, "fleet-state") != 1 {
		t.Fatalf("fleet-state receipts after a further step = %d, want still 1", countKind(msgs, "fleet-state"))
	}
	undeals, err := c.XRange(ctx, "s:"+sprint+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range undeals {
		if m.Values["kind"] == "card undeal" && m.Values["reason"] == ReasonSSHTimeout {
			n++
		}
	}
	if n != 12 {
		t.Fatalf("%d card undeal receipts with reason %s, want 12 (4 cards, 3 passes)", n, ReasonSSHTimeout)
	}
	// A PROBING bench deals nothing: the function refuses it and the plan
	// skips it, so the fourth pass opens no session.
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Benches) != 0 {
		t.Fatalf("a PROBING bench was dealt: %+v", res.Benches)
	}
	reply, err := c.FCall(ctx, "ns_card_deal", nil, "ctl-hang", "live-token", "reconciler", "", sprint, "card-00", "4", "4."+strings.Repeat("a", 32), strings.Repeat("b", 12)).StringSlice()
	if err != nil || len(reply) < 2 || reply[0] != "NONE" || reply[1] != "probing" {
		t.Fatalf("ns_card_deal on the held bench = %v (%v), want NONE probing", reply, err)
	}

	t.Run("an ok row clears the count and ssh_fail_after is read from cfg:fleet", func(t *testing.T) {
		seedFleet(t, c, sprint, 2, map[string]int{"ctl-b": 4})
		seedLease(t, c, "live-token")
		if reply, err := c.FCall(ctx, "ns_fleet_config", nil, "", "", "1").StringSlice(); err != nil || len(reply) < 4 || reply[3] != "1" {
			t.Fatalf("ns_fleet_config ssh_fail_after=1: %v (%v)", reply, err)
		}
		st := newFnStore(c)
		res, err := st.Reserve(ctx, "live-token", "ctl-b", poolCards(sprint, 2))
		if err != nil || len(res) != 2 {
			t.Fatalf("reserve: %d, %v", len(res), err)
		}
		if err := st.SSH(ctx, "live-token", "ctl-b", SSHOK, "2 cards"); err != nil {
			t.Fatal(err)
		}
		if got, _ := c.HGet(ctx, RowKey("ctl-b"), "timeouts").Result(); got != "0" {
			t.Fatalf("timeouts after an ok row = %q, want 0", got)
		}
		// A refusal returns the batch but is not a timeout.
		fl, err := st.Fail(ctx, "live-token", "ctl-b", SSHRefused, "kex_exchange_identification", res)
		if err != nil || fl.Returned != 2 || fl.Timeouts != 0 || fl.State != "UP" {
			t.Fatalf("refused fail = %+v (%v), want 2 returned, 0 timeouts, UP", fl, err)
		}
		if n := zcard(t, c, "s:"+sprint+":pool"); n != 2 {
			t.Fatalf("pool after the refusal = %d, want 2", n)
		}
		res, err = st.Reserve(ctx, "live-token", "ctl-b", poolCards(sprint, 2))
		if err != nil || len(res) != 2 {
			t.Fatalf("reserve again: %d, %v", len(res), err)
		}
		fl, err = st.Fail(ctx, "live-token", "ctl-b", SSHTimeout, "hung", res)
		if err != nil || fl.Returned != 2 || fl.Timeouts != 1 || !fl.Hold {
			t.Fatalf("timeout fail with ssh_fail_after=1 = %+v (%v), want 2 returned, 1 timeout, hold", fl, err)
		}
		if reply, err := c.FCall(ctx, "ns_fleet_step", nil, "ctl-b").StringSlice(); err != nil || len(reply) == 0 || reply[0] != "OK" {
			t.Fatalf("ns_fleet_step: %v (%v)", reply, err)
		}
		if state, _ := c.HGet(ctx, "bench:ctl-b:state", "state").Result(); state != "PROBING" {
			t.Fatalf("bench state after one timeout with ssh_fail_after=1 and a step = %q, want PROBING", state)
		}
		// A repeat with the same attempts writes nothing more to the cards.
		fl, err = st.Fail(ctx, "live-token", "ctl-b", SSHTimeout, "hung", res)
		if err != nil || fl.Returned != 0 {
			t.Fatalf("repeated fail = %+v (%v), want 0 returned", fl, err)
		}
		if n := zcard(t, c, "s:"+sprint+":pool"); n != 2 {
			t.Fatalf("pool after the repeat = %d, want 2", n)
		}
	})

	t.Run("a stale token is fenced before any write", func(t *testing.T) {
		seedFleet(t, c, sprint, 2, map[string]int{"ctl-c": 4})
		seedLease(t, c, "live-token")
		st := newFnStore(c)
		res, err := st.Reserve(ctx, "live-token", "ctl-c", poolCards(sprint, 2))
		if err != nil || len(res) != 2 {
			t.Fatalf("reserve: %d, %v", len(res), err)
		}
		if _, err := st.Fail(ctx, "stale-token", "ctl-c", SSHTimeout, "hung", res); !errors.Is(err, ErrFenced) {
			t.Fatalf("stale fail: %v, want FENCED", err)
		}
		if n, _ := c.Exists(ctx, RowKey("ctl-c")).Result(); n != 0 {
			t.Fatal("a fenced fail wrote the row")
		}
		if n := zcard(t, c, "bench:ctl-c:starting"); n != 2 {
			t.Fatalf("a fenced fail returned reservations: starting = %d, want 2", n)
		}
	})
}

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
