package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The line the seat gets when 27 cards have sat queued for the default bound
// and the one stopped stream is friends-now, push refused, the lander at land.
const landingLine27 = "no landing for 15 min: 27 queued; 27 in stopped streams (friends-now: push refused); lander at land for 15 min"

func dryLanding() LandingFacts {
	return LandingFacts{
		Now: t0.Add(15 * time.Minute), LastLanding: t0, Queued: 27,
		Stopped:    []StoppedStream{{Name: "friends-now", Reason: "push refused", Queued: 27}},
		LanderStep: "land", LanderSince: t0, Bound: LandingBoundDefault,
	}
}

func notesOfType(p Plan, typ string) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

func queueStopped(w *world, n int) {
	w.t.Helper()
	w.s.Work.SetRows([]string{"friends-now"})
	w.s.Merge.SetRows([]string{"friends-now"})
	w.s.Work.Put(&Card{ID: "landed-1", Row: "friends-now", Col: Landed, Rev: 1, Fields: map[string]string{"landed": stamp(t0)}})
	w.s.Merge.Put(&Card{ID: CtlID("friends-now"), Row: "friends-now", Col: Ctl, Rev: 1,
		Fields: map[string]string{"state": StreamStopped, "cause": "rejected"}})
	for i := 0; i < n; i++ {
		id := "q" + itoa(i)
		w.s.Merge.Put(&Card{ID: id, Row: "friends-now", Col: Queued, Rev: 1, Fields: map[string]string{PrimaryField: id}})
	}
}

// A queue with nothing landed past the bound raises one judgment, the exact line,
// and does not raise it again inside the bound, even after that judgment is closed.
func TestLandingAlarmOneJudgmentNotRepeatedInsideTheBound(t *testing.T) {
	t.Parallel()
	line, ok := LandingLine(dryLanding())
	require.True(t, ok)
	assert.Equal(t, landingLine27, line)

	w := newWorld(t)
	w.s.Now = t0.Add(15 * time.Minute)
	queueStopped(w, 27)
	p, _ := TickDeadlines(w.s, TickReq{})
	got := notesOfType(p, NNoLanding)
	require.Len(t, got, 1)
	assert.Equal(t, landingLine27, got[0].What)
	assert.Equal(t, Judgment, got[0].Kind)
	assert.True(t, got[0].Marked)
	assert.True(t, got[0].StreamLevel)
	assert.Equal(t, []string{"ack", "wait"}, got[0].Decisions)
	w.must(p)

	w.tick(time.Minute)
	p, _ = TickDeadlines(w.s, TickReq{})
	assert.Empty(t, notesOfType(p, NNoLanding), "open already: not a second judgment inside the bound")

	w.s.Open = nil
	p, _ = TickDeadlines(w.s, TickReq{})
	assert.Empty(t, notesOfType(p, NNoLanding), "closed inside the bound: not raised again")

	w.tick(LandingBoundDefault)
	p, _ = TickDeadlines(w.s, TickReq{})
	again := notesOfType(p, NNoLanding)
	require.Len(t, again, 1, "a full bound later the dry queue is one judgment again")
	assert.Contains(t, again[0].What, "friends-now: push refused")
}

// A lander inside a gate does not alarm, however long the queue has been dry.
func TestALanderInsideAGateDoesNotAlarm(t *testing.T) {
	t.Parallel()
	f := dryLanding()
	f.LanderStep = "gate"
	f.InGate = true
	_, ok := LandingLine(f)
	assert.False(t, ok)

	w := newWorld(t)
	w.s.Now = t0.Add(15 * time.Minute)
	queueStopped(w, 27)
	w.s.Open = []Open{{Key: OpenKey("stuck", StreamSubject("friends-now")), Note: Note{
		ID: "stuck", Kind: Judgment, Type: NOpStuck, Stream: "friends-now", StreamLevel: true, At: t0,
		What: "landing stuck at step=gate waiting on the tree gate for 10m0s",
	}}}
	p, _ := TickDeadlines(w.s, TickReq{})
	assert.Empty(t, notesOfType(p, NNoLanding))
}

// A stopped stream is named with its reason. The tick reads a rejected push as
// "push refused"; the line names the stream beside that reason.
func TestAStoppedStreamIsNamedWithItsReason(t *testing.T) {
	t.Parallel()
	f := dryLanding()
	f.Stopped = []StoppedStream{
		{Name: "zeta", Reason: "red", Queued: 2},
		{Name: "friends-now", Reason: "push refused", Queued: 25},
	}
	line, ok := LandingLine(f)
	require.True(t, ok)
	assert.Equal(t, "no landing for 15 min: 27 queued; 27 in stopped streams (friends-now: push refused, zeta: red); lander at land for 15 min", line)

	w := newWorld(t)
	w.s.Now = t0.Add(15 * time.Minute)
	queueStopped(w, 3)
	p, _ := TickDeadlines(w.s, TickReq{})
	got := notesOfType(p, NNoLanding)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].What, "3 in stopped streams (friends-now: push refused)")
}

// The seat inbox collapses same-kind judgments on one subject to one line with a
// count, lists overdue first, and opens with the overdue line.
func TestSeatInboxCollapsesSameKindAndLeadsWithOverdue(t *testing.T) {
	t.Parallel()
	old := t0.Add(-DeadlineJudgment - time.Minute)
	open := []Open{
		{Key: "a|p1", Note: Note{ID: "a", Kind: Judgment, Type: NWorkLate, At: old, Primaries: []string{"p1"}}},
		{Key: "b|p1", Note: Note{ID: "b", Kind: Judgment, Type: NWorkLate, At: old, Primaries: []string{"p1"}}},
		{Key: "c|p1", Note: Note{ID: "c", Kind: Judgment, Type: NWorkLate, At: t0, Primaries: []string{"p1"}}},
		{Key: "d|" + StreamSubject(""), Note: Note{ID: "d", Kind: Judgment, Type: NNoLanding, At: old, StreamLevel: true}},
	}
	lead, lines := SeatInbox(open, t0, DeadlineJudgment)
	assert.Equal(t, "3 overdue: a work card is past its deadline 2, no landing 1", lead)
	require.Len(t, lines, 2)
	assert.Equal(t, "3 "+NWorkLate+" p1", lines[0])
	assert.Equal(t, "1 "+NNoLanding+" "+StreamSubject(""), lines[1])

	w := newWorld(t)
	w.s.Now = t0
	w.s.Open = open
	p, _ := TickDeadlines(w.s, TickReq{})
	got := notesOfType(p, NInboxLead)
	require.Len(t, got, 1)
	assert.Equal(t, lead, got[0].What)
	assert.Equal(t, Happened, got[0].Kind)
	assert.Equal(t, "coordinator", got[0].To)
	w.must(p)
	p, _ = TickDeadlines(w.s, TickReq{})
	assert.Empty(t, notesOfType(p, NInboxLead), "the same overdue line is one note")
}

// A stream stopped for a rejected push resumes itself, and the judgment is closed.
// A protected branch, an auth, or a tree gate stays for a mind.
func TestARejectedPushResumesAndClosesTheJudgment(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Merge.SetRows([]string{"friends-now"})
	w.s.Merge.Put(&Card{ID: CtlID("friends-now"), Row: "friends-now", Col: Ctl, Rev: 1,
		Fields: map[string]string{"state": StreamStopped, "cause": "rejected"}})
	w.s.Merge.Put(&Card{ID: "c1", Row: "friends-now", Col: Queued, Rev: 1, Fields: map[string]string{PrimaryField: "c1"}})
	n := Note{ID: "j1", Kind: Judgment, Type: NRejected, Stream: "friends-now", StreamLevel: true, At: t0, What: "[rejected] (fetch first)"}
	w.s.Open = []Open{{Key: OpenKey("j1", StreamSubject("friends-now")), Note: n}}

	answers := RuleAnswers(w.s, TickReq{AnswerRules: true})
	require.Len(t, answers, 1)
	assert.Equal(t, RulePushResume, answers[0].Rule)
	assert.Equal(t, ActResume, answers[0].Act)

	p, _ := TickRuleResume(w.s, TickReq{AnswerRules: true})
	require.Empty(t, p.Refused)
	var closed bool
	for _, u := range p.Units {
		for _, c := range u.Closes {
			if c.Note.ID == "j1" {
				closed = true
			}
		}
	}
	assert.True(t, closed, "the rule's resume closes the judgment, so it is not left overdue")

	for _, what := range []string{"protected branch", "auth failed", "permission denied", "the tree gate failed"} {
		w.s.Open[0].Note.What = what
		answers = RuleAnswers(w.s, TickReq{AnswerRules: true})
		require.Len(t, answers, 1)
		assert.Equal(t, ActLeft, answers[0].Act, what)
	}
}
