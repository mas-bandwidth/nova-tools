package ci

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
)

// The refusal-grammar rule: a refusal is one line in one grammar
// (`<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>`, docs/STANDARD.md,
// "The status word leads every line", skeleton contract 1.1), it is on stderr
// and nothing is on stdout. The walk is functional
// (refusalgrammar_functional_test.go); the judge below is proved in the unit
// tier.

// refusalGrammarLedgerPath is the shrink-only counted ledger of the refusals
// that are not yet the one grammar: `cmd/<tool>:<kind> <count> <why>`, the count
// the invocations of that kind that miss the grammar. It is one counted file,
// not a package-sharded directory, because the ledger ratchet
// (ledger_ratchet_test.go) refuses every shard a new rule adds over its merge
// base; the tree's newer counted ledgers are one top-level file for that reason.
const refusalGrammarLedgerPath = "testdata/refusal-grammar_allowlist.txt"

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

// refusalStatusRe matches a line whose status word is REFUSED: the token, one
// or more words as the reader typed it, then REFUSED. A FAILED or OK line that
// quotes a refusal after its own status word is not a refusal line.
var refusalStatusRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(?: [A-Za-z0-9][A-Za-z0-9_-]*)* REFUSED\b`)

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
		if refusalStatusRe.MatchString(line) {
			refused = append(refused, line)
		}
	}
	if len(refused) == 0 {
		return ""
	}
	if strings.TrimSpace(stdout) != "" {
		return fmt.Sprintf("a refusal wrote to stdout: %q; a refusal belongs on stderr (exit %d)", firstLine(stdout), code)
	}
	for _, line := range refused {
		if !refusalGrammarRe.MatchString(line) {
			return fmt.Sprintf("a stderr line holding REFUSED is not `<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>`: %q", line)
		}
	}
	return ""
}

// refusalGrammar is one walk's measure against the ledger, shared by the walk's
// parallel subtests and checked once they have all finished.
type refusalGrammar struct {
	mu             sync.Mutex
	list           *allowlist.List
	measured       map[string]int
	sites          map[string][]string // key -> "tool kind: problem", in the order found
	begun, settled int
}

func newRefusalGrammar(t *testing.T) *refusalGrammar {
	t.Helper()
	return &refusalGrammar{
		list:     loadAllowlist(t, refusalGrammarLedgerPath, allowlist.Options{Ceiling: true, Counted: true}),
		measured: map[string]int{},
		sites:    map[string][]string{},
	}
}

func (g *refusalGrammar) begin() { g.mu.Lock(); g.begun++; g.mu.Unlock() }

func (g *refusalGrammar) settle() { g.mu.Lock(); g.settled++; g.mu.Unlock() }

// short records one way a tool's refusal falls short of kind; "" records nothing.
func (g *refusalGrammar) short(tool, kind, where, problem string) {
	if problem == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key := "cmd/" + tool + ":" + kind
	g.measured[key]++
	g.sites[key] = append(g.sites[key], where+": "+problem+"; to clear it: "+refusalGrammarRemedy[kind])
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
	const remedy = "every refusal is one line in the one grammar on stderr; on internal/tool it is by construction (a row's count only falls)"
	res := allowlist.CheckCountedMode(t, g.list, g.measured, allowlist.Updating())
	var out []string
	for _, k := range res.Unlisted {
		for _, w := range g.sites[k] {
			out = append(out, fmt.Sprintf("%s: %s (no row in %s): %s", w, k, refusalGrammarLedgerPath, remedy))
		}
	}
	for _, c := range res.Over {
		out = append(out, fmt.Sprintf("%s lists %s at %d sites, but %d are there now: %s", refusalGrammarLedgerPath, c.Key, c.Listed, c.Measured, remedy))
	}
	for _, c := range res.Lowered {
		out = append(out, fmt.Sprintf("%s lists %s at %d sites, but only %d are there now; lower the row's count to %d (the list only shrinks; NOVA_CI_UPDATE=1 lowers it)", refusalGrammarLedgerPath, c.Key, c.Listed, c.Measured, c.Measured))
	}
	for _, row := range res.Stale {
		out = append(out, fmt.Sprintf("%s lists %s, but no site of that key is there any more; delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)", refusalGrammarLedgerPath, row.Key))
	}
	sort.Strings(out)
	for _, m := range out {
		assert.Fail(t, m)
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
		{"the witness: stdout on a refusal", "SEND OK to=x\n", good, "wrote to stdout"},
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
