package sprint_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHoldingAFriendReturnsItsStartedCards: held and down mean no begun cards (the owner,
// 2026-10-04, on a held friend still showing two working cards: "Notice how <friend> is held,
// but still has working cards? nonono."). hold <friend>, with --return or without, and friend
// down return every card she has begun to ready; a started card with a push carries its
// pushed head and the generation whose branch holds it to its next deal, so the next taker
// starts from that work; the log says so. A plain hold leaves her ready cards not begun on
// her row, none taken into a lane it frees; --return takes them too (docs/SPEC-SPRINT.md
// sections 1 and 11).
func TestHoldingAFriendReturnsItsStartedCards(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	r := newHoldRig(t, 0, 4)
	r.tick()
	r.startFriends()
	r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{{ID: "f1-9", Brief: friendsBrief("only friend amy")}}}))
	r.tick()
	s := r.snap()
	amy := onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working)
	require.Len(t, amy, 2, "amy works two of the four")
	require.Equal(t, []string{"f1-9.w1"}, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready), "and one waits ready behind her lanes")
	pushed, running := amy[0], amy[1]
	gen := s.Fleet.Card(pushed).F("gen")
	started := map[string]string{
		pushed:  "a push on its branch sprint/t-" + pushed + ".g" + gen + ".e1 at " + sha,
		running: "her beat names it running",
	}

	// hold with no --return: nothing she has begun stays, and her card not begun waits
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "out of credits", Started: started})
	s = r.snap()
	assert.Empty(t, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Working), "a held friend keeps no begun card")
	assert.Equal(t, []string{"f1-9.w1"}, onRow(s, sprint.FriendRow("amy"), "f1", sprint.Ready), "her card not begun waits ready, taken into no lane")
	wc := s.Fleet.Card(pushed)
	assert.Equal(t, sprint.Withdrawn, wc.Col)
	assert.Equal(t, sha, wc.F(sprint.FieldCarryHead), "the pushed head is carried")
	assert.Equal(t, gen, wc.F(sprint.FieldCarryGen), "and the generation whose branch holds it")
	assert.Empty(t, s.Fleet.Card(running).F(sprint.FieldCarryHead), "a card started with no push carries nothing")
	assert.Empty(t, wc.F(sprint.FieldTakeEnded), "a hold is no failure: no redeal spent")
	for _, id := range amy {
		// its primary goes back to ready when the pump drains the work table's queue (the
		// machine runs), and the same tick deals it again
		assert.Equal(t, sprint.Withdrawn, s.Fleet.Card(id).Col, "%s: returned", id)
	}
	lines := r.holdLines()
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[len(lines)-1], "friend amy held: out of credits; her work begun is handed back now, her cards not begun wait")
	assert.Contains(t, lines[len(lines)-1], "withdrew 2")
	assert.Contains(t, lines[len(lines)-1], "carried the pushed head of 1: "+pushed)
	assert.Contains(t, logWhat(t, r), "the next generation starts from its pushed head "+sha+" (gen "+gen+")")

	// the next deal gives it to bob at its next generation, the carry still on it
	r.tick()
	s = r.snap()
	wc = s.Fleet.Card(pushed)
	require.Equal(t, sprint.FriendRow("bob"), wc.Row, "the next taker is the friend up")
	assert.Contains(t, []string{sprint.Ready, sprint.Working}, wc.Col)
	assert.NotEqual(t, gen, wc.F("gen"), "its own branch")
	assert.Equal(t, sha, wc.F(sprint.FieldCarryHead), "the carry survives the deal: the next taker starts from it")
	assert.Equal(t, gen, wc.F(sprint.FieldCarryGen))

	// friend down (and the line a friend's daemon sends at a usage limit) takes her started
	// cards the same way
	bob := onRow(s, sprint.FriendRow("bob"), "f1", sprint.Working)
	require.NotEmpty(t, bob)
	const sha2 = "89abcdef0123456789abcdef0123456789abcdef"
	down := map[string]string{bob[0]: "a push on its branch sprint/t-" + bob[0] + ".g" + s.Fleet.Card(bob[0]).F("gen") + ".e1 at " + sha2}
	r.must(store.FriendTakeStep(sprint.FriendTakeReq{Friend: "bob", All: true, Hold: true, Started: down, Who: "coordinator"}))
	s = r.snap()
	assert.Empty(t, onRow(s, sprint.FriendRow("bob"), "f1", sprint.Ready, sprint.Working), "a friend down keeps no card")
	assert.Equal(t, sha2, s.Fleet.Card(bob[0]).F(sprint.FieldCarryHead))

	// hold --return takes her card not begun too
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Release: true, Reason: "credits back"})
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "away for the night", Return: true})
	assert.Empty(t, onRow(r.snap(), sprint.FriendRow("amy"), "f1", sprint.Ready, sprint.Working), "--return leaves her no card")
}

// logWhat is every note's words in the log, one line each.
func logWhat(t *testing.T, r *holdRig) string {
	t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(t, err)
	var b strings.Builder
	for _, l := range lines {
		if l.Note != nil {
			b.WriteString(l.Note.What + "\n")
		}
	}
	return b.String()
}

func TestPushedTipReadsOnlyAPushAtAFullSha(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	assert.Equal(t, sha, sprint.PushedTip("a push on its branch sprint/t-a.w1.g1.e1 at "+sha))
	assert.Empty(t, sprint.PushedTip("her beat names it running"))
	assert.Empty(t, sprint.PushedTip("a push on sprint/x cannot be read (timeout)"))
	assert.Empty(t, sprint.PushedTip("a push on its branch sprint/x at abc"), "a short sha is no head")
}
