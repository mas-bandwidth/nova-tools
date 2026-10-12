package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lane fault stands until a person clears it: nova-friend status reports
// it as lane-fault=<n> refusal="<first line>", and nova-friend lanes
// --clear-faults removes it so the lanes take cards again (docs/SPEC-FRIEND.md,
// the lanes).
const lanesFaultReport = `lane-fault=2 refusal="content_filter_error: I'm sorry, but I can't help with that request."`

func TestStatusReportsTheLaneFault(t *testing.T) {
	t.Parallel()

	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Width: 4, Mode: friend.ModeOneShot}))
	require.NoError(t, friend.WriteLaneFault(state, lanesFaultReport))

	cli.Do(t, "status", "--as", "bob", "--dir", t.TempDir(), "--state-dir", state).Exit(0).
		Out(`lane_fault=2`, `refusal="content_filter_error: I'm sorry, but I can't help with that request."`)
}

func TestTheLanesVerbReportsAndClearsTheFault(t *testing.T) {
	t.Parallel()

	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteLaneFault(state, lanesFaultReport))

	// the fault the daemon wrote is read back
	cli.Do(t, "lanes", "--as", "bob", "--state-dir", state).Exit(0).
		Out(`lane_fault=2 refusal="content_filter_error: I'm sorry, but I can't help with that request."`)

	// a dry run names the fault and writes nothing
	cli.Do(t, "lanes", "--as", "bob", "--state-dir", state, "--clear-faults", "--dry-run").Exit(0).
		Out("cleared=would")
	assert.Equal(t, lanesFaultReport, friend.ReadLaneFault(state), "a dry run clears nothing")

	// the clear removes the fault file and the lanes take cards again
	cli.Do(t, "lanes", "--as", "bob", "--state-dir", state, "--clear-faults").Exit(0).
		Out("cleared=yes")
	assert.Equal(t, "", friend.ReadLaneFault(state), "the clear removes the fault file")

	// nothing to clear is a clear that changed nothing
	cli.Do(t, "lanes", "--as", "bob", "--state-dir", state, "--clear-faults").Exit(0).
		Out("cleared=none")
}

// TestTheLanesHelpExampleIsWhatTheToolPrints runs the lanes verb's help example
// as written through the one comparator: the line a reader pastes prints the
// line the help shows (docs/SPEC-FRIEND.md, the lanes).
func TestTheLanesHelpExampleIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	step := onboarding.Step{
		Line: "$ nova-friend lanes --as bob --clear-faults",
		Args: []string{"lanes", "--as", "bob", "--clear-faults"},
		Want: []string{"LANES OK cleared=none"},
	}
	doc, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	assert.Contains(t, string(doc), strings.TrimPrefix(step.Line, "$ "), "the executed command is also in the reference")
	var out, errb strings.Builder
	w := newRig(t).world()
	code := run(step.Args, strings.NewReader(""), &out, &errb, w)
	got := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	require.Equal(t, 0, code, errb.String())
	for _, p := range onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{got}, nil) {
		assert.Fail(t, "the help example differs", p.Message)
	}
}
