package decide

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judgment schema is a schema like any other, and its verb options are pinned to
// SPEC-NOVA-DECIDE section 13 word for word, as the read's verdict rule is to section 6;
// every fix and reason option has its text, and every option a text names is an option.
func TestJudgmentSchemaIsValidAndPinnedToTheSpec(t *testing.T) {
	t.Parallel()
	s := JudgmentSchema()
	assert.Empty(t, s.Problems())
	require.Equal(t, slices.Sorted(slices.Values(Verbs)), slices.Sorted(maps.Keys(s.Questions["verb"].Criteria)))
	assert.Equal(t, slices.Sorted(maps.Keys(Fixes)), slices.Sorted(maps.Keys(s.Questions["fix"].Criteria)))
	assert.Equal(t, slices.Sorted(maps.Keys(Reasons)), slices.Sorted(maps.Keys(s.Questions["reason"].Criteria)))
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	for _, v := range Verbs {
		row := fmt.Sprintf("| `%s` | %s |", v, s.Questions["verb"].Criteria[v])
		assert.Contains(t, string(spec), row, "SPEC-NOVA-DECIDE section 13 states the %s option as the schema asks it", v)
	}
	for kind, short := range Kinds {
		assert.Contains(t, string(spec), fmt.Sprintf("| %s | `%s` |", kind, short), "section 13 lists the routine kind %s", kind)
	}
	assert.NotEqual(t, ReadSchema().Hash(), s.Hash())
}

// Choose applies only what it may: an allowed verb at or above the bar that is not a drop
// or a release; everything else is listed, saying why, and a rework carries its fix and a
// drop its reason.
func TestChooseAppliesAtTheBarAndListsTheRest(t *testing.T) {
	t.Parallel()
	answers := func(verb string, p float64) map[string]Answer {
		return map[string]Answer{
			"verb":   {Type: Choice, Value: verb, P: map[string]float64{verb: p}},
			"fix":    {Type: Choice, Value: "retry", P: map[string]float64{"retry": 0.7}},
			"reason": {Type: Choice, Value: "already-done", P: map[string]float64{"already-done": 0.9}},
		}
	}
	allowed := []string{VerbRework, VerbDrop}
	for _, tc := range []struct {
		name, verb string
		p          float64
		act, why   string
	}{
		{"above the bar", VerbRework, 0.9, ActApply, ""},
		{"at the bar", VerbRework, 0.8, ActApply, ""},
		{"under the bar", VerbRework, 0.79, ActList, "p=0.79 is under the bar 0.80"},
		{"a drop, however sure", VerbDrop, 0.99, ActList, "a drop is the coordinator's: the listed edits were already made"},
		{"a verb the judgment does not print", VerbAck, 0.99, ActList, "chose ack, which the judgment does not print (it prints rework, drop)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Choose(answers(tc.verb, tc.p), allowed, 0.8)
			assert.Equal(t, tc.act, c.Act)
			assert.Equal(t, tc.p, c.P)
			if tc.why == "" {
				assert.Empty(t, c.Why)
			} else {
				assert.Contains(t, c.Why, tc.why)
			}
		})
	}
	assert.Equal(t, "retry", Choose(answers(VerbRework, 0.9), allowed, 0.8).Fix)
	assert.Equal(t, "already-done", Choose(answers(VerbDrop, 0.9), allowed, 0.8).Reason)
	assert.Equal(t, ActList, Choose(answers(VerbRelease, 1), []string{VerbRelease}, 0.5).Act, "a release is never applied")
}

// A provider's refusal for want of payment is recognised in every spelling the night of
// 2026-10-02 met, and a 402 that is a number in another sentence is not one.
func TestPaymentRefusalIsRecognised(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		`the provider opencode failed it, last error: 402 "Insufficient account funds"`,
		"HTTP 402: payment required",
		"status=402",
		"provider error: insufficient_quota",
		"Your credit balance is too low to access the API",
		"out-of-credit on the route",
	} {
		assert.True(t, PaymentRefusal("an unrelated line", s), s)
	}
	for _, s := range []string{"see PR 4021 and issue 402 for the history", "attempt 3 reached its bound on flash (redealt 3 times)", "the tests went red"} {
		assert.False(t, PaymentRefusal(s), s)
	}
}

// The state names the judgment, the card and the verbs allowed, and carries the card's
// last MaxHistory log lines, each cut at MaxLine, saying how many were left out.
func TestJudgmentStateIsBounded(t *testing.T) {
	t.Parallel()
	var hist []string
	for i := range 50 {
		hist = append(hist, fmt.Sprintf("12:00:%02d  line %d", i, i))
	}
	hist[49] = strings.Repeat("x", 500)
	s := JudgmentState(JudgmentInput{Kind: "work came back failed", Text: "the tests\nwent red", Card: "s1-3", Cards: 2, History: hist, Allowed: []string{VerbRework, VerbDrop}})
	assert.Contains(t, s, "kind: work came back failed\ncard: s1-3 (one of 2 cards in this judgment)\ntext: the tests went red\n")
	assert.Contains(t, s, "ALLOWED VERBS (the answer is one of these):\nrework, drop\n")
	assert.Contains(t, s, "(10 earlier lines not shown)\n12:00:10  line 10\n")
	assert.NotContains(t, s, "line 9\n")
	assert.Contains(t, s, strings.Repeat("x", MaxLine)+"...\n")
	assert.Equal(t, s, JudgmentState(JudgmentInput{Kind: "work came back failed", Text: "the tests went red", Card: "s1-3", Cards: 2, History: hist, Allowed: []string{VerbRework, VerbDrop}}),
		"the same judgment is the same state: a replay is answered from the record")
}

// The history is the card's log less its summary, cost and field-change lines.
func TestHistoryOfKeepsTheStory(t *testing.T) {
	t.Parallel()
	log := "12:10:56  space finished attempt 1: FAILED\n" +
		"12:10:56  judgment: work came back failed: the tests went red (decisions: rework with a fix, drop)\n" +
		"    fix: run the gate\n" +
		"12:12:26  names-01 cost: read names-01.r1.reader-hetzner by reader-hetzner, ok, ran 31s, cost $0.01\n" +
		"12:39:50  names-09 changed by the machine: asked=reader-space\n" +
		"LOG OK lines=5 of=9\n"
	assert.Equal(t, []string{
		"12:10:56  space finished attempt 1: FAILED",
		"12:10:56  judgment: work came back failed: the tests went red (decisions: rework with a fix, drop)",
		"fix: run the gate",
	}, HistoryOf(log))
}

// A printed decision makes the verb its words start with; one that is no verb the
// decision chooses makes none.
func TestVerbOfReadsThePrintedDecisions(t *testing.T) {
	t.Parallel()
	for d, v := range map[string]string{
		"rework with the finding": VerbRework, "rework with a fix": VerbRework, "rework": VerbRework,
		"ask another reader": VerbAskAnother, "ask --another": VerbAskAnother,
		"drop": VerbDrop, "ack": VerbAck, "wait": VerbWait, "accept": VerbAccept, "release": VerbRelease,
		"look at the card": "", "resolve and resume": "", "reader add": "", "fleet level": "",
	} {
		assert.Equal(t, v, VerbOf(d), d)
	}
}

// The outcome is read off the card: landed, off the table, or another judgment on it.
func TestJudgmentOutcomeReadsTheCard(t *testing.T) {
	t.Parallel()
	assert.Equal(t, OutcomeLanded, JudgmentOutcome("landed", true, false))
	assert.Equal(t, OutcomeDropped, JudgmentOutcome("", false, false))
	assert.Equal(t, OutcomeCameBack, JudgmentOutcome("review", true, true))
	assert.Empty(t, JudgmentOutcome("working", true, false), "a card still on its way has no outcome yet")
	for _, raw := range []string{"0", "0.8", "1"} {
		_, err := ParseJudgmentBar(raw)
		assert.NoError(t, err, raw)
	}
	for _, raw := range []string{"", "1.2", "-0.1", "high"} {
		_, err := ParseJudgmentBar(raw)
		assert.ErrorContains(t, err, "is not a probability", raw)
	}
}
