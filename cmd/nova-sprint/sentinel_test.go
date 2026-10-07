package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H7: through the command: init names the coordinator, where --json carries it
// and the where frame does not, release is refused for another actor and
// without a reason, and card of a sentinel lists what it needs and who needs it.
func TestReleaseIsTheCoordinators(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, "lead", w.Coordinator, "where --json: %+v", w)
	require.NotContains(t, ta.ok("where"), "coordinator:", "where shows the coordinator line")
	ta.ok("add --stream s1 --count 1 --one --actor lead --brief-file " + proBriefFile(t))
	out := ta.ok("add --stream s1 --sentinel stop --actor lead")
	require.Contains(t, out, "MOVED sentinel stop -> waiting stream=s1", "add --sentinel")
	ta.ok("add --stream s2 b --one --needs stop --actor lead --brief-file " + proBriefFile(t))
	out = ta.ok("card --fields stop")
	require.Contains(t, out, "NEEDS s1-1 ready\n", "card stop")
	require.Contains(t, out, "NEEDED-BY b\n", "card stop")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.cutReads()
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
	ta.cutReads()
	ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
	ta.ok("accept s1-1 --actor lead")
	ta.ok("merge --stream s1")
	g := ta.group(sprint.NSentinelReached, "s1")
	code, _, errs := ta.do("release stop --reason 'looked' --actor someone")
	require.Equal(t, 2, code, "another actor: %d %s", code, errs)
	require.Contains(t, errs, "release is the coordinator's alone: lead, not someone", "another actor: %d %s", code, errs)
	code, _, errs = ta.do("release stop --actor lead")
	require.Equal(t, 1, code, "no reason: %d %s", code, errs)
	require.Contains(t, errs, "release wants --reason", "no reason: %d %s", code, errs)
	out = ta.ok("release stop --reason 'the layer is read and green' --actor lead --answers " + g.ID)
	require.Contains(t, out, "sentinel stop waiting -> landed (released by lead); 1 cards are now ready", "release")
	require.Contains(t, out, "b waiting -> ready", "release")
	ta.clean()
}

// sentinel set <id> --needs a,b re-points a sentinel's needs in one step (docs/SPEC-SPRINT.md
// section 16): on 2026-10-04 the cards a release sentinel waited on were deferred, and the
// sentinel could only be dropped and added again, which lost its place, its log and its id.
// A sentinel needing two cards, with two others dropped beside them, is set to the one
// left: its id, stream, score and log stay, and one log line names the needs before and
// after; it is released when that card lands. (A card a waiting card needs is refused by
// drop without --cascade, docs/SPEC-SPRINT.md section 11, so the sentinel does not wait
// on the dropped ones; the answer of a blocked judgment by the set is pinned at the
// store.) A need that is no card on the table is refused naming every one, and nothing
// changes; so is an id that is no sentinel, a cycle, and --needs "".
func TestSentinelSetNeedsRepointsInPlace(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 4 --brief-file " + proBriefFile(t))
	ta.ok("add --stream rel --sentinel v1 --needs s1-3,s1-4")
	ta.ok("add --stream rel r1 --one --brief-file " + proBriefFile(t))
	before := ta.primary("v1")
	ta.ok("drop s1-1 s1-2 --reason 'deferred to the next release'")
	require.False(t, hasGroup(ta.inboxGroups(), sprint.NBlocked), "the drop blocks nothing: no card waits on the dropped ones")
	logBefore := ta.ok("log --card v1")

	// refused, naming every need not on the table at once; nothing changes
	code, _, errs := ta.do("sentinel set v1 --needs s1-3,nosuch,s1-1")
	require.Equal(t, 1, code, "unknown needs: %s", errs)
	require.Contains(t, errs, "not a card on the table: nosuch (no card), s1-1 (off the table, dropped)", "unknown needs: %s", errs)
	require.Equal(t, "s1-3,s1-4", ta.primary("v1").F("needs"), "a refused set changed the needs")
	require.Equal(t, logBefore, ta.ok("log --card v1"), "a refused set wrote the log")
	code, _, errs = ta.do("sentinel set s1-3 --needs s1-1")
	require.Equal(t, 1, code, "not a sentinel: %s", errs)
	require.Contains(t, errs, "s1-3 is no sentinel", "not a sentinel: %s", errs)
	code, _, errs = ta.do("sentinel set nosuch --needs s1-3")
	require.Equal(t, 1, code, "no card: %s", errs)
	require.Contains(t, errs, "nosuch is no sentinel on the table", "no card: %s", errs)
	code, _, errs = ta.do("sentinel set v1 --needs r1")
	require.Equal(t, 1, code, "a cycle: %s", errs)
	require.Contains(t, errs, "cycle", "a cycle: %s", errs)
	code, _, errs = ta.do("sentinel set v1 --needs ''")
	require.Equal(t, 2, code, "--needs empty: %s", errs)
	require.Contains(t, errs, "released, not emptied", "--needs empty: %s", errs)
	require.Equal(t, "s1-3,s1-4", ta.primary("v1").F("needs"), "a refused set changed the needs")

	out := ta.ok("sentinel set v1 --needs s1-3")
	require.Contains(t, out, "sentinel v1 needs s1-3,s1-4 -> s1-3", "sentinel set")
	after := ta.primary("v1")
	require.Equal(t, "s1-3", after.F("needs"))
	require.Equal(t, before.Row, after.Row, "the stream")
	require.Equal(t, before.Score, after.Score, "the place in line")
	require.Equal(t, sprint.Waiting, after.Col)
	logAfter := ta.ok("log --card v1")
	kept, _, _ := strings.Cut(logBefore, "LOG OK")
	require.True(t, strings.HasPrefix(logAfter, kept), "the log is kept:\n%s\n---\n%s", logBefore, logAfter)
	added, _, _ := strings.Cut(strings.TrimPrefix(logAfter, kept), "LOG OK")
	require.Contains(t, added, "v1 changed by coordinator: needs=s1-3, needs_set=s1-3,s1-4 -> s1-3", "one log line with the needs before and after:\n%s", added)
	require.False(t, hasGroup(ta.inboxGroups(), sprint.NBlocked), "the set opens no blocked judgment")
	require.Equal(t, sprint.Waiting, ta.primary("r1").Col, "r1 still waits behind the sentinel")

	// the one need left lands: the sentinel is reached and released
	ta.deal(1)
	ta.ok("take --as m1 s1-3.w1@1")
	ta.ok("finish --as m1 s1-3.w1@1")
	ta.cutReads()
	ta.ok("read --as reader-a --ok s1-3.r1.reader-a")
	ta.cutReads() // the second read, the first ok
	ta.ok("read --as reader-b --ok s1-3.r1.reader-b")
	ta.ok("accept s1-3")
	ta.ok("merge --stream s1")
	require.NotEmpty(t, ta.primary("v1").F("reached"), "v1 is reached when s1-3 lands")
	out = ta.ok("release v1 --reason 'the release is read and green'")
	require.Contains(t, out, "sentinel v1 waiting -> landed", "release")
	require.Contains(t, out, "r1 waiting -> ready", "release")
	ta.clean()
}
