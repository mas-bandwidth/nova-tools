package ci

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The refusal-grammar rule: a refusal is one line in one grammar
// (`<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>`, docs/STANDARD.md,
// "The status word leads every line", skeleton contract 1.1), it is on stderr
// and nothing is on stdout. The walk is functional
// (refusalgrammar_functional_test.go); the judge below is proved in the unit
// tier.

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

// refusalStatusWordRe finds a line's first status word: OK, REFUSED, FAILED, or
// a MORE or NOTE continuation, at the line's start or after whitespace. It
// finds REFUSED on its own, so a malformed line whose REFUSED carries no token
// prefix is still judged, while a FAILED, OK or continuation line that only
// quotes a refusal after its own status word is not a refusal line.
var refusalStatusWordRe = regexp.MustCompile(`(?:^|\s)(OK|REFUSED|FAILED|MORE|NOTE)(?:[^A-Za-z0-9_-]|$)`)

// refusalLineIsRefusal reports whether line's own status word is REFUSED.
func refusalLineIsRefusal(line string) bool {
	m := refusalStatusWordRe.FindStringSubmatch(line)
	return len(m) == 2 && m[1] == "REFUSED"
}

// refusalGrammarRe is the one refusal line:
// `<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>` (docs/STANDARD.md,
// "The status word leads every line"). The token is the tool or verb as it was
// invoked: internal/tool upper-cases it (`SEND`), a hand printer writes it as
// the reader typed it (`nova-sprint`, `nova-swarm install`).
var refusalGrammarRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(?: [A-Za-z0-9][A-Za-z0-9_-]*)* REFUSED(?: [a-z][a-z0-9_-]*=(?:"[^"]*"|\S+))*: .+; run: .+$`)

// refusalGrammarAnswers is "" when the invocation did not refuse, or refused in
// the one grammar with nothing on stdout; otherwise it names the first breach.
func refusalGrammarAnswers(code int, stdout, stderr string) string {
	var refused []string
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if refusalLineIsRefusal(line) {
			refused = append(refused, line)
		}
	}
	if len(refused) == 0 {
		return ""
	}
	if stdout != "" {
		return fmt.Sprintf("a refusal wrote to stdout: %q; a refusal belongs on stderr (exit %d)", firstLine(stdout), code)
	}
	for _, line := range refused {
		if !refusalGrammarRe.MatchString(line) {
			return fmt.Sprintf("a stderr line holding REFUSED is not `<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>`: %q", line)
		}
	}
	return ""
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

// TestRefusalGrammarJudges is the witness: a fixture that breaks the rule once
// is refused naming the line, and the fixed fixture passes. The table pins the
// judge on the shapes the walk meets (skeleton contract 1.1, docs/STANDARD.md
// section 2).
func TestRefusalGrammarJudges(t *testing.T) {
	t.Parallel()
	const good = "SEND REFUSED: --to is required; run: nova-bus help send\n"
	for _, tc := range []struct {
		name, stdout, stderr string
		want                 string
	}{
		{"the fixed fixture", "", good, ""},
		{"a refusal with reason and facts", "", `SEND REFUSED reason=usage topic="a b": --to is required; run: nova-bus help send` + "\n", ""},
		{"a bare refusal", "", "BUS REFUSED: no verb given; the verbs are send, recv; run: nova-bus help\n", ""},
		{"a hand printer's lower-case token", "", "nova-sprint REFUSED: no verb; available: add, land; run: nova-sprint help\n", ""},
		{"a two-word token", "", "nova-swarm install REFUSED: install wants the unit's kind first; run: nova-swarm install -h\n", ""},
		{"the witness: no colon before the why", "", "SEND REFUSED --to is required; run: nova-bus help send\n", "not `<TOKEN> REFUSED"},
		{"the witness: no remedy", "", "SEND REFUSED: --to is required\n", "not `<TOKEN> REFUSED"},
		{"the witness: a fact then no remedy", "", "EGRESS REFUSED reason=no_command: --plan wants the ruleset to apply\n", "not `<TOKEN> REFUSED"},
		{"the witness: REFUSED where the remedy belongs", "", "nova-fuse lift lockdown REFUSED, forever, by design: a blown fuse is not reset;\n", "not `<TOKEN> REFUSED"},
		{"the witness: REFUSED with no token before it", "", "REFUSED: --to is required; run: nova-bus help send\n", "not `<TOKEN> REFUSED"},
		{"the witness: REFUSED alone", "", "REFUSED\n", "not `<TOKEN> REFUSED"},
		{"the witness: stdout on a refusal", "SEND OK to=x\n", good, "wrote to stdout"},
		{"the witness: whitespace-only stdout on a refusal", " \n", good, "wrote to stdout"},
		{"a FAILED line quoting a refusal is not one", "", "SELFTEST FAILED step=add why=nova-sprint add REFUSED: x; run: y\n", ""},
		{"a line with no REFUSED status word", "", "SEND: --to is required; run: nova-bus help send\n", ""},
		{"a run that did not refuse", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := refusalGrammarAnswers(2, tc.stdout, tc.stderr)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.want)
		})
	}
}
