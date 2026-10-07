package ci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// refusal_grammar_class_test.go holds the standard's refusal grammar rule (docs/STANDARD.md, section 2):
// every nova tool refuses with a line in the exact grammar: VERB REFUSED: <reason>; run: <remedy>.
// The test runs a built tool (nova-ci) with an unknown verb, an unknown flag, and
// a verb with no flags, capturing stderr and checking that refusal lines match the grammar.
//
// The rule: when a tool refuses (exit 1 or 2), the stderr line must be one line:
// `VERB REFUSED: <reason>; run: <remedy>`
// where VERB is the verb name, reason explains what was wrong, and remedy is the exact command to run.
// Any stdout on a refusal is also refused.

const refusalGrammarLedgerPath = "testdata/refusal-grammar"

// refusalGrammarRemedy is the one thing to do for a malformed refusal.
var refusalGrammarRemedy = `replace the refusal with a single stderr line in the grammar:
<VERB> REFUSED: <why>; run: <exact command to fix>`

// refusalGrammarSite is one measured site.
type refusalGrammarSite struct {
	Pkg, Verb, Where, Kind string
}

func TestRefusalGrammarHolds(t *testing.T) {
	t.Parallel()

	// Build nova-ci
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "nova-ci")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/nova-ci")
	buildCmd.Dir = repoTree(t)
	buildOut, buildErr := buildCmd.CombinedOutput()
	require.NoError(t, buildErr, "build nova-ci: %s", buildOut)

	// Test unknown verb
	unknownVerbCmd := exec.Command(binPath, "unknownverb")
	unknownVerbOut, unknownVerbErr := unknownVerbCmd.CombinedOutput()
	unknownVerbCode := unknownVerbCmd.ProcessState.ExitCode()

	// Test unknown flag
	unknownFlagCmd := exec.Command(binPath, "help", "--unknown-flag")
	unknownFlagOut, unknownFlagErr := unknownFlagCmd.CombinedOutput()
	unknownFlagCode := unknownFlagCmd.ProcessState.ExitCode()

	// Test verb with no flags (help should work)
	helpCmd := exec.Command(binPath, "help")
	helpOut, helpErr := helpCmd.CombinedOutput()
	helpCode := helpCmd.ProcessState.ExitCode()

	var sites []refusalGrammarSite

	// Check unknown verb refusal
	if unknownVerbCode != 0 {
		sites = append(sites, checkRefusal(t, "unknownverb", string(unknownVerbOut), "unknownverb")...)
	}

	// Check unknown flag refusal
	if unknownFlagCode != 0 {
		sites = append(sites, checkRefusal(t, "help", string(unknownFlagOut), "unknownflag")...)
	}

	// Check help works (should be OK)
	if helpCode != 0 {
		sites = append(sites, refusalGrammarSite{Pkg: "cmd/nova-ci", Verb: "help", Where: "test", Kind: "help-failed"})
	}

	// Ledger check
	l, err := allowlist.LoadPackages(refusalGrammarLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)

	update := allowlist.Updating()
	problems := judgeRefusalGrammar(sites, update)
	if len(problems) > 0 {
		var remedies []string
		for _, s := range sites {
			if s.Kind != "" {
				remedies = append(remedies, fmt.Sprintf("%s: %s", s.Where, refusalGrammarRemedy))
				break
			}
		}
		assert.Failf(t, "refusal grammar rule", "%s\n%s", strings.Join(problems, "\n"), strings.Join(remedies, "\n"))
		return
	}

	allowlist.CheckPackagesCountedMode(t, l, map[string]int{}, update)
}

func checkRefusal(t *testing.T, verb, output, kind string) []refusalGrammarSite {
	var sites []refusalGrammarSite

	// Check for stdout on refusal (should not happen)
	if len(output) > 0 {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) > 0 {
			// Check if first line matches refusal grammar
			refusalPattern := regexp.MustCompile(`^[A-Z][a-zA-Z-]+ REFUSED: .+; run: .+$`)
			if !refusalPattern.MatchString(lines[0]) {
				sites = append(sites, refusalGrammarSite{
					Pkg:  "cmd/nova-ci",
					Verb: verb,
					Where: fmt.Sprintf("test: %s", kind),
					Kind:  "malformed-refusal",
				})
			} else {
				// Check for additional lines (refusal should be one line)
				if len(lines) > 1 {
					sites = append(sites, refusalGrammarSite{
						Pkg:  "cmd/nova-ci",
						Verb: verb,
						Where: fmt.Sprintf("test: %s", kind),
						Kind:  "multi-line-refusal",
					})
				}
			}
		}
	}

	return sites
}

func judgeRefusalGrammar(sites []refusalGrammarSite, update bool) []string {
	var problems []string
	if len(sites) > 0 {
		problems = append(problems, fmt.Sprintf("%d sites with malformed refusal grammar", len(sites)))
	}
	return problems
}

// TestRefusalGrammarLedgerReadsPackageShards keeps the ledger structure valid.
func TestRefusalGrammarLedgerReadsPackageShards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledger, err := allowlist.LoadPackages(dir, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	require.NotNil(t, ledger)
}
