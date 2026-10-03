package decide

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// briefer is a fake backend for the brief: p(converges) is the number after
// "converges=" in the card, a card holding "vague" names step 2 ambiguous and
// fails commit_stated, and a card holding "down" is a backend failure. It
// counts its asks.
type briefer struct {
	mu   sync.Mutex
	asks int
}

func (*briefer) Name() string { return "fake" }

func (b *briefer) Ask(ctx context.Context, s Schema, state string) (map[string]Answer, Usage, error) {
	b.mu.Lock()
	b.asks++
	b.mu.Unlock()
	if strings.Contains(state, "down") {
		return nil, Usage{}, errors.New("the backend answered HTTP 503")
	}
	conv := 0.9
	if _, v, ok := strings.Cut(state, "converges="); ok {
		f, err := strconv.ParseFloat(strings.Fields(v)[0], 64)
		if err != nil {
			return nil, Usage{}, err
		}
		conv = f
	}
	t := map[string]FixedAnswer{"converges": {Noul: p(conv)}, "minutes": {Choice: "20-45", P: map[string]float64{"20-45": 0.6, "10-20": 0.4}},
		"ambiguous_step": {Choice: "none", P: map[string]float64{"none": 0.8}}}
	for _, q := range briefNeeds {
		t[q] = FixedAnswer{Noul: p(0.9)}
	}
	if strings.Contains(state, "vague") {
		t["commit_stated"] = FixedAnswer{Noul: p(0.2)}
		t["ambiguous_step"] = FixedAnswer{Choice: "step-2", P: map[string]float64{"step-2": 0.7, "none": 0.3}}
	}
	return Fixed{Table: t}.Ask(ctx, s, state)
}

// The brief schema is a schema like any other, and every question it asks is a
// row of SPEC-NOVA-DECIDE section 9's table, so a question added or renamed
// without the spec turns this red.
func TestBriefSchemaIsValid(t *testing.T) {
	t.Parallel()
	s := BriefSchema()
	assert.Empty(t, s.Problems())
	assert.Len(t, s.Questions, 9)
	assert.Len(t, s.Questions["ambiguous_step"].Criteria, briefSteps+2, "none, step-1 to step-12, unnumbered")
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	for name, q := range s.Questions {
		assert.True(t, strings.Contains(string(spec), "| `"+name+"` | "+q.Type+" |"), "SPEC-NOVA-DECIDE section 9 has no row for the brief's question %s", name)
	}
	assert.NotEqual(t, BriefOp("c1", "one brief"), BriefOp("c1", "another brief"), "a rewritten brief is a decision of its own")
	assert.True(t, strings.HasPrefix(BriefOp("c1", "x"), "c1@brief-"))
	assert.True(t, strings.HasPrefix(BriefState("x\n\n"), "CARD (the whole brief a flash child with no memory is handed):\nx\n\nFRAME (what the sprint gives the child"), "the card, then the frame")
}

// A brief's reading names p(converges), the minutes, and every question the card
// fails: a need under 0.5 with its p, and the ambiguous step with its p.
func TestBriefOfNamesTheFailedQuestions(t *testing.T) {
	t.Parallel()
	answers, _, err := Ask(context.Background(), &briefer{}, BriefSchema(), "a vague card converges=0.35")
	require.NoError(t, err)
	b := BriefOf(Decision{Answers: answers})
	assert.Equal(t, Brief{Converges: 0.35, Minutes: "20-45", Ambiguous: "step-2", Failed: []string{"commit_stated(0.20)", "ambiguous_step:step-2(0.70)"}}, b)
	assert.Equal(t, "p_converges=0.35 minutes=20-45 failed=commit_stated(0.20),ambiguous_step:step-2(0.70)", b.Line())
	answers, _, err = Ask(context.Background(), &briefer{}, BriefSchema(), "a full card")
	require.NoError(t, err)
	assert.Equal(t, "p_converges=0.90 minutes=20-45 failed=-", BriefOf(Decision{Answers: answers}).Line())
}

// The bar is empty (report only) or a probability; it refuses a card under it.
func TestParseBriefBarIsEmptyOrAProbability(t *testing.T) {
	t.Parallel()
	bar, err := ParseBriefBar(" ")
	require.NoError(t, err)
	assert.False(t, bar.Refuses(Brief{Converges: 0}), "no bar refuses nothing")
	bar, err = ParseBriefBar("0.6")
	require.NoError(t, err)
	assert.True(t, bar.Refuses(Brief{Converges: 0.59}))
	assert.False(t, bar.Refuses(Brief{Converges: 0.6}), "at the bar is added")
	for _, raw := range []string{"x", "1.5", "-0.1"} {
		_, err := ParseBriefBar(raw)
		assert.ErrorContains(t, err, "is not a probability in [0, 1]; set a decimal, or empty to report only", raw)
	}
}

// A batch reads the record once, asks what it does not hold at most width at a
// time, and appends every new decision; asked again it asks nothing. A backend
// that fails one card is that card's error, never the batch's: the rest are
// recorded. A rewritten brief is a new decision under a new op id.
func TestBriefsAsksOnceAndReplaysFromTheRecord(t *testing.T) {
	t.Parallel()
	record := filepath.Join(t.TempDir(), "brief.jsonl")
	b := &briefer{}
	cards := map[string]string{"a": "card a converges=0.8", "b": "card b vague converges=0.3", "c": "card c down"}
	made, err := Briefs(context.Background(), b, cards, record, at, 2, 0)
	require.NoError(t, err)
	require.Len(t, made, 3)
	assert.Equal(t, BriefOp("a", cards["a"]), made[0].ID)
	assert.InDelta(t, 0.8, BriefOf(made[0].Decision).Converges, 1e-9)
	assert.InDelta(t, 0.3, BriefOf(made[1].Decision).Converges, 1e-9)
	var backend *BackendError
	require.ErrorAs(t, made[2].Err, &backend)
	assert.Equal(t, "fake", backend.Backend)
	assert.Equal(t, 3, b.asks)

	again, err := Briefs(context.Background(), b, map[string]string{"a": cards["a"], "b": cards["b"]}, record, at, 2, 0)
	require.NoError(t, err)
	assert.True(t, again[0].Existing && again[1].Existing)
	assert.Equal(t, 3, b.asks, "a recorded brief asks nothing")

	_, err = Briefs(context.Background(), b, map[string]string{"b": "card b rewritten converges=0.7"}, record, at, 2, 0)
	require.NoError(t, err)
	ds, err := Load(record)
	require.NoError(t, err)
	assert.Len(t, ds, 3, "a, b, and b's rewritten brief; c's failure recorded nothing")
	assert.Equal(t, "b", ds[2].Inputs["card"])
}

// A card's end attaches to its newest brief: landed at attempt 1, reworked after,
// dropped; a card with no brief, or no record at all, attaches nothing and makes no
// file; a card already labelled otherwise is that card's conflict and the rest are
// attached. The calibration of converges reads the attached ends.
func TestAttachBriefsAttachesEachCardsEnd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	none := filepath.Join(dir, "none.jsonl")
	n, failed, err := AttachBriefs(none, map[string]End{"a": {BriefLanded, ""}}, at)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, failed)
	assert.NoFileExists(t, none, "attaching to no record makes none")

	record := filepath.Join(dir, "brief.jsonl")
	cards := map[string]string{"a": "converges=0.9", "b": "converges=0.4", "c": "converges=0.2"}
	_, err = Briefs(context.Background(), &briefer{}, cards, record, at, 4, 0)
	require.NoError(t, err)
	_, err = Briefs(context.Background(), &briefer{}, map[string]string{"c": "c rewritten converges=0.3"}, record, at, 4, 0)
	require.NoError(t, err)
	ends := map[string]End{"c": {BriefDropped, "obsolete"}, "zz": {BriefDropped, ""}}
	for card, attempt := range map[string]string{"a": "1", "b": "3"} {
		label, note := LandLabel(attempt)
		ends[card] = End{label, note}
	}
	n, failed, err = AttachBriefs(record, ends, at)
	require.NoError(t, err)
	assert.Equal(t, 3, n, "a, b and c; zz has no brief decision")
	assert.Empty(t, failed)
	n, failed, err = AttachBriefs(record, map[string]End{"a": {BriefDropped, ""}, "b": {BriefReworked, "landed at attempt 3"}}, at)
	require.NoError(t, err)
	assert.Zero(t, n, "the same label again changes nothing")
	var conflict *ConflictError
	assert.ErrorAs(t, failed["a"], &conflict, "a card's end is attached once")

	ds, err := Load(record)
	require.NoError(t, err)
	require.Len(t, ds, 4)
	assert.Equal(t, "landed at attempt 3", ds[1].Outcome.Note)
	assert.Nil(t, ds[2].Outcome, "c's first brief is not its newest")
	assert.Equal(t, BriefDropped, ds[3].Outcome.Label)
	cal, err := Calibrate(ds, BriefName, "converges", []string{BriefLanded}, []string{BriefReworked, BriefDropped})
	require.NoError(t, err)
	assert.InDelta(t, 1.0, cal.AUC(), 1e-9)
	assert.Equal(t, Bar{At: 0.9, Caught: 1, Bounced: 0}, cal.CatchAll())
}
