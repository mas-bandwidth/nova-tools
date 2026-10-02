//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE DEFECT THIS IS FOR. `nova-wake serve --as Johnny` is a process started BESIDE a
// line, not the line itself. It polls by running `nova-bus wait`, and every wait writes
// and pushes a BEAT for --as <name>; Johnny's own harness loop beats for Johnny too. The
// two writers collide on from-johnny/BEAT forever, and the measured failure is #1517:
//
//	WAKE BUS LINE WAIT NOTE beat push failed: the rebase over what arrived conflicted on
//	from-johnny/BEAT, which this tool will not settle for you
//
// The asked shape is a READ-ONLY poll: `wait --no-beat` fetches, lists and beats nothing.
// The control is in the SAME test on purpose: a fix that made every wait stop beating
// would satisfy "no BEAT written" and would break presence everywhere, so the default
// must still write and commit its BEAT in the same run of this test.
func TestIssue1517WaitWithNoBeatWritesNoBeat(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	beat := filepath.Join(checkout, "from-ada", "BEAT")
	{
		_, err := os.Stat(beat)
		require.Truef(t, os.IsNotExist(err), "the fixture already holds a BEAT (%v); this test needs a line with none", err)
	}
	beatCommits := func() string {
		return strings.TrimSpace(gitIn(t, checkout, "log", "--format=%H", "--", "from-ada/BEAT"))
	}
	{
		before := beatCommits()
		require.Emptyf(t, before, "a BEAT commit already exists before the wait: %s", before)
	}

	poll := func(extra ...string) result {
		return invoke(t, "", waitFlags(checkout, "Ada", "300ms", extra...)...)
	}

	// THE FLAG: no entry beat, no tick beat, no beat commit, no beat file.
	quiet := poll("--no-beat").mustCode(t, 0).mustContain(t, "stdout", "WAIT TIMEOUT")
	{
		_, err := os.Stat(beat)
		require.Truef(t, os.IsNotExist(err), "wait --no-beat wrote from-ada/BEAT: %v", err)
	}
	{
		after := beatCommits()
		require.Emptyf(t, after, "wait --no-beat committed a BEAT: %s", after)
	}

	// THE CONTROL, flipped by #3144: the default writes no BEAT either. The bus carries
	// notes, never beats; presence is friend:<name> in Redis.
	control := poll().mustCode(t, 0).mustContain(t, "stdout", "WAIT TIMEOUT")
	{
		_, err := os.Stat(beat)
		require.Truef(t, os.IsNotExist(err), "the control wait (no --no-beat) wrote from-ada/BEAT: %v", err)
	}
	{
		after := beatCommits()
		require.Emptyf(t, after, "the control wait (no --no-beat) committed a BEAT: %s\n%s", after, control.stdout)
	}

	// THE SAME CALL OTHERWISE: the exit code and the terminal WAIT line's cursor are the
	// same with the flag as without -- the flag turns off the beat and nothing else.
	require.Equalf(t, control.code, quiet.code, "exit with --no-beat = %d, without = %d", quiet.code, control.code)
	cursor := func(out string) string {
		return field(t, out[strings.Index(out, "WAIT TIMEOUT"):], "cursor=")
	}
	{
		got, want := cursor(quiet.stdout), cursor(control.stdout)
		require.Equalf(t, want, got, "the WAIT TIMEOUT cursor differs: --no-beat %q, default %q", got, want)
	}
}
