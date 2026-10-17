package sprint

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRelease is the release check's snapshot as a test builds it: a clock and a log.
type fakeRelease struct {
	now        time.Time
	lines      []Line
	dealtMax   time.Duration
	acceptance AcceptanceSnapshot
}

func (f fakeRelease) Now() time.Time                 { return f.now }
func (f fakeRelease) Log() []Line                    { return f.lines }
func (f fakeRelease) DealtMax() time.Duration        { return f.dealtMax }
func (f fakeRelease) Acceptance() AcceptanceSnapshot { return f.acceptance }
func fleetMove(at time.Time, card, from, to string, set map[string]string) Line {
	return Line{Kind: LineMove, At: at, Table: Fleet, Card: card, Stream: "s1", From: from, To: to, Set: set}
}

// hr is the clock t0 plus h hours.
func hr(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func relFacts(now time.Time, lines ...Line) fakeRelease {
	return fakeRelease{now: now, lines: lines, dealtMax: DealtMaxDefault}
}

func TestReleaseCheckFailsWhileAFriendWasStuckInTheLastFourHours(t *testing.T) {
	t.Parallel()
	amy := FriendRow("amy")

	t.Run("a friend's working card past its deadline fails, naming her and the time", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h, now hour 3: late since hour 2, inside the last four hours
		f := relFacts(hr(3), fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Equal(t, CheckNoStuckFriend, r.Name)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, "s1-1.w1")
		assert.Contains(t, r.Evidence, stamp(hr(2)), "the moment she became stuck")
		assert.Contains(t, r.Evidence, "nova-sprint card s1-1", "what to look at")
		assert.Equal(t, "RELEASE CHECK no-stuck-friend fail "+r.Evidence, r.Line())
	})

	t.Run("a stuck spell that ended before the window is not a failure", func(t *testing.T) {
		t.Parallel()
		// late from hour 2 to hour 3 (finished), now hour 9: the window opens at hour 5
		f := relFacts(hr(9),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(hr(3), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
		assert.Equal(t, "RELEASE CHECK no-stuck-friend ok "+r.Evidence, r.Line())
	})

	t.Run("a spell that ended inside the window fails: stuck at any moment", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(6),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(hr(3), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, stamp(hr(2)), "the spell began at hour 2, inside the window opened at hour 2")
	})

	t.Run("a card finished inside its deadline is never stuck", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(3),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", nil),
			fleetMove(hr(1), "s1-1.w1", amy+":working", amy+":done_ok", nil))
		assert.True(t, NoStuckFriend(f).OK)
	})

	t.Run("her own longer deadline holds", func(t *testing.T) {
		t.Parallel()
		set := map[string]string{"friend_deadline": strconv.Itoa(5 * 3600)}
		f := relFacts(hr(4), fleetMove(hr(0), "s1-1.w1", "", amy+":working", set))
		assert.True(t, NoStuckFriend(f).OK, "late only from hour 5")
		f.now = hr(6)
		assert.False(t, NoStuckFriend(f).OK)
	})

	t.Run("a card dealt and never taken past the dealt bound fails", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(7), fleetMove(hr(0), "s1-2.w1", "", amy+":ready", nil))
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "dealt, never taken")
		assert.Contains(t, r.Evidence, stamp(hr(6)), "dealt at 0 and the dealt bound is 6h")
	})

	t.Run("a card taken back from her stops the clock", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(9),
			fleetMove(hr(0), "s1-2.w1", "", amy+":ready", nil),
			fleetMove(hr(1), "s1-2.w1", amy+":ready", "m1:ready", nil))
		assert.True(t, NoStuckFriend(f).OK, "it left her row")
	})

	t.Run("a card taken back inside its deadline resets to untaken bound and does not fail", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, withdrawn at hour 1 (untaken bound is 6h from hour 1 = hour 7), now hour 3
		f := relFacts(hr(3),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(1), "s1-1.w1", amy+":working", amy+":withdrawn", map[string]string{FieldTakenBack: "taken back", "untaken_since": stamp(hr(1))}),
		)
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
		assert.Equal(t, "RELEASE CHECK no-stuck-friend ok "+r.Evidence, r.Line())

		// but at hour 8, it is late past the untaken bound (hour 7)
		f.now = hr(8)
		r8 := NoStuckFriend(f)
		assert.False(t, r8.OK)
		assert.Contains(t, r8.Evidence, "dealt, never taken")
		assert.Contains(t, r8.Evidence, stamp(hr(7)))
	})

	t.Run("a card taken back after its deadline was stuck and fails inside the window", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h, withdrawn at hour 3 (stuck from hour 2 to hour 3), now hour 4
		f := relFacts(hr(4),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(3), "s1-1.w1", amy+":working", amy+":withdrawn", map[string]string{FieldTakenBack: "taken back", "untaken_since": stamp(hr(3))}),
		)
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, stamp(hr(2)))
	})

	t.Run("a card whose friend deadline is increased historically does not fail", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0 with default 2h deadline; at hour 1 friend_deadline updated to 5h; checked at hour 4
		f := relFacts(hr(4),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(1), "s1-1.w1", amy+":working", amy+":working", map[string]string{FieldFriendDeadline: strconv.Itoa(5 * 3600)}),
		)
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)

		// checked at hour 6: late from hour 5
		f.now = hr(6)
		r6 := NoStuckFriend(f)
		assert.False(t, r6.OK)
		assert.Contains(t, r6.Evidence, stamp(hr(5)))
	})

	t.Run("a card stuck before a clear inside the window fails", func(t *testing.T) {
		t.Parallel()
		// taken at hour 0, deadline 2h; clear runs at hour 2.5; checked at hour 3
		clearLine := Line{Kind: LineMove, At: hr(2.5), Verb: "clear"}
		f := relFacts(hr(3),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			clearLine,
		)
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "friend amy")
		assert.Contains(t, r.Evidence, stamp(hr(2)))
	})

	t.Run("a card not yet stuck before a clear does not fail", func(t *testing.T) {
		t.Parallel()
		// taken at hour 2.2, deadline 2h; clear runs at hour 2.5; checked at hour 3
		clearLine := Line{Kind: LineMove, At: hr(2.5), Verb: "clear"}
		f := relFacts(hr(3),
			fleetMove(hr(2.2), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(2.2))}),
			clearLine,
		)
		r := NoStuckFriend(f)
		assert.True(t, r.OK, r.Evidence)
	})

	t.Run("a machine's late card is not a friend's", func(t *testing.T) {
		t.Parallel()
		f := relFacts(hr(9), fleetMove(hr(0), "s1-1.w1", "", "m1:working", nil))
		assert.True(t, NoStuckFriend(f).OK)
	})

	t.Run("no log is no stuck friend", func(t *testing.T) {
		t.Parallel()
		assert.True(t, NoStuckFriend(relFacts(hr(9))).OK)
	})

	t.Run("consecutive late periods for a card merge across intervening moves of other cards", func(t *testing.T) {
		t.Parallel()
		bob := FriendRow("bob")
		f := relFacts(hr(4),
			fleetMove(hr(0), "s1-1.w1", "", amy+":working", map[string]string{"first_taken": stamp(hr(0))}),
			fleetMove(hr(1), "s1-2.w1", "", bob+":working", map[string]string{"first_taken": stamp(hr(1))}),
			fleetMove(hr(2.5), "s1-1.w1", amy+":working", amy+":working", map[string]string{"first_taken": stamp(hr(0)), "note": "update"}),
			fleetMove(hr(3.5), "s1-2.w1", bob+":working", bob+":working", map[string]string{"first_taken": stamp(hr(1)), "note": "update"}),
		)
		r := NoStuckFriend(f)
		assert.False(t, r.OK)
		spans, _ := friendLateSpans(f.Log(), f.Now(), f.DealtMax())
		assert.Len(t, spans, 2, "one merged span per card, not fragmented")
	})
}

func TestTheReleaseReportSaysOKOrNotReadyByTheChecksRun(t *testing.T) {
	t.Parallel()
	good := relFacts(hr(1))
	rep, err := RunReleaseChecks(good, []string{CheckNoStuckFriend})
	require.NoError(t, err)
	assert.Equal(t, "RELEASE OK checks=1", rep.Summary)
	assert.Equal(t, 0, rep.ExitCode())
	assert.Equal(t, 1, len(rep.Results))

	bad := relFacts(hr(3), fleetMove(hr(0), "s1-1.w1", "", FriendRow("amy")+":working", nil))
	rep, err = RunReleaseChecks(bad, []string{CheckNoStuckFriend})
	require.NoError(t, err)
	assert.Equal(t, "RELEASE NOT READY failed=1", rep.Summary)
	assert.Equal(t, 1, rep.ExitCode())
	assert.False(t, rep.Ready)

	_, err = RunReleaseChecks(good, []string{"no-such-check"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-check")
	assert.Contains(t, err.Error(), CheckNoStuckFriend, "the refusal names the checks there are")
}

func TestEveryReleaseCheckStatesItsBar(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, c := range ReleaseChecks {
		assert.NotEmpty(t, c.Name)
		assert.NotEmpty(t, c.Bar, c.Name)
		assert.False(t, seen[c.Name], "%s twice", c.Name)
		seen[c.Name] = true
	}
}

func TestReleaseStreamsFilterKeepsTheStreamsTheGlobNames(t *testing.T) {
	t.Parallel()
	a := fleetMove(hr(0), "a-1.w1", "", FriendRow("amy")+":working", nil)
	a.Stream = "alpha"
	b := fleetMove(hr(0), "b-1.w1", "", FriendRow("bob")+":working", nil)
	b.Stream = "beta"
	got, err := ReleaseStreamLines([]Line{a, b}, "al*")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a-1.w1", got[0].Card)
	all, err := ReleaseStreamLines([]Line{a, b}, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
	_, err = ReleaseStreamLines(nil, "[")
	require.Error(t, err)
}

func goodAcceptance() AcceptanceSnapshot {
	w, rd, m := NewTable(Work), NewTable(Readers), NewTable(Merge)
	w.SetRows([]string{"s1"})
	w.Put(&Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{"kind": "primary", "stream": "s1", "tier": "flash", "head": "final-head", "brief": "RESULT: s1-1 tier: flash\nPATHS: internal/sprint/releasecheck*.go\n"}})
	w.Put(&Card{ID: "s1-2", Row: "s1", Fields: map[string]string{"kind": "primary", "stream": "s1", "outcome": "dropped", "reason": "obsolete"}})
	rd.Put(&Card{ID: "s1-1.r1.alice", Row: "alice", Col: OK, Fields: map[string]string{"primary": "s1-1", "head": "final-head", "reader": "alice"}})
	landing := "landing-sha"
	unit := []CommandEvidence{{Revision: landing, Command: "go test", Result: "exit=0", Packages: []string{"internal/sprint", "internal/docs", "internal/ci"}}}
	functional := []CommandEvidence{{Revision: landing, Command: "go test -tags functional", Result: "exit=0", Packages: []string{"internal/sprint", "internal/docs", "internal/ci"}}}
	manifest := CommandEvidence{Revision: landing, Command: "go list ./...", Result: "exit=0", Packages: []string{"internal/sprint", "internal/docs", "internal/ci"}}
	cmd := func(name string) CommandEvidence {
		return CommandEvidence{Revision: landing, Command: name, Result: "exit=0"}
	}
	return AcceptanceSnapshot{
		Work: w, Readers: rd, Merge: m, Streams: []string{"s1"}, Coordinator: "coordinator", LastLandings: map[string]string{"s1": landing},
		TreeGates: map[string]TreeGateEvidence{"s1": {Revision: landing, Manifest: manifest, Unit: unit, Functional: functional}},
		Docs: map[string]DocsEvidence{"s1": {Revision: landing, Links: cmd("nova-check links"), NoCode: cmd("nova-check nocode"), Reviews: []DocumentReviewEvidence{
			{Stream: "s1", Revision: landing, Reviewer: "reviewer-a", Scope: "present-tense"}, {Stream: "s1", Revision: landing, Reviewer: "reviewer-a", Scope: "no-people-or-machine-names"}, {Stream: "s1", Revision: landing, Reviewer: "reviewer-a", Scope: "help-matches-code"},
		}}},
		Dev: map[string]DevEvidence{"s1": {Revision: "dev-head", Ancestors: []string{landing}}},
	}
}

func TestReleaseCheckRunsTheAcceptanceSentinelsSixChecks(t *testing.T) {
	t.Parallel()
	base := goodAcceptance()
	cases := []struct {
		name    string
		run     func(ReleaseFacts) ReleaseResult
		breakIt func(*AcceptanceSnapshot)
	}{
		{CheckStreamTerminal, CheckStreamTerminalResult, func(s *AcceptanceSnapshot) { s.Work.Card("s1-1").Col = Ready }},
		{CheckStreamTreeGate, CheckStreamTreeGateResult, func(s *AcceptanceSnapshot) { x := s.TreeGates["s1"]; x.Revision = "stale"; s.TreeGates["s1"] = x }},
		{CheckStreamReads, CheckStreamReadsResult, func(s *AcceptanceSnapshot) { s.Readers.Drop("s1-1.r1.alice") }},
		{CheckStreamDocs, CheckStreamDocsResult, func(s *AcceptanceSnapshot) { x := s.Docs["s1"]; x.NoCode.Result = "exit=1"; s.Docs["s1"] = x }},
		{CheckStreamDev, CheckStreamDevResult, func(s *AcceptanceSnapshot) {
			x := s.Dev["s1"]
			x.Ancestors = nil
			x.QueuedPromotions = nil
			s.Dev["s1"] = x
		}},
		{CheckStreamJudgments, CheckStreamJudgmentsResult, func(s *AcceptanceSnapshot) {
			s.Open = []Open{{Key: "j1|stream:s1", Note: Note{ID: "j1", Stream: "s1", StreamLevel: true}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			green := fakeRelease{acceptance: base}
			r := tc.run(green)
			require.True(t, r.OK, r.Evidence)
			tc.breakIt(&base)
			red := tc.run(fakeRelease{acceptance: base})
			require.False(t, red.OK)
			require.NotEmpty(t, red.Evidence)
			require.Contains(t, red.Line(), "RELEASE CHECK "+tc.name+" fail ")
		})
		base = goodAcceptance()
	}
	checks := ReleaseChecks[1:]
	require.Len(t, checks, 6)
	for _, c := range checks {
		assert.NotNil(t, c.Run)
		assert.NotEmpty(t, c.Bar)
	}
}
