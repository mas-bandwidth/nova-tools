package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Defect #1451, Rowan's standing door rule of 2026-09-19: an exit-1 line -- the verb
// RAN and answered NO -- never carries the door `; run: nova-check help`. Only an
// exit-2 line -- the tool could not run, or the invocation was wrong -- carries it.
// The door exists to tell a reader who mis-invoked the tool where the usage is; a
// verdict is not a mis-invocation.
//
// The dogfood gate answered exit 1 on an empty receipts directory but printed the
// exit-2 door, because it called refuse (which appends the door and returns 2) and
// then threw that 2 away. The assertion below is on the printed line, not on which
// helper ran: the exit-1 line must still name its own remedy, and must NOT carry the
// door. The control pins the other half: a genuine exit-2 refusal from the same verb
// still ends in the door.
func TestAnExitOneVerdictCarriesNoDoor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	require.NoError(t, os.MkdirAll(receipts, 0o755))

	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	require.EqualValues(t, 1, code, "empty receipts: exit %d, want exactly 1; stderr: %s", code, stderr)
	require.Contains(t, stderr, "--allow-empty", "the exit-1 verdict lost its own remedy:\n%s", stderr)
	require.NotContains(t, stderr, "run: nova-check help", "an exit-1 verdict carries the exit-2 door:\n%s", stderr)

	code, _, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--no-such-flag")
	require.EqualValues(t, 2, code, "unknown flag: exit %d, want exactly 2; stderr: %s", code, stderr)
	require.True(t, strings.HasSuffix(strings.TrimRight(stderr, "\n"), "run: nova-check help"), "an exit-2 invocation refusal lost the door:\n%s", stderr)
}
