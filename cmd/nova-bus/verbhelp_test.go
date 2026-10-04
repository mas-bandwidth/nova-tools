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
		for line := range strings.SplitSeq(banner, "\n") {
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
	for line := range strings.SplitSeq(banner, "\n") {
		if strings.Contains(line, "printf '%s\\n'") {
			continue
		}
		assert.LessOrEqualf(t, len(line), 100, "a banner line runs past 100 columns:\n%s", line)
	}

	// The banner's first-send recipe runs as printed: its own functional test
	// (TestTheBannersFirstSendRecipeRunsAsPrinted) reads every indented line under
	// "first send, from nothing", strips a comment only where two spaces and an open
	// parenthesis stand on the SAME line, and dispatches on each remaining command's
	// first word -- a parenthetical on a line of its own reaches that dispatch as a
	// command the runner does not know, so the recipe line and its trailing
	// parenthetical must stay one line. The unit tier holds the same shape: after that
	// strip, every recipe line opens with a command the functional runner can run.
	var recipe []string
	bannerLines := strings.Split(banner, "\n")
	for i := 0; i < len(bannerLines); i++ {
		if strings.HasPrefix(strings.TrimSpace(bannerLines[i]), "first send, from nothing") {
			for i++; i < len(bannerLines) && !strings.HasPrefix(bannerLines[i], "  git "); i++ {
			}
			for ; i < len(bannerLines) && strings.HasPrefix(bannerLines[i], "  ") && strings.TrimSpace(bannerLines[i]) != ""; i++ {
				recipe = append(recipe, strings.TrimSpace(bannerLines[i]))
			}
		}
	}
	require.NotEmpty(t, recipe, "the banner prints no first-send recipe lines")
	for _, line := range recipe {
		command := line
		if j := strings.Index(command, "  ("); j >= 0 {
			command = strings.TrimSpace(command[:j])
		}
		for part := range strings.SplitSeq(command, " && ") {
			fields := strings.Fields(part)
			require.NotEmptyf(t, fields, "the recipe prints an empty command line: %q", line)
			switch fields[0] {
			case "cd", "git", "printf", "nova-bus":
			default:
				assert.Failf(t, "a recipe line is not a command the runner can run",
					"the recipe runs %q, which TestTheBannersFirstSendRecipeRunsAsPrinted does not know how to run (line %q)", part, line)
			}
		}
	}
	// The draft line of the recipe keeps its trailing parenthetical on the same line:
	// that is the shape the functional runner strips as the recipe's comment.
	draftLine := ""
	for _, line := range recipe {
		if strings.HasPrefix(line, "nova-bus draft") {
			draftLine = line
		}
	}
	require.NotEmptyf(t, draftLine, "the recipe prints no draft line: %q", recipe)
	assert.Containsf(t, draftLine, "  (then replace", "the recipe's draft line lost its trailing parenthetical on the same line: %q", draftLine)
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
