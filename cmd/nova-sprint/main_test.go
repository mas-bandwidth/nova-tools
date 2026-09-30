package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// testApp is the command over an in-memory store, its clock stepped by hand.
type testApp struct {
	t      *testing.T
	a      *app
	m      *store.Mem
	mu     sync.Mutex
	now    time.Time
	prefix string
	// live is the fleet members that beat before every command line and
	// after every step of the clock: the machines alive.
	live []string
}

func newTestApp(t *testing.T) *testApp { return newTestAppPrefix(t, "t-") }

// newTestAppPrefix is newTestApp under the given table prefix ("" is none).
func newTestAppPrefix(t *testing.T, prefix string) *testApp {
	ta := &testApp{t: t, m: store.NewMem(), now: t0, live: []string{"m1", "m2"}, prefix: prefix}
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_PREFIX": prefix, "NOVA_SPRINT_ACTOR": "coordinator"}
	ta.a = newApp(func(k string) string { return env[k] })
	ta.a.now = func() time.Time { ta.mu.Lock(); defer ta.mu.Unlock(); return ta.now }
	ta.a.sleep = func(d time.Duration) { ta.mu.Lock(); ta.now = ta.now.Add(d); ta.mu.Unlock(); ta.beat() }
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return ta.m, nil }
	ta.a.meter = hostload.Source{NCPU: 4, Load1: func() (float64, bool) { return 1, true }}
	return ta
}

// beat is one beat of every live member, at load 0.
func (ta *testApp) beat() {
	ta.mu.Lock()
	live := append([]string(nil), ta.live...)
	ta.mu.Unlock()
	st := &store.Store{B: ta.m, Names: sprint.Names{Prefix: ta.prefix}, Now: ta.a.now}
	zero := 0.0
	for _, m := range live {
		if _, err := st.Beat(context.Background(), m, &zero, hostload.Source{}); err != nil {
			ta.t.Fatal(err)
		}
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
	st, err := ta.a.store(common{redis: "mem:0", prefix: "t-", actor: "tester"})
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
	if code != 0 {
		ta.t.Fatalf("%s: exit %d\n%s%s", line, code, out, errs)
	}
	return out
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
	if err := json.Unmarshal([]byte(out), v); err != nil {
		ta.t.Fatalf("%s: %v\n%s", line, err, out)
	}
}

// deal deals up to n ready primaries, as the machine's tick does, without
// the rest of the tick: a test that needs exact queues deals by hand.
func (ta *testApp) deal(n int) {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", prefix: "t-", actor: "tester"})
	if err != nil {
		ta.t.Fatal(err)
	}
	res, err := st.Run(context.Background(), store.DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: n}}))
	if err != nil || len(res.Refused) > 0 {
		ta.t.Fatalf("deal %d: %+v %v", n, res.Refused, err)
	}
}

func (ta *testApp) clean() {
	ta.t.Helper()
	if code, out, errs := ta.do("check"); code != 0 {
		ta.t.Fatalf("check: %s%s", out, errs)
	}
}

func TestTheCommandDrivesAStreamToLanded(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	out := ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	if !strings.Contains(out, "INIT OK tables=t-work,t-readers,t-merge,t-fleet view=t-sprint") {
		t.Fatalf("init: %s", out)
	}
	out = ta.ok("add --stream s1 --count 4")
	if !strings.Contains(out, "ADD OK moved=4") || !strings.Contains(out, "\nSTOPPED  0/4 0.0%") || strings.Contains(out, "-> ETA") {
		t.Fatalf("add: %s", out)
	}
	ta.clean()
	ta.ok("start")
	ta.ok("tick")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	if len(q.Cards) != 2 || q.Cards[0].Gen != 1 {
		t.Fatalf("m1's queue: %+v", q.Cards)
	}
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
	if !strings.Contains(out, "ACCEPT OK moved=4") {
		t.Fatalf("accept: %s", out)
	}
	ta.ok("merge --stream s1 --batch 10")
	out = ta.ok("where")
	for _, want := range []string{"SPRINT TABLE", "4/4 100.0% -> ETA", "work ", "merge ", "fleet "} {
		if !strings.Contains(out, want) {
			t.Errorf("where lacks %q:\n%s", want, out)
		}
	}
	var w whereView
	ta.json("where", &w)
	if w.Landed != 4 || w.All != 4 || w.Tables["merge"]["s1"]["state"] != "landed" {
		t.Fatalf("where --json: %+v", w)
	}
	ta.clean()
	out = ta.ok("inbox")
	if !strings.Contains(out, "HAPPENED") || !strings.Contains(out, "stream landed") {
		t.Fatalf("inbox: %s", out)
	}
	out = ta.ok("card --fields s1-1")
	if !strings.Contains(out, "PRIMARY s1-1 place=s1:landed") || !strings.Contains(out, "WORK s1-1.w1") || !strings.Contains(out, "MERGE s1-1") {
		t.Fatalf("card: %s", out)
	}
}

func TestJudgmentsReachTheInboxAndTheCoordinatorAnswers(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 3")
	ta.ok("tick") // the third is dealt when the member's ready queue has room
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	code, _, errs := ta.do("finish --as m1 s1-3.w1@1 --failed --report 'tests red'")
	if code != 0 {
		t.Fatalf("finish failed: %s", errs)
	}
	out := ta.ok("inbox")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "JUDGMENT ") || !strings.Contains(lines[0], "work came back failed") || !strings.Contains(lines[0], "size=1") {
		t.Fatalf("judgment first: %s", out)
	}
	// the inbox group is a set, named by its id
	id := strings.Fields(lines[0])[1]
	out = ta.ok("rework --group " + id + " --fix 'handle the empty case'")
	if !strings.Contains(out, "s1-3 review -> working (rework)") {
		t.Fatalf("rework: %s", out)
	}
	out = ta.ok("inbox --read")
	if strings.Contains(out, "JUDGMENT") {
		t.Fatalf("an answered judgment is still open: %s", out)
	}
	out = ta.ok("inbox")
	if strings.Contains(out, "HAPPENED") {
		t.Fatalf("the cursor did not move: %s", out)
	}
	// accept refused without two readers: exit 1, the reason on stderr
	ta.ok("ask")
	code, _, errs = ta.do("accept s1-1")
	if code != 1 || !strings.Contains(errs, "REFUSED s1-1: needs ok from two different readers") || !strings.Contains(errs, "ACCEPT FAIL") {
		t.Fatalf("accept with no reads: %d %s", code, errs)
	}
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
	if !strings.Contains(out, "stream stopped: conflict on a card") {
		t.Fatalf("inbox: %s", out)
	}
	if code, _, errs := ta.do("merge --stream s1"); code != 1 || !strings.Contains(errs, "stopped") {
		t.Fatalf("merge of a stopped stream: %s", errs)
	}
	ta.ok("resume --stream s1 --did 'rebased s1-2'")
	ta.ok("merge --stream s1")
	var w whereView
	ta.json("where", &w)
	if w.Landed != 2 {
		t.Fatalf("landed %d", w.Landed)
	}
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
	if code != 1 || !strings.Contains(errs, "stale") {
		t.Fatalf("a stale finish: %d %s", code, errs)
	}
	ta.ok("take --as m2 --limit 5")
	first := ta.ok("finish --as m2 " + card + "@2 --op w-1")
	again := ta.ok("finish --as m2 " + card + "@2 --op w-1")
	if !strings.Contains(again, "replay=yes") || strings.Count(first, "MOVED") != strings.Count(again, "MOVED") {
		t.Fatalf("retry:\n%s\n%s", first, again)
	}
	if code, _, errs := ta.do("finish --as m2 " + card); code != 2 || !strings.Contains(errs, "<card>@<gen>") {
		t.Fatalf("a finish without its generation: %d %s", code, errs)
	}
}

func TestEveryVerbHasHelpAndRefusesBadUse(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	for _, v := range verbs {
		code, out, errs := ta.do(v.name + " -h")
		if code != 0 || !strings.Contains(out, "usage: nova-sprint "+v.name) || errs != "" {
			t.Errorf("%s -h: %d %q %q", v.name, code, out, errs)
		}
	}
	if code, out, _ := ta.do("help"); code != 0 || !strings.Contains(out, "nova-sprint play") {
		t.Errorf("help: %d", code)
	}
	for _, line := range []string{"", "nosuch", "start now", "take", "merge", "read --as reader-a", "rank x", "teardown", "add --stream s1"} {
		if code, _, errs := ta.do(line); code != 2 || !strings.Contains(errs, "run: nova-sprint") {
			t.Errorf("%q: exit %d %q", line, code, errs)
		}
	}
	if len(ta.m.Calls) > 0 && ta.m.Calls["apply"] > 0 {
		t.Errorf("a refused invocation wrote")
	}
}

// With no prefix (the default) the tables are the plain names, and clear and
// teardown want the view's name, sprint.
func TestNoPrefixIsTheDefault(t *testing.T) {
	t.Parallel()
	ta := newTestAppPrefix(t, "")
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	if out := ta.ok("where"); !strings.Contains(out, "work | waiting") || !strings.Contains(out, "readers ") || !strings.Contains(out, "fleet |") {
		t.Fatalf("where with no prefix: %s", out)
	}
	for _, verb := range []string{"clear", "teardown"} {
		for _, wrong := range []string{"none", "", "t-sprint", "work"} {
			code, _, errs := ta.do(verb + " --confirm '" + wrong + "'")
			if code != 2 || !strings.Contains(errs, "wants --confirm sprint") {
				t.Fatalf("%s --confirm %q: %d %s", verb, wrong, code, errs)
			}
		}
	}
	if out := ta.ok("clear --confirm sprint"); !strings.Contains(out, "CLEAR OK epoch=0->1") {
		t.Fatalf("clear: %s", out)
	}
	if out := ta.ok("teardown --confirm sprint"); !strings.Contains(out, "TEARDOWN OK sprint=sprint prefix= keys=") {
		t.Fatalf("teardown: %s", out)
	}
	if code, _, _ := ta.do("where"); code == 0 {
		t.Fatalf("the tables are still there")
	}
	prefixed := newTestApp(t)
	prefixed.ok("init --readers reader-a,reader-b --members m1")
	if code, _, errs := prefixed.do("clear --confirm sprint"); code != 2 || !strings.Contains(errs, "wants --confirm t-sprint") {
		t.Fatalf("a prefixed sprint takes its own view name: %d %s", code, errs)
	}
}

// A verb on a stopped machine ends with a line that starts with STOPPED and
// has no ETA, as the header of where does; with no cards it is STOPPED alone.
func TestVerbLineOnAStoppedMachineHasNoETA(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	out := ta.ok("init --readers reader-a --members m1")
	if last := lastLine(out); last != "STOPPED" {
		t.Fatalf("init on a stopped machine, no cards: last line %q in %s", last, out)
	}
	out = ta.ok("add --stream s1 --count 3")
	if last := lastLine(out); last != "STOPPED  0/3 0.0%" || strings.Contains(out, "-> ETA") {
		t.Fatalf("add on a stopped machine: last line %q in %s", last, out)
	}
	ta.ok("start")
	out = ta.ok("add --stream s2 --count 1")
	if last := lastLine(out); last != "0/4 0.0% -> ETA  machine: running" {
		t.Fatalf("add on a running machine: last line %q in %s", last, out)
	}
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

func TestTeardownWantsTheSamePrefix(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	if code, _, _ := ta.do("teardown --confirm other"); code != 2 {
		t.Fatalf("teardown with another prefix: %d", code)
	}
	ta.ok("teardown --confirm t-sprint")
	if code, _, _ := ta.do("where"); code == 0 {
		t.Fatalf("the tables are still there")
	}
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
	if code, _, _ := ta.do("clear --confirm other"); code != 2 {
		t.Fatalf("clear with another prefix: %d", code)
	}
	out := ta.ok("clear --confirm t-sprint")
	if !strings.Contains(out, "CLEAR OK epoch=0->1") || !strings.Contains(out, "primaries=2") || !strings.HasSuffix(strings.TrimSpace(out), "\nSTOPPED") || strings.Contains(out, "-> ETA") {
		t.Fatalf("clear: %s", out)
	}
	code, _, errs := ta.do("finish --as m1 --epoch 0 s1-1.w1@1")
	if code != 1 || !strings.Contains(errs, "cleared at") || !strings.Contains(errs, "epoch is now 1") {
		t.Fatalf("a late finish: %d %s", code, errs)
	}
	var w whereView
	ta.json("where", &w)
	if w.Epoch != 1 || w.All != 0 || w.Tables["merge"]["s1"]["state"] != "waiting" {
		t.Fatalf("where after clear: %+v", w)
	}
	ta.json("where --at-epoch 0", &w)
	if w.Epoch != 0 || w.All != 2 {
		t.Fatalf("where at the old epoch: %+v", w)
	}
	if out := ta.ok("card --fields s1-1 --at-epoch 0"); !strings.Contains(out, "place=s1:working") {
		t.Fatalf("card at the old epoch: %s", out)
	}
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	var q struct {
		Epoch uint64
		Cards []queueCard
	}
	ta.json("queue --as m1", &q)
	if q.Epoch != 1 || len(q.Cards) != 2 || q.Cards[0].ID != "s1-1.w1" {
		t.Fatalf("the same ids in the new epoch: %+v", q)
	}
	ta.ok("take --as m1 --epoch 1 s1-1.w1@1")
	ta.ok("clear --confirm t-sprint")
	ta.clean()
}
