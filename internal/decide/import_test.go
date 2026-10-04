package decide

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC-NOVA-DECIDE section 4 (the record) and the import subsection: each item is one
// labelled record with a stable id, so a second import adds nothing.
func TestImportIsIdempotentAndCountsPerKind(t *testing.T) {
	t.Parallel()
	const td = "testdata/import/"
	rec := t.TempDir() + "/decisions.jsonl"
	src := ImportSources{
		Verdicts:  td + "verdicts/read-*/VERDICT.md",
		Judgments: td + "judgments",
		Log:       td + "log.json",
		Reports:   td + "reports/*/REPORT.md",
	}
	now := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)

	first, err := Import(rec, src, now)
	require.NoError(t, err)
	assert.Equal(t, ImportCount{New: 2}, first.Kinds[ImportVerdict])
	assert.Equal(t, ImportCount{New: 2}, first.Kinds[ImportJudgment])
	assert.Equal(t, ImportCount{New: 2}, first.Kinds[ImportReport])
	assert.Equal(t, 1, first.Unanswered, "a judgment the log has no answer for is counted, not recorded")

	ds, err := Load(rec)
	require.NoError(t, err)
	require.Len(t, ds, 6)
	labels := map[string]int{}
	for _, d := range ds {
		require.NotNil(t, d.Outcome, "%s carries its label", d.ID)
		labels[d.Decision+"="+d.Outcome.Label]++
	}
	assert.Equal(t, map[string]int{
		"import-verdict=ACCEPT": 1, "import-verdict=REWORK": 1,
		"import-judgment=ack": 1, "import-judgment=rework": 1,
		"import-report=HOLD": 2,
	}, labels)

	second, err := Import(rec, src, now.Add(time.Hour))
	require.NoError(t, err)
	for _, k := range []string{ImportVerdict, ImportJudgment, ImportReport} {
		assert.Equal(t, ImportCount{Existing: 2}, second.Kinds[k], k)
	}
	again, err := Load(rec)
	require.NoError(t, err)
	assert.Len(t, again, 6, "a second import adds nothing")
}

func TestImportRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	rec := t.TempDir() + "/decisions.jsonl"
	_, err := Import(rec, ImportSources{Judgments: "testdata/import/judgments"}, time.Time{})
	assert.ErrorContains(t, err, "--log")
	_, err = Import(rec, ImportSources{}, time.Time{})
	assert.ErrorContains(t, err, "no source")
}
