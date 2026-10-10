package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// openRouterList is a copy of OpenRouter's models endpoint as it publishes it:
// USD per token, as decimal strings; a router model priced -1 is on it too.
const openRouterList = `{"data":[
 {"id":"deepseek/deepseek-v4-flash","pricing":{"prompt":"0.0000003","completion":"0.0000012","input_cache_read":"0.000000006","request":"0","image":"0"}},
 {"id":"x-ai/grok-4","pricing":{"prompt":"0.000003","completion":"0.000015","input_cache_read":"0.00000075"}},
 {"id":"openrouter/auto","pricing":{"prompt":"-1","completion":"-1"}}
]}`

// writeList saves the list where --from reads it.
func writeList(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// The 2026-10-05 finding as a test: a flash route priced by hand at a tenth of the
// list's input price is a judgment, never set silently, and it is named stale; a
// row a little off the list is set to it with today's date and the list's URL; an
// OpenCode row is priced from OpenRouter's list and the verb says it is assumed;
// --dry-run writes nothing (docs/SPEC-CONFIG.md, "route prices").
func TestRoutePricesRefreshSetsTheListPrice(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	for _, args := range [][]string{
		{"route", "add", "flash-ds-openrouter", "--tier", "flash", "--provider", "openrouter", "--model", "deepseek/deepseek-v4-flash", "--deadline", "60",
			"--price_input", "0.29", "--price_cache_read", "0.006", "--price_output", "1.10", "--price_as_of", "2026-10-01", "--price_source", "typed"},
		{"route", "add", "flash-ds-hand", "--tier", "flash", "--provider", "openrouter", "--model", "deepseek/deepseek-v4-flash", "--deadline", "60",
			"--price_input", "0.03", "--price_cache_read", "0.006", "--price_output", "1.20"},
		{"route", "add", "flash-ds-opencode", "--tier", "flash", "--provider", "opencode", "--model", "deepseek-v4-flash", "--deadline", "60"},
		{"route", "add", "pro-gone", "--tier", "pro", "--provider", "openrouter", "--model", "nobody/none", "--deadline", "60"},
		{"route", "add", "pro-off", "--tier", "pro", "--provider", "openrouter", "--model", "x-ai/grok-4", "--deadline", "60", "--enabled", "false", "--note", "off"},
		{"route", "add", "pro-direct", "--tier", "pro", "--provider", "deepseek", "--model", "deepseek-v4", "--deadline", "60"},
	} {
		code, _, errs := h.run(t, args...)
		require.Equal(t, 0, code, errs)
	}
	from := writeList(t, openRouterList)
	const url = config.OpenRouterModelsURL

	code, out, errs := h.run(t, "route", "prices", "--refresh", "--from", from, "--dry-run")
	require.Equal(t, 1, code, "a judgment is exit 1, on a dry run too:\n%s\n%s", out, errs)
	assert.Contains(t, out, "PRICES DRY-RUN route=flash-ds-openrouter list=deepseek/deepseek-v4-flash changed=price_input:0.29->0.3,price_output:1.1->1.2\n")
	assert.Contains(t, out, "STALE route=flash-ds-hand price_input: have 0.03, the list says 0.3\n")
	for _, name := range []string{"flash-ds-openrouter", "flash-ds-opencode"} {
		_, show, _ := h.run(t, "route", "show", name)
		assert.NotContains(t, show, "price_as_of=2023-11-14", "--dry-run wrote %s", name)
	}

	code, out, errs = h.run(t, "route", "prices", "--refresh", "--from", from)
	require.Equal(t, 1, code, "a judgment is exit 1:\n%s\n%s", out, errs)
	assert.Contains(t, out, "PRICES SET route=flash-ds-openrouter list=deepseek/deepseek-v4-flash changed=price_input:0.29->0.3,price_output:1.1->1.2 rev=")
	assert.Contains(t, out, "PRICES SET route=flash-ds-opencode list=deepseek/deepseek-v4-flash changed=price_input:-->0.3,price_cache_read:-->0.006,price_output:-->1.2 rev=")
	assert.Contains(t, out, "STALE route=flash-ds-hand price_input: have 0.03, the list says 0.3\n")
	assert.Contains(t, out, "NOTE route=flash-ds-opencode provider=opencode: priced from openrouter's list, assumed until opencode publishes its own\n")
	assert.Contains(t, out, "JUDGMENT route=flash-ds-hand price_input moved past 2x: have 0.03, the list says 0.3; a price that moves that far is never set silently; "+
		"decide: nova-config route set flash-ds-hand --price_input 0.3 --price_source "+url+" --price_as_of 2023-11-14\n")
	assert.Contains(t, out, "PRICES MISSING route=pro-gone model=nobody/none: not on the list; nothing set\n")
	assert.NotContains(t, out, "pro-off", "a disabled route is left as it is")
	assert.NotContains(t, out, "pro-direct", "a provider with no list is left as it is")
	assert.True(t, strings.HasSuffix(out, "CONFIG PRICES source="+url+" as_of=2023-11-14 routes=4 set=3 same=0 missing=1 judgments=1 stale=1\n"), out)

	_, show, _ := h.run(t, "route", "show", "flash-ds-openrouter")
	assert.Contains(t, show, " price_input=0.3 price_cache_read=0.006 price_cache_write=- price_output=1.2 ")
	assert.Contains(t, show, " price_source="+url+" price_as_of=2023-11-14 ")
	_, show, _ = h.run(t, "route", "show", "flash-ds-hand")
	assert.Contains(t, show, " price_input=0.03 ", "the judgment's price is left as it was")
	assert.Contains(t, show, " price_source="+url+" price_as_of=2023-11-14 ", "the prices it set are dated")

	// a second read of the same list changes nothing and writes no revision
	code, out, _ = h.run(t, "route", "prices", "--refresh", "--from", from, "--provider", "opencode")
	require.Equal(t, 0, code, out)
	assert.Equal(t, "PRICES SAME route=flash-ds-opencode list=deepseek/deepseek-v4-flash\n"+
		"NOTE route=flash-ds-opencode provider=opencode: priced from openrouter's list, assumed until opencode publishes its own\n"+
		"CONFIG PRICES source="+url+" as_of=2023-11-14 routes=1 set=0 same=1 missing=0 judgments=0 stale=0\n", out)
}

func TestRoutePricesRefusals(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	cases := []struct {
		name string
		args []string
		code int
		errs string
	}{
		{"no --refresh", []string{"route", "prices"}, 2, "want route prices --refresh"},
		{"a provider with no list", []string{"route", "prices", "--refresh", "--provider", "deepseek"}, 2, "deepseek publishes no list this verb reads; the lists: openrouter, opencode (assumed from openrouter)"},
		{"a list that is not the list", []string{"route", "prices", "--refresh", "--from", writeList(t, `{"data":[]}`)}, 2, "the models list holds no priced model"},
		{"a list file not there", []string{"route", "prices", "--refresh", "--from", filepath.Join(t.TempDir(), "none.json")}, 2, "none.json"},
	}
	for _, tc := range cases {
		code, out, errs := h.run(t, tc.args...)
		assert.Equal(t, tc.code, code, tc.name)
		assert.Empty(t, out, tc.name)
		assert.Contains(t, errs, tc.errs, tc.name)
		assert.Equal(t, 1, strings.Count(errs, "\n"), "%s: one refusal line", tc.name)
	}
}

// The reader's finding, 2026-10-06, as a test: the loop row docs/SPEC-CONFIG.md
// gives for the daily refresh passed --provider openrouter, and a refresh given
// the flag reads only that provider's rows (PlanPriceRefresh filters on it), so
// the opencode rows, priced from OpenRouter's list until OpenCode publishes its
// own, were never refreshed. The documented row passes no --provider: the
// default refreshes every provider with a list (docs/SPEC-CONFIG.md, "route
// prices").
func TestRoutePricesLoopRowRefreshesEveryProviderWithAList(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/SPEC-CONFIG.md")
	require.NoError(t, err)
	doc := string(raw)

	// the row the doc gives for the daily run, up to the row's closing backtick
	start := strings.Index(doc, "nova-config loop add route-prices")
	require.NotEqual(t, -1, start, "the spec gives the daily loop row")
	row := doc[start:]
	if end := strings.Index(row, "`"); end != -1 {
		row = row[:end]
	}
	_, quoted, ok := strings.Cut(row, "'")
	require.True(t, ok, "the loop row's argv is a JSON array in quotes")
	rawArgv, after, ok := strings.Cut(quoted, "'")
	require.True(t, ok, "the loop row's argv closes its quote")
	var argv []string
	require.NoError(t, json.Unmarshal([]byte(rawArgv), &argv))
	require.GreaterOrEqual(t, len(argv), 4, "the argv runs the refresh: %s", rawArgv)

	assert.Equal(t, []string{"nova-config", "route", "prices", "--refresh"}, argv[:4], "the daily row runs the refresh")
	assert.NotContains(t, argv, "--provider", "the daily row refreshes every provider with a list: a --provider filter reads only that provider's rows and leaves the rest stale")
	assert.Contains(t, after, "--every 86400", "the row runs once a day")
}
