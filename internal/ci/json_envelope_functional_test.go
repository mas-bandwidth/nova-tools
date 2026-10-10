//go:build functional

package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/require"
)

// json_envelope_functional_test.go builds every command and runs each with --json
// to check that the output is exactly one JSON object whose result.status and
// result.exit match the process exit. It is the class test for the rule
// docs/STANDARD.md section 2: one output structure, two renderings.

// jsonEnvelopeLedgerPath is the shrink-only counted package ledger of the
// json-envelope violations: one shard per tool at
// `testdata/json-envelope/cmd/<tool>.txt`, a row `cmd/<tool>:<kind> <count> <why>`.
const jsonEnvelopeLedgerPath = "testdata/json-envelope"

// The kinds of invocation the walk judges.
const (
	jsonEnvelopeBare       = "bare"          // the bare command with --json
	jsonEnvelopeVerb       = "unknown-verb"  // an unknown verb with --json
	jsonEnvelopeFlag       = "unknown-flag"  // an unknown flag on a verb with --json
	jsonEnvelopeNoArg      = "verb-no-flags" // a verb run with no flags and --json
	jsonEnvelopeTranscript = "transcript"    // a documented transcript command with --json
)

// jsonEnvelopeRemedy is the remedy line for json-envelope violations.
const jsonEnvelopeRemedy = "ensure the --json output is exactly one object with result.status and result.exit matching the exit"

// TestJsonEnvelopeClassRuleHoldsOverTheRepository walks every built tool with
// --json to verify the output envelope matches the contract.
func TestJsonEnvelopeClassRuleHoldsOverTheRepository(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	found := 0
	ledger, err := allowlist.LoadPackages(jsonEnvelopeLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)

	sites := map[string][]string{}
	addSite := func(pkg, where, problem string) {
		key := pkg + ":json"
		sites[key] = append(sites[key], where+": "+problem+"; to clear it: "+jsonEnvelopeRemedy)
	}

	// Test all tools in parallel but collect results sequentially
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tool := e.Name()
		bin := buildTool(t, root, tool)

		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			found++

			// Test bare command with --json
			_, stdout, stderr := runIn(t, bin, "--json")
			checkJsonEnvelope(t, "cmd/"+tool+":bare", stdout, stderr, addSite)

			// Test unknown verb with --json
			_, stdout, stderr = runIn(t, bin, "--json", noSuchVerb)
			checkJsonEnvelope(t, "cmd/"+tool+":unknown-verb", stdout, stderr, addSite)

			// Test verbs if any
			_, banner, _ := runBare(t, root, tool, bin, []string{"help"})
			verbs := usageVerbs(tool, banner)
			if len(verbs) > 0 {
				// Test first verb with unknown flag
				args := append([]string{"--json"}, strings.Fields(verbs[0])...)
				args = append(args, noSuchFlag)
				_, stdout, stderr = runIn(t, bin, args...)
				checkJsonEnvelope(t, "cmd/"+tool+":unknown-flag", stdout, stderr, addSite)

				// Test verb with no flags
				args = append([]string{"--json"}, strings.Fields(verbs[0])...)
				_, stdout, stderr = runIn(t, bin, args...)
				checkJsonEnvelope(t, "cmd/"+tool+":verb-no-flags", stdout, stderr, addSite)
			}
		})
	}

	require.NotZero(t, found, "no command directories found under cmd/")

	// Check ledger against collected sites
	update := allowlist.Updating()
	for key, problemList := range sites {
		rowCount := 0
		if row, listed := ledger.Get(key); listed {
			rowCount = row.Count
		}
		siteCount := len(problemList)

		if !listed {
			addSite(key, "", "no ledger row for this tool; the ledger gains no row")
		} else if siteCount > rowCount {
			addSite(key, "", "over its ledger count; the count only falls")
		} else if siteCount < rowCount {
			if !update {
				addSite(key, "", "the ledger has entries but the tree has none; delete the row")
			}
		}
	}

	// Fail if there are any problems
	if len(sites) > 0 {
		for key, problems := range sites {
			for _, p := range problems {
				t.Errorf("%s: %s", key, p)
			}
		}
	}
}

// checkJsonEnvelope verifies that the output is exactly one JSON object with
// correct result.status and result.exit fields.
func checkJsonEnvelope(t *testing.T, key, stdout, stderr string, addSite func(string, string, string)) {
	t.Helper()

	// For errors/refusals, output goes to stderr
	output := stdout
	if output == "" {
		output = stderr
	}

	// Output should be one JSON object
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(output), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}

	if len(lines) != 1 {
		addSite(key, "output", fmt.Sprintf("expected exactly one JSON line, got %d", len(lines)))
		return
	}

	// Parse the JSON
	var result struct {
		Result struct {
			Verb   string `json:"verb"`
			Status string `json:"status"`
			Exit   int    `json:"exit"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(lines[0]), &result); err != nil {
		addSite(key, "parse", "failed to parse JSON: "+err.Error())
		return
	}

	// Check that status and exit match
	// Exit 0 -> status ok
	// Exit 1 -> status failed
	// Exit 2 -> status refused
	expectedStatus := ""
	switch result.Result.Exit {
	case 0:
		expectedStatus = "ok"
	case 1:
		expectedStatus = "failed"
	case 2:
		expectedStatus = "refused"
	}

	if result.Result.Status != expectedStatus {
		addSite(key, "status", fmt.Sprintf("result.status=%s but expected %s for exit %d", result.Result.Status, expectedStatus, result.Result.Exit))
	}
}
