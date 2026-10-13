package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCairnIndexSinceCursor verifies --since filters index entries strictly
// after the cursor, by entry id and by RFC3339 timestamp.
func TestCairnIndexSinceCursor(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	c.ok("open", "--session", "s1", "--publish", "manual", "--now", "2026-01-01T00:00:00Z")
	c.ok("append", "--session", "s1", "--entry", "e1", "--text", "first", "--now", "2026-01-01T01:00:00Z")
	c.ok("append", "--session", "s1", "--entry", "e2", "--text", "second", "--now", "2026-01-01T02:00:00Z")
	c.ok("append", "--session", "s1", "--entry", "e3", "--text", "third", "--now", "2026-01-01T03:00:00Z")

	// By entry id: strictly after e1.
	out := c.ok("index", "--since", "e1")
	require.Contains(t, out, "entry=e2")
	require.Contains(t, out, "entry=e3")
	require.NotContains(t, out, "entry=e1")
	require.Contains(t, out, "entries=2")

	// By RFC3339: strictly after 02:00.
	out = c.ok("index", "--since", "2026-01-01T02:00:00Z")
	require.Contains(t, out, "entry=e3")
	require.NotContains(t, out, "entry=e2")
	require.Contains(t, out, "entries=1")

	// JSON mode carries the filtered entries.
	res := c.run("index", "--since", "e1", "--json")
	require.Zero(t, res.Code, res.Stderr)
	var payload struct {
		Facts map[string]any   `json:"facts"`
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &payload), res.Stdout)
	require.EqualValues(t, 2, payload.Facts["entries"])

	// Unknown cursor refuses at exit 2.
	res = c.run("index", "--since", "no-such-entry")
	require.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
}
