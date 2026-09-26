//go:build functional

package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

const oneCountSHA = "0123456789abcdef0123456789abcdef01234567"

// oneCountPlace moves a pushed card to where through the library's one task
// move, the way the verbs do: ready -> working -> review | merging -> landed
// at a sha, parked, or done/fail (cancel).
func oneCountPlace(t *testing.T, c *redis.Client, id, where string) {
	t.Helper()
	ctx := context.Background()
	move := func(to string, o taskcard.Opts) {
		t.Helper()
		o.By = "one-count"
		if o.Why == "" {
			o.Why = "fixture"
		}
		if _, err := taskcard.Move(ctx, c, id, to, o); err != nil {
			t.Fatalf("%s -> %s: %v", id, to, err)
		}
	}
	switch where {
	case ws.Waiting, ws.Ready:
	case ws.Parked:
		move(ws.Parked, taskcard.Opts{})
	case ws.Done:
		if _, err := taskcard.Cancel(ctx, c, id, "one-count", "not needed"); err != nil {
			t.Fatalf("cancel %s: %v", id, err)
		}
	case ws.Review:
		// review is a copy's end: deal a copy to bench:b, start it, end it
		// --fail exit 1 (the typed-verdict wait, #4072)
		c.HSet(ctx, "bench:b:desired", "slots", "2")
		b, _ := taskcard.ParseConsumer("bench:b")
		d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, IDs: []string{id}, By: "one-count"})
		if err != nil {
			t.Fatalf("deal %s: %v", id, err)
		}
		if _, err := taskcard.Work(ctx, c, b, "one-count", 0, false, d[0].Copy); err != nil {
			t.Fatalf("work %s: %v", d[0].Copy, err)
		}
		if e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, Why: "child exit 1", By: "b",
			Fields: []string{"exit", "1"}}); err != nil || e[0].To != ws.Review {
			t.Fatalf("end --fail %s: %v %v", id, e, err)
		}
	default:
		move(ws.Working, taskcard.Opts{As: "f1", Friend: "f1", SetFriend: true})
		switch where {
		case ws.Merging:
			move(where, taskcard.Opts{})
		case ws.Landed:
			move(ws.Merging, taskcard.Opts{})
			if _, err := taskcard.Land(ctx, c, id, "one-count", oneCountSHA, ""); err != nil {
				t.Fatalf("land %s: %v", id, err)
			}
		}
	}
}

// TestOneCountFunctional (#one-count): the one-count fixture pushed through
// the real library on a throwaway redis-server (ns_sprint_begin and
// ns_sprint_open, ns_tcard_push creating each stream's sentinel, ns_tcard_move
// for every state, a land at a sha), then sprint status, the table headline,
// the live loop's headline, the total row and ws counts print the same
// numbers; landing one more card moves them all together.
func TestOneCountFunctional(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	st := store.New(c)
	opened := time.Now()
	if line, err := sprint.Begin(ctx, st, oneCountSprint, "one-count.lisp", "sha-one-count", opened); err != nil || line != "" {
		t.Fatalf("begin: %q %v", line, err)
	}
	if line, err := sprint.Finish(ctx, st, oneCountSprint, "one-count.lisp", "sha-one-count", opened, nil); err != nil || line != "" {
		t.Fatalf("open: %q %v", line, err)
	}
	for _, card := range oneCountCards {
		where := ws.Ready
		if card.where == ws.Waiting || card.where == ws.Parked {
			where = ws.Waiting
		}
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: card.id, Where: where, Stream: card.stream, Sprint: oneCountSprint,
			Kind: "build", Ref: "nova-tools#4000", Origin: "issue:mas-bandwidth/nova-tools#4000", Repo: "mas-bandwidth/nova-tools",
			Title: "STREAM: " + card.stream + " | " + card.id, By: "one-count"}); err != nil {
			t.Fatalf("push %s: %v", card.id, err)
		}
		oneCountPlace(t, c, card.id, card.where)
	}
	for _, s := range oneCountStreams {
		if n, _ := c.ZScore(ctx, ws.Key(s, ws.Waiting), ws.SentinelID(s)).Result(); n == 0 {
			t.Fatalf("stream %s has no sentinel waiting", s)
		}
	}

	now := time.Now() // after every move: the three lands are in its hour
	got, cells := oneCountPrintouts(t, c, now)
	want := got["ws counts"]
	if want.done != 3 || want.total != 11 || want.left != 8 || want.pct != 27 || !strings.HasSuffix(want.eta, " ET") {
		t.Fatalf("ws counts %+v; want 3/11 27%%, left 8, an eta in ET (the three sentinels aside)", want)
	}
	assertOneCount(t, got, want)
	if cells != [6]int{2, 2, 2, 1, 1, 3} {
		t.Fatalf("total row %v; want waiting 2 (the 3 sentinels aside) ready 2 working 2 review 1 merging 1 landed 3", cells)
	}

	// Mutation: g2 lands through the library. Every printout moves together.
	oneCountPlace(t, c, "g2", ws.Merging)
	if _, err := taskcard.Land(ctx, c, "g2", "one-count", oneCountSHA, ""); err != nil {
		t.Fatalf("land g2: %v", err)
	}
	now = time.Now()
	got, cells = oneCountPrintouts(t, c, now)
	want = got["ws counts"]
	if want.done != 4 || want.total != 11 || want.left != 7 || want.pct != 36 {
		t.Fatalf("after g2 lands ws counts %+v; want 4/11 36%%, left 7", want)
	}
	assertOneCount(t, got, want)
	if cells != [6]int{2, 2, 1, 1, 1, 4} {
		t.Fatalf("total row after the land %v", cells)
	}
}

// probeCard is one card of the sentinel probe's fixture.
type probeCard struct{ id, stream, where string }

// The sentinel probe's fixture (the cold read of #4411): three streams, each
// with its sentinel waiting (the library creates it at the stream's first
// push). alpha holds a card in waiting, ready, working, review and landed,
// plus a parked and a cancelled card (neither counted); beta a card in
// waiting, ready and merging; gamma only a cancelled card, so its sentinel
// is its only member in the six sets. The one count: 8 cards, 1 landed.
var probeCards = []probeCard{
	{"pa1", "alpha", ws.Waiting}, {"pa2", "alpha", ws.Ready}, {"pa3", "alpha", ws.Working},
	{"pa4", "alpha", ws.Review}, {"pa5", "alpha", ws.Landed}, {"pa6", "alpha", ws.Parked}, {"pa7", "alpha", ws.Done},
	{"pb1", "beta", ws.Waiting}, {"pb2", "beta", ws.Ready}, {"pb3", "beta", ws.Merging},
	{"pg1", "gamma", ws.Done},
}

// probePrinters runs every printer of a card count through its verb (or,
// for the live loop's tick and the progress duty, the function the verb
// runs) and fails unless each carries want (done/total, left; eta "-" when
// total is 0) and the per-stream cells sum to the same numbers. It returns
// the PROGRESS lines of the duty's run.
func probePrinters(t *testing.T, addr string, c *redis.Client, p *reconcile.Progress, l *reconcile.Lease, out *strings.Builder, done, total int) string {
	t.Helper()
	ctx := context.Background()
	left := total - done
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	header := fmt.Sprintf("%d/%d done %d%%, left %d, eta ", done, total, pct, left)
	run := func(want int, args ...string) string {
		t.Helper()
		code, stdout, stderr := runSprint(append(append([]string{}, args...), "--redis", addr)...)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\n%s%s", args, code, want, stdout, stderr)
		}
		return stdout
	}
	// sprint status
	st := run(0, "sprint", "status")
	if !strings.HasPrefix(st, oneCountSprint+" open "+header) || (total == 0 && !strings.HasSuffix(st, "eta -\n")) {
		t.Fatalf("sprint status %q; want %q", st, header)
	}
	// ws counts
	wc := run(0, "ws", "counts")
	if !strings.Contains(wc, fmt.Sprintf(" total=%d done=%d/%d pct=%d left=%d eta=", total, done, total, pct, left)) ||
		(total == 0 && !strings.Contains(wc, ` eta="-"`)) {
		t.Fatalf("ws counts %q; want %d/%d left %d", wc, done, total, left)
	}
	// the table, one-shot and the live loop's steady tick: the headline and
	// the total row; with no card neither prints and the one count's total
	// row is all zeros
	tbl := run(0, "table", "--layout", "live", "--once")
	r := table.NewSprintReader(c, table.SprintConfig{})
	if _, err := r.Read(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, err := r.Read(ctx, time.Now())
	if err != nil || snap.RoundTrips != 1 {
		t.Fatalf("live tick: %v, %d round trips", err, snap.RoundTrips)
	}
	live := snap.Render(time.Now())
	var row [6]int64
	for i, n := range snap.Counts.Total.Cells {
		row[i] = n
	}
	sum := row[0] + row[1] + row[2] + row[3] + row[4] + row[5]
	if sum != int64(total) || row[5] != int64(done) {
		t.Fatalf("live total row %v; want %d cards, %d landed", row, total, done)
	}
	for what, body := range map[string]string{"table --once": tbl, "live tick": live} {
		if total > 0 {
			if !strings.Contains(body, "\n"+header) {
				t.Fatalf("%s headline is not %q:\n%s", what, header, body)
			}
			m := totalRE.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("%s: no total row:\n%s", what, body)
			}
			var got [6]int64
			for i := range got {
				got[i], _ = strconv.ParseInt(m[i+1], 10, 64)
			}
			if got != row {
				t.Fatalf("%s total row %v; the one count's %v", what, got, row)
			}
		} else if headerRE.MatchString(body) || strings.Contains(body, "\nstream ") || totalRE.MatchString(body) {
			t.Fatalf("%s prints a count with no card:\n%s", what, body)
		}
	}
	// ws show and stream ls: per stream, summed; gamma (its sentinel alone)
	// is 0 everywhere, shown as a stop, never 1 waiting
	show := run(0, "ws", "show", "--order")
	showRE := regexp.MustCompile(`(?m)^STREAM \d+ "([^"]+)" cards=(\d+) live=(\d+) landed=(\d+) sentinel=(\w+)$`)
	var sc, sl, sd int
	for _, m := range showRE.FindAllStringSubmatch(show, -1) {
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		sc, sl, sd = sc+n(2), sl+n(3), sd+n(4)
		if m[1] == "gamma" && (n(2) != 0 || n(3) != 0 || m[5] != ws.Waiting) {
			t.Fatalf("ws show gamma %q; want cards=0 live=0 sentinel=waiting", m[0])
		}
	}
	if sc != total || sl != left || sd != done || !strings.Contains(show, fmt.Sprintf("SHOW streams=3 cards=%d ", total)) {
		t.Fatalf("ws show sums cards=%d live=%d landed=%d; want %d %d %d\n%s", sc, sl, sd, total, left, done, show)
	}
	ls := run(0, "stream", "ls")
	lsRE := regexp.MustCompile(`(?m)^\d+ "([^"]+)" waiting=(\d+) ready=(\d+) working=(\d+) review=(\d+) merging=(\d+) landed=(\d+) parked=\d+$`)
	lc, ld := 0, 0
	for _, m := range lsRE.FindAllStringSubmatch(ls, -1) {
		for i := 2; i <= 7; i++ {
			v, _ := strconv.Atoi(m[i])
			lc += v
			if m[1] == "gamma" && v != 0 {
				t.Fatalf("stream ls gamma %q; want 0 everywhere", m[0])
			}
		}
		v, _ := strconv.Atoi(m[7])
		ld += v
	}
	if lc != total || ld != done {
		t.Fatalf("stream ls sums %d cards %d landed; want %d %d\n%s", lc, ld, total, done, ls)
	}
	// the default wide table: the retired card family's row refuses, never
	// a row of zeros beside the one count
	wide := run(0, "table", "--once")
	if want := "pipeline " + oneCountSprint + " " + table.RetiredPipeline + "\n"; !strings.Contains(wide, want) || strings.Contains(wide, " landed=0 ") {
		t.Fatalf("table --once:\n%s\nwant %q", wide, want)
	}
	// PROGRESS left= per stream: the one count's left
	out.Reset()
	if _, err := p.Run(ctx, l); err != nil {
		t.Fatalf("progress: %v", err)
	}
	pl := 0
	for _, m := range regexp.MustCompile(`PROGRESS (\S+) left=(\d+) `).FindAllStringSubmatch(out.String(), -1) {
		v, _ := strconv.Atoi(m[2])
		pl += v
		if m[1] == "gamma" && v != 0 {
			t.Fatalf("PROGRESS gamma left=%d; want 0", v)
		}
	}
	if strings.Contains(out.String(), "PROGRESS alpha ") && pl != left {
		t.Fatalf("PROGRESS lefts sum %d; want %d\n%s", pl, left, out.String())
	}
	return out.String()
}

// probeStore is the sentinel probe's fixture, pushed through the library on
// a throwaway redis-server: the sprint open, probeCards placed (1/8), a
// reconcile lease and the progress duty with a clock the test moves.
type probeStore struct {
	addr  string
	c     *redis.Client
	st    *store.Store
	p     *reconcile.Progress
	l     *reconcile.Lease
	out   *strings.Builder
	clock *time.Time
}

func newProbeStore(t *testing.T) *probeStore {
	t.Helper()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	st := store.New(c)
	opened := time.Now()
	if line, err := sprint.Begin(ctx, st, oneCountSprint, "probe.lisp", "sha-probe", opened); err != nil || line != "" {
		t.Fatalf("begin: %q %v", line, err)
	}
	if line, err := sprint.Finish(ctx, st, oneCountSprint, "probe.lisp", "sha-probe", opened, nil); err != nil || line != "" {
		t.Fatalf("open: %q %v", line, err)
	}
	for _, card := range probeCards {
		where := ws.Ready
		if card.where == ws.Waiting || card.where == ws.Parked {
			where = ws.Waiting
		}
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: card.id, Where: where, Stream: card.stream, Sprint: oneCountSprint,
			Kind: "build", Ref: "nova-tools#4411", Origin: "issue:mas-bandwidth/nova-tools#4411", Repo: "mas-bandwidth/nova-tools",
			Title: "STREAM: " + card.stream + " | " + card.id, By: "probe"}); err != nil {
			t.Fatalf("push %s: %v", card.id, err)
		}
		oneCountPlace(t, c, card.id, card.where)
	}
	for _, s := range []string{"alpha", "beta", "gamma"} {
		if _, err := c.ZScore(ctx, ws.Key(s, ws.Waiting), ws.SentinelID(s)).Result(); err != nil {
			t.Fatalf("stream %s has no sentinel waiting: %v", s, err)
		}
	}
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "probe-4411"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	ps := &probeStore{addr: addr, c: c, st: st, l: l, out: &strings.Builder{}, clock: new(time.Time)}
	*ps.clock = time.Now()
	ps.p = &reconcile.Progress{Client: c, Out: ps.out, Now: func() time.Time { return *ps.clock }}
	return ps
}

// printers runs probePrinters against the fixture: every printer says
// done/total.
func (ps *probeStore) printers(t *testing.T, done, total int) string {
	t.Helper()
	return probePrinters(t, ps.addr, ps.c, ps.p, ps.l, ps.out, done, total)
}

// TestOneCountSentinelProbe (#4411, the cold read's probes): with a sentinel
// in every stream, a parked, a cancelled and a review card, every printer
// (sprint status, ws counts, table --layout live --once and the live tick's
// headline and total row, ws show, stream ls, PROGRESS left=) says 1/8;
// the default table --once refuses the retired pipeline row; sprint status
// --sprint other-sprint is REFUSED naming the open sprint; sprint clear
// --force prints cards=8, and afterwards every printer says 0/0 with eta -.
func TestOneCountSentinelProbe(t *testing.T) {
	t.Parallel()
	ps := newProbeStore(t)
	addr := ps.addr

	lines := ps.printers(t, 1, 8)
	t.Logf("PROGRESS before the clear:\n%s", lines)

	code, stdout, _ := runSprint("sprint", "status", "--redis", addr, "--sprint", "other-sprint")
	if want := `REFUSED sprint status --sprint other-sprint: not the open sprint; open=` + oneCountSprint + ` remedy="nova-sprint sprint status"` + "\n"; code != 1 || stdout != want {
		t.Fatalf("status other-sprint: exit %d %q; want 1 %q", code, stdout, want)
	}

	code, stdout, stderr := runSprint("sprint", "clear", "--redis", addr, "--why", "probe", "--force")
	if code != 0 || !strings.HasPrefix(stdout, "CLEARED streams=3 cards=8 ") {
		t.Fatalf("clear: exit %d\n%s%s; want CLEARED streams=3 cards=8 (the one count's 8)", code, stdout, stderr)
	}
	t.Logf("%s", strings.SplitN(stdout, "\n", 2)[0])

	*ps.clock = ps.clock.Add(time.Minute) // past the duty's every_s gate
	lines = ps.printers(t, 0, 0)
	t.Logf("PROGRESS after the clear:\n%s", lines)
}

// TestOneCountRetiredFamilyProbe (#4411, the cold read's probe run
// verbatim): one SADD s:<S>:idx:card:landed old1 plus its roster entry in
// sprint:<S>:cards, the family card push and 02_card_move.lua still write.
// Every printer of the one count still says 1/8; the wide table --once's
// pipeline row and census --sprint are both REFUSED with the ws counts
// remedy, never landed=1 or cards=1; card fsck's counts carry the family's
// label.
func TestOneCountRetiredFamilyProbe(t *testing.T) {
	t.Parallel()
	ps := newProbeStore(t)
	ctx := context.Background()
	if err := ps.c.SAdd(ctx, "s:"+oneCountSprint+":idx:card:landed", "old1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := ps.c.ZAdd(ctx, "sprint:"+oneCountSprint+":cards", redis.Z{Score: 1, Member: "s:" + oneCountSprint + ":card:old1"}).Err(); err != nil {
		t.Fatal(err)
	}
	ps.printers(t, 1, 8) // the wide row refused among them

	code, wide, stderr := runSprint("table", "--once", "--redis", ps.addr)
	if want := "pipeline " + oneCountSprint + " " + table.RetiredPipeline + "\n"; code != 0 || !strings.Contains(wide, want) ||
		regexp.MustCompile(`(?m)^pipeline .*=\d`).MatchString(wide) {
		t.Fatalf("table --once: exit %d\n%s%s\nwant %q and no pipeline number", code, wide, stderr, want)
	}
	code, census, stderr := runSprint("census", "--redis", ps.addr, "--sprint", oneCountSprint)
	if want := `REFUSED census reads a retired key family; remedy="nova-sprint ws counts"` + "\n"; code != 1 || census != want {
		t.Fatalf("census --sprint: exit %d %q %q; want 1 %q", code, census, stderr, want)
	}
	_, fsck, stderr := runSprint("card", "fsck", "--redis", ps.addr, "--sprint", oneCountSprint)
	if !strings.HasPrefix(fsck, "CARD FSCK sprint="+oneCountSprint+" family=retired cards=") {
		t.Fatalf("card fsck: %q %q; want its counts labelled family=retired", fsck, stderr)
	}
	t.Logf("%s%s%s", regexp.MustCompile(`(?m)^pipeline .*\n`).FindString(wide), census, strings.SplitN(fsck, "\n", 2)[0])
}

// TestOneCountLiveTableNotOpenProbe (#4411): table --layout live --once
// --sprint naming a sprint that is not the open one is REFUSED on stdout,
// exit 1, naming the open sprint (as sprint status is), and so is the loop
// on its first tick; the open sprint's own name renders the one count.
func TestOneCountLiveTableNotOpenProbe(t *testing.T) {
	t.Parallel()
	ps := newProbeStore(t)
	want := `REFUSED table --layout live --sprint other: not the open sprint; open=` + oneCountSprint + ` remedy="nova-sprint table --layout live"` + "\n"
	for _, args := range [][]string{
		{"table", "--layout", "live", "--once", "--sprint", "other", "--redis", ps.addr},
		{"table", "--layout", "live", "--loop", "1", "--sprint", "other", "--redis", ps.addr},
	} {
		if code, stdout, stderr := runSprint(args...); code != 1 || stdout != want {
			t.Fatalf("%v: exit %d %q %q; want 1 %q", args, code, stdout, stderr, want)
		}
	}
	code, stdout, stderr := runSprint("table", "--layout", "live", "--once", "--sprint", oneCountSprint, "--redis", ps.addr)
	if code != 0 || !strings.Contains(stdout, "\n1/8 done 12%, left 7, eta ") {
		t.Fatalf("--sprint %s: exit %d %q %q; want the one count 1/8", oneCountSprint, code, stdout, stderr)
	}
	t.Logf("%s", want)
}

// TestOneCountClearKeepsParked (#4411): a parked card is not work in flight
// and not landed, and a clear is not a cancel. After sprint clear --force
// with one parked card (pa6), every printer says 0/0, the CLEARED receipt
// says parked_kept=1, ws counts still reads parked=1 (outside the x/y) and
// stream ls alpha parked=1.
func TestOneCountClearKeepsParked(t *testing.T) {
	t.Parallel()
	ps := newProbeStore(t)
	code, stdout, stderr := runSprint("sprint", "clear", "--redis", ps.addr, "--why", "probe", "--force")
	if code != 0 || !strings.HasPrefix(stdout, "CLEARED streams=3 cards=8 ") || !strings.Contains(stdout, " parked_kept=1 by=") {
		t.Fatalf("clear: exit %d\n%s%s; want CLEARED streams=3 cards=8 ... parked_kept=1", code, stdout, stderr)
	}
	*ps.clock = ps.clock.Add(time.Minute)
	ps.printers(t, 0, 0)
	_, counts, _ := runSprint("ws", "counts", "--redis", ps.addr)
	if !strings.Contains(counts, " landed=0 parked=1 total=0 done=0/0 ") {
		t.Fatalf("ws counts after the clear %q; want parked=1 beside total=0 done=0/0", counts)
	}
	_, ls, _ := runSprint("stream", "ls", "--redis", ps.addr)
	if !regexp.MustCompile(`(?m)^\d+ "alpha" waiting=0 ready=0 working=0 review=0 merging=0 landed=0 parked=1$`).MatchString(ls) {
		t.Fatalf("stream ls after the clear:\n%s\nwant alpha parked=1 and zeros", ls)
	}
	if n, err := ps.c.ZScore(context.Background(), ws.Key("alpha", ws.Parked), "pa6").Result(); err != nil || n == 0 {
		t.Fatalf("pa6 is not in ws:alpha:parked after the clear: %v", err)
	}
	t.Logf("%s", strings.SplitN(stdout, "\n", 2)[0])
}
