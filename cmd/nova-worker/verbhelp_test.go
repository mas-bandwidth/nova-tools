package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and none of
// them reads a card, opens a store, runs a runner or writes a file (the CLI style's
// rule (b), #4505; internal/nsprint/verbflag is the seam).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, swarmRun, []testverbhelp.Case{
		{Verb: "version"},
		{Verb: "doctor", Flags: []string{"--path", "{dir}/nova-worker", "--local", "{dir}/local"}},
		{Verb: "verify", Flags: []string{"--result", "{dir}/result"}},
		{Verb: "lint", Flags: []string{"--card", "{dir}/card"}},
		{Verb: "template", Flags: []string{"--name", "read-pr"}},
		{Verb: "profile", Flags: []string{"--jobs", "{dir}/jobs/*"}},
		{Verb: "native", Flags: []string{"--card", "{dir}/card", "--slot", "{dir}/slot", "--root", "{dir}/root"}},
		{Verb: "slots"},
		{Verb: "slots init", Flags: []string{"--store", "{dir}/store"}},
		{Verb: "slots take", Flags: []string{"--store", "{dir}/store", "--owner", "o"}},
		{Verb: "slots release", Flags: []string{"--store", "{dir}/store", "--owner", "o"}},
		{Verb: "slots list", Flags: []string{"--store", "{dir}/store"}},
		{Verb: "worker check"},
	})
	testverbhelp.HelpVerb(t, swarmRun, "nova-worker", "version", "native", "slots take", "worker")
	assertTheBannerCutItsLongParenthesesIntoTheVerbHelp(t)
	assertTheExitCodesWrapAtOneHundredColumns(t)
	assertTheStepWallFlagSaysWhatItWantsOnBothLines(t)
}

// assertTheBannerCutItsLongParenthesesIntoTheVerbHelp holds the J05 cold rating's first
// costliest lines: member, lint, disk-guard and step each show their usage line plus at
// most six wrapped lines, and every sentence cut from the banner is found in the verb's
// own -h (docs/STANDARD.md section 3, ONBOARDING point 6: the banner has three questions,
// the verb's -h has the detail).
func assertTheBannerCutItsLongParenthesesIntoTheVerbHelp(t *testing.T) {
	t.Helper()
	for _, verb := range []string{"member", "lint", "disk-guard", "step"} {
		lines := verbParenthesisLines(usage, verb)
		assert.NotEmpty(t, lines, "%s: the banner has no parenthesis under its usage line", verb)
		assert.LessOrEqual(t, len(lines), 6, "%s: the banner's paragraph is %d wrapped lines, want at most 6:\n  %s",
			verb, len(lines), strings.Join(lines, "\n  "))
	}
	// Every sentence the banner cut stands in the verb's -h: the moved detail, not a
	// promise that it left the tool.
	for verb, phrases := range map[string][]string{
		"member": {
			"Each tick beats, reads the queue, reports ended children",
			"Decisions are recorded and routed on the sprint row's gate bars",
			"Completed launches leave no checkout",
			"A staging refusal reports why so the sprint can deal the card to another member.",
		},
		"lint": {
			"a bare --card holds the card to nova-worker's own card contract",
			"the same for every adopter",
			"--rules lists every check",
			"an adopter's own rules go in --child-rules-file",
			"the four checks of a coding card",
			"nova-sprint add holds a brief to the --child-rules tokens only",
		},
		"disk-guard": {
			"every Go build cache",
			"never git prune",
			"DISK-GUARD OK freed=<bytes>",
			"WOULD-TRIM",
		},
	} {
		help := swarmHelp(t, verb, "-h")
		for _, phrase := range phrases {
			assert.Contains(t, help, phrase, "%s -h does not carry the sentence the banner cut: %q", verb, phrase)
		}
	}
}

// assertTheExitCodesWrapAtOneHundredColumns holds the J05 cold rating's second costliest
// line: the exit table wraps at 100 columns in the by-verb shape nova-ci uses, in the
// banner and in every verb's -h.
func assertTheExitCodesWrapAtOneHundredColumns(t *testing.T) {
	t.Helper()
	for _, out := range append([]string{swarmHelp(t, "help")},
		swarmHelp(t, "member", "-h"), swarmHelp(t, "lint", "-h"), swarmHelp(t, "disk-guard", "-h"),
		swarmHelp(t, "step", "-h"), swarmHelp(t, "native", "-h"), swarmHelp(t, "doctor", "-h")) {
		for _, line := range exitParagraphLines(out) {
			assert.LessOrEqual(t, len(line), 100, "the exit table has a line over 100 columns (%d):\n%s", len(line), line)
		}
	}
}

// assertTheStepWallFlagSaysWhatItWantsOnBothLines holds the J05 cold rating's third
// costliest line: --sandbox names the wall binary, stated the same way on the banner's
// step usage line and in step -h (a flag's line and its help cannot part).
func assertTheStepWallFlagSaysWhatItWantsOnBothLines(t *testing.T) {
	t.Helper()
	step := swarmHelp(t, "step", "-h")
	var usageLine string
	for _, l := range strings.Split(usage, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "nova-worker step ") {
			usageLine = strings.TrimSpace(l)
			break
		}
	}
	require.NotEmpty(t, usageLine, "the banner has a step usage line")
	assert.Contains(t, step, usageLine, "step -h does not show the step usage line the banner prints, byte for byte")
	for _, line := range []string{usage, step} {
		assert.Contains(t, line, "--sandbox <binary>", "the step usage line does not name the value --sandbox wants: <binary>")
		assert.Contains(t, line, "the wall binary", "the step help does not say what <binary> is: the wall binary")
	}
}

// verbParenthesisLines returns the more-indented continuation lines a banner verb's usage
// line owns: its wrapped parenthesis, up to the next usage line or blank line.
func verbParenthesisLines(banner, verb string) []string {
	lines := strings.Split(banner, "\n")
	start, indent := -1, 0
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "nova-worker "+verb+" ") {
			start, indent = i, len(l)-len(strings.TrimLeft(l, " "))
			break
		}
	}
	if start < 0 {
		return nil
	}
	var got []string
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) == "" || len(l)-len(strings.TrimLeft(l, " ")) <= indent {
			break
		}
		got = append(got, strings.TrimSpace(l))
	}
	return got
}

// exitParagraphLines returns the lines of the one `exit codes:` paragraph in out, from its
// label to the next blank line.
func exitParagraphLines(out string) []string {
	lines := strings.Split(out, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "exit codes:") {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var got []string
	for i, l := range lines[start:] {
		if i > 0 && l != "" && l[0] != ' ' && l[0] != '\t' {
			break
		}
		if strings.TrimSpace(l) == "" {
			break
		}
		got = append(got, l)
	}
	return got
}

func swarmRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr, time.Now().UTC())
}

// A verb's -h ends with its effect (docs/STANDARD.md section 2, the tool-answers dry-run
// rule): the inspections say they write nothing, native and member say what they send.
func TestAVerbHelpStatesItsEffect(t *testing.T) {
	t.Parallel()
	for verb, want := range verbEffect {
		exit, stdout, _ := runSwarm(t, append(strings.Fields(verb), "-h")...)
		require.Equal(t, 0, exit, verb)
		assert.True(t, strings.HasSuffix(stdout, "effect: "+want+"\n"), "%s -h does not end with its effect:\n%s", verb, stdout)
	}
	for _, verb := range []string{"version", "doctor", "lint", "template", "worker", "worker check"} {
		assert.True(t, strings.HasPrefix(verbEffect[verb], "inspection: "), "%s is not stated as an inspection: %q", verb, verbEffect[verb])
	}
	for _, verb := range []string{"native", "member"} {
		assert.True(t, strings.HasPrefix(verbEffect[verb], "delivery: "), "%s is not stated as a delivery: %q", verb, verbEffect[verb])
	}
}
