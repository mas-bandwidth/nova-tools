package decide

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// brokenByMark answers the read BOUNCE for a diff that carries the text BROKEN and LAND for any
// other, with every probability at 1: the fake backend of the shadow read's pins.
type brokenByMark struct{}

func (brokenByMark) Name() string { return "fake" }

func (brokenByMark) Ask(_ context.Context, s Schema, state string) (map[string]Answer, Usage, error) {
	verdict, defect := Land, 0.0
	if strings.Contains(state, "BROKEN") {
		verdict, defect = Bounce, 1
	}
	out := map[string]Answer{}
	for name, q := range s.Questions {
		if q.Type == Choice {
			out[name] = Answer{Type: Choice, Value: verdict, P: map[string]float64{verdict: 1}}
		} else {
			out[name] = noulAnswer(map[bool]float64{true: defect, false: 0}[name == "defect"])
		}
	}
	return out, Usage{}, nil
}

// Jev reads each card entering review in shadow (SPEC-NOVA-DECIDE section 6, jev-shadow-heavy-read.w1):
// the read decision is asked over the card and its diff and recorded with every answer marked
// shadow, never as a read. The broken answer (verdict BOUNCE) is scored for precision and recall
// against the heavy-read verdicts (the import's gold) and against the readers' outcome.
func TestShadowReadsAreScoredAgainstHeavyVerdictsAndReaders(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 4, 19, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	shadowRecord, readRecord, heavyRecord := filepath.Join(dir, ShadowReadName+".jsonl"), filepath.Join(dir, ReadName+".jsonl"), filepath.Join(dir, "backfill.jsonl")

	cards := []struct{ id, diff string }{
		{"c1", "+BROKEN line"}, {"c2", "+BROKEN line"}, {"c3", "+fine line"}, {"c4", "+BROKEN line"},
	}
	for _, c := range cards {
		d, existing, err := ShadowReadAsk(context.Background(), brokenByMark{}, c.id, "card "+c.id, c.diff, 0, shadowRecord, at)
		require.NoError(t, err)
		assert.False(t, existing)
		assert.Equal(t, ShadowReadName, d.Decision)
		assert.Equal(t, "true", d.Inputs["shadow"])
		for q, a := range d.Answers {
			assert.Equal(t, ShadowMethod, a.Method, q)
		}
		_, existing, err = ShadowReadAsk(context.Background(), brokenByMark{}, c.id, "card "+c.id, c.diff, 0, shadowRecord, at)
		require.NoError(t, err)
		assert.True(t, existing, "a card at a diff is shadow-read once")
	}
	shadows, err := Load(shadowRecord)
	require.NoError(t, err)
	require.Len(t, shadows, 4)
	for _, d := range shadows {
		assert.Empty(t, d.Acts, "a shadow read is never applied")
	}
	_, err = os.Stat(readRecord)
	assert.True(t, os.IsNotExist(err), "a shadow read is never counted as a read")

	// Two heavy verdicts, imported as the backfill does: c1 reworked (broken), c2 accepted.
	verdicts := filepath.Join(dir, "verdicts")
	for card, line := range map[string]string{"c1": "REWORK c1 finding: a test is weakened.", "c2": "ACCEPT c2 0123456789abcdef0123456789abcdef01234567"} {
		require.NoError(t, os.MkdirAll(filepath.Join(verdicts, card), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(verdicts, card, "VERDICT.md"), []byte(line+"\n\nHeavy read.\n"), 0o644))
	}
	_, err = Import(heavyRecord, ImportSources{Verdicts: filepath.Join(verdicts, "*", "VERDICT.md")}, at)
	require.NoError(t, err)
	heavy, err := Load(heavyRecord)
	require.NoError(t, err)

	// Two reader outcomes: c3 and c4 each had a read find them broken after the shadow read.
	for _, d := range shadows {
		if id := d.Inputs["card"]; id == "c3" || id == "c4" {
			label, _ := ShadowReadOutcome(d, CardMark{Placed: true, Broken: 1})
			assert.Equal(t, Bounce, label)
			_, _, err := Attach(shadowRecord, Outcome{ID: d.ID, Label: label, At: at.Format(time.RFC3339)})
			require.NoError(t, err)
		}
		if d.Inputs["card"] == "c1" {
			label, _ := ShadowReadOutcome(d, CardMark{Placed: true})
			assert.Empty(t, label, "a card standing with nothing new has no outcome yet")
			label, _ = ShadowReadOutcome(d, CardMark{Placed: true, Landed: true})
			assert.Equal(t, Land, label)
		}
	}
	shadows, err = Load(shadowRecord)
	require.NoError(t, err)

	assert.Equal(t, []ReadScore{
		// c1 broken and found broken; c2 broken-by-Jev but accepted by the heavy read.
		{Against: ScoreHeavy, Count: 2, TP: 1, FP: 1, Precision: 50, Recall: 100},
		// c3 found broken though Jev said LAND; c4 broken on both.
		{Against: ScoreReaders, Count: 2, TP: 1, FN: 1, Precision: 100, Recall: 50},
	}, ShadowReadScores(shadows, heavy))
}
