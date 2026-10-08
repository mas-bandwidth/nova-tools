package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Collect finishes every outbox report of a working card (the coordinator's finish-loop.py,
// 92 finishes on the night of 2026-10-05): the card's own friend's tree first, else any
// friend's; LAND with a Head at that head, HOLD and FAIL failed with the report's first 600
// characters; a lane its runner ENDed with no report is a harness fault to return when dead lanes are asked; and a
// card no longer working (finished once) is never named again.
func TestCollectFinishesEveryOutboxReportOfAWorkingCard(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	long := "the bench went red.\n" + strings.Repeat("x", 700) + "\n"
	cards := []CollectCard{
		{Friend: "amy", Card: "land.w1", Job: "land.w1~15"},
		{Friend: "amy", Card: "away.w1", Job: "away.w1~15.g3"},
		{Friend: "amy", Card: "hold.w1", Job: "hold.w1~15"},
		{Friend: "amy", Card: "fail.w1", Job: "fail.w1~15"},
		{Friend: "amy", Card: "dead.w1", Job: "dead.w1~15"},
		{Friend: "amy", Card: "limit.w1", Job: "limit.w1~15"},
		{Friend: "amy", Card: "again.w1", Job: "again.w1~15"},
		{Friend: "amy", Card: "quiet.w1", Job: "quiet.w1~15"},
		{Friend: "amy", Card: "writing.w1", Job: "writing.w1~15"},
		{Friend: "bob", Card: "bobs.w1", Job: "bobs.w1~15"},
	}
	runner := strings.Join([]string{
		"2026-10-06 07:00:00 AM START dead.w1~15 tier=heavy model=m",
		"2026-10-06 07:10:00 AM END dead.w1~15 model=m exit=1 wall=600s report=no",
		"2026-10-06 07:00:00 AM START limit.w1~15 tier=heavy model=m",
		"2026-10-06 07:01:00 AM LIMIT limit.w1~15 model=m until=later: limit",
		"2026-10-06 07:01:00 AM END limit.w1~15 model=m exit=1 wall=60s report=no",
		"2026-10-06 07:00:00 AM START again.w1~15 tier=heavy model=m",
		"2026-10-06 07:01:00 AM END again.w1~15 model=m exit=1 wall=60s report=no",
		"2026-10-06 07:02:00 AM START again.w1~15 tier=heavy model=m", // running again: not dead
		"2026-10-06 07:00:00 AM START hold.w1~15 tier=heavy model=m",
		"2026-10-06 07:30:00 AM END hold.w1~15 model=m exit=0 wall=1800s report=Verdict: HOLD",
	}, "\n")
	trees := []CollectTree{
		{Friend: "bob", Reports: map[string]string{
			"away.w1~15.g3": "**Verdict:** land\nHead: " + strings.ToUpper(head) + "\n\nwritten ahead, in bob's tree.\n",
			"land.w1~15":    "Verdict: FAIL\n\nnot amy's: her own report is read first.\n",
		}},
		{Friend: "amy", Runner: runner, Reports: map[string]string{
			"land.w1~15":    "# land\n\nVerdict: LAND\nHead: " + head + "\n\nThe card is done.\n",
			"hold.w1~15":    "Verdict: HOLD\nHead: " + head + "\n\nno push\n",
			"fail.w1~15":    "Verdict: FAIL\n\n" + long,
			"writing.w1~15": "I am still writing it.\n",
			"gone.w1~15":    "Verdict: LAND\nHead: " + head + "\n", // no working card names it
		}, Unread: map[string]string{"quiet.w1~15": "it is a symlink"}},
	}

	got := map[string]Collected{}
	for _, c := range Collect(cards, trees, true) {
		_, twice := got[c.Card]
		require.False(t, twice, "one line per card: %s", c.Card)
		got[c.Card] = c
	}
	assert.Len(t, got, 7, "every card with a report or a dead lane, and none other: %v", got)

	assert.Equal(t, Collected{CollectCard: cards[0], From: "amy", Verdict: "LAND", Head: head, Report: "friend amy LAND: The card is done."}, got["land.w1"], "her own tree is read first")
	assert.Equal(t, Collected{CollectCard: cards[1], From: "bob", Verdict: "LAND", Head: head, Report: "friend amy LAND: written ahead, in bob's tree."}, got["away.w1"], "a report in another friend's tree finishes her card, at its generation")
	assert.Equal(t, Collected{CollectCard: cards[2], From: "amy", Verdict: "HOLD", Head: head, Failed: true, Report: "friend amy HOLD: Verdict: HOLD Head: " + head + " no push"}, got["hold.w1"])
	fail := got["fail.w1"]
	assert.True(t, fail.Failed)
	assert.Empty(t, fail.Head)
	assert.Equal(t, "friend amy FAIL: "+collectChars(("Verdict: FAIL\n\n" + long)[:600], 600), fail.Report, "a failed finish carries the report's first 600 characters")
	assert.Equal(t, Collected{CollectCard: cards[4], Dead: true, Report: "harness-fault: no report; friend amy lane ended: 2026-10-06 07:10:00 AM END dead.w1~15 model=m exit=1 wall=600s report=no"}, got["dead.w1"], "a lane ENDed with no report returns without a failed finish")
	assert.True(t, got["fail.w1"].Failed, "an explicit FAIL report still consumes a failed finish")
	assert.NotContains(t, got, "limit.w1", "a run stopped at its usage limit is run again, never dead")
	assert.NotContains(t, got, "again.w1", "a job started again after its END is running")
	assert.Equal(t, "outbox/quiet.w1~15/REPORT.md of amy cannot be read: it is a symlink", got["quiet.w1"].Left)
	assert.Equal(t, "outbox/writing.w1~15/REPORT.md of amy has no Verdict line", got["writing.w1"].Left)
	assert.False(t, got["writing.w1"].Failed, "a report with no Verdict line is left, never failed")
	assert.NotContains(t, got, "bobs.w1", "bob has no report and no runner log")

	// dead lanes only when asked
	for _, c := range Collect(cards, trees, false) {
		assert.False(t, c.Dead, "%s", c.Card)
	}

	// finished once: the finished cards leave working, and the same trees finish nothing again
	still := []CollectCard{cards[5], cards[6], cards[7], cards[8], cards[9]}
	for _, c := range Collect(still, trees, true) {
		assert.NotEmpty(t, c.Left, "a report finished once is never finished twice: %v", c)
	}
	// a card dealt again is another job (its generation): last generation's report does not finish it
	assert.Empty(t, Collect([]CollectCard{{Friend: "amy", Card: "land.w1", Job: "land.w1~15.g2"}}, trees, true))
}

func TestRunnerEndedReadsTheJobsLastEvent(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		log  string
		dead bool
	}{
		{"", false},
		{"t START j~1 x\nt END j~1 exit=0 report=Verdict: LAND", false},
		{"t START j~1 x\nt END j~1 exit=0 report=no", true},
		{"t START j~1 x\nt END j~1 exit=0 report=no\nt START j~1 x", false},
		{"t START j~1 x\nt LIMIT j~1 x\nt END j~1 exit=1 report=no", false},
		{"t START j~10 x\nt END j~10 exit=1 report=no", false}, // another job
	} {
		_, dead := RunnerEnded(c.log, "j~1")
		assert.Equal(t, c.dead, dead, "%q", c.log)
	}
}
