package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store/storetest"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
)

var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// testApp is the command over an in-memory store, its clock stepped by hand.
type testApp struct {
	t   *testing.T
	a   *app
	m   *store.Mem
	mu  sync.Mutex
	now time.Time
	// live is the fleet members that beat before every command line and
	// after every step of the clock: the machines alive.
	live []string
	// quiet is the readers that do not beat: every other reader of the readers
	// table beats with the members (a reader's own queue is its beat).
	// sent is every message the app sent on the friends' bus.
	sent  []bus.Message
	quiet map[string]bool
	// queue is the merge queues land asks, a fake: no forge is asked
	queue *heldQueue
}

func newTestApp(t *testing.T) *testApp {
	ta := &testApp{t: t, m: store.NewMem(), now: t0, live: []string{"m1", "m2"}}
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_ACTOR": "coordinator"}
	ta.a = newApp(func(k string) string { return env[k] })
	ta.a.now = func() time.Time { ta.mu.Lock(); defer ta.mu.Unlock(); return ta.now }
	ta.a.sleep = func(d time.Duration) { ta.mu.Lock(); ta.now = ta.now.Add(d); ta.mu.Unlock(); ta.beat() }
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return ta.m, nil }
	// a broken read's branch is checked against origin only by a test that gives a tip
	ta.a.readTip, ta.a.readHeads = nil, nil
	// the friends' bus: every message sent is kept, none goes anywhere
	ta.a.bus = func(_ context.Context, m bus.Message, _ func(string)) error {
		ta.mu.Lock()
		defer ta.mu.Unlock()
		ta.sent = append(ta.sent, m)
		return nil
	}
	// run's wait on a quiet log steps the clock by the time it may take
	ta.m.LogWait = func(d time.Duration) { ta.a.sleep(d) }
	ta.a.meter = hostload.Source{NCPU: 4, Load1: func() (float64, bool) { return 1, true }}
	// land asks no forge: every branch's merge queue is clear unless a test holds one
	ta.queue = &heldQueue{held: map[string]bool{}}
	ta.a.mergeQueue = ta.queue
	// every part a tick plans on its twin is checked against a fresh read
	ta.a.checkTwin = func(twin, fresh *sprint.Snapshot) error {
		if d := storetest.TwinDiff(twin, fresh); d != "" {
			return errors.New(d)
		}
		return nil
	}
	return ta
}

// homeOfItsOwn gives the app a home under the test's temp: a verb that walks the
// home reads that, never the machine's (gc --dry-run walks the bench root and every Go
// build cache under it; on a bench that is millions of entries and a minute of a test).
func (ta *testApp) homeOfItsOwn() {
	home := ta.t.TempDir()
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return env(k)
	}
}

// beat is one beat of every live member, at load 0.
func (ta *testApp) beat() {
	ta.mu.Lock()
	live := append([]string(nil), ta.live...)
	ta.mu.Unlock()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	zero := 0.0
	for _, m := range live {
		_, err := st.Beat(context.Background(), m, &zero, hostload.Source{})
		require.NoError(ta.t, err)
	}
	rows, err := st.ReaderRows(context.Background())
	require.NoError(ta.t, err)
	for _, r := range rows {
		ta.mu.Lock()
		quiet := ta.quiet[r]
		ta.mu.Unlock()
		if quiet {
			continue
		}
		_, err := st.ReaderBeat(context.Background(), r, "")
		require.NoError(ta.t, err)
	}
}

// do runs a command line and returns its exit code, stdout and stderr.
func (ta *testApp) do(line string) (int, string, string) {
	var out, errb bytes.Buffer
	ta.beat()
	code := ta.a.run(ta.withEpoch(split(line)), &out, &errb)
	return code, out.String(), errb.String()
}

// withEpoch is a report as an outside actor makes it: with the epoch its
// cards were handed at (the sprint's now), unless the line names one.
func (ta *testApp) withEpoch(args []string) []string {
	if len(args) == 0 {
		return args
	}
	switch args[0] {
	case "finish", "read", "merge", "ci", "take":
	default:
		return args
	}
	for _, a := range args {
		if a == "--epoch" || strings.HasPrefix(a, "--epoch=") || a == "--help" || a == "-h" {
			return args
		}
	}
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	if err != nil {
		return args
	}
	es, err := st.EpochNow(context.Background())
	if err != nil {
		return args
	}
	return append(append([]string(nil), args...), "--epoch", strconv.FormatUint(es.N, 10))
}

func (ta *testApp) ok(line string) string {
	ta.t.Helper()
	code, out, errs := ta.do(line)
	require.Equal(ta.t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	return out
}

// clearWithReturns exercises Clear's STOP fence before clearing an epoch with
// active owner work. The fixture observes each child exit before returning it.
func (ta *testApp) clearWithReturns(epoch uint64, row string, cards ...string) string {
	ta.t.Helper()
	code, _, why := ta.do("clear --confirm sprint")
	require.Equal(ta.t, 2, code, "clear captures active owner leases: %s", why)
	require.Contains(ta.t, why, "captured owner work/read leases")
	for _, card := range cards {
		ta.ok("stop-return --as " + row + " --epoch " + strconv.FormatUint(epoch, 10) + " " + card + " --reason 'fixture child exited'")
	}
	ta.ok("start")
	return ta.ok("clear --confirm sprint")
}

// split is words, with '...' quoting one word.
func split(line string) []string {
	var out []string
	var cur strings.Builder
	quoted, in := false, false
	for _, r := range line {
		switch {
		case r == '\'':
			quoted, in = !quoted, true
		case r == ' ' && !quoted:
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

func (ta *testApp) json(line string, v any) {
	ta.t.Helper()
	out := ta.ok(line + " --json")
	require.NoError(ta.t, json.Unmarshal([]byte(out), v), "%s: %s", line, out)
}

// deal deals up to n ready primaries, as the machine's tick does, without
// the rest of the tick: a test that needs exact queues deals by hand.
func (ta *testApp) deal(n int) {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	res, err := st.Run(context.Background(), dealStep(sprint.DealReq{Sel: sprint.Sel{Limit: n}}))
	require.NoError(ta.t, err, "deal %d: %+v %v", n, res.Refused, err)
	require.Empty(ta.t, res.Refused, "deal %d: %+v %v", n, res.Refused, err)
}

func (ta *testApp) clean() {
	ta.t.Helper()
	code, out, errs := ta.do("check")
	require.Equal(ta.t, 0, code, "check: %s%s", out, errs)
}

func TestTheCommandDrivesAStreamToLanded(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	out := ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	require.Contains(t, out, "INIT OK tables=work,readers,merge,fleet view=sprint", "init")
	out = ta.ok("add --stream s1 --count 4")
	require.Contains(t, out, "ADD OK stream=s1 cards=4 before=- moved=4", "add")
	require.Contains(t, out, "\nSTOPPED  0/4 0.0%", "add")
	require.NotContains(t, out, "-> ETA", "add")
	ta.clean()
	ta.ok("start")
	ta.ok("tick")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 2, "m1's queue: %+v", q.Cards)
	require.Equal(t, 1, q.Cards[0].Gen, "m1's queue: %+v", q.Cards)
	for _, m := range []string{"m1", "m2"} {
		ta.ok("take --as " + m + " --limit 5")
		ta.json("queue --as "+m, &q)
		var words []string
		for _, c := range q.Cards {
			words = append(words, c.ID+"@1")
		}
		ta.ok("finish --as " + m + " " + strings.Join(words, " "))
	}
	ta.ok("ask")
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		ta.ok("read --as " + r + " --ok --limit 10")
	}
	ta.clean()
	out = ta.ok("accept --read-ok")
	require.Contains(t, out, "ACCEPT OK moved=4", "accept")
	ta.ok("merge --stream s1 --batch 10")
	// the landings are queued for the next tick's pump, which drains them, and
	// the tick's done part stops the machine of a sprint that is done
	ta.ok("tick")
	out = ta.ok("where --all")
	for _, want := range []string{"SPRINT TABLE", "DONE", "work ", "merge ", "friends ", "fleet "} {
		assert.Contains(t, out, want, "where lacks %q", want)
	}
	var w whereView
	ta.json("where --archived", &w) // the tick archived s1 as its last card landed
	require.True(t, w.Done, "where --json: %+v", w)
	require.Equal(t, [2]int64{4, 4}, [2]int64{w.Landed, w.All}, "a sprint done counts the epoch's cards: %+v", w)
	require.Equal(t, [2]int64{4, 4}, [2]int64{w.ArchivedLanded, w.ArchivedCards}, "where --json: %+v", w)
	require.Equal(t, "4/4 100.0% done", w.Summary, "a sprint done counts the epoch's cards")
	require.Equal(t, "landed", w.Tables["merge"]["s1"]["state"], "where --json: %+v", w)
	require.Equal(t, []string{"s1"}, w.Archived.Streams, "where --json: %+v", w)
	ta.clean()
	out = ta.ok("inbox")
	require.Contains(t, out, "HAPPENED", "inbox")
	require.Contains(t, out, "stream landed", "inbox")
	out = ta.ok("card --fields s1-1")
	require.Contains(t, out, "PRIMARY s1-1 place=s1:landed", "card")
	require.Contains(t, out, "WORK s1-1.w1", "card")
	require.Contains(t, out, "MERGE s1-1", "card")
}

func TestJudgmentsReachTheInboxAndTheCoordinatorAnswers(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 3")
	ta.ok("tick") // the third is dealt when the member's ready queue has room
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	code, _, errs := ta.do("finish --as m1 s1-3.w1@1 --failed --report 'tests red'")
	require.Equal(t, 0, code, "finish failed: %s", errs)
	out := ta.ok("inbox")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.True(t, strings.HasPrefix(lines[0], "JUDGMENT "), "judgment first: %s", out)
	require.Contains(t, lines[0], "work came back failed", "judgment first: %s", out)
	require.Contains(t, lines[0], "size=1", "judgment first: %s", out)
	// the inbox group is a set, named by its id
	id := strings.Fields(lines[0])[1]
	out = ta.ok("rework --group " + id + " --fix 'handle the empty case'")
	require.Contains(t, out, "s1-3 work review -> working (rework)", "rework")
	out = ta.ok("inbox --read")
	require.NotContains(t, out, "JUDGMENT", "an answered judgment is still open")
	out = ta.ok("inbox")
	require.NotContains(t, out, "HAPPENED", "the cursor did not move")
	// accept refused without two readers: exit 1, the reason on stderr
	ta.ok("ask")
	code, _, errs = ta.do("accept s1-1")
	require.Equal(t, 1, code, "accept with no reads: %d %s", code, errs)
	require.Contains(t, errs, "REFUSED s1-1: needs ok from two different readers", "accept with no reads: %d %s", code, errs)
	require.Contains(t, errs, "ACCEPT FAILED", "accept with no reads: %d %s", code, errs)
	ta.clean()
}

func TestAStoppedStreamWaitsForResume(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 5")
	ta.ok("read --as reader-b --ok --limit 5")
	ta.ok("accept --stream s1")
	ta.ok("merge --stream s1 --conflict s1-2")
	out := ta.ok("inbox")
	require.Contains(t, out, "stream stopped: conflict on a card", "inbox")
	code, _, errs := ta.do("merge --stream s1")
	require.Equal(t, 1, code, "merge of a stopped stream: %s", errs)
	require.Contains(t, errs, "stopped", "merge of a stopped stream: %s", errs)
	ta.ok("resume --stream s1 --did 'rebased s1-2'")
	ta.ok("merge --stream s1")
	ta.ok("tick") // the pump drains the landings the merge queued
	var w whereView
	ta.json("where", &w)
	// the tick archives s1 as its last card lands: its cards leave the headline
	landed, _ := epochCounts(w)
	require.Equal(t, int64(2), landed, "landed %d, archived %d, done %v", w.Landed, w.ArchivedLanded, w.Done)
	ta.clean()
}

func TestAStaleFinishIsRefusedAndARetryReplays(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	card := q.Cards[0].ID
	ta.ok("take --as m1 " + card + "@1")
	ta.ok("fleet down m1")
	code, _, errs := ta.do("finish --as m1 " + card + "@1")
	require.Equal(t, 1, code, "a stale finish: %d %s", code, errs)
	require.Contains(t, errs, "stale", "a stale finish: %d %s", code, errs)
	ta.ok("take --as m2 --limit 5")
	first := ta.ok("finish --as m2 " + card + "@2 --op w-1")
	again := ta.ok("finish --as m2 " + card + "@2 --op w-1")
	require.Contains(t, again, "replay=yes", "retry:\n%s\n%s", first, again)
	require.Equal(t, strings.Count(first, "MOVED"), strings.Count(again, "MOVED"), "retry:\n%s\n%s", first, again)
	code, _, errs = ta.do("finish --as m2 " + card)
	require.Equal(t, 2, code, "a finish without its generation: %d %s", code, errs)
	require.Contains(t, errs, "<card>@<gen>", "a finish without its generation: %d %s", code, errs)
}

func TestEveryVerbHasHelpAndRefusesBadUse(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, v := range verbs {
		code, out, errs := ta.do(v.name + " -h")
		assert.Equal(t, 0, code, "%s -h: %d %q %q", v.name, code, out, errs)
		assert.Contains(t, out, "usage: nova-sprint "+v.name, "%s -h: %d %q %q", v.name, code, out, errs)
		assert.Empty(t, errs, "%s -h: %d %q %q", v.name, code, out, errs)
	}
	code, out, _ := ta.do("help")
	assert.Equal(t, 0, code, "help: %d", code)
	assert.Contains(t, out, "nova-sprint play", "help: %d", code)
	for _, line := range []string{"", "nosuch", "start now", "take", "merge", "read --as reader-a", "rank x", "teardown", "add --stream s1"} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 2, code, "%q: exit %d %q", line, code, errs)
		assert.Contains(t, errs, "run: nova-sprint", "%q: exit %d %q", line, code, errs)
	}
	assert.False(t, len(ta.m.Calls) > 0 && ta.m.Calls["apply"] > 0, "a refused invocation wrote")
}

// The tables are the plain names, and clear and
// teardown want the view's name, sprint.
func TestTablesAreNamedPlainlyAndConfirmIsTheViewName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	out := ta.ok("where --all")
	require.Contains(t, out, "work | waiting", "where with no prefix: %s", out)
	require.Contains(t, out, "readers ", "where with no prefix: %s", out)
	require.Contains(t, out, "fleet |", "where with no prefix: %s", out)
	for _, verb := range []string{"clear", "teardown"} {
		for _, wrong := range []string{"none", "", "t-sprint", "work", "prefix"} {
			code, _, errs := ta.do(verb + " --confirm '" + wrong + "'")
			require.Equal(t, 2, code, "%s --confirm %q: %d %s", verb, wrong, code, errs)
			require.Contains(t, errs, "wants --confirm sprint", "%s --confirm %q: %d %s", verb, wrong, code, errs)
		}
	}
	require.Contains(t, ta.ok("clear --confirm sprint"), "CLEAR OK epoch=0->1", "clear")
	require.Contains(t, ta.ok("teardown --confirm sprint"), "TEARDOWN OK sprint=sprint keys=", "teardown")
	code, _, _ := ta.do("where")
	require.NotEqual(t, 0, code, "the tables are still there")
	const none = "there is no prefix: the tables are always work, merge, readers and fleet and the view is sprint"
	for _, verb := range []string{"where", "card p1", "log", "clear", "teardown", "inbox", "check", "repair", "init", "add --stream s1", "fleet up m1", "goal set a", "goal show", "goal", "reader add r"} {
		var name string
		if f := strings.Fields(verb); f[0] == "goal" && len(f) == 1 {
			name = "goal"
		} else if f[0] == "goal" || f[0] == "fleet" || f[0] == "reader" {
			name = f[0] + " " + f[1]
		} else {
			name = f[0]
		}
		want := "nova-sprint " + name + " REFUSED: " + none + "; run: nova-sprint " + name + " -h\n"
		if name == "goal" {
			want = "nova-sprint goal REFUSED: " + none + "; run: nova-sprint goal -h\n"
		}
		code, _, errs := ta.do(verb + " --prefix x")
		require.Equal(t, 2, code, "%s --prefix x: exit %d, stderr %q, want %q", verb, code, errs, want)
		require.Equal(t, want, errs, "%s --prefix x: exit %d, stderr %q, want %q", verb, code, errs, want)
	}
	get := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "NOVA_SPRINT_PREFIX" {
			return "dev-"
		}
		return get(k)
	}
	code, _, errs := ta.do("where")
	require.Equal(t, 2, code, "a set NOVA_SPRINT_PREFIX is refused: %d %q", code, errs)
	require.Equal(t, "nova-sprint where REFUSED: NOVA_SPRINT_PREFIX is set: "+none+"; unset it; run: nova-sprint where -h\n", errs, "a set NOVA_SPRINT_PREFIX is refused: %d %q", code, errs)
	// before the --redis check: with no store named the refusal is still this one
	ta.a.getenv = func(k string) string { return map[string]string{"NOVA_SPRINT_PREFIX": "dev-"}[k] }
	code, _, errs = ta.do("where")
	require.Equal(t, 2, code, "the prefix variable is checked before --redis: %d %q", code, errs)
	require.True(t, strings.HasPrefix(errs, "nova-sprint where REFUSED: NOVA_SPRINT_PREFIX is set: "), "the prefix variable is checked before --redis: %d %q", code, errs)
}

// A verb on a stopped machine ends with a line that starts with STOPPED and
// has no ETA, as the header of where does; with no cards it is STOPPED alone.
func TestVerbLineOnAStoppedMachineHasNoETA(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	out := ta.ok("init --readers reader-a --members m1")
	require.Equal(t, "STOPPED", lastLine(out), "init on a stopped machine, no cards: last line %q in %s", lastLine(out), out)
	out = ta.ok("add --stream s1 --count 3")
	require.Equal(t, "STOPPED  0/3 0.0%", lastLine(out), "add on a stopped machine: last line %q in %s", lastLine(out), out)
	require.NotContains(t, out, "-> ETA", "add on a stopped machine: last line %q in %s", lastLine(out), out)
	ta.ok("start")
	out = ta.ok("add --stream s2 --count 1 --one")
	// the added card is queued for the next tick's pump: the table counts it
	// once the pump has drained the queue
	require.Equal(t, "0/3 0.0% -> ETA -  machine: running", lastLine(out), "add on a running machine: last line %q in %s", lastLine(out), out)
	out = ta.ok("tick")
	last := lastLine(out)
	require.True(t, strings.HasPrefix(last, "0/4 0.0% -> ETA"), "the tick after an add on a running machine: last line %q in %s", last, out)
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

func TestTeardownWantsTheSamePrefix(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, _, _ := ta.do("teardown --confirm other")
	require.Equal(t, 2, code, "teardown with another prefix: %d", code)
	ta.ok("teardown --confirm sprint")
	code, _, _ = ta.do("where")
	require.NotEqual(t, 0, code, "the tables are still there")
}

// clear: a new epoch, the old one readable, a late worker refused naming it,
// the same ids again.
func TestClearByTheCommand(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	code, _, _ := ta.do("clear --confirm other")
	require.Equal(t, 2, code, "clear with another prefix: %d", code)
	out := ta.clearWithReturns(0, "m1", "s1-1.w1@1", "s1-2.w1@1")
	require.Contains(t, out, "CLEAR OK epoch=0->1", "clear")
	require.Contains(t, out, "primaries=2", "clear")
	require.Contains(t, out, "\nSTOPPED\n", "clear: %s", out)
	require.NotContains(t, out, "-> ETA", "clear")
	code, _, errs := ta.do("finish --as m1 --epoch 0 s1-1.w1@1")
	require.Equal(t, 1, code, "a late finish: %d %s", code, errs)
	require.Contains(t, errs, "cleared at", "a late finish: %d %s", code, errs)
	require.Contains(t, errs, "epoch is now 1", "a late finish: %d %s", code, errs)
	var w whereView
	ta.json("where", &w)
	require.Equal(t, uint64(1), w.Epoch, "where after clear: %+v", w)
	require.Equal(t, int64(0), w.All, "where after clear: %+v", w)
	require.Equal(t, "waiting", w.Tables["merge"]["s1"]["state"], "where after clear: %+v", w)
	ta.json("where --at-epoch 0", &w)
	require.Equal(t, uint64(0), w.Epoch, "where at the old epoch: %+v", w)
	require.Equal(t, int64(2), w.All, "where at the old epoch: %+v", w)
	require.Contains(t, ta.ok("card --fields s1-1 --at-epoch 0"), "place=s1:working", "card at the old epoch")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	var q struct {
		Epoch uint64
		Cards []queueCard
	}
	ta.json("queue --as m1", &q)
	require.Equal(t, uint64(1), q.Epoch, "the same ids in the new epoch: %+v", q)
	require.Len(t, q.Cards, 2, "the same ids in the new epoch: %+v", q)
	require.Equal(t, "s1-1.w1", q.Cards[0].ID, "the same ids in the new epoch: %+v", q)
	ta.ok("start")
	ta.ok("take --as m1 --epoch 1 s1-1.w1@1")
	ta.clearWithReturns(1, "m1", "s1-1.w1@1")
	ta.clean()
}

// parse is every verb's one reading of its words: flags anywhere among them, and every
// word after the first -- that is not a flag's value taken as it is, however it looks.
func TestParseTakesEveryWordAfterTheTerminatorAsItIs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		args, pos  []string
		file, text string
		on         bool
	}{
		{"flags anywhere, no --", []string{"a", "--file", "f", "b", "--on", "--text=t", "c"}, []string{"a", "b", "c"}, "f", "t", true},
		{"flags before --, words that look like flags after it", []string{"--file", "f", "a", "--", "--file", "g", "--on"}, []string{"a", "--file", "g", "--on"}, "f", "", false},
		{"a word after -- that begins with a dash", []string{"--", "-x", "--text", "t"}, []string{"-x", "--text", "t"}, "", "", false},
		{"-- after a word", []string{"a", "--", "--on"}, []string{"a", "--on"}, "", "", false},
		{"-- as a flag's value", []string{"--text", "--", "a", "--on"}, []string{"a"}, "", "--", true},
		{"-- as a flag's value, then the terminator", []string{"--text", "--", "--", "--on"}, []string{"--on"}, "", "--", false},
		{"a second -- is a word", []string{"--", "--", "a"}, []string{"--", "a"}, "", "", false},
		{"a boolean takes no word", []string{"--on", "--", "--file", "f"}, []string{"--file", "f"}, "", "", true},
		{"one dash, and a lone dash is a word", []string{"-file=f", "-", "-on=true"}, []string{"-"}, "f", "", true},
	} {
		fs := verbflag.New("try")
		file, text, on := fs.String("file", "", ""), fs.String("text", "", ""), fs.Bool("on", false, "")
		pos, err := parse(fs, c.args)
		require.NoError(t, err, c.name)
		require.Equal(t, c.pos, pos, c.name)
		require.Equal(t, []any{c.file, c.text, c.on}, []any{*file, *text, *on}, c.name)
	}
	fs := verbflag.New("try")
	fs.String("text", "", "")
	_, err := parse(fs, []string{"a", "--nope"})
	require.ErrorContains(t, err, "unknown flag --nope", "an unknown flag after a word is refused")
	_, err = parse(fs, []string{"a", "--text"})
	require.ErrorContains(t, err, "--text wants a value", "a flag with no value is refused")
	require.PanicsWithValue(t, verbflag.Help{FS: fs}, func() { _, _ = parse(fs, []string{"a", "--help"}) }, "help after a word is help")
	pos, err := parse(fs, []string{"a", "--", "--help"})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "--help"}, pos, "help after -- is a word")
}

// dry runs a --dry-run line and holds that it wrote nothing: the store's whole state
// (store.Mem.Snapshot) is the same after it as before.
func (ta *testApp) dry(line string) string {
	ta.t.Helper()
	before, err := ta.m.Snapshot()
	require.NoError(ta.t, err)
	out := ta.ok(line)
	after, err := ta.m.Snapshot()
	require.NoError(ta.t, err)
	require.JSONEq(ta.t, string(before), string(after), "%s wrote to the store", line)
	return out
}

// dealStep cuts and deals work cards by hand, the step the tick's deal replaced
// (sprint.TickDeal): a test that needs exact queues deals with it.
func dealStep(r sprint.DealReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "deal", Load: []string{sprint.Work, sprint.Fleet, sprint.Merge}, Mirrors: true, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Deal(s, r) }}
}
