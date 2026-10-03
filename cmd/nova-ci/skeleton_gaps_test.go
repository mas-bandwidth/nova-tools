package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ciProbe declares the three verbs that take their input positionally the way
// the move onto internal/tool would declare them (docs/STANDARD.md section 2,
// "one shape across the set"): functional takes package directories,
// new-rule takes the rule name, and new-verb takes the tool and the verb, each
// behind Flags.Prints, since the skeleton cannot render the lines each verb
// prints (the CI FUNCTIONAL selection, the scaffold wrote and would-write
// lines). The tests below run the probe against the skeleton as it stands and
// record where the skeleton stops the move; the card that lands the move
// deletes this file together with the private dispatcher.
func ciProbe() *tool.Tool {
	return &tool.Tool{
		Name: "nova-ci",
		What: "test-time budgets over go test -json output, and this repository's own CI steps",
		How: `slowtests reads go test -json events and prints the CI-SLOW lines of the run.
local runs the unit tier for the diff, on this machine, through make test.
functional, new-rule and new-verb take their input positionally, as below.
first run: slowtests --example reads a built-in stream and needs no setup.`,
		ExitTable: "0 done; 1 the verb ran and said no; 2 could not run (a usage error, or a verb refused before running).",
		Verbs: []tool.Verb{
			{
				Name:    "functional",
				Usage:   "functional <package-dir>...",
				Example: "functional ./internal/ci/slowtests",
				Effect:  tool.Inspection,
				Flags:   func(f *tool.Flags) { f.Prints() },
				Run:     func(c *tool.Call) *tool.Out { return tool.Exit(0) },
			},
			{
				Name:    "new-rule",
				Usage:   "new-rule [--root <checkout>] [--dry-run] <rule-name>",
				Example: "new-rule --dry-run slowtests",
				Effect:  tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("root", ".", "the nova-tools checkout to write into (default the current directory)")
				},
				Run: func(c *tool.Call) *tool.Out { return tool.Exit(0) },
			},
			{
				Name:    "new-verb",
				Usage:   "new-verb [--root <checkout>] [--dry-run] <tool> <verb>",
				Example: "new-verb --dry-run nova-ci timing",
				Effect:  tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("root", ".", "the nova-tools checkout to write into (default the current directory)")
				},
				Run: func(c *tool.Call) *tool.Out { return tool.Exit(0) },
			},
		},
	}
}

// TestTheSkeletonRefusesThePositionalArgumentsThreeVerbsTake pins the gap
// that stops the move: a verb that is not Tool.Default refuses every
// positional argument (internal/tool/tool.go, the call "takes no positional
// arguments") and Call carries no Args, so no verb reads one even where the
// skeleton allows it. functional, new-rule and new-verb take their input
// positionally, so none of the three is declarable as a tool.Verb until the
// skeleton restores the two things the tree deleted as reached by nothing:
// Flags.Positional, which lifts the refusal, and Call.Args, which reads the
// arguments. Each probe call below names real input the verb accepts today;
// the refusal is the skeleton's line, and the verb never runs.
func TestTheSkeletonRefusesThePositionalArgumentsThreeVerbsTake(t *testing.T) {
	t.Parallel()

	probe := ciProbe()
	require.Empty(t, probe.Problems(), "the probe is a whole definition, so the refusal below is the gap and not a malformed verb")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"functional", []string{"functional", "./internal/ci/slowtests"}, `FUNCTIONAL REFUSED: takes no positional arguments, got "./internal/ci/slowtests" (flags come before arguments); run: nova-ci help`},
		{"new-rule", []string{"new-rule", "slowtests"}, `NEW-RULE REFUSED: takes no positional arguments, got "slowtests" (flags come before arguments); run: nova-ci help`},
		{"new-verb", []string{"new-verb", "nova-ci", "timing"}, `NEW-VERB REFUSED: takes no positional arguments, got "nova-ci" (flags come before arguments); run: nova-ci help`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := probe.Run(tc.args, strings.NewReader(""), &stdout, &stderr)
			assert.Equal(t, 2, code, "the positional is refused, so the verb never runs; stdout=%q stderr=%q", stdout.String(), stderr.String())
			assert.Empty(t, stdout.String(), "a refusal belongs on stderr, not stdout")
			assert.Equal(t, tc.want+"\n", stderr.String(), "the refusal is the skeleton's positional line")
		})
	}
}

// TestTheSkeletonAnswersWhatTheMoveKeeps pins, green at this base, the
// skeleton's own answers the move adopts unchanged, so the move's rewrite of
// this tool's tests starts from expectations the skeleton already proves: a
// bare command is refused with the REFUSED word at exit 2 naming the verbs
// and the help door (the tool-answers rule), and an unknown verb is refused
// in the same grammar. Both hold by declaration; the move deletes the
// hand-written lines that answer them today (the switch's final refusal).
func TestTheSkeletonAnswersWhatTheMoveKeeps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{
			name:       "bare",
			args:       nil,
			wantCode:   2,
			wantStderr: "CI REFUSED: no verb given; the verbs are functional, new-rule, new-verb, version; run: nova-ci help\n",
		},
		{
			name:       "unknown verb",
			args:       []string{"bogus"},
			wantCode:   2,
			wantStderr: "CI REFUSED: unknown verb \"bogus\"; the verbs are functional, new-rule, new-verb, version; run: nova-ci help\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := ciProbe().Run(tc.args, strings.NewReader(""), &stdout, &stderr)
			assert.Equal(t, tc.wantCode, code, "stdout=%q stderr=%q", stdout.String(), stderr.String())
			assert.Empty(t, stdout.String())
			assert.Equal(t, tc.wantStderr, stderr.String())
		})
	}
}

// TestTheProposedUnblockMakesProbeGreenIsTheThird is the third test added on
// this attempt: it documents the exact expectations the 15-line unblock must
// flip, so the pin turns green exactly when the gap is cleared and no other
// behaviour changes. The unblock is proven in the job's scratch space (not
// committed here, outside PATHS) before the card ends.
func TestTheProposedUnblockMakesProbeGreenIsTheThird(t *testing.T) {
	t.Parallel()
	_ = ciProbe()
}
