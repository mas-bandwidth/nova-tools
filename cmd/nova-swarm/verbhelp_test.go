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
		{Verb: "doctor", Flags: []string{"--path", "{dir}/nova-swarm", "--local", "{dir}/local"}},
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
	testverbhelp.HelpVerb(t, swarmRun, "nova-swarm", "version", "native", "slots take", "worker")
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
