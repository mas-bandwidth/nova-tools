//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exit_word_functional_test.go builds every command and associates each executed
// command and each tool's transcripts in docs/TESTS.md with its captured exit status
// and last status-bearing output line, checking them against the status contract.
// It needs no store and starts no server; the builds are the functional tier's.

// TestEveryExitWordMatchesStatusContract walks every built tool: bare, with an
// unknown verb, with an unknown flag on a verb, each verb with no flags, and each
// command in its docs/TESTS.md transcript.
// The ledger is checked only when every tool ran.
func TestEveryExitWordMatchesStatusContract(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	transcripts := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
	exitWord := newExitWord(t)
	found := 0
	t.Cleanup(func() { exitWord.check(t, found) })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found++
		tool := e.Name()
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			exitWord.begin()
			bin := buildTool(t, root, tool)
			_, banner, helpErr := runBare(t, root, tool, bin, []string{"help"})
			require.Empty(t, helpErr, "`%s help` wrote to stderr: %s", tool, helpErr)
			verbs := usageVerbs(tool, banner)
			toolTable := parseExitTable(banner)
			verbTables := make(map[string][]int, len(verbs))
			for _, v := range verbs {
				verbTables[v] = verbExitTable(t, bin, v, toolTable)
			}

			code, out, errs := runIn(t, bin)
			exitWord.short(tool, exitWordBare, tool, exitWordAnswers(code, out, errs, toolTable))

			code, out, errs = runIn(t, bin, noSuchVerb)
			exitWord.short(tool, exitWordVerb, tool+" "+noSuchVerb, exitWordAnswers(code, out, errs, toolTable))

			if len(verbs) > 0 {
				args := append(strings.Fields(verbs[0]), noSuchFlag)
				code, out, errs = runIn(t, bin, args...)
				exitWord.short(tool, exitWordFlag, tool+" "+verbs[0]+" "+noSuchFlag, exitWordAnswers(code, out, errs, verbTables[verbs[0]]))
			}
			for _, v := range verbs {
				code, out, errs := runIn(t, bin, strings.Fields(v)...)
				exitWord.short(tool, exitWordNoArg, tool+" "+v, exitWordAnswers(code, out, errs, verbTables[v]))
			}

			if lines, err := onboarding.FirstRun(transcripts, tool); err == nil && len(lines) > 0 {
				if steps, err := onboarding.Steps(tool, lines); err == nil {
					for _, s := range steps {
						table := toolTable
						longestMatch := -1
						for _, v := range verbs {
							vf := strings.Fields(v)
							if len(s.Args) >= len(vf) && slices.Equal(s.Args[:len(vf)], vf) {
								if len(vf) > longestMatch {
									longestMatch = len(vf)
									table = verbTables[v]
								}
							}
						}
						code, out, errs := runIn(t, bin, s.Args...)
						exitWord.short(tool, exitWordTranscript, s.Line, exitWordAnswers(code, out, errs, table))
					}
				}
			}
			exitWord.settle()
		})
	}
	require.NotZero(t, found, "no command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
}

// verbExitTable reads a verb's help via `<verb> -h` (or `<tool> help <verb>`
// when -h is refused), parses its published exit table, and falls back to the
// tool's table when the verb states none.
func verbExitTable(t *testing.T, bin, verb string, fallback []int) []int {
	t.Helper()
	args := append(strings.Fields(verb), "-h")
	code, out, _ := runIn(t, bin, args...)
	if code != 0 || strings.TrimSpace(out) == "" {
		code, out, _ = runIn(t, bin, append([]string{"help"}, strings.Fields(verb)...)...)
	}
	if code == 0 && strings.TrimSpace(out) != "" {
		if vt := parseExitTable(out); len(vt) > 0 {
			return vt
		}
	}
	return fallback
}

// exitWordLedgerPath is the shrink-only counted package ledger of the
// exit-word violations: one shard per tool at
// `testdata/exit-word/cmd/<tool>.txt`, a row `cmd/<tool>:<kind> <count> <why>`.
const exitWordLedgerPath = "testdata/exit-word"

// The kinds of invocation the walk judges.
const (
	exitWordBare       = "bare"          // the bare command
	exitWordVerb       = "unknown-verb"  // an unknown verb
	exitWordFlag       = "unknown-flag"  // an unknown flag on a verb
	exitWordNoArg      = "verb-no-flags" // a verb run with no flags
	exitWordTranscript = "transcript"    // a documented transcript command
)

// exitWordRemedy is the remedy line for exit-word violations.
const exitWordRemedy = "align the exit code with the status word (OK→0, FAILED→1, REFUSED→2) or add it to the verb's exit table"

// exitWord is one walk's measure against its package ledger, shared by the
// walk's parallel subtests and checked once they have all finished.
type exitWord struct {
	mu             sync.Mutex
	ledger         *siteLedger
	begun, settled int
}

func newExitWord(t *testing.T) *exitWord {
	t.Helper()
	allow, err := allowlist.LoadPackages(exitWordLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	requireReasons(t, allow)
	return &exitWord{ledger: &siteLedger{path: exitWordLedgerPath, allow: allow, sites: map[string][]string{}}}
}

func (w *exitWord) begin() { w.mu.Lock(); w.begun++; w.mu.Unlock() }

func (w *exitWord) settle() { w.mu.Lock(); w.settled++; w.mu.Unlock() }

func (w *exitWord) short(tool, kind, where, problem string) {
	if problem == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ledger.add("cmd/"+tool+":"+kind, where+": "+problem+"; to clear it: "+exitWordRemedy)
}

func (w *exitWord) check(t *testing.T, tools int) {
	t.Helper()
	if w.begun != tools || w.settled != tools {
		t.Logf("exit-word: %d of %d tools measured; the ledger is checked only when every tool is", w.settled, tools)
		return
	}
	for _, v := range w.ledger.violations(t, "a tool's last status word and exit code agree (a row's count only falls)") {
		assert.Fail(t, v)
	}
}
