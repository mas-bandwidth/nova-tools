package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fuseProbe declares this tool's verbs the way the move onto internal/tool
// would declare them (docs/STANDARD.md section 2, "one shape across the
// set"): the gate's own words CLEAR, BLOWN and LOCKDOWN in Tool.Words,
// HelpRefused set because exit 0 here means CLEAR (docs/STANDARD.md section
// 3, the one exception), and the verbs that take their input positionally --
// check <surface>, lockdown <reason>, quarantine <surface> <reason> and lift
// quarantine <surface> (docs/SPEC.md, "check -- the gate" to "lift -- soft
// succeeds, hard refuses forever") -- declared as ordinary tool.Verbs. The
// tests below run it against the skeleton as it stands and record where the
// skeleton stops the move; the card that lands the move deletes this file
// together with the private dispatcher.
func fuseProbe() *tool.Tool {
	return &tool.Tool{
		Name:        "nova-fuse",
		What:        "a recorded decision to stop reading an untrusted source, checked before every read",
		ExitTable:   "0 clear, or done and verified by re-reading the box; 1 blown; 2 could not run",
		HelpRefused: true,
		Words:       []string{"CLEAR", "BLOWN", "LOCKDOWN"},
		Verbs: []tool.Verb{
			{
				Name:   "check",
				Usage:  "check --box <path> [surface]",
				Effect: tool.Inspection,
				Flags:  func(f *tool.Flags) { f.Required("box", "the path of the fuse box JSON file") },
				Run:    func(*tool.Call) *tool.Out { return tool.Done().As("CLEAR") },
			},
			{
				Name:   "lift quarantine",
				Usage:  "lift quarantine --box <path> <surface>",
				Effect: tool.LocalWrite,
				Flags:  func(f *tool.Flags) { f.Required("box", "the path of the fuse box JSON file") },
				Run:    func(*tool.Call) *tool.Out { return tool.Done() },
			},
			{
				Name:   "lift lockdown",
				Usage:  "lift lockdown",
				Effect: tool.Inspection,
				Run: func(*tool.Call) *tool.Out {
					return tool.Refuse("a blown fuse is REPLACED, only in a live conversation with your person")
				},
			},
		},
	}
}

// TestTheSkeletonRefusesThePositionalArgumentsFourVerbsTake pins the first
// gap: a verb that is not Tool.Default refuses every positional argument
// (internal/tool/tool.go, the call "takes no positional arguments") and Call
// carries no Args, so no verb reads one even where the skeleton allows it.
// Four of this tool's verbs take their input positionally, so none of the
// four is declarable as a tool.Verb until the skeleton restores the two
// things 267cdf904 deleted as reached by nothing: Flags.Positional, which
// lifts the refusal, and Call.Args, which reads the arguments. The probe's
// check names --box and a surface, the shortest call the gate answers; the
// refusal below is the skeleton's line, and the gate never runs.
func TestTheSkeletonRefusesThePositionalArgumentsFourVerbsTake(t *testing.T) {
	t.Parallel()

	probe := fuseProbe()
	require.Empty(t, probe.Problems(), "the probe is a whole definition, so the refusal below is the gap and not a malformed verb")

	var stdout, stderr bytes.Buffer
	code := probe.Run([]string{"check", "--box", "./fuse-box.json", "a-forum"}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 2, code, "the surface is refused, so the gate never runs; stdout=%q", stdout.String())
	assert.Empty(t, stdout.String())
	assert.Equal(t, `CHECK REFUSED: takes no positional arguments, got "a-forum" (flags come before arguments); run: nova-fuse help`+"\n", stderr.String())
}

// TestHelpRefusedReachesOnlyNamedVerbs pins the second gap and what is not a
// gap, for a tool with HelpRefused set. A named verb's -h is a refusal at
// exit 2 (the row "check -h"): the skeleton carries this tool's rule by
// declaration, and the move keeps it. A group's -h is answered with the
// group's usage at exit 0 (the row "lift -h"): Tool.HelpRefused does not
// reach inGroup, and this tool refuses `lift -h` at exit 2 today, because its
// exit 0 means CLEAR -- so the group form is the one answer the move cannot
// take. lift quarantine and lift lockdown are two verbs under the word lift,
// and their effects differ (docs/SPEC.md, "lift -- soft succeeds, hard
// refuses forever"). The unblock is a line in inGroup that honours the
// switch, named here as the shape the moving card will check against.
func TestHelpRefusedReachesOnlyNamedVerbs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // exact, when the answer is stdout
		wantStderr string // exact, when the answer is stderr
	}{
		{
			name:       "check -h",
			args:       []string{"check", "-h"},
			wantCode:   2,
			wantStderr: "CHECK REFUSED: -h is not an answer this tool gives, its exit 0 means CLEAR; run: nova-fuse help\n",
		},
		{
			name:     "lift -h",
			args:     []string{"lift", "-h"},
			wantCode: 0,
			wantStdout: "usage: nova-fuse lift <quarantine|lockdown> [flags]\n" +
				"  nova-fuse lift quarantine --box <path> <surface>\n" +
				"  nova-fuse lift lockdown\n" +
				"`nova-fuse lift <verb> -h` lists a verb's flags.\n" +
				"exit codes: 0 clear, or done and verified by re-reading the box; 1 blown; 2 could not run\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := fuseProbe().Run(tc.args, strings.NewReader(""), &stdout, &stderr)
			assert.Equal(t, tc.wantCode, code, "stdout=%q stderr=%q", stdout.String(), stderr.String())
			assert.Equal(t, tc.wantStdout, stdout.String())
			assert.Equal(t, tc.wantStderr, stderr.String())
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
			wantStderr: "FUSE REFUSED: no verb given; the verbs are check, lift quarantine, lift lockdown, version; run: nova-fuse help\n",
		},
		{
			name:       "unknown verb",
			args:       []string{"lockdownn"},
			wantCode:   2,
			wantStderr: "FUSE REFUSED: unknown verb \"lockdownn\"; the verbs are check, lift quarantine, lift lockdown, version; run: nova-fuse help\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := fuseProbe().Run(tc.args, strings.NewReader(""), &stdout, &stderr)
			assert.Equal(t, tc.wantCode, code, "stdout=%q stderr=%q", stdout.String(), stderr.String())
			assert.Empty(t, stdout.String())
			assert.Equal(t, tc.wantStderr, stderr.String())
		})
	}
}
