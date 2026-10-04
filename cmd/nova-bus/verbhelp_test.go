package main

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// neither touches the bus it was pointed at (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	bus := []string{"--bus", "{dir}/bus"}
	var cases []testverbhelp.Case
	for _, v := range []string{"draft", "prepare", "send", "reply", "inbox", "wait", "receipt", "close", "check", "names"} {
		cases = append(cases, testverbhelp.Case{Verb: v, Flags: bus})
	}
	cases = append(cases, testverbhelp.Case{Verb: "version"})
	testverbhelp.Check(t, busRun, cases)
	testverbhelp.HelpVerb(t, busRun, "nova-bus", "send", "inbox", "version")

	// The two `draft` forms stand as two usage lines with nothing saying which is
	// which; a reader cannot tell which writes a new note and which writes a reply.
	// Each carries the clause the code's own comments give it, on its own line.
	banner := invoke(t, "", "help").mustCode(t, 0).stdout
	for _, clause := range []string{
		"(a new note, printed to stdout or --out)",
		"(a reply, written into --draft-dir from --body-file, the note's From/To/Re lines filled in)",
	} {
		onOwnLine := false
		for _, line := range strings.Split(banner, "\n") {
			if strings.TrimSpace(line) == clause {
				onOwnLine = true
				break
			}
		}
		assert.Truef(t, onOwnLine, "the banner does not give a draft form its own line %q:\n%s", clause, banner)
	}

	// The receipt-word-count sentence read as if the flag were optional when the
	// flag, the file and the variable are all absent. What the tool DOES with none
	// of the three set is a refusal, so the sentence says so and the run shows it.
	assert.Containsf(t, banner, "and with none of the three set,\ninbox and wait refuse at exit 2",
		"the banner does not say what happens with none of the three receipt-word-count sources set:\n%s", banner)
	assert.NotContainsf(t, banner, "none of them is a refusal", "the banner still carries the sentence a cold reader read as optional:\n%s", banner)
	for _, verb := range []string{"inbox", "wait"} {
		args := []string{verb, "--bus", t.TempDir(), "--as", "Ada"}
		if verb == "wait" {
			args = append(args, "--remote", "origin", "--branch", "main", "--timeout", "1s")
		}
		r := invoke(t, "", args...)
		assert.Equalf(t, 2, r.code, "%s with none of the three receipt-word-count sources set exits %d; the banner says it refuses at exit 2:\n%s", verb, r.code, r.stderr)
		assert.Containsf(t, r.stderr, "receipt-max-words must be given", "%s with none of the three set does not refuse on the count:\n%s", verb, r.stderr)
	}

	// Every banner line fits a column a terminal shows. The participants.json printf
	// is the one exception and stays one command: its JSON is a single-quoted shell
	// argument, and a backslash continuation inside the quotes is not something a
	// shell runs, so the line is exempt here rather than split across arguments.
	for _, line := range strings.Split(banner, "\n") {
		if strings.Contains(line, "printf '%s\\n'") {
			continue
		}
		assert.LessOrEqualf(t, len(line), 100, "a banner line runs past 100 columns:\n%s", line)
	}
}

func busRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr, now())
}

// Every verb's -h states its effect, one of the three, and a verb that writes takes
// --dry-run; wait alone has none, because every poll fast-forwards the checkout and a wait
// that wrote nothing could not see a note arrive (toolanswers ledger, cmd/nova-bus dry-run 1).
func TestEveryVerbStatesItsEffectAndAWriterTakesDryRun(t *testing.T) {
	t.Parallel()
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			help := invoke(t, "", verb, "-h").mustCode(t, 0).stdout
			effect := regexp.MustCompile(`(?m)^effect: (inspection|local write|delivery): `).FindStringSubmatch(help)
			require.NotNil(t, effect, "%s -h states no effect:\n%s", verb, help)
			if effect[1] != "inspection" && verb != "wait" {
				assert.Regexp(t, `(?m)^  --dry-run  `, help, "%s writes (%s) and its -h lists no --dry-run", verb, effect[1])
			}
		})
	}
}
