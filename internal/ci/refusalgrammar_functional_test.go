//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		found++
		tool := e.Name()
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
