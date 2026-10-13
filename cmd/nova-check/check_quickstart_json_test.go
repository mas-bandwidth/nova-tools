package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckQuickstartEmitsCompositeJson verifies quickstart --json emits one
// composite JSON object with both checks, counts and exit consistency.
func TestCheckQuickstartEmitsCompositeJson(t *testing.T) {
	t.Parallel()

	clean := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(clean, "a.md"), []byte("# ok\n"), 0o644))
	code, stdout, _ := runCheck(t, "quickstart", "--dir", clean, "--json")
	require.Equal(t, 0, code, stdout)
	var payload struct {
		Verb   string `json:"verb"`
		Status string `json:"status"`
		Exit   int    `json:"exit"`
		Dir    string `json:"dir"`
		Checks []struct {
			Name string `json:"name"`
			Exit int    `json:"exit"`
		} `json:"checks"`
		Failed []string `json:"failed"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload), stdout)
	require.Equal(t, "quickstart", payload.Verb)
	require.Equal(t, "ok", payload.Status)
	require.Equal(t, 0, payload.Exit)
	require.Len(t, payload.Checks, 2)
	require.Equal(t, "links", payload.Checks[0].Name)
	require.Equal(t, "nocode", payload.Checks[1].Name)
	require.Empty(t, payload.Failed)

	broken := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(broken, "a.md"), []byte("[x](missing.md)\n"), 0o644))
	code, stdout, _ = runCheck(t, "quickstart", "--dir", broken, "--json")
	require.Equal(t, 1, code, stdout)
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload), stdout)
	require.Equal(t, "failed", payload.Status)
	require.Equal(t, 1, payload.Exit)
	require.Contains(t, payload.Failed, "links")

	// Text mode unchanged.
	code, stdout, _ = runCheck(t, "quickstart", "--dir", clean)
	require.Equal(t, 0, code)
	require.Contains(t, stdout, "QUICKSTART OK")
}
