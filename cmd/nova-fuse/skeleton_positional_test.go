package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The move of this tool onto internal/tool (docs/STANDARD.md section 2, "one
// shape across the set") stops at one gap in the skeleton, and this test is the
// record of that gap: a verb that is not Tool.Default refuses every positional
// argument, and Call carries no Args, so no verb reads one even where the
// skeleton allows it. Four of this tool's verbs take their input positionally —
// `check <surface>`, `lockdown <reason>`, `quarantine <surface> <reason>` and
// `lift quarantine <surface>` (docs/SPEC.md, from "check — the gate" to
// "lift — soft succeeds, hard refuses forever") — so none of the four is
// declarable as a tool.Verb until the skeleton takes back the two things it
// deleted at 267cdf904 as reached by nothing: Flags.Positional, which lifts the
// refusal, and Call.Args, which reads the arguments. The probe below declares
// this tool's check verb and its status words the way the move would, and the
// refusal it answers with is the skeleton's line, not this tool's. The move
// declares Flags.Positional on the four verbs, reads their arguments with
// Call.Args, and deletes this file and the private dispatcher together.
func TestTheSkeletonRefusesThePositionalArgumentsFourVerbsTake(t *testing.T) {
	t.Parallel()

	probe := &tool.Tool{
		Name:        "nova-fuse",
		What:        "a recorded decision to stop reading an untrusted source, checked before every read",
		ExitTable:   "0 clear, or done and verified by re-reading the box; 1 blown; 2 could not run",
		HelpRefused: true,
		Words:       []string{"CLEAR", "BLOWN", "LOCKDOWN"},
		Verbs: []tool.Verb{{
			Name:   "check",
			Usage:  "check --box <path> [surface]",
			Effect: tool.Inspection,
			Flags:  func(f *tool.Flags) { f.Required("box", "the path of the fuse box JSON file") },
			Run:    func(*tool.Call) *tool.Out { return tool.Done().As("CLEAR") },
		}},
	}
	require.Empty(t, probe.Problems(), "the probe is a whole definition, so the refusal below is the gap and not a malformed verb")

	var stdout, stderr bytes.Buffer
	code := probe.Run([]string{"check", "--box", "./fuse-box.json", "a-forum"}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 2, code, "the surface is refused, so the gate never runs; stdout=%q", stdout.String())
	assert.Empty(t, stdout.String())
	assert.Equal(t, `CHECK REFUSED: takes no positional arguments, got "a-forum" (flags come before arguments); run: nova-fuse help`+"\n", stderr.String())
}
