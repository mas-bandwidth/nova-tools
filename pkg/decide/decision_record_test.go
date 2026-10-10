package decide

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every judgment answer the coordinator gives (SPEC-NOVA-DECIDE section 13, the coordinator's
// own decisions) is one judgment-answer record: the judgment's kind, its text and the evidence
// paths it names as inputs, the verb, the --reason or --fix text and the actor. The machine
// then attaches the card's fate as the outcome: landed, bounced (a read broken or a finish
// failed since the answer) or dropped, each once.
func TestEveryJudgmentAnswerWritesARecordAndItsOutcomeFollows(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 4, 17, 0, 0, 0, time.UTC)
	rows := []struct {
		name, verb, reason, fix string
		now                     CardMark
		label                   string
	}{
		{name: "accept lands", verb: "accept", now: CardMark{Placed: true, Landed: true}, label: LabelLanded},
		{name: "accept over a reader's verdict", verb: "accept", reason: "the finding is wrong", now: CardMark{Placed: true, Landed: true}, label: LabelLanded},
		{name: "rework bounces on a broken read", verb: "rework", fix: "handle the empty case", now: CardMark{Placed: true, Broken: 2}, label: LabelBounced},
		{name: "recut bounces on a failed finish", verb: "recut", now: CardMark{Placed: true, Failed: 1}, label: LabelBounced},
		{name: "drop is dropped", verb: "drop", reason: "obsolete", now: CardMark{Dropped: true}, label: LabelDropped},
		{name: "wait lands", verb: "wait", reason: "30m", now: CardMark{Placed: true, Landed: true}, label: LabelLanded},
		{name: "hold has no outcome while it stands", verb: "hold", now: CardMark{Placed: true}},
		{name: "ack lands", verb: "ack", reason: "a flaky runner", now: CardMark{Placed: true, Landed: true}, label: LabelLanded},
	}
	record := filepath.Join(t.TempDir(), JudgmentAnswerName+".jsonl")
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			in := AnswerInput{Note: "ci-" + row.verb + "-1.1", Kind: "a reader found it broken", Card: "s1-" + row.verb,
				Text: "s1-4 broken: see outbox/s1-4/REPORT.md and internal/x/y.go:12.", Verb: row.verb, Reason: row.reason, Fix: row.fix,
				Actor: "rowan", Mark: CardMark{Placed: true, Broken: 1}}
			d := AnswerDecision(in, at)
			assert.Equal(t, JudgmentAnswerName, d.Decision)
			assert.Equal(t, row.verb, d.Answers["verb"].Value)
			assert.Equal(t, map[string]string{"card": in.Card, "note": in.Note, "kind": in.Kind, "verb": row.verb, "reason": row.reason, "fix": row.fix,
				"actor": "rowan", "evidence": "outbox/s1-4/REPORT.md\ninternal/x/y.go", "broken_reads": "1", "failed": "0"}, d.Inputs)
			assert.Contains(t, d.State, in.Text)

			have, err := Append(record, d)
			require.NoError(t, err)
			assert.Nil(t, have)
			_, err = Append(record, d)
			require.NoError(t, err, "the same answer again is one record")

			label, note := AnswerOutcome(d, row.now)
			require.Equal(t, row.label, label, note)
			if label == "" {
				return
			}
			_, changed, err := Attach(record, Outcome{ID: d.ID, Label: label, Note: note, At: at.Format(time.RFC3339)})
			require.NoError(t, err)
			assert.True(t, changed)
		})
	}
	ds, err := Load(record)
	require.NoError(t, err)
	require.Len(t, ds, len(rows), "one record per answer")
	for i, d := range ds {
		if rows[i].label == "" {
			assert.Nil(t, d.Outcome, rows[i].name)
			continue
		}
		require.NotNil(t, d.Outcome, rows[i].name)
		assert.Equal(t, rows[i].label, d.Outcome.Label)
	}
}

// A card's second bounce is read against the counters its answer recorded, so an answer's own
// rework is not a bounce, and a card still standing has no outcome.
func TestAnAnswersOutcomeIsReadAgainstItsOwnCounters(t *testing.T) {
	t.Parallel()
	d := AnswerDecision(AnswerInput{Note: "n1", Card: "c", Verb: "rework", Actor: "rowan", Mark: CardMark{Placed: true, Broken: 2, Failed: 1}}, time.Unix(0, 0))
	for _, c := range []struct {
		now   CardMark
		label string
	}{
		{CardMark{Placed: true, Broken: 2, Failed: 1}, ""},
		{CardMark{Placed: true, Broken: 3, Failed: 1}, LabelBounced},
		{CardMark{Placed: true, Broken: 2, Failed: 2}, LabelBounced},
		{CardMark{Placed: true, Landed: true, Broken: 3}, LabelLanded},
		{CardMark{}, ""},
	} {
		label, _ := AnswerOutcome(d, c.now)
		assert.Equal(t, c.label, label, "%+v", c.now)
	}
}
