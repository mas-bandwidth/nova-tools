package decide

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The score asks the read's five questions and one noul per escalation class but
// outside_paths, which is read from inside_paths; every class names its escalations and
// is a row of SPEC-NOVA-DECIDE section 9; the fixture record's score decisions carry this
// schema's hash, so a reworded class names the stale fixture.
func TestScoreSchemaAsksTheReadAndEveryClass(t *testing.T) {
	t.Parallel()
	s := ScoreSchema()
	assert.Empty(t, s.Problems())
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	want := slices.Collect(maps.Keys(ReadSchema().Questions))
	seen := map[string]bool{}
	for _, c := range Classes() {
		require.False(t, seen[c.Name], "class %s named twice", c.Name)
		seen[c.Name] = true
		assert.Regexp(t, `^E\d+(, E\d+)*$`, c.Escalations, "class %s names the escalations it stands for", c.Name)
		assert.Contains(t, string(spec), "| `"+c.Name+"` | "+c.Escalations+" |", "SPEC-NOVA-DECIDE section 9 has no row for %s", c.Name)
		if c.Name != OutsidePaths {
			want = append(want, c.Name)
		}
	}
	assert.ElementsMatch(t, want, slices.Collect(maps.Keys(s.Questions)))
	assert.NotContains(t, s.Questions, OutsidePaths, "outside_paths is 1 - inside_paths, never asked twice")
	fixture, err := Load(filepath.Join("..", "..", "cmd", "nova-decide", "testdata", "record.jsonl"))
	require.NoError(t, err)
	scored := 0
	for _, d := range fixture {
		if d.Decision == ScoreName {
			scored++
			assert.Equal(t, s.Hash(), d.Schema, "the score schema changed and cmd/nova-decide/testdata/record.jsonl still carries %s's old hash: regenerate its score decisions", d.ID)
		}
	}
	assert.NotZero(t, scored, "the fixture record holds score decisions")
}

// scoreAnswers is a score's answers: every question low, but the ones given.
func scoreAnswers(over map[string]float64) map[string]Answer {
	out := map[string]Answer{"verdict": {Type: Choice, Value: Land, P: map[string]float64{Land: 0.9}}}
	for name, q := range ScoreSchema().Questions {
		if q.Type == Noul {
			out[name] = noulAnswer(0.05)
		}
	}
	out["inside_paths"] = noulAnswer(0.95)
	for name, p := range over {
		out[name] = noulAnswer(p)
	}
	return out
}

// Every class's p is its noul's yes, outside_paths 1 - inside_paths; the top class is the
// highest, the first in class order on a tie.
func TestClassPAndTopReadTheClasses(t *testing.T) {
	t.Parallel()
	d := Decision{Decision: ScoreName, Answers: scoreAnswers(map[string]float64{"inside_paths": 0.3, "invented_reason": 0.6})}
	ps := ClassP(d)
	assert.Len(t, ps, len(Classes()))
	assert.InDelta(t, 0.7, ps[OutsidePaths], 1e-9)
	top, p := Top(d)
	assert.Equal(t, OutsidePaths, top)
	assert.InDelta(t, 0.7, p, 1e-9)
	tie := Decision{Answers: scoreAnswers(map[string]float64{"ledger_ceiling": 0.8, "stranded_fragment": 0.8})}
	top, _ = Top(tie)
	assert.Equal(t, "stranded_fragment", top, "the first in class order on a tie")
}

// A landed diff is scored under <card>@landed@<head> (the head cut to twelve), and scored
// again under that id over the same card and diff it is answered from the record with no
// call; CardOf reads the card back from the id.
func TestScoreRecordsUnderTheLandedOp(t *testing.T) {
	t.Parallel()
	record := filepath.Join(t.TempDir(), "score.jsonl")
	b := &scoreBackend{answers: scoreAnswers(map[string]float64{"stranded_fragment": 0.9})}
	op := ScoreOp("diary-7", "0123456789abcdef0123")
	assert.Equal(t, "diary-7@landed@0123456789ab", op)
	assert.Equal(t, "diary-7", CardOf(op))
	assert.Equal(t, "card-1", CardOf("card-1"))
	d, err := Score(context.Background(), b, "the card", "the diff", record, op, at)
	require.NoError(t, err)
	_, err = Score(context.Background(), b, "the card", "the diff", record, op, at)
	require.NoError(t, err)
	assert.Equal(t, 1, b.asks, "a recorded op asks nothing")
	assert.Equal(t, ScoreName, d.Decision)
	assert.Equal(t, ReadState("the card", "the diff", ""), d.State, "the score is asked over the read's state")
	top, p := Top(d)
	assert.Equal(t, "stranded_fragment", top)
	assert.InDelta(t, 0.9, p, 1e-9)
}

// scoreBackend answers the score with fixed answers and counts its asks.
type scoreBackend struct {
	answers map[string]Answer
	asks    int
}

func (b *scoreBackend) Name() string { return "fake" }

func (b *scoreBackend) Ask(context.Context, Schema, string) (map[string]Answer, Usage, error) {
	b.asks++
	return b.answers, Usage{}, nil
}

// Findings counts, over the score decisions made since the window's start, every class
// each gives a p at or above the bar, and unnamed for p(defect) at the bar with no class
// there; a read decision and a score before the window count nothing; most cards first,
// then class order.
func TestFindingsClustersTheClassesAtTheBar(t *testing.T) {
	t.Parallel()
	stamp := func(d time.Duration) string { return at.Add(d).UTC().Format(time.RFC3339) }
	score := func(id string, d time.Duration, over map[string]float64) Decision {
		return Decision{ID: id, Decision: ScoreName, At: stamp(d), Answers: scoreAnswers(over)}
	}
	ds := []Decision{
		score("a@landed@1", 0, map[string]float64{"cut_citation": 0.8, "stranded_fragment": 0.5}),
		score("b@landed@2", time.Hour, map[string]float64{"cut_citation": 0.7}),
		score("c@landed@3", time.Hour, map[string]float64{"defect": 0.9}),
		score("d@landed@4", time.Hour, map[string]float64{"stranded_fragment": 0.49}),
		score("old@landed@5", -48*time.Hour, map[string]float64{"cut_citation": 0.99}),
		{ID: "r", Decision: ReadName, At: stamp(0), Answers: scoreAnswers(map[string]float64{"defect": 0.9})},
	}
	clusters, scored := Findings(ds, at.Add(-time.Hour), 0.5)
	assert.Equal(t, 4, scored)
	assert.Equal(t, []Cluster{
		{Class: "cut_citation", Count: 2, Cards: []string{"a", "b"}},
		{Class: "stranded_fragment", Count: 1, Cards: []string{"a"}},
		{Class: Unnamed, Count: 1, Cards: []string{"c"}},
	}, clusters)
	none, _ := Findings(ds, at.Add(2*time.Hour), 0.5)
	assert.Empty(t, none)
}

// One outcome labels every class a review found, joined by +: calibrate counts the
// decision for each of its words, and a word of no class's label counts for none.
func TestCalibrateCountsEachClassOfAJoinedLabel(t *testing.T) {
	t.Parallel()
	d := func(id, label string, frag float64) Decision {
		return Decision{ID: id, Decision: ScoreName, Schema: "s", Answers: scoreAnswers(map[string]float64{"stranded_fragment": frag}),
			Outcome: &Outcome{ID: id, Label: label}}
	}
	ds := []Decision{d("a", "stranded_fragment+invented_reason", 0.9), d("b", "cut_citation", 0.2), d("c", "clean", 0.1), d("e", "ugly", 0.8)}
	c, err := Calibrate(ds, ScoreName, "stranded_fragment", []string{"stranded_fragment"}, []string{"clean"})
	require.NoError(t, err)
	assert.Equal(t, []float64{0.9}, c.Positives)
	assert.Equal(t, []float64{0.1}, c.Negatives)
	assert.Equal(t, 2, c.Skipped, "another class's card and an unclassed one are skipped")
	c, err = Calibrate(ds, ScoreName, "stranded_fragment", []string{"invented_reason", "cut_citation"}, []string{"clean"})
	require.NoError(t, err)
	assert.Len(t, c.Positives, 2)
}
