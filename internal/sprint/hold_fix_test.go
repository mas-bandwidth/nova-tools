package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdHead is the head a held attempt pushed. The next attempt carries it.
const holdHead = "0123456789abcdef0123456789abcdef01234567"

func holdCardBrief(id, paths, tier string) string {
	return id + ": the work (s1) tier: " + tier + "\nREPO: owner/repo\nBASE: sprint/s1\nPATHS: " + paths + "\n\nThe task.\n"
}

func holdWorld(t *testing.T, cards ...CardAdd) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: cards}))
	return w
}

func markLand(w *world, mark string) {
	w.t.Helper()
	w.s.StreamCtl("s1").Fields[FieldLandProtected] = mark
}

func finishHold(t *testing.T, w *world, id, report string) {
	t.Helper()
	if w.state(id) == Ready {
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	}
	wc := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	require.NotNil(t, wc, id)
	if wc.Col != Working {
		w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
		wc = w.s.Fleet.Card(wc.ID)
	}
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Failed: true, Head: holdHead, Report: report}))
}

func answerFor(w *world, id string) RuleAnswer {
	for _, a := range RuleAnswers(w.s, TickReq{}) {
		if a.Subject == id {
			return a
		}
	}
	return RuleAnswer{}
}

func TestHoldFixLinesAreAppliedAtFinish(t *testing.T) {
	t.Parallel()
	brief := func(id string) string { return holdCardBrief(id, "internal/sprint/hold_fix.go", "flash") }
	ready := func(t *testing.T, w *world, id string) {
		t.Helper()
		pr := w.s.Work.Card(id)
		require.Equal(t, Ready, pr.Col)
		assert.Empty(t, pr.F("result"))
		assert.Zero(t, pr.Int("failed"))
		assert.Equal(t, "1", pr.F(FieldBriefAttempt))
		assert.Equal(t, holdHead, pr.F("head"))
		assert.Equal(t, PriorityFix, pr.F(FieldPriority))
		assert.Contains(t, pr.F("fix"), "start from head "+holdHead)
		assert.Contains(t, pr.F("brief"), "CARRY: "+id+" attempt 1 head="+holdHead)
		assert.Nil(t, w.s.Work.Card(id+"-t"), "the card keeps its id")
		require.Len(t, w.notesOf(NHoldFix), 1)
		assert.Equal(t, "coordinator", w.notesOf(NHoldFix)[0].To)
		assert.Empty(t, w.notesOf(NWorkFailed))
		assert.Empty(t, w.openOn(id))
		wc := w.s.Fleet.Card(pr.F("work"))
		require.NotNil(t, wc)
		assert.Equal(t, DoneFailed, wc.Col)
		w.clean("applied")
	}

	t.Run("PATHS-PROPOSED on a marked stream widens in place", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		markLand(w, LandProtectedAny)
		finishHold(t, w, "s1-1", "HOLD: the fix is outside PATHS\nPATHS-PROPOSED: internal/sprint/widen.go")
		ready(t, w, "s1-1")
		assert.Contains(t, w.s.Work.Card("s1-1").F("brief"), "PATHS: internal/sprint/hold_fix.go,internal/sprint/widen.go")
		assert.Contains(t, w.notesOf(NHoldFix)[0].What, "PATHS widened in place")
	})

	t.Run("NEEDS of a landed card adds the dependency", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t,
			CardAdd{ID: "s1-1", Brief: brief("s1-1")},
			CardAdd{ID: "s1-2", Brief: brief("s1-2")},
		)
		land(w, "s1-2")
		finishHold(t, w, "s1-1", "HOLD: waiting on the other card\nNEEDS: s1-2")
		ready(t, w, "s1-1")
		assert.Equal(t, "s1-2", w.s.Work.Card("s1-1").F("needs"))
	})

	t.Run("TIER recuts when a friend serves it", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		w.s.Friends = []FriendSeat{{Name: "worker", Status: Up, Tiers: []string{"flash", "pro", "heavy"}}}
		finishHold(t, w, "s1-1", "HOLD: this tier cannot run it\nTIER: pro")
		ready(t, w, "s1-1")
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, "pro", pr.F(FieldTierNow))
		assert.Contains(t, pr.F("brief"), "tier: pro")
		assert.NotContains(t, pr.F("brief"), "tier: flash")
	})

	t.Run("GATE-HOST linux marks the next attempt for a bench", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		finishHold(t, w, "s1-1", "HOLD: this gate needs Linux\nGATE-HOST: linux")
		ready(t, w, "s1-1")
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, "linux", pr.F("gate_host"))
		assert.Contains(t, w.notesOf(NHoldFix)[0].What, "gate host linux")
		next := &Card{ID: WorkCardID("s1-1", 2), Fields: map[string]string{"kind": "work", "primary": "s1-1", "stream": "s1", "attempt": "2"}}
		assert.Equal(t, "linux", PacketOf("", w.s.Epoch, next, pr, nil, nil).GateHost)
	})

	t.Run("PATHS and TIER apply together", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		markLand(w, LandProtectedAny)
		w.s.Friends = []FriendSeat{{Name: "worker", Status: Up, Tiers: []string{"pro"}}}
		finishHold(t, w, "s1-1", "HOLD: both\nPATHS-PROPOSED: internal/sprint/rules.go\nTIER: pro")
		ready(t, w, "s1-1")
		pr := w.s.Work.Card("s1-1")
		assert.Contains(t, pr.F("brief"), "internal/sprint/rules.go")
		assert.Equal(t, "pro", pr.F(FieldTierNow))
	})

	t.Run("NEEDS of a card that has not landed parks in review", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t,
			CardAdd{ID: "s1-1", Brief: brief("s1-1")},
			CardAdd{ID: "s1-2", Brief: brief("s1-2")},
		)
		finishHold(t, w, "s1-1", "HOLD: the other card has not landed\nNEEDS: s1-2")
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, Review, pr.Col)
		assert.Equal(t, "failed", pr.F("result"))
		assert.Equal(t, "1", pr.F("failed"))
		assert.Equal(t, "s1-2@1", pr.F(FieldRuleNeed))
		assert.Empty(t, pr.F("needs"), "an unlanded need is not stored on the card")
		require.Len(t, w.notesOf(NWorkFailed), 1)
		assert.Contains(t, w.notesOf(NWorkFailed)[0].What, "waiting on s1-2 by rule "+RuleHoldNeed)
		assert.NotContains(t, w.notesOf(NWorkFailed)[0].What, holdFixRefused)
		a := answerFor(w, "s1-1")
		assert.Equal(t, RuleHoldNeed, a.Rule)
		assert.Equal(t, ActNeed, a.Act)
		w.clean("parked")
	})

	t.Run("a note with no fix line is unchanged", func(t *testing.T) {
		t.Parallel()
		const report = "HOLD: the gate is red"
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		markLand(w, LandProtectedAny)
		finishHold(t, w, "s1-1", report)
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, Review, pr.Col)
		assert.Equal(t, "failed", pr.F("result"))
		assert.Equal(t, brief("s1-1"), pr.F("brief"))
		require.Len(t, w.notesOf(NWorkFailed), 1)
		assert.Equal(t, report, w.notesOf(NWorkFailed)[0].What)
		assert.Empty(t, w.notesOf(NHoldFix))
		w.clean("unchanged")
	})

	t.Run("a mention that is not its own line is unchanged", func(t *testing.T) {
		t.Parallel()
		const report = "HOLD: the note says PATHS-PROPOSED: internal/sprint/widen.go in passing"
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		markLand(w, LandProtectedAny)
		finishHold(t, w, "s1-1", report)
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, Review, pr.Col)
		assert.Equal(t, brief("s1-1"), pr.F("brief"))
		assert.Equal(t, report, w.notesOf(NWorkFailed)[0].What)
	})

	t.Run("an unmarked stream keeps the paths rule", func(t *testing.T) {
		t.Parallel()
		const report = "HOLD: outside PATHS\nPATHS-PROPOSED: internal/sprint/widen.go"
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		finishHold(t, w, "s1-1", report)
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, Review, pr.Col)
		assert.Equal(t, report, w.notesOf(NWorkFailed)[0].What)
		assert.Equal(t, ActTwin, answerFor(w, "s1-1").Act)
	})

	t.Run("a shared path on a marked stream stays the paths rule", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t,
			CardAdd{ID: "s1-1", Brief: brief("s1-1")},
			CardAdd{ID: "s1-2", Brief: holdCardBrief("s1-2", "internal/sprint/widen.go", "flash")},
		)
		markLand(w, LandProtectedAny)
		finishHold(t, w, "s1-1", "HOLD: shared\nPATHS-PROPOSED: internal/sprint/widen.go")
		require.Equal(t, Review, w.state("s1-1"))
		assert.NotContains(t, w.notesOf(NWorkFailed)[0].What, holdFixRefused)
		assert.Equal(t, ActTwinCmd, answerFor(w, "s1-1").Act)
	})

	t.Run("a land-protected proposal already in review is not twinned", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t, CardAdd{ID: "s1-1", Brief: brief("s1-1")})
		finishHold(t, w, "s1-1", "HOLD: outside PATHS\nPATHS-PROPOSED: internal/sprint/widen.go")
		require.Equal(t, ActTwin, answerFor(w, "s1-1").Act)
		markLand(w, LandProtectedAny)
		a := answerFor(w, "s1-1")
		assert.Equal(t, ActLeft, a.Act)
		assert.Equal(t, RulePaths, a.Rule)
		assert.Nil(t, w.s.Work.Card("s1-1-t"))
	})

	refusals := []struct {
		name, report, want string
		mark               string
		friends            []FriendSeat
		cards              []CardAdd
	}{
		{
			name:   "a path outside the land-protected set",
			mark:   "other/repo",
			report: "HOLD: outside\nPATHS-PROPOSED: internal/sprint/widen.go",
			want:   "outside the stream's land-protected set",
		},
		{
			name:   "a proposal that climbs out",
			mark:   LandProtectedAny,
			report: "HOLD: outside\nPATHS-PROPOSED: /tmp/x.go",
			want:   "climbs out of the repository",
		},
		{
			name:   "an unknown card",
			report: "HOLD: missing\nNEEDS: missing-card",
			want:   "not a card on the table",
		},
		{
			name:    "a tier no friend serves",
			report:  "HOLD: tier\nTIER: heavy",
			want:    "not served by the stream's friends",
			friends: []FriendSeat{{Name: "worker", Status: Up, Tiers: []string{"flash"}}},
		},
		{
			name:    "a tier outside flash pro and heavy",
			report:  "HOLD: tier\nTIER: frontier",
			want:    "not flash, pro or heavy",
			friends: []FriendSeat{{Name: "worker", Status: Up, Tiers: []string{"flash", "pro", "heavy"}}},
		},
		{
			name:   "a card that needs itself",
			report: "HOLD: loop\nNEEDS: s1-1",
			want:   "NEEDS names itself",
		},
		{
			name:   "an unsupported gate host",
			report: "HOLD: gate host\nGATE-HOST: darwin",
			want:   "GATE-HOST darwin is not linux",
		},
		{
			name:    "one line that cannot apply blocks the rest",
			mark:    LandProtectedAny,
			report:  "HOLD: both\nPATHS-PROPOSED: internal/sprint/rules.go\nTIER: heavy",
			want:    "not served by the stream's friends",
			friends: []FriendSeat{{Name: "worker", Status: Up, Tiers: []string{"flash"}}},
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cards := tc.cards
			if cards == nil {
				cards = []CardAdd{{ID: "s1-1", Brief: brief("s1-1")}}
			}
			w := holdWorld(t, cards...)
			if tc.mark != "" {
				markLand(w, tc.mark)
			}
			w.s.Friends = tc.friends
			finishHold(t, w, "s1-1", tc.report)
			pr := w.s.Work.Card("s1-1")
			require.Equal(t, Review, pr.Col, tc.name)
			assert.Equal(t, "failed", pr.F("result"))
			assert.Equal(t, brief("s1-1"), pr.F("brief"), "an unapplied line does not edit the brief")
			assert.Empty(t, pr.F(FieldTierNow))
			require.Len(t, w.notesOf(NWorkFailed), 1)
			assert.Contains(t, w.notesOf(NWorkFailed)[0].What, holdFixRefused)
			assert.Contains(t, w.notesOf(NWorkFailed)[0].What, tc.want)
			assert.Equal(t, ActLeft, answerFor(w, "s1-1").Act)
			w.clean("refused")
		})
	}

	t.Run("a need that cycles stands", func(t *testing.T) {
		t.Parallel()
		w := holdWorld(t,
			CardAdd{ID: "s1-1", Brief: brief("s1-1")},
			CardAdd{ID: "s1-2", Brief: brief("s1-2"), Needs: []string{"s1-1"}},
		)
		require.Equal(t, Waiting, w.state("s1-2"))
		finishHold(t, w, "s1-1", "HOLD: loop\nNEEDS: s1-2")
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, Review, pr.Col)
		assert.Empty(t, pr.F("needs"))
		require.Len(t, w.notesOf(NWorkFailed), 1)
		assert.Contains(t, w.notesOf(NWorkFailed)[0].What, "which needs s1-1")
		assert.Contains(t, w.notesOf(NWorkFailed)[0].What, holdFixRefused)
		w.clean("cycle")
	})
}

// Friend sync collapses a REPORT.md into a prefixed one-line finish. The real
// collected shape must still carry a structured fix through Finish.
func TestACollectedFriendHoldAppliesItsFixAtFinish(t *testing.T) {
	t.Parallel()
	w := holdWorld(t, CardAdd{ID: "s1-1", Brief: holdCardBrief("s1-1", "internal/sprint/hold_fix.go", "flash")})
	markLand(w, LandProtectedAny)
	raw := "Verdict: HOLD\nHead: " + holdHead + "\n\nThe brief needs another path.\nPATHS-PROPOSED: internal/sprint/widen.go\n"
	got := collectReport(CollectCard{Friend: "amy", Card: "s1-1", Job: "s1-1.w1"}, "amy", raw)
	require.True(t, got.Failed)
	assert.Contains(t, got.Report, "; PATHS-PROPOSED: internal/sprint/widen.go")
	finishHold(t, w, "s1-1", got.Report)
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, Ready, pr.Col)
	assert.Contains(t, pr.F("brief"), "internal/sprint/widen.go")
	assert.Equal(t, PriorityFix, pr.F(FieldPriority))
}

func TestACollectedFriendHoldKeepsLaterFixLines(t *testing.T) {
	t.Parallel()
	w := holdWorld(t, CardAdd{ID: "s1-1", Brief: holdCardBrief("s1-1", "internal/sprint/hold_fix.go", "flash")})
	raw := "Verdict: HOLD\nHead: " + holdHead + "\n\nThe gate needs Linux; TIER: pro was only discussed.\n\nTIER: heavy\nGATE-HOST: linux\n"
	got := collectReport(CollectCard{Friend: "amy", Card: "s1-1", Job: "s1-1.w1"}, "amy", raw)
	require.True(t, got.Failed)
	assert.Contains(t, got.Report, "; TIER: heavy; GATE-HOST: linux")
	assert.NotContains(t, got.Report, "; TIER: pro")
	finishHold(t, w, "s1-1", got.Report)
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, Ready, pr.Col)
	assert.Contains(t, pr.F("brief"), "tier: heavy")
	assert.Contains(t, pr.F("brief"), "GATE-HOST: linux")
}

func TestHoldFixProseMentionsAreNotInstructions(t *testing.T) {
	t.Parallel()
	for _, report := range []string{
		"HOLD: TIER: pro was considered but is not the fix",
		"HOLD: we discussed NEEDS: s1-2 without requesting it",
		"HOLD: GATE-HOST: linux appeared in the old brief",
		"HOLD: PATHS-PROPOSED: internal/sprint/widen.go was an example",
		"HOLD: no instruction; TIER: pro was considered but is not the fix",
		"TIER: pro was considered but is not the fix",
	} {
		_, ok := parseHoldFix(report)
		assert.False(t, ok, report)
	}
}
