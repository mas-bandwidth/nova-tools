package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
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

// TestRoutePricesDailyLoopRefreshesEveryProviderWithAList pins the daily loop row
// (docs/SPEC-CONFIG.md, "route prices") to the verb's default read. The reader
// found the row passing --provider openrouter, which PlanPriceRefresh filters to,
// so the opencode rows the list is assumed for were never refreshed and could go
// stale indefinitely; the argv names no --provider, so every provider with a list
// is read.
func TestRoutePricesDailyLoopRefreshesEveryProviderWithAList(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-CONFIG.md"))
	require.NoError(t, err)
	row := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "loop add route-prices") {
			row = line
			break
		}
	}
	require.NotEmpty(t, row, "docs/SPEC-CONFIG.md names the daily route-prices loop row")
	assert.Contains(t, row, `"--refresh"`)
	assert.Contains(t, row, "--every 86400")
	assert.NotContains(t, row, "--provider", "the daily loop refreshes every provider with a list; --provider would skip the opencode rows")
}
