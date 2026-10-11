//go:build functional

package ci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// measureToolAnswers runs one built tool through the mistakes an AI makes
// (toolanswers_class_test.go): bare, an unknown verb, an unknown flag on a
// verb, each group's -h, and reads every verb's help for its effect and its
// dry run. helps is what everyVerbAnswersHelp read; bareErr the bare command's
// stderr. Each run is in a directory of its own with HOME there.
func measureToolAnswers(t *testing.T, a *toolAnswers, tool, bin, banner, bareErr string, helps map[string]string) {
	t.Helper()
	verbs := usageVerbs(tool, banner)
	a.short(tool, answerBare, tool, bareAnswers(bareErr))

	code, out, errs := runIn(t, bin, noSuchVerb)
	a.short(tool, answerVerb, tool+" "+noSuchVerb, unknownVerbAnswers(code, out+errs, verbs))

	// One verb per tool: every verb of a tool parses through one seam. The
	// first that lists a flag shows whether the answer names them.
	verb, flags := verbs[0], []string(nil)
	for _, v := range verbs {
		if f := helpFlags(helps[v]); len(f) > 0 {
			verb, flags = v, f
			break
		}
	}
	code, out, errs = runIn(t, bin, append(strings.Fields(verb), noSuchFlag)...)
	a.short(tool, answerFlag, tool+" "+verb+" "+noSuchFlag, unknownFlagAnswers(code, out+errs, flags))

	// The verb-help walk visits the groups too: each group's -h is help that
	// names the group's verbs.
	for _, g := range verbGroups(verbs) {
		code, out, errs = runIn(t, bin, g, "-h")
		a.short(tool, answerGroup, tool+" "+g+" -h", groupHelpAnswers(code, out, errs, groupMembers(g, verbs)))
	}
	for _, v := range verbs {
		problem := "its -h is not help (exit 0 on stdout)"
		if h, ok := helps[v]; ok {
			problem = dryRunAnswers(h)
		}
		a.short(tool, answerDry, tool+" "+v, problem)
	}
}

// runIn runs bin with args in a fresh directory, HOME and TMPDIR there, stdin
// empty, under a deadline.
func runIn(t *testing.T, bin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + dir}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		require.Fail(t, fmt.Sprintf("running %s %s: %v", bin, strings.Join(args, " "), err))
	}
	return code, out.String(), errb.String()
}

// toolAnswersLedgerPath is the shrink-only ledger of the tools that do not yet
// answer every mistake: one shard per tool, `cmd/<tool>:<kind> <count> <why>`,
// the count the verbs (or groups) short of the rule.
const toolAnswersLedgerPath = "testdata/toolanswers"

// The kinds of answer the walk asks of every tool.
const (
	answerBare  = "bare"         // a bare command is refused with the REFUSED word
	answerVerb  = "unknown-verb" // an unknown verb is answered with the tool's verbs
	answerFlag  = "unknown-flag" // an unknown flag is answered with the verb's flags
	answerGroup = "group-help"   // a verb group's -h is help at exit 0
	answerDry   = "dry-run"      // a verb's -h states its effect, and a verb that writes takes --dry-run
)

// toolAnswersRemedy is, for each kind, what a tool's author does to clear it;
// on pkg/tool every one of them holds by construction.
var toolAnswersRemedy = map[string]string{
	answerBare: "a bare `<tool>` prints `<TOOL> REFUSED: no verb given; the verbs are ...; run: <tool> help` on stderr at exit 2 " +
		"(pkg/tool's Run does it)",
	answerVerb: "an unknown verb is refused at exit 2 in one line naming the tool's verbs, as `<TOOL> REFUSED: unknown verb \"x\"; " +
		"the verbs are ...; run: <tool> help` (pkg/tool's Run does it)",
	answerFlag: "an unknown flag is refused naming the verb's flags (and the nearest), never the flag package's " +
		"`flag provided but not defined` line (pkg/tool does it; a tool not on it prints verbflag.Explain)",
	answerGroup: "`<tool> <group> -h` names the group's verbs on stdout at exit 0, as `usage: <tool> <group> <a|b> [flags]` (a two-word " +
		"Verb.Name on pkg/tool; verbflag.Print reads the group from the banner's usage lines)",
	answerDry: "a verb's -h says `effect: inspection|local write|delivery`, and a verb that writes takes --dry-run that writes " +
		"nothing (Verb.Effect and Verb.DryRun with Call.DryRun on pkg/tool)",
}

// The arguments the walk hands a tool: names no tool has.
const (
	noSuchVerb = "zz-no-such-verb"
	noSuchFlag = "--zz-no-such-flag"
)

// toolAnswers is one walk's measure against the ledger, shared by the walk's
// parallel subtests and checked once they have all finished.
type toolAnswers struct {
	mu             sync.Mutex
	ledger         *siteLedger
	begun, settled int
}

func newToolAnswers(t *testing.T) *toolAnswers {
	t.Helper()
	allow, err := allowlist.LoadPackages(toolAnswersLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	requireReasons(t, allow)
	return &toolAnswers{ledger: &siteLedger{path: toolAnswersLedgerPath, allow: allow, sites: map[string][]string{}}}
}

// begin is called as each tool's subtest starts; settle when its measure is
// complete (or the tool is not measured at all).
func (a *toolAnswers) begin() { a.mu.Lock(); a.begun++; a.mu.Unlock() }

func (a *toolAnswers) settle() { a.mu.Lock(); a.settled++; a.mu.Unlock() }

// short records one way tool falls short of kind; "" records nothing.
func (a *toolAnswers) short(tool, kind, where, problem string) {
	if problem == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ledger.add("cmd/"+tool+":"+kind, where+": "+problem+"; to clear it: "+toolAnswersRemedy[kind])
}

// check holds the measure to the ledger, once every one of the tools ran:
// a run of some of them (-run, or a subtest that stopped early) cannot tell a
// fixed tool from one it did not reach, so it says so and checks nothing.
func (a *toolAnswers) check(t *testing.T, tools int) {
	t.Helper()
	if a.begun != tools || a.settled != tools {
		t.Logf("tool-answers: %d of %d tools measured; the ledger is checked only when every tool is", a.settled, tools)
		return
	}
	for _, v := range a.ledger.violations(t, "a tool answers every mistake with the way forward; on pkg/tool it does by construction (a row's count only falls)") {
		assert.Fail(t, v)
	}
}
