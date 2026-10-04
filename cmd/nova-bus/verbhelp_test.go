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
