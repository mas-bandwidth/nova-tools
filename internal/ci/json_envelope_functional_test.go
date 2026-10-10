//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// json_envelope_functional_test.go builds every command and runs the same walk
// exit_word_functional_test.go runs, with --json added to every invocation, so
// each command's stdout is checked against the one-object contract: exactly one
// JSON object whose result.status and result.exit agree with the process exit.
// It needs no store and starts no server.

// TestEveryJSONEnvelopeMatchesItsExit walks every built tool: bare, with an
// unknown verb, with an unknown flag on a verb, each verb with no flags, and
// each command in its docs/TESTS.md transcript, each with --json.
// The ledger is checked only when every tool ran.
func TestEveryJSONEnvelopeMatchesItsExit(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	transcripts := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
	env := newJSONEnvelope(t)
	found := 0
	t.Cleanup(func() { env.check(t, found) })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found++
		tool := e.Name()
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			env.begin()
			bin := buildTool(t, root, tool)
			_, banner, helpErr := runBare(t, root, tool, bin, []string{"help"})
			require.Empty(t, helpErr, "`%s help` wrote to stderr: %s", tool, helpErr)
			verbs := usageVerbs(tool, banner)

			code, out, _ := runIn(t, bin, "--json")
			env.short(tool, jsonEnvelopeBare, tool+" --json", code, out)

			code, out, _ = runIn(t, bin, "--json", noSuchVerb)
			env.short(tool, jsonEnvelopeVerb, tool+" --json "+noSuchVerb, code, out)

			if len(verbs) > 0 {
				args := append([]string{"--json"}, strings.Fields(verbs[0])...)
				args = append(args, noSuchFlag)
				code, out, _ = runIn(t, bin, args...)
				env.short(tool, jsonEnvelopeFlag, tool+" "+verbs[0]+" "+noSuchFlag+" --json", code, out)
			}
			for _, v := range verbs {
				args := append([]string{"--json"}, strings.Fields(v)...)
				code, out, _ := runIn(t, bin, args...)
				env.short(tool, jsonEnvelopeNoArg, tool+" "+v+" --json", code, out)
			}

			if lines, err := onboarding.FirstRun(transcripts, tool); err == nil && len(lines) > 0 {
				if steps, err := onboarding.Steps(tool, lines); err == nil {
					for _, s := range steps {
						args := append([]string{"--json"}, s.Args...)
						code, out, _ := runIn(t, bin, args...)
						env.short(tool, jsonEnvelopeTranscript, tool+" "+s.Line+" --json", code, out)
					}
				}
			}
			env.settle()
		})
	}
	require.NotZero(t, found, "no command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
}

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
const jsonEnvelopeRemedy = "print exactly one JSON object on stdout whose result.status and result.exit match the exit"

// jsonEnvelope is one walk's measure against its package ledger, shared by the
// walk's parallel subtests and checked once they have all finished.
type jsonEnvelope struct {
	mu             sync.Mutex
	ledger         *siteLedger
	begun, settled int
}

func newJSONEnvelope(t *testing.T) *jsonEnvelope {
	t.Helper()
	allow, err := allowlist.LoadPackages(jsonEnvelopeLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	requireReasons(t, allow)
	return &jsonEnvelope{ledger: &siteLedger{path: jsonEnvelopeLedgerPath, allow: allow, sites: map[string][]string{}}}
}

func (w *jsonEnvelope) begin() { w.mu.Lock(); w.begun++; w.mu.Unlock() }

func (w *jsonEnvelope) settle() { w.mu.Lock(); w.settled++; w.mu.Unlock() }

func (w *jsonEnvelope) short(tool, kind, where string, exit int, stdout string) {
	problem := jsonEnvelopeAnswers(exit, stdout)
	if problem == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ledger.add("cmd/"+tool+":"+kind, where+": "+problem)
}

func (w *jsonEnvelope) check(t *testing.T, tools int) {
	t.Helper()
	if w.begun != tools || w.settled != tools {
		t.Logf("json-envelope: %d of %d tools measured; the ledger is checked only when every tool is", w.settled, tools)
		return
	}
	for _, v := range w.ledger.violations(t, jsonEnvelopeRemedy) {
		assert.Fail(t, v)
	}
}
