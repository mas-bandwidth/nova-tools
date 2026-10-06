package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOpenRouterModelsJSON = `{
  "data": [
    {
      "id": "deepseek/deepseek-v4.1-flash",
      "pricing": {
        "prompt": "0.00000030",
        "completion": "0.00000120",
        "input_cache_read": "0.000000006"
      }
    },
    {
      "id": "x-ai/grok-4",
      "pricing": {
        "prompt": "0.00000300",
        "completion": "0.00001500"
      }
    }
  ]
}`

func TestRoutePricesRefreshSetsTheListPrice(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	h.transport = fakeOpenRouterResponse(testOpenRouterModelsJSON)

	// Add routes:
	// flash-deepseek41-openrouter: enabled
	// flash-deepseek41-opencode: enabled
	// held-openrouter: disabled
	code, _, errs := h.run(t, "route", "add", "flash-deepseek41-openrouter", "--tier", "flash", "--provider", "openrouter", "--model", "deepseek/deepseek-v4.1-flash", "--deadline", "600")
	require.Equal(t, 0, code, errs)

	code, _, errs = h.run(t, "route", "add", "flash-deepseek41-opencode", "--tier", "flash", "--provider", "opencode", "--model", "deepseek-v4.1-flash", "--deadline", "600")
	require.Equal(t, 0, code, errs)

	code, _, errs = h.run(t, "route", "add", "held-openrouter", "--tier", "flash", "--provider", "openrouter", "--model", "x-ai/grok-4", "--deadline", "600", "--enabled", "false", "--note", "held for test")
	require.Equal(t, 0, code, errs)

	// Dry run first: writes nothing
	code, out, errs := h.run(t, "route", "prices", "--refresh", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG DRY-RUN kind=route name=flash-deepseek41-openrouter")
	assert.Contains(t, out, "CONFIG DRY-RUN kind=route name=flash-deepseek41-opencode")
	assert.NotContains(t, out, "held-openrouter")

	// Verify route has no prices yet
	code, out, _ = h.run(t, "route", "show", "flash-deepseek41-openrouter")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "price_input=-")

	// Real refresh
	code, out, errs = h.run(t, "route", "prices", "--refresh")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG SET kind=route name=flash-deepseek41-openrouter")
	assert.Contains(t, out, "CONFIG SET kind=route name=flash-deepseek41-opencode")
	assert.Contains(t, out, "assumed from OpenRouter")

	// Verify openrouter route fields
	code, out, _ = h.run(t, "route", "show", "flash-deepseek41-openrouter")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "price_input=0.3")
	assert.Contains(t, out, "price_output=1.2")
	assert.Contains(t, out, "price_cache_read=0.006")
	assert.Contains(t, out, "price_source="+config.OpenRouterModelsURL)

	// Verify opencode route fields and note
	code, out, _ = h.run(t, "route", "show", "flash-deepseek41-opencode")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "price_input=0.3")
	assert.Contains(t, out, "price_output=1.2")
	assert.Contains(t, out, "price_cache_read=0.006")
	assert.Contains(t, out, "note=assumed\\x20from\\x20OpenRouter")

	// Verify disabled route was not refreshed
	code, out, _ = h.run(t, "route", "show", "held-openrouter")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "price_input=-")

	// Test silently-changed price over 2x:
	// Set price_input to 0.05. The list has 0.3 (0.3 / 0.05 = 6x > 2x).
	code, _, errs = h.run(t, "route", "set", "flash-deepseek41-openrouter", "--price_input", "0.05")
	require.Equal(t, 0, code, errs)

	code, out, errs = h.run(t, "route", "prices", "--refresh")
	assert.Equal(t, 1, code, "refuses price change over 2x")
	assert.True(t, strings.Contains(out, "JUDGMENT") || strings.Contains(errs, "JUDGMENT"), "judgment-shaped line")
	assert.Contains(t, errs, "price changed over 2x")

	// Verify price was NOT changed to 0.3
	code, out, _ = h.run(t, "route", "show", "flash-deepseek41-openrouter")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "price_input=0.05")
}
