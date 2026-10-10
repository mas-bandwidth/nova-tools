package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An empty run is a lane that wrote no report. On 2026-10-09/10 a friend's opencode lanes
// ended so, the classifier took the report's Cost line for its first error ("cost line"), and the failed
// rule reworked the card on its tier straight back to the lane that ran it empty.

// emptyRunReport is the failed report of an empty run as the sprint log quoted it.
func emptyRunReport(friend string) string {
	return "friend " + friend + " FAIL: Verdict: FAIL nova-friend of " + friend + ": the lane ended with exit 0 after 154s and wrote no report (harness fault, not a finding); first error: Cost: $0.00 (intro rate, route flash-mercury) (invoice effective: $0.00) (opencode: $0.00) tokens input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury"
}

func TestAnEmptyRunIsItsOwnClassBeforeTheCostLine(t *testing.T) {
	t.Parallel()
	for name, report := range map[string]string{
		"the log's report":         emptyRunReport("amy"),
		"the lane's fault words":   "friend amy FAIL: harness-fault: no report; first error: the harness printed no error line",
		"no report, a cost beside":"friend amy FAIL: the lane wrote no report; Cost: $0.02 tokens input=10 cache_read=0 cache_write=0 output=0",
	} {
		assert.Equal(t, ClassEmptyRun, HarnessFault(report), name)
	}
	// reversed witnesses: a run that spent tokens is no empty run, its Cost line is the cost line's
	zhi := "friend zhi HOLD: Cost: $0.09 (list price, route flash-deepseek41-direct) (opencode: -) tokens input=84549 cache_read=3719936 cache_write=0 output=34474 reasoning=0 model=deepseek/deepseek-v4.1-flash harness=dsh price_route=flash-deepseek41-direct"
	assert.Equal(t, "cost line", HarnessFault(zhi), "tokens spent: the cost line")
	assert.Equal(t, "lane died", HarnessFault(harnessFaults["lane died"]), "a runner's kill stays lane died")
	// a zero tokens line with a report is no empty run: the daemon can read a lane's tokens
	// from the wrong session store, so zero tokens alone is no evidence of an empty run
	zero := "friend amy HOLD: Cost: $0.00 (opencode: $0.00) tokens input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=m harness=opencode"
	assert.NotEqual(t, ClassEmptyRun, HarnessFault(zero), "zero tokens alone")
}

// emptyRunOnAmy is a world with s1-1 a card for any friend (WHO: friend) dealt to amy alone,
// started, and failed with the report; amy and bob's seats.
func emptyRunOnAmy(t *testing.T, who, report string) (*world, FriendSeat, FriendSeat) {
	t.Helper()
	w := friendWorld(t, friendBrief(who))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	dealWith(w, amy)
	startLanes(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, FriendRow("amy"), wc.Row, "dealt to amy")
	require.Equal(t, Working, wc.Col, "started on her row")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Report: report, Failed: true}))
	require.Equal(t, Review, w.state("s1-1"))
	return w, amy, bob
}

func TestAnEmptyRunIsNeverReworkedOntoTheFriendWhoseLaneRanItEmpty(t *testing.T) {
	t.Parallel()
	t.Run("any friend's card: the rework goes to another friend", func(t *testing.T) {
		t.Parallel()
		w, amy, bob := emptyRunOnAmy(t, "friend", emptyRunReport("amy"))
		a := answerOn(t, w, on(amy, bob), NWorkFailed, "s1-1")
		rules(w, on(amy, bob))
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, 1, pr.Int("reworks"), "reworked by rule: %s %s", a.Act, a.Why)
		assert.Equal(t, "amy", pr.F(FieldFriendsLeft), "the primary has left amy")
		assert.Contains(t, pr.F(FieldNote), "never again on friend amy")
		dealWith(w, amy, bob)
		wc := w.s.Fleet.Card(WorkCardID("s1-1", 2))
		require.NotNil(t, wc, "the next attempt is dealt")
		assert.Equal(t, FriendRow("bob"), wc.Row, "never back to amy")
	})
	t.Run("amy alone up: it waits, never back to her", func(t *testing.T) {
		t.Parallel()
		w, amy, _ := emptyRunOnAmy(t, "friend", emptyRunReport("amy"))
		rules(w, on(amy))
		dealWith(w, amy)
		if wc := w.s.Fleet.Card(WorkCardID("s1-1", 2)); wc != nil {
			assert.NotEqual(t, FriendRow("amy"), wc.Row, "never back to amy")
		}
	})
	t.Run("reversed: a cost-line fault leaves no friend", func(t *testing.T) {
		t.Parallel()
		w, amy, bob := emptyRunOnAmy(t, "friend", harnessFaults["cost line"])
		rules(w, on(amy, bob))
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, 1, pr.Int("reworks"))
		assert.Empty(t, pr.F(FieldFriendsLeft), "only an empty run leaves the friend")
	})
	t.Run("reversed: a card that names its friend stays hers", func(t *testing.T) {
		t.Parallel()
		w, amy, bob := emptyRunOnAmy(t, "friend amy", emptyRunReport("amy"))
		rules(w, on(amy, bob))
		pr := w.s.Work.Card("s1-1")
		require.Equal(t, 1, pr.Int("reworks"))
		assert.Empty(t, pr.F(FieldFriendsLeft), "a named friend's rework is hers alone (ReworkPinned): leaving her would strand it")
		dealWith(w, amy, bob)
		wc := w.s.Fleet.Card(WorkCardID("s1-1", 2))
		require.NotNil(t, wc)
		assert.Equal(t, FriendRow("amy"), wc.Row)
	})
}
