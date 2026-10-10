package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/require"
)

// memberCoverRun invokes dispatch with a minimal app and captured output.
func memberCoverRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	app := &application{getenv: func(string) string { return "" }, lookPath: memberCoverLookPath}
	var out, errout bytes.Buffer
	code := app.dispatch(args, &out, &errout)
	return code, out.String(), errout.String()
}

// memberCoverLookPath is a lookPath that returns not found.
var memberCoverLookPath = func(string) (string, error) { return "", nil }

func TestNovaTableMemberCoverCmdMemberNoArgs(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "MEMBER REFUSED")
}

func TestNovaTableMemberCoverCmdMemberWrongSubcommand(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "list")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "MEMBER REFUSED")
}

func TestNovaTableMemberCoverCmdMemberUnknownFlag(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "create", "t", "i", "--unknown")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "MEMBER-CREATE REFUSED")
	require.Contains(t, stderr, "unknown flag")
}

func TestNovaTableMemberCoverCmdMemberCreateDryRunNoRedis(t *testing.T) {
	t.Parallel()
	// needs two positionals: table and id
	code, stdout, stderr := memberCoverRun(t, "member", "create", "b1", "--dry-run")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "wants a table and a new member ID")
}

func TestNovaTableMemberCoverCmdMemberCreateIDTooLong(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("x", ntable.LimitMemberIDBytes+1)
	code, stdout, stderr := memberCoverRun(t, "member", "create", "demo", longID)
	require.EqualValues(t, 1, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "MEMBER-CREATE REFUSED")
	require.Contains(t, stderr, "limit exceeded")
}

func TestNovaTableMemberCoverCmdMemberCreateNoDryRunEmptyRedis(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "create", "t", "i", "--redis", "")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--redis <addr> is required")
}

func TestNovaTableMemberCoverCmdCheckNoArgs(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "check")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CHECK REFUSED")
	require.Contains(t, stderr, "wants one table name")
}

func TestNovaTableMemberCoverCmdCheckTwoArgs(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "check", "t1", "t2")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CHECK REFUSED")
	require.Contains(t, stderr, "wants one table name")
}

func TestNovaTableMemberCoverCmdCheckUnknownFlag(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "check", "t", "--badflag")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CHECK REFUSED")
	require.Contains(t, stderr, "unknown flag")
}

func TestNovaTableMemberCoverCmdCheckRedisEmpty(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "check", "demo", "--redis", "")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--redis <addr> is required")
}

func TestNovaTableMemberCoverCmdMemberFindOneArg(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "find", "demo")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "MEMBER-FIND REFUSED")
}

func TestNovaTableMemberCoverCmdMemberFindRedisEmpty(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "find", "demo", "i", "--redis", "")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--redis <addr> is required")
}

func TestNovaTableMemberCoverCmdMemberReadTableOnly(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "MEMBER-READ REFUSED")
}

func TestNovaTableMemberCoverCmdMemberReadTableWithCell(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo", "--cell")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--cell needs a value")
}

func TestNovaTableMemberCoverCmdMemberReadCellNoColon(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo", "--cell", "build")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--cell wants <row>:<col>")
}

func TestNovaTableMemberCoverCmdMemberReadAtEpochInvalid(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo", "--cell", "build:ready", "--at-epoch", "x")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--at-epoch wants an unsigned integer")
}

func TestNovaTableMemberCoverCmdMemberReadTableWithIDs(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo", "i1", "i2")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--redis <addr> is required")
}

func TestNovaTableMemberCoverCmdMemberReadCellBuildReady(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo", "--cell", "build:ready", "--cell", "build:ready")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--redis <addr> is required")
}

func TestNovaTableMemberCoverCmdMemberReadIDsAtEpoch(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := memberCoverRun(t, "member", "read", "demo", "i1", "--at-epoch", "3")
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "--redis <addr> is required")
}
