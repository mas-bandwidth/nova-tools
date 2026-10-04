package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// An auto sentinel (the owner, 2026-10-04: "I love the auto release on dep land
// sentinel"): add --sentinel --auto is held only until its needs land. It is
// never reached and raises nothing; the tick on which its last need lands
// releases it, as release does, with the log line "released: its needs
// landed", and the cards behind it and the card of another stream that names
// it go to ready in the same plan. Not before: while one need is in review, a
// tick leaves it waiting, and the merge step that lands the last need on a
// RUNNING machine leaves it to the tick. A sentinel without --auto is not
// released by the tick: it is reached and waits for the coordinator.
func TestAnAutoSentinelIsReleasedOnTheTickItsLastNeedLands(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream s1 --count 2 --actor lead")
	out := ta.ok("add --stream s1 --sentinel stop --auto --actor lead")
	require.Contains(t, out, "MOVED sentinel stop -> waiting stream=s1 score=3; auto: the tick releases it when its needs land", "add --auto")
	require.NotContains(t, out, "reached", "an auto sentinel is never reached")
	ta.ok("add --stream s1 b --actor lead")
	ta.ok("add --stream s2 c --needs stop --actor lead")
	ta.ok("add --stream s3 --sentinel gate --needs s1-1 --actor lead")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, int64(1), w.Auto, "where --json: %+v", w)
	require.Equal(t, int64(1), w.Held, "where --json: only the manual sentinel holds back: %+v", w)
	require.Contains(t, w.Summary, " held=1 auto=1 ", "where: %+v", w)

	ta.deal(2)
	ta.ok("take --as m1 s1-1.w1@1 s1-2.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1")
	ta.ok("ask --actor lead")
	ta.ok("read --as reader-a --ok --limit 10")
	ta.ok("read --as reader-b --ok --limit 10")
	ta.ok("accept s1-1 --actor lead")
	ta.ok("merge --stream s1 --batch 1")
	require.Equal(t, sprint.Landed, ta.primary("s1-1").Col)
	ta.ok("start --actor lead")
	out = ta.ok("tick")
	require.NotContains(t, out, "sentinel stop", "a need in review: the tick left it")
	require.Equal(t, sprint.Waiting, ta.primary("stop").Col, "one need still in review")

	ta.ok("merge --stream s1 --batch 1") // the last need's landing, queued for the tick's pump
	require.Equal(t, sprint.Waiting, ta.primary("stop").Col, "released before the tick")
	out = ta.ok("tick")
	require.Contains(t, out, "MOVED drain: s1-2 merging -> landed", "tick")
	require.Contains(t, out, "MOVED resolve: sentinel stop waiting -> landed (released: its needs landed); 2 cards are now ready", "tick")
	stop := ta.primary("stop")
	require.Equal(t, sprint.Landed, stop.Col, "stop: %v", stop.Fields)
	require.Empty(t, stop.F("reached"), "stop: %v", stop.Fields)
	require.Equal(t, sprint.AutoReason, stop.F("release_reason"), "stop: %v", stop.Fields)
	require.Equal(t, sprint.MachineActor, stop.F("released_by"), "stop: %v", stop.Fields)
	for _, id := range []string{"b", "c"} {
		require.NotEqual(t, sprint.Waiting, ta.primary(id).Col, "%s still waits after the release", id)
	}
	require.Contains(t, ta.ok("log --card stop"), "sentinel stop landed, released: its needs landed: 2 cards are now ready", "the log")
	gate := ta.primary("gate")
	require.Equal(t, sprint.Waiting, gate.Col, "a sentinel without --auto stays the coordinator's")
	require.NotEmpty(t, gate.F("reached"), "gate: %v", gate.Fields)
	ta.group(sprint.NSentinelReached, "s3")
	ta.clean()
}

// sentinel set <id> --auto converts a waiting sentinel, held or not: one whose
// needs have already landed (a held stop at the head of a stream: nothing
// before it) is released at once in the step, as release does, and what waits
// behind it goes to ready; one whose needs have not landed loses its hold and
// waits for the tick. Refused: a card that is no sentinel, one already auto,
// an actor who is not the coordinator; add --auto with --held, or with no
// sentinel.
func TestSentinelSetAutoReleasesAHeldSentinelAtOnceWhenItsNeedsLanded(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream w --sentinel gate --held --actor lead")
	ta.ok("add --stream w a b --actor lead")
	ta.ok("add --stream v --sentinel later --held --needs a --actor lead")
	refused := func(line, want string) {
		t.Helper()
		code, out, errs := ta.do(line)
		require.NotEqual(t, 0, code, "%s: %s%s", line, out, errs)
		require.Contains(t, errs, want, "%s: %s%s", line, out, errs)
	}
	refused("add --stream w --sentinel x --auto --held --actor lead", "--auto or --held, not both")
	refused("add --stream w y --auto --actor lead", "--auto goes with --sentinel or --sentinel-every")
	refused("sentinel set a --auto --actor lead", "REFUSED a: not a sentinel")
	refused("sentinel set gate --auto --actor other", "sentinel set is the coordinator's alone: lead, not other")
	refused("sentinel set gate --actor lead", "wants the sentinels it sets and --auto")
	require.Equal(t, sprint.Waiting, ta.primary("gate").Col, "a refusal moved the sentinel")

	out := ta.ok("sentinel set gate later --auto --actor lead")
	require.Contains(t, out, "sentinel gate waiting -> landed (released: its needs landed); 2 cards are now ready", "sentinel set")
	require.Contains(t, out, "sentinel later made auto by lead: the tick releases it when its needs land (it waits for a)", "sentinel set")
	gate := ta.primary("gate")
	require.Equal(t, sprint.Landed, gate.Col, "gate: %v", gate.Fields)
	require.Equal(t, "lead", gate.F("released_by"), "gate: %v", gate.Fields)
	require.Empty(t, gate.F(sprint.FieldHeld), "gate: %v", gate.Fields)
	for _, id := range []string{"a", "b"} {
		require.Equal(t, sprint.Ready, ta.primary(id).Col, "%s behind the released sentinel", id)
	}
	later := ta.primary("later")
	require.Equal(t, sprint.Waiting, later.Col, "later: %v", later.Fields)
	require.True(t, sprint.IsAuto(later), "later: %v", later.Fields)
	require.Empty(t, later.F(sprint.FieldHeld), "later: %v", later.Fields)
	refused("sentinel set later --auto --actor lead", "REFUSED later: is auto already")
	out = ta.ok("handover")
	require.Contains(t, out, "SENTINEL later stream=v auto, the tick releases it when its needs land", "handover")
	ta.clean()
}

// A dropped need of an auto sentinel raises the blocked judgment, as it does
// for any waiting card, and the tick does not release it; the coordinator's
// ack waives the need, and the next tick releases it.
func TestADroppedNeedOfAnAutoSentinelRaisesBlockedNotARelease(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("add --stream s4 d --actor lead")
	ta.ok("add --stream s1 --sentinel stop --auto --needs d --actor lead")
	ta.ok("add --stream s1 b --actor lead")
	ta.ok("drop d --reason 'not wanted' --actor lead")
	ta.ok("start --actor lead")
	out := ta.ok("tick")
	require.NotContains(t, out, "sentinel stop waiting -> landed", "released past a dropped need")
	require.Equal(t, sprint.Waiting, ta.primary("stop").Col, "released past a dropped need")
	require.Equal(t, sprint.Waiting, ta.primary("b").Col, "moved behind a sentinel not released")
	g := ta.group(sprint.NBlocked, "s1")
	require.Equal(t, []string{"stop"}, g.Primaries, "the blocked judgment: %+v", g)
	ta.clean()

	ta.ok("ack " + g.Notes[0] + " --reason 'd is not needed' --actor lead")
	out = ta.ok("tick")
	require.Contains(t, out, "sentinel stop waiting -> landed (released: its needs landed)", "the tick after the ack")
	require.Equal(t, sprint.Landed, ta.primary("stop").Col)
	require.NotEqual(t, sprint.Waiting, ta.primary("b").Col)
	ta.clean()
}
