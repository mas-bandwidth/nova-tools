package decide

import (
	"bufio"
	"encoding/json"
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

// calItem is one judgment of the calibration fixture: what the coordinator answered and
// what became of the card.
type calItem struct {
	ID      string   `json:"id"`
	Card    string   `json:"card"`
	Kind    string   `json:"kind"`
	Type    string   `json:"type"`
	Allowed []string `json:"allowed"`
	Answer  string   `json:"coordinator_answer"`
	Verb    string   `json:"coordinator_verb"`
	Outcome string   `json:"outcome"` // landed, dropped, came-back, open
}

func readCal(t *testing.T) []calItem {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "judgment-calibration.jsonl"))
	require.NoError(t, err)
	defer f.Close()
	var out []calItem
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var it calItem
		dec := json.NewDecoder(strings.NewReader(sc.Text()))
		dec.DisallowUnknownFields()
		require.NoError(t, dec.Decode(&it), sc.Text())
		out = append(out, it)
	}
	require.NoError(t, sc.Err())
	return out
}

// agreementTables is the calibration's two tables as SPEC-NOVA-DECIDE section 9 prints
// them: by kind, how often the judgment decision chose the coordinator's verb and what it
// applies at the bar; by the card's outcome, how often it agreed.
func agreementTables(items []calItem, ds []Decision, bar float64) string {
	type row struct{ n, agree, applied, appliedAgree int }
	byKind, byOutcome := map[string]*row{}, map[string]*row{}
	total := &row{}
	for _, it := range items {
		d := Find(ds, it.ID)
		if d == nil {
			continue
		}
		agree := d.Answers["verb"].Value == it.Verb
		applied := Choose(d.Answers, it.Allowed, bar).Act == ActApply
		if byKind[it.Kind] == nil {
			byKind[it.Kind] = &row{}
		}
		if byOutcome[it.Outcome] == nil {
			byOutcome[it.Outcome] = &row{}
		}
		for _, r := range []*row{byKind[it.Kind], byOutcome[it.Outcome], total} {
			r.n++
			if agree {
				r.agree++
			}
			if applied {
				r.applied++
				if agree {
					r.appliedAgree++
				}
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "| kind | judgments | the coordinator's verb | applied at %.1f | of those, the coordinator's verb |\n| --- | --- | --- | --- | --- |\n", bar)
	line := func(name string, r *row) {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", name, r.n, r.agree, r.applied, r.appliedAgree)
	}
	for _, k := range slices.Sorted(maps.Keys(byKind)) {
		line(k, byKind[k])
	}
	line("all", total)
	b.WriteString("\n| the card's outcome | judgments | the coordinator's verb |\n| --- | --- | --- |\n")
	for _, o := range []string{OutcomeLanded, OutcomeDropped, OutcomeCameBack, "open"} {
		if r := byOutcome[o]; r != nil {
			fmt.Fprintf(&b, "| %s | %d | %d |\n", o, r.n, r.agree)
		}
	}
	return b.String()
}

// The calibration fixture is the coordinator's own answers to 100 judgments, and the
// record is Jev's answers to the same states, under this schema, each labelled with the
// card's outcome; the agreement SPEC-NOVA-DECIDE section 9 states is recomputed here from
// the two, so a changed schema, fixture or record turns this red until the spec says the
// new numbers.
func TestJudgmentCalibrationAgreement(t *testing.T) {
	t.Parallel()
	items := readCal(t)
	require.Len(t, items, 100)
	ds, err := Load(filepath.Join("testdata", "judgment-record.jsonl"))
	require.NoError(t, err)
	require.Len(t, ds, len(items), "every fixture judgment has Jev's decision")
	for _, it := range items {
		d := Find(ds, it.ID)
		require.NotNil(t, d, it.ID)
		assert.Equal(t, JudgmentSchema().Hash(), d.Schema, "%s was asked under another schema: regenerate the record", it.ID)
		assert.Contains(t, Verbs, it.Verb, it.ID)
		assert.Equal(t, it.Kind, Kinds[it.Type], it.ID)
		if it.Outcome != "open" {
			require.NotNil(t, d.Outcome, it.ID)
			assert.Equal(t, it.Outcome, d.Outcome.Label, it.ID)
		}
	}
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	tables := agreementTables(items, ds, 0.8)
	assert.True(t, strings.Contains(string(spec), tables), "SPEC-NOVA-DECIDE section 9 states the calibration as the record computes it:\n%s", tables)
}
