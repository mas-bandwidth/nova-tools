package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCairnReceiptJSONIncludesText: card cairn-receipt-json-includes-text.
// receipt --json carries the stored words by default; plain text without
// --text is unchanged.
func TestCairnReceiptJSONIncludesText(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	prose := "the words to keep"
	c.ok("append", "--session", "s", "--entry", "e", "--text", prose, "--publish", "manual")

	result := c.run("receipt", "--session", "s", "--entry", "e", "--json")
	require.Zero(t, result.Code, result.Stderr)
	var out struct {
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &out), result.Stdout)
	require.Equal(t, prose, out.Facts["text"], "JSON receipt must include text by default: %s", result.Stdout)
	require.Equal(t, float64(len(prose)), out.Facts["bytes"], "byte count unchanged: %s", result.Stdout)

	plain := c.ok("receipt", "--session", "s", "--entry", "e")
	require.NotContains(t, plain, " text=", "plain receipt without --text remains unchanged")
}
