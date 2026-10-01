package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A launch refused at staging is the member's failure, never the card's (tla/CardContract.tla,
// StageRefused and Restage), on the mem twin: the member's failed finish whose report begins
// `staging refused` withdraws the card for the next deal, which places it on a member that
// has not refused it, spending none of the card's redeal bound.

// stagingReason is the stage's own reason, as the fleet store saw it on 2026-10-01.
const stagingReason = "no bench mirror for https://example.com/mas-bandwidth/quack.git: a card may not clone directly from github without a bench mirror"

// stagingLine is a member's report of a launch refused at staging.
const stagingLine = cardhdr.EndStaging + ": " + stagingReason + "; no child ran"

// fourMembers is the route harness with m3 and m4 up and beating beside m1 and m2.
func fourMembers(t *testing.T) *harness {
	h := routeHarness(t, route("pro-a", "pro", 1))
	h.mu.Lock()
	h.live = append(h.live, "m3", "m4")
	h.mu.Unlock()
	h.beat()
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m4"}))
	return h
}

// stagingNotes is the inbox's staging-refusal notes' words.
func (h *harness) stagingNotes() []string {
	h.t.Helper()
	notes, _, err := h.st.B.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []string
	for _, x := range notes {
		if x.Type == sprint.NStagingRefused {
			out = append(out, x.What)
		}
	}
	return out
}

// A staging refusal redeals the card to another member at the same attempt, spends none of
// its redeal bound, keeps one ATTEMPT record ending `staging refused`, opens no failed-work
// judgment, and the inbox names the member and the reason.
func TestAStagingRefusalRedealsTheCardToAnotherMemberAndSpendsNoBound(t *testing.T) {
	t.Parallel()
	h := fourMembers(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	w := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Ready, w.Col)
	first := w.Row

	h.failTake("s1-1.w1", stagingLine)
	h.machine()
	w = h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Ready, w.Col, "dealt again by the tick")
	assert.NotEqual(t, first, w.Row, "dealt to another member")
	assert.Equal(t, "1", w.F("attempt"), "the same attempt")
	assert.Equal(t, 0, w.Int("redeals"), "the card's redeal bound is not spent")
	assert.Equal(t, 0, h.snap().Work.Card("s1-1").Int("failed"))
	assert.Empty(t, h.openOf(sprint.NWorkFailed), "no failed-work judgment")
	assert.Zero(t, h.notesOf(sprint.NWorkFailed))
	lines := sprint.AttemptLines(w)
	require.Len(t, lines, 2, "one ATTEMPT record for the refusal, then the card's own")
	refused := lines[:1]
	assert.Contains(t, refused[0], "ATTEMPT 1 card=s1-1.w1")
	assert.Contains(t, refused[0], "member="+first)
	assert.Contains(t, refused[0], "end=staging refused: "+stagingReason)
	assert.Equal(t, []string{"staging refused on " + first + ": " + stagingReason + "; no child ran"}, h.stagingNotes())
	h.clean("redealt after a staging refusal")
}

// Three members refuse the card at staging, the fourth runs it: the attempt lands ok, its
// redeal bound unspent, with three refusal records, each on its own member.
func TestThreeStagingRefusalsThenAnOkOnTheFourthLandsTheAttempt(t *testing.T) {
	t.Parallel()
	h := fourMembers(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	seen := map[string]bool{}
	for i := 1; i <= 3; i++ {
		w := h.snap().Fleet.Card("s1-1.w1")
		require.Equal(t, sprint.Ready, w.Col, "refusal %d", i)
		require.False(t, seen[w.Row], "refusal %d: %s refused it already", i, w.Row)
		seen[w.Row] = true
		h.failTake("s1-1.w1", stagingLine)
		h.machine()
	}
	w := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Ready, w.Col)
	require.False(t, seen[w.Row], "the fourth member is one that has not refused it")
	gens := map[string]int{w.ID: w.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: w.Row, Sel: sprint.Sel{IDs: []string{w.ID}}, Gens: gens, Who: w.Row}))
	h.must(FinishStep(sprint.FinishReq{As: w.Row, Sel: sprint.Sel{IDs: []string{w.ID}}, Gens: gens, Head: "abc123", Branch: "work/s1-1", Report: "ok", Who: w.Row}))
	w = h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.DoneOK, w.Col, "the attempt lands ok")
	assert.Equal(t, 0, w.Int("redeals"))
	assert.Len(t, sprint.AttemptLines(w), 4, "three refusals and the card's own")
	assert.Len(t, h.stagingNotes(), 3)
	assert.Empty(t, h.openOf(sprint.NBound))
	h.clean("landed after three staging refusals")
}

// Every member up refuses the card at staging: the coordinator sees one judgment naming the
// members and the reason, and the card is not dealt again.
func TestEveryMemberUpRefusingAtStagingIsOneJudgment(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro", 1))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	for i := 1; i <= 2; i++ {
		h.failTake("s1-1.w1", stagingLine)
		h.machine()
	}
	h.machine()
	w := h.snap().Fleet.Card("s1-1.w1")
	assert.Equal(t, sprint.Withdrawn, w.Col, "not dealt again")
	assert.Equal(t, 0, w.Int("redeals"))
	open := h.openOf(sprint.NBound)
	require.Len(t, open, 1, "one judgment")
	for _, want := range []string{"m1", "m2", stagingReason} {
		assert.Contains(t, open[0].Note.What, want)
	}
	assert.Empty(t, h.openOf(sprint.NWorkFailed))
	h.clean("refused at staging by every member up")
}

// The level step never moves a card back onto a member that refused it at staging (the
// reader's probe on #5000: two members, four cards, m1 refusing every card; the level moved
// s1-3.w1 back onto m1 every tick, 12 records at gen 37, 24 notes and no end). A member with no
// mirror for a repo refuses every card of it, so this is the fleet's case, not a corner. Each
// member refuses a card at most once: one record and one note per member per card.
func TestTheLevelNeverMovesACardBackOntoItsRefuser(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro", 1))
	h.addReady("s1", 4, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	for round := 0; round < 12; round++ {
		for _, c := range h.snap().Fleet.Cell("m1", sprint.Ready) {
			h.failTake(c.ID, stagingLine)
		}
		h.machine()
	}
	s := h.snap()
	for _, c := range s.Fleet.Column(sprint.Ready, sprint.Working, sprint.Withdrawn) {
		takes, _ := sprint.StagingTakes(c)
		per := map[string]int{}
		for _, x := range takes {
			per[x.Member]++
		}
		for m, n := range per {
			assert.LessOrEqual(t, n, 1, "%s: %s refused it %d times", c.ID, m, n)
		}
		if contains(sprint.StagingRefusers(c), "m1") {
			assert.NotEqual(t, "m1", c.Row, "%s is back on its refuser m1", c.ID)
		}
	}
	assert.LessOrEqual(t, len(h.stagingNotes()), 4, "one note per card m1 refused")
	assert.Empty(t, s.Fleet.Cell("m1", sprint.Ready), "m1 holds none of the cards it refused")
	assert.Len(t, s.Fleet.Cell("m2", sprint.Ready), 4, "m2, which never refused, holds them all")
	h.clean("levelled around a refuser")
}
