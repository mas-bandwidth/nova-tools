package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judgments the seat answered by hand with a loop every few minutes are the tick's
// (docs/SPEC-SPRINT.md section 8, answered by rule): a reader's broken read with a finding
// reworks the card with the finding, a friend's card too; work that came back failed on a
// harness fault is reworked with its failure as the fix; a report that arrives for an
// attempt the deadline already failed finishes that attempt. On the core's twin (world).

// answerHead is the head a friend's attempt pushes.
const answerHead = "0123456789abcdef0123456789abcdef01234567"

// friendInReview is a world with s1-1 a friend's card dealt to amy, started, and finished:
// LAND at answerHead, or failed with the report given; amy's seat as the tick reads it.
func friendInReview(t *testing.T, failed bool, report string) (*world, FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}
	dealWith(w, amy)
	startLanes(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, Working, wc.Col, "started on her row")
	r := FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Report: report, Failed: failed}
	if !failed {
		r.Head = answerHead
	}
	w.must(Finish(w.s, r))
	require.Equal(t, Review, w.state("s1-1"))
	return w, amy
}

func TestABrokenReadWithAFindingReworksAFriendsCardByRule(t *testing.T) {
	t.Parallel()
	finding := "internal/x/a.go:12 drops the error from Close; return it"
	w, amy := friendInReview(t, false, "friend amy LAND: done")
	brokenOnce(t, w, finding)
	require.Len(t, openOf(w, NReadBroken, "s1-1"), 1, "the reader's finding is a judgment")

	a := answerOn(t, w, on(amy), NReadBroken, "s1-1")
	rules(w, on(amy))
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, 1, pr.Int("reworks"), "a friend's card found broken with a finding is reworked by rule: %s %s", a.Act, a.Why)
	assert.Equal(t, Ready, pr.Col, "its next attempt waits for her deal")
	assert.Equal(t, finding, pr.F("fix"), "the finding is the fix")
	assert.Empty(t, openOf(w, NReadBroken, "s1-1"), "the judgment is answered")
	require.Len(t, logged(w, RuleReadBroken), 1, "a decided note names the rule")
	assert.Contains(t, pr.F(FieldNote), NRuleAnswered+" "+RuleReadBroken+": ", "a note on the card names the rule")
	dealWith(w, amy)
	wc := w.s.Fleet.Card(WorkCardID("s1-1", 2))
	require.NotNil(t, wc, "the next attempt is dealt")
	assert.Equal(t, FriendRow("amy"), wc.Row, "to her")
	assert.Equal(t, finding, wc.F("fix"), "carrying the finding")
}
