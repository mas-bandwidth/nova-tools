package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportJSONNamesAnUnpricedCostAsNull pins the two renderings of a cost no source
// reported (docs/STANDARD.md section 2: one value, two renderings; rule 15: an absence is
// a dash, never 0). The TEXT line keeps usd=- and usd_per_mtok=-; the SAME value in --json
// is null, never the string "-" and never a zero, the way counts.go renders an absent
// count. A cost a source did report keeps its value in both.
func TestReportJSONNamesAnUnpricedCostAsNull(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := reposFile(t, dir)
	pool := mkdir(t, filepath.Join(dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRowCost("j1", "1", "-", "deepseek", "deepseek-v3", "schema", "2026-09-11T10:00:00Z", "100", "100", "-", "-", "-", "0.02"))
	swarmUsage(t, pool, "j2", swarmRowCost("j2", "1", "-", "openai", "gpt-4o", "schema", "2026-09-11T11:00:00Z", "1000", "200", "-", "-", "-", "-"))
	swarmUsage(t, pool, "j3", swarmRowCost("j3", "1", "-", "openai", "gpt-4o", "schema", "2026-09-12T11:00:00Z", "1000", "200", "-", "-", "-", "-"))

	text := invoke(t, "report", "--who", "friend-c", "--day", "2026-09-11", "--repos", repos, "--swarm", "pool="+pool)
	wantExit(t, text, 0)
	wantContains(t, lineWith(text.stderr, "TOKENS AVG day=2026-09-11 model=openai/gpt-4o"), "usd=- usd_per_mtok=-")

	_, got := asJSON(t, "report", "--who", "friend-c", "--day", "2026-09-11", "--repos", repos, "--swarm", "pool="+pool, "--json")
	unpriced := avgItem(t, got, "openai/gpt-4o")
	assert.Nil(t, unpriced["usd"], "an unpriced cost is null in the object, never - and never 0")
	assert.Nil(t, unpriced["usd_per_mtok"], "an unpriced rate is null in the object")
	priced := avgItem(t, got, "deepseek/deepseek-v3")
	assert.Equal(t, "0.02", priced["usd"], "a cost a source reported keeps its value")

	// A day whose only model is unpriced: the one AVG-ALL line is null in the object too.
	_, all := asJSON(t, "report", "--who", "friend-c", "--day", "2026-09-12", "--repos", repos, "--swarm", "pool="+pool, "--json")
	assert.Nil(t, avgItem(t, all, "openai/gpt-4o")["usd"], "an unpriced cost is null on any day")
	total := itemFields(t, all, "avg-all")
	assert.Nil(t, total["usd"], "AVG-ALL over only unpriced models is null")
	assert.Nil(t, total["usd_per_mtok"], "AVG-ALL's rate over only unpriced models is null")
}

// avgItem is the fields of the avg item for one model, or a failure when the object
// carries none: a type assertion over a missing row would pass by checking nothing.
func avgItem(t *testing.T, j jsonResult, model string) map[string]any {
	t.Helper()
	for _, it := range j.Items {
		if it.Kind == "avg" && it.Fields["model"] == model {
			return it.Fields
		}
	}
	require.FailNow(t, "no avg item for the model", "the --json object carries no avg item for %q: %+v", model, j.Items)
	return nil
}
