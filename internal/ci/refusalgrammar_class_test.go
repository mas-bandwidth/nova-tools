package ci

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/verb"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// The refusal-grammar rule: a refusal is one line in one grammar
// (`<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>`, docs/STANDARD.md,
// "The status word leads every line", skeleton contract 1.1), it is on stderr
// and nothing is on stdout. The walk is functional
// (refusalgrammar_functional_test.go); the judge below is proved in the unit
// tier.

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
// invoked: pkg/tool upper-cases it (`SEND`), a hand printer writes it as
// the reader typed it (`nova-sprint`, `nova-swarm install`). Its two captures
// are the why and the remedy, so the judge below reads them back and refuses an
// empty one: the line needs a non-whitespace why and a non-whitespace remedy.
var refusalGrammarRe = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9_-]*(?: [A-Za-z0-9][A-Za-z0-9_-]*)* REFUSED(?: [a-z][a-z0-9_-]*=(?:"[^"]*"|\S+))*): (.*); run: (.*)$`)

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
		m := refusalGrammarRe.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(m[1]) == "" || strings.TrimSpace(m[2]) == "" {
			return fmt.Sprintf("a stderr line holding REFUSED is not `<TOKEN> REFUSED[ k=v ...]: <why>; run: <remedy>`: %q", line)
		}
	}
	return ""
}

// judged answers for one fixture: its arguments the invocation's stdout and
// stderr at exit 2; the judge's answer on stderr at exit 1, silence at 0.
var judged = testkit.Main(func(args []string, _ io.Reader, _, stderr io.Writer) int {
	if problem := refusalGrammarAnswers(2, args[0], args[1]); problem != "" {
		fmt.Fprintln(stderr, problem)
		return 1
	}
	return 0
})

// TestRefusalGrammarJudges is the witness: a fixture that breaks the rule
// once is refused naming the line, and the fixed fixture passes. Each row's
// wanted code and stderr text are the judge's verdict on the fixture (the
// shapes the walk meets, skeleton contract 1.1, docs/STANDARD.md section 2).
func TestRefusalGrammarJudges(t *testing.T) {
	t.Parallel()
	const good = "SEND REFUSED: --to is required; run: nova-bus help send\n"
	verb.Refusals(t, judged, []verb.Refusal{
		{Name: "the fixed fixture", Args: []string{"", good}, Code: 0},
		{Name: "a refusal with reason and facts", Args: []string{"", `SEND REFUSED reason=usage topic="a b": --to is required; run: nova-bus help send` + "\n"}, Code: 0},
		{Name: "a bare refusal", Args: []string{"", "BUS REFUSED: no verb given; the verbs are send, recv; run: nova-bus help\n"}, Code: 0},
		{Name: "a hand printer's lower-case token", Args: []string{"", "nova-sprint REFUSED: no verb; available: add, land; run: nova-sprint help\n"}, Code: 0},
		{Name: "a two-word token", Args: []string{"", "nova-swarm install REFUSED: install wants the unit's kind first; run: nova-swarm install -h\n"}, Code: 0},
		{Name: "the witness: no colon before the why", Args: []string{"", "SEND REFUSED --to is required; run: nova-bus help send\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: no remedy", Args: []string{"", "SEND REFUSED: --to is required\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: a whitespace-only why", Args: []string{"", "SEND REFUSED:  ; run: nova-bus help send\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: a whitespace-only remedy", Args: []string{"", "SEND REFUSED: --to is required; run:  \n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: a fact then no remedy", Args: []string{"", "EGRESS REFUSED reason=no_command: --plan wants the ruleset to apply\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: REFUSED where the remedy belongs", Args: []string{"", "nova-fuse lift lockdown REFUSED, forever, by design: a blown fuse is not reset;\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: REFUSED with no token before it", Args: []string{"", "REFUSED: --to is required; run: nova-bus help send\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: REFUSED alone", Args: []string{"", "REFUSED\n"}, Code: 1, Stderr: "not `<TOKEN> REFUSED"},
		{Name: "the witness: stdout on a refusal", Args: []string{"SEND OK to=x\n", good}, Code: 1, Stderr: "wrote to stdout"},
		{Name: "the witness: whitespace-only stdout on a refusal", Args: []string{" \n", good}, Code: 1, Stderr: "wrote to stdout"},
		{Name: "a FAILED line quoting a refusal is not one", Args: []string{"", "SELFTEST FAILED step=add why=nova-sprint add REFUSED: x; run: y\n"}, Code: 0},
		{Name: "a line with no REFUSED status word", Args: []string{"", "SEND: --to is required; run: nova-bus help send\n"}, Code: 0},
		{Name: "a run that did not refuse", Args: []string{"", ""}, Code: 0},
	})
}
