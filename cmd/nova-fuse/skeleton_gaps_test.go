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
// succeeds, hard refuses forever") -- declaring Positional and reading
// Call.Args. The first test below pins the unblock sk-00-tool's skeleton
// gained (Flags.Positional, Call.Args); the second pins what still stops the
// move. The card that lands the move deletes this file together with the
// private dispatcher.
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
				Flags: func(f *tool.Flags) {
					f.Required("box", "the path of the fuse box JSON file")
					f.Positional()
				},
				Run: func(c *tool.Call) *tool.Out {
					return tool.Done().As("CLEAR").Fact("surface", strings.Join(c.Args(), " "))
				},
			},
			{
				Name:   "lift quarantine",
				Usage:  "lift quarantine --box <path> <surface>",
				Effect: tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					f.Required("box", "the path of the fuse box JSON file")
					f.Positional()
				},
				Run: func(*tool.Call) *tool.Out { return tool.Done() },
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

// TestTheSkeletonTakesThePositionalArgumentsFourVerbsTake pins the unblock:
// a verb that declares Flags.Positional reads its arguments through Call.Args,
// so check <surface> and lift quarantine <surface> are declarable as
// tool.Verbs. This is the two things 267cdf904 deleted as reached by nothing,
// restored; it was the first of the two gaps that stopped the move.
func TestTheSkeletonTakesThePositionalArgumentsFourVerbsTake(t *testing.T) {
	t.Parallel()

	probe := fuseProbe()
	require.Empty(t, probe.Problems(), "the probe is a whole definition, so the line below is the skeleton's and not a malformed verb")

	var stdout, stderr bytes.Buffer
	code := probe.Run([]string{"check", "--box", "./fuse-box.json", "a-forum"}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 0, code, "stderr=%q", stderr.String())
	assert.Equal(t, "CHECK CLEAR surface=a-forum\n", stdout.String())
	assert.Empty(t, stderr.String())
}

// TestTheLiftWordRefusesFlagsBeforeThePowerBeforeTheSkeletonCan pins the gap
// that still stops the move. The private cmdLift answers `lift --box <path>
// lockdown` with "takes a power" before any flag is parsed, because the hard
// half must not depend on a flag being present or the box being readable
// (TestUsageErrorsExitTwo, "lift with flags before the power"). The skeleton
// matches the longest verb name first and parses its flags, so the same argv
// reaches the group's "wants one of its verbs" refusal and never the design's.
// Nothing in internal/tool lets a verb refuse on the order of the words before
// its flags are parsed, so that one case cannot keep its truth table and the
// move stays blocked until the skeleton gains it.
func TestTheLiftWordRefusesFlagsBeforeThePowerBeforeTheSkeletonCan(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := fuseProbe().Run([]string{"lift", "--box", "./fuse-box.json", "lockdown"}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 2, code, "stdout=%q stderr=%q", stdout.String(), stderr.String())
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "lift wants one of its verbs", "the skeleton answers with the group's verbs, where the tool says the flags came before the power: stderr=%q", stderr.String())
	assert.NotContains(t, stderr.String(), "takes a power", "the design refusal is unreachable once the skeleton parses the flags first")
}
