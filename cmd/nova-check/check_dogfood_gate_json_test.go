package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckDogfoodGateEmitsStructuredJson verifies dogfood gate --json emits
// structured gate status queryable by automated tooling.
func TestCheckDogfoodGateEmitsStructuredJson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "ran it", "issue": 0,
	})
	authors := writeAuthors(t, dir, "nova-example links = Rowan\nnova-example quickstart = Rowan\nnova-example nocode = Rowan\n")

	// Passing gate with --require-all=false: only links covered, gate passes
	// without require-all (findings only from open edges / unmatched).
	code, stdout, _ := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--authors", authors, "--json")
	require.Equal(t, 0, code, stdout)
	var payload struct {
		Verb    string `json:"verb"`
		Status  string `json:"status"`
		Exit    int    `json:"exit"`
		Passed  bool   `json:"passed"`
		Summary struct {
			Verbs       int `json:"verbs"`
			Dogfooded   int `json:"dogfooded"`
			ByNonAuthor int `json:"by_nonauthor"`
			OpenEdges   int `json:"open_edges"`
			Unmatched   int `json:"unmatched"`
		} `json:"summary"`
		Findings []struct {
			Kind string `json:"kind"`
			Tool string `json:"tool"`
			Verb string `json:"verb"`
		} `json:"findings"`
		Uncovered []struct {
			Tool string `json:"tool"`
			Verb string `json:"verb"`
		} `json:"uncovered"`
		Receipts int `json:"receipts"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload), stdout)
	require.Equal(t, "dogfood gate", payload.Verb)
	require.True(t, payload.Passed)
	require.Equal(t, 3, payload.Summary.Verbs)
	require.Equal(t, 1, payload.Receipts)

	// With --require-all, uncovered verbs appear and gate fails exit 1.
	code, stdout, _ = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--authors", authors, "--require-all", "--json")
	require.Equal(t, 1, code, stdout)
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload), stdout)
	require.False(t, payload.Passed)
	require.NotEmpty(t, payload.Uncovered)

	// Text mode unchanged.
	code, stdout, _ = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--authors", authors)
	require.Equal(t, 0, code)
	require.Contains(t, stdout, "DOGFOOD GATE OK")
}
