//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusalgrammar_functional_test.go builds every command and runs the mistakes
// an AI makes plus each verb with no flags, then judges every refusal against
// the one grammar. It needs no store and starts no server; the builds are the
// functional tier's.

// TestEveryRefusalFollowsTheGrammar walks every built tool: bare, with an
// unknown verb, with an unknown flag on a verb, and each verb with no flags.
// Where a run refuses (its stderr holds REFUSED) the line must be
// `<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>` and stdout must be empty.
// The ledger is checked only when every tool ran.
func TestEveryRefusalFollowsTheGrammar(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	grammar := newRefusalGrammar(t)
	found := 0
	t.Cleanup(func() { grammar.check(t, found) })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tool := e.Name()
		if shimTools[tool] {
			continue
		}
		found++
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			grammar.begin()
			bin := buildTool(t, root, tool)
			_, banner, helpErr := runBare(t, root, tool, bin, []string{"help"})
			require.Empty(t, helpErr, "`%s help` wrote to stderr: %s", tool, helpErr)
			verbs := usageVerbs(tool, banner)

			code, out, errs := runIn(t, bin)
			grammar.short(tool, refuseBare, tool, refusalGrammarAnswers(code, out, errs))

			code, out, errs = runIn(t, bin, noSuchVerb)
			grammar.short(tool, refuseVerb, tool+" "+noSuchVerb, refusalGrammarAnswers(code, out, errs))

			if len(verbs) > 0 {
				args := append(strings.Fields(verbs[0]), noSuchFlag)
				code, out, errs = runIn(t, bin, args...)
				grammar.short(tool, refuseFlag, tool+" "+verbs[0]+" "+noSuchFlag, refusalGrammarAnswers(code, out, errs))
			}
			for _, v := range verbs {
				code, out, errs := runIn(t, bin, strings.Fields(v)...)
				grammar.short(tool, refuseNoArg, tool+" "+v, refusalGrammarAnswers(code, out, errs))
			}
			grammar.settle()
		})
	}
	require.NotZero(t, found, "no command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
}

// refusalGrammarLedgerPath is the shrink-only counted package ledger of the
// refusals that are not yet the one grammar: one shard per tool at
// `testdata/refusal-grammar/cmd/<tool>.txt`, a row `cmd/<tool>:<kind> <count>
// <why>`, the count the refusals of that kind that miss the grammar.
const refusalGrammarLedgerPath = "testdata/refusal-grammar"

// The kinds of invocation the walk judges.
const (
	refuseBare  = "bare"          // the bare command
	refuseVerb  = "unknown-verb"  // an unknown verb
	refuseFlag  = "unknown-flag"  // an unknown flag on a verb
	refuseNoArg = "verb-no-flags" // a verb run with no flags
)

// refusalGrammarRemedy is, for each kind, what a tool's author does to clear it;
// on internal/tool every one of them holds by construction.
var refusalGrammarRemedy = map[string]string{
	refuseBare:  "the bare command refuses through internal/tool's Run: `<TOKEN> REFUSED: no verb given; the verbs are ...; run: <tool> help` on stderr at exit 2",
	refuseVerb:  "the unknown verb refuses through internal/tool's Run: `<TOKEN> REFUSED: unknown verb \"x\"; the verbs are ...; run: <tool> help`",
	refuseFlag:  "the verb parses its flags through internal/tool's Flags (or verbflag.Explain): `<VERB> REFUSED: unknown flag --x; ...; run: <tool> help <verb>`",
	refuseNoArg: "the verb returns tool.Refuse(...) (or Refused()) with its problem and its remedy, so the skeleton renders `<VERB> REFUSED[ k=v ...]: <why>; run: <remedy>` on stderr",
}

// refusalGrammar is one walk's measure against its package ledger, shared by the
// walk's parallel subtests and checked once they have all finished.
type refusalGrammar struct {
	mu             sync.Mutex
	ledger         *siteLedger
	begun, settled int
}

func newRefusalGrammar(t *testing.T) *refusalGrammar {
	t.Helper()
	allow, err := allowlist.LoadPackages(refusalGrammarLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	requireReasons(t, allow)
	return &refusalGrammar{ledger: &siteLedger{path: refusalGrammarLedgerPath, allow: allow, sites: map[string][]string{}}}
}

// begin is called as each tool's subtest starts; settle when its measure is
// complete (or the tool is not measured at all).
func (g *refusalGrammar) begin() { g.mu.Lock(); g.begun++; g.mu.Unlock() }

func (g *refusalGrammar) settle() { g.mu.Lock(); g.settled++; g.mu.Unlock() }

// short records one way a tool's refusal falls short of kind; "" records nothing.
func (g *refusalGrammar) short(tool, kind, where, problem string) {
	if problem == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ledger.add("cmd/"+tool+":"+kind, where+": "+problem+"; to clear it: "+refusalGrammarRemedy[kind])
}

// check holds the measure to the ledger, once every one of the tools ran:
// a run of some of them (-run, or a subtest that stopped early) cannot tell a
// fixed tool from one it did not reach, so it says so and checks nothing.
func (g *refusalGrammar) check(t *testing.T, tools int) {
	t.Helper()
	if g.begun != tools || g.settled != tools {
		t.Logf("refusal-grammar: %d of %d tools measured; the ledger is checked only when every tool is", g.settled, tools)
		return
	}
	for _, v := range g.ledger.violations(t, "every refusal is one line in the one grammar on stderr; on internal/tool it is by construction (a row's count only falls)") {
		assert.Fail(t, v)
	}
}
