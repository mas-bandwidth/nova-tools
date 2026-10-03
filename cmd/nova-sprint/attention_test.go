package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where --json carries one attention object: open judgment subjects plus the
// queued cards of stopped streams (waiting on the coordinator) versus ready
// work plus asked reads (takeable). The values are pinned to literal fixture
// numbers, not re-derived, so a change in the counting breaks the test.

// An acknowledged judgment is not open: OpenJudgmentSubjects counts the open
// (unacked) subjects only, so acking the one blocked judgment drops it to zero
// even though the note stays in the open index.
func TestAttentionCountsOpenJudgmentsNotAcked(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.ok("add --stream s2 b --needs s1-1")
	ta.ok("drop s1-1 --reason obsolete")

	var w whereView
	ta.json("where", &w)
	require.Equal(t, attention{
		OpenJudgmentSubjects: 1,
		WaitingOnCoordinator: 1,
		OnePersonBound:       true,
	}, w.Attention, "one blocked judgment: %+v", w.Attention)

	g := ta.group(sprint.NBlocked, "s2")
	ta.ok("ack " + g.Notes[0] + " --reason fine")

	ta.json("where", &w)
	require.Equal(t, attention{
		OpenJudgmentSubjects: 0,
		Ready:                1,
		Takeable:             1,
		OnePersonBound:       false,
	}, w.Attention, "acked judgment is not open: %+v", w.Attention)
}

// A stopped stream contributes its queued cards, not one per stream: three
// queued cards in one stopped stream must be three, and the merged judgment
// (one open subject) plus those three is four waiting-on-coordinator units.
func TestAttentionSumsQueuedCardsForStoppedStreams(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.deal(100)
	ta.ok("take --as m1 --limit 100")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	var words []string
	for _, c := range q.Cards {
		words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
	ta.ok("ask --limit 100")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --red --suspect s1-1 s1-2 s1-3")

	var w whereView
	ta.json("where", &w)
	require.Equal(t, attention{
		OpenJudgmentSubjects: 1,
		StoppedMergeCards:    3,
		WaitingOnCoordinator: 4,
		OnePersonBound:       true,
	}, w.Attention, "one stopped stream with three queued cards: %+v", w.Attention)

	ta.ok("resume --stream s1 --did fixed")
	ta.json("where", &w)
	require.Equal(t, attention{
		OpenJudgmentSubjects: 0,
		StoppedMergeCards:    0,
		WaitingOnCoordinator: 0,
		OnePersonBound:       false,
	}, w.Attention, "resumed stream has nothing waiting: %+v", w.Attention)
}
