package main

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// C1: after a clear, inbox --read moves the new epoch's cursor, and does not
// write the old epoch's.
func TestInboxReadAfterClear(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'red'")
	var before struct{ Cursor string }
	ta.json("inbox --at-epoch 0", &before)
	ta.ok("clear --confirm sprint")
	// new epoch: produce a happened notification
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1")
	var v struct {
		Last, Cursor string
	}
	ta.json("inbox --read", &v)
	if v.Last == "" {
		t.Fatalf("no notification at the new epoch")
	}
	var after struct{ Cursor string }
	ta.json("inbox", &after)
	var old struct{ Cursor string }
	ta.json("inbox --at-epoch 0", &old)
	t.Logf("last=%q new-epoch cursor after --read=%q old-epoch cursor before=%q after=%q", v.Last, after.Cursor, before.Cursor, old.Cursor)
	if after.Cursor != v.Last {
		t.Errorf("inbox --read at epoch 1 did not move epoch 1's cursor (cursor %q, last %q)", after.Cursor, v.Last)
	}
	if old.Cursor != before.Cursor {
		t.Errorf("inbox --read at epoch 1 wrote epoch 0's cursor: %q -> %q", before.Cursor, old.Cursor)
	}
}

// C1: reader add after a clear adds the reader at the new epoch.
func TestReaderAddAfterClear(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("clear --confirm sprint")
	code, out, errs := ta.do("reader add reader-c")
	if code != 0 {
		t.Errorf("reader add after clear: exit %d %s%s", code, out, errs)
	}
}

// C1, C2: wait on a judgment of the new epoch works; a wait or an ack naming
// a judgment of the old epoch is refused and changes nothing.
func TestWaitAfterClear(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'red'")
	oldg := ta.group(sprint.NWorkFailed, "s1")
	oldNote := oldg.Notes[0]
	ta.ok("clear --confirm sprint")
	// an old judgment's wait: must not change the old epoch, must say cleared
	code, out, errs := ta.do("wait " + oldNote + " --for 1h")
	t.Logf("wait old judgment after clear: %d %s%s", code, out, errs)
	if code == 0 {
		t.Errorf("wait on an old epoch's judgment after clear succeeded (it wrote the old epoch): %s", out)
	}
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'red again'")
	g := ta.group(sprint.NWorkFailed, "s1")
	code, out, errs = ta.do("wait " + g.Notes[0] + " --for 1h")
	if code != 0 {
		t.Errorf("wait on the new epoch's judgment: exit %d %s%s", code, out, errs)
	}
	// ack of the OLD judgment id after clear
	code, out, errs = ta.do("ack " + oldNote + " --reason x")
	t.Logf("ack old judgment after clear: %d %s%s", code, out, errs)
	if code == 0 && !strings.Contains(out+errs, "refused=1") {
		t.Errorf("ack of an old judgment after clear: %s", out)
	}
}

func TestInboxReadRefusesAnEarlierEpoch(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("clear --confirm sprint")
	if code, _, errs := ta.do("inbox --read --at-epoch 0"); code != 2 || !strings.Contains(errs, "give one of them") {
		t.Fatalf("inbox --read --at-epoch 0: exit %d %s", code, errs)
	}
}

// epochImage is everything the store holds of an epoch, read as it was: every
// card of the four tables with its place, revision and fields, the inbox with
// its cursor and open judgments, and the fence.
func (ta *testApp) epochImage(e uint64) string {
	ta.t.Helper()
	st, err := ta.a.storeAt(common{redis: "mem:0", actor: "tester"}, int64(e))
	if err != nil {
		ta.t.Fatal(err)
	}
	ctx := context.Background()
	s, err := st.Load(ctx, store.All, nil)
	if err != nil {
		ta.t.Fatal(err)
	}
	var lines []string
	for _, tb := range []*sprint.Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		lines = append(lines, fmt.Sprintf("%s rows %v", tb.Name, tb.Rows()))
		for _, c := range tb.Cards() {
			lines = append(lines, fmt.Sprintf("%s %s %s:%s rev %d %v", tb.Name, c.ID, c.Row, c.Col, c.Rev, c.Fields))
		}
	}
	v, err := st.Inbox(ctx, 0, 0, 10000)
	if err != nil {
		ta.t.Fatal(err)
	}
	lines = append(lines, "cursor "+v.Cursor+" last "+v.Last)
	for _, o := range v.Open {
		lines = append(lines, fmt.Sprintf("open %s review %s", o.Key, o.Note.Review))
	}
	if p := ta.m.AtEpoch(e, true).(*store.Mem).Pending(); p != nil {
		lines = append(lines, "fence "+p.ID)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// C1: after a clear, every verb of the verb table (its example, as help
// prints it) reads and writes only the new epoch: no store call names epoch
// 0, and epoch 0's full image is what it was.
func TestEveryVerbAfterAClearLeavesTheOldEpochAlone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	ta.ok("add --stream s1 --count 9")
	ta.ok("add --stream s2 --count 3")
	ta.deal(4)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report red")
	ta.ok("inbox --read")
	ta.ok("clear --confirm sprint")
	ta.ok("add --stream s1 --count 9")
	ta.ok("add --stream s2 --count 3")
	ta.deal(4)
	ta.ok("take --as m1 --limit 2")
	ta.ok("finish --as m1 s1-2.w1@1 --failed --report red") // the deal takes the streams in turns: m1 holds s1-1 and s1-2
	g := ta.group(sprint.NWorkFailed, "s1")
	img := ta.epochImage(0)
	lines := []string{"inbox --read", "wait " + g.Notes[0] + " --for 1h", "clear --confirm sprint"}
	for _, v := range verbs {
		switch v.name {
		case "run":
			continue // ticks for ever; tick is its one tick
		case "play":
			lines = append(lines, v.example+" --ticks 2")
			continue
		case "teardown":
			// drops every epoch by design; refused, it touches none
			lines = append(lines, "teardown --confirm refused")
			continue
		}
		// an example names epoch 0, the epoch of a fresh sprint; the verb here
		// runs after a clear, where the epoch is the new one (ta.do adds it)
		lines = append(lines, strings.ReplaceAll(v.example, " --epoch 0", ""))
	}
	// the clear last: it reads the epoch it leaves (1), never epoch 0
	slices.SortStableFunc(lines, func(a, b string) int {
		switch {
		case strings.HasPrefix(a, "clear --confirm sprint") == strings.HasPrefix(b, "clear --confirm sprint"):
			return 0
		case strings.HasPrefix(a, "clear --confirm sprint"):
			return 1
		}
		return -1
	})
	for _, line := range lines {
		before := ta.m.Touched(0)
		code, out, errs := ta.do(line)
		if n := ta.m.Touched(0) - before; n != 0 {
			t.Errorf("%s (exit %d) made %d store calls naming epoch 0:\n%s%s", line, code, n, out, errs)
		}
	}
	if got := ta.epochImage(0); got != img {
		t.Fatalf("epoch 0 changed:\n%s\nwas\n%s", got, img)
	}
}

// Every report names its epoch: a worker from before a clear, finishing the
// card it holds, is refused, with no epoch and with its old one, and the new
// epoch's card of the same name is untouched.
func TestAnOldWorkersFinishIsRefusedAfterAClear(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 p1")
	ta.deal(1)
	ta.ok("take --as m1 p1.w1@1")
	ta.ok("clear --confirm sprint")
	ta.ok("add --stream s1 p1")
	ta.deal(1)
	raw := func(line string) (int, string) {
		var out, errb bytes.Buffer
		code := ta.a.run(split(line), &out, &errb)
		return code, out.String() + errb.String()
	}
	if code, out := raw("finish --as m1 p1.w1@1"); code == 0 || !strings.Contains(out, "--epoch") {
		t.Fatalf("a finish naming no epoch: %d %s", code, out)
	}
	if code, out := raw("finish --as m1 p1.w1@1 --epoch 0"); code == 0 || !strings.Contains(out, "cleared") {
		t.Fatalf("a finish of epoch 0: %d %s", code, out)
	}
	if code, out := raw("read --as reader-a --ok p1.r1.reader-a"); code == 0 || !strings.Contains(out, "--epoch") {
		t.Fatalf("a read naming no epoch: %d %s", code, out)
	}
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	if len(q.Cards) != 1 || q.Cards[0].ID != "p1.w1" || q.Cards[0].Col != "ready" {
		t.Fatalf("the new epoch's card: %+v", q.Cards)
	}
}
