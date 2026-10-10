package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNovaTableCellCoverAddUnknownFlag tests that an unknown flag is refused.
func TestNovaTableCellCoverAddUnknownFlag(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready", "m1", "--unknown")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-ADD REFUSED:")
	require.Contains(t, stderr, "unknown flag")
}

// TestNovaTableCellCoverAddThreePositionals tests that three positionals are refused.
func TestNovaTableCellCoverAddThreePositionals(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-ADD REFUSED:")
	require.Contains(t, stderr, "wants a table")
}

// TestNovaTableCellCoverAddScoreX tests that --score x is refused.
func TestNovaTableCellCoverAddScoreX(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready", "m1", "--score", "x")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-ADD REFUSED:")
	require.Contains(t, stderr, "--score wants a number")
}

// TestNovaTableCellCoverAddDryRunWithScore tests that a valid dry-run with score produces a plan.
func TestNovaTableCellCoverAddDryRunWithScore(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready", "b3", "b4", "--score", "5", "--dry-run")...)
	require.EqualValues(t, 0, code)
	require.Empty(t, stderr)
	require.True(t, strings.HasPrefix(stdout, "TABLE DRY-RUN verb=cell-add "))
	require.Contains(t, stdout, "score=5")
	require.Contains(t, stdout, ` sends="FCALL ns_`)
	require.Contains(t, stdout, " redis=- dialled=0 written=0\n")
	require.Equal(t, 1, strings.Count(stdout, "\n"))
}

// TestNovaTableCellCoverAddOverlongMemberID tests that an overlong member ID is refused.
func TestNovaTableCellCoverAddOverlongMemberID(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("m", 257)
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready", longID)...)
	require.NotEqualValues(t, 0, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-ADD REFUSED:")
	require.Contains(t, stderr, "member id bytes")
}

// TestNovaTableCellCoverAddDryRunOverlongMemberID tests that --dry-run does not change the member ID refusal.
func TestNovaTableCellCoverAddDryRunOverlongMemberID(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("m", 257)
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready", longID, "--dry-run")...)
	require.NotEqualValues(t, 0, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-ADD REFUSED:")
	require.Contains(t, stderr, "member id bytes")
}

// TestNovaTableCellCoverAddNoDryRunWithEmptyRedis tests that --redis "" without --dry-run is refused.
func TestNovaTableCellCoverAddNoDryRunWithEmptyRedis(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "add", "demo", "build", "ready", "m1")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-ADD REFUSED:")
	require.Contains(t, stderr, "--redis <addr> is required")
}

// TestNovaTableCellCoverRemoveThreePositionals tests that three positionals are refused.
func TestNovaTableCellCoverRemoveThreePositionals(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "remove", "demo", "build", "ready")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-REMOVE REFUSED:")
	require.Contains(t, stderr, "wants a table")
}

// TestNovaTableCellCoverRemoveDryRun tests that a valid dry-run produces a plan.
func TestNovaTableCellCoverRemoveDryRun(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "remove", "demo", "build", "ready", "m1", "--dry-run")...)
	require.EqualValues(t, 0, code)
	require.Empty(t, stderr)
	require.True(t, strings.HasPrefix(stdout, "TABLE DRY-RUN verb=cell-remove "))
	require.Contains(t, stdout, ` sends="FCALL ns_`)
	require.Contains(t, stdout, " redis=- dialled=0 written=0\n")
	require.Equal(t, 1, strings.Count(stdout, "\n"))
}

// TestNovaTableCellCoverRemoveOverlongMemberID tests that an overlong member ID is refused.
func TestNovaTableCellCoverRemoveOverlongMemberID(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("m", 257)
	code, stdout, stderr := runTable(at("", "cell", "remove", "demo", "build", "ready", longID)...)
	require.NotEqualValues(t, 0, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-REMOVE REFUSED:")
	require.Contains(t, stderr, "member id bytes")
}

// TestNovaTableCellCoverRemoveNoDryRunWithEmptyRedis tests that --redis "" without --dry-run is refused.
func TestNovaTableCellCoverRemoveNoDryRunWithEmptyRedis(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "remove", "demo", "build", "ready", "m1")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-REMOVE REFUSED:")
	require.Contains(t, stderr, "--redis <addr> is required")
}

// TestNovaTableCellCoverMoveFourPositionals tests that four positionals are refused.
func TestNovaTableCellCoverMoveFourPositionals(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "move", "demo", "build", "ready", "done")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MOVE REFUSED:")
	require.Contains(t, stderr, "wants a table")
}

// TestNovaTableCellCoverMoveDryRunOneMember tests that a valid dry-run with one member produces a plan.
func TestNovaTableCellCoverMoveDryRunOneMember(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "move", "demo", "build", "ready", "done", "m1", "--dry-run")...)
	require.EqualValues(t, 0, code)
	require.Empty(t, stderr)
	require.True(t, strings.HasPrefix(stdout, "TABLE DRY-RUN verb=cell-move "))
	require.Contains(t, stdout, ` sends="FCALL ns_`)
	require.Contains(t, stdout, " redis=- dialled=0 written=0\n")
	require.Equal(t, 1, strings.Count(stdout, "\n"))
}

// TestNovaTableCellCoverMoveDryRunTwoMembers tests that a valid dry-run with two members produces a plan.
func TestNovaTableCellCoverMoveDryRunTwoMembers(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "move", "demo", "build", "ready", "done", "m1", "m2", "--dry-run")...)
	require.EqualValues(t, 0, code)
	require.Empty(t, stderr)
	require.True(t, strings.HasPrefix(stdout, "TABLE DRY-RUN verb=cell-move "))
	require.Contains(t, stdout, ` sends="FCALL ns_`)
	require.Contains(t, stdout, " redis=- dialled=0 written=0\n")
	require.Equal(t, 1, strings.Count(stdout, "\n"))
}

// TestNovaTableCellCoverMoveOverlongMemberID tests that an overlong member ID is refused.
func TestNovaTableCellCoverMoveOverlongMemberID(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("m", 257)
	code, stdout, stderr := runTable(at("", "cell", "move", "demo", "build", "ready", "done", longID)...)
	require.NotEqualValues(t, 0, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MOVE REFUSED:")
	require.Contains(t, stderr, "member id bytes")
}

// TestNovaTableCellCoverMoveNoDryRunWithEmptyRedis tests that --redis "" without --dry-run is refused.
func TestNovaTableCellCoverMoveNoDryRunWithEmptyRedis(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "move", "demo", "build", "ready", "done", "m1")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MOVE REFUSED:")
	require.Contains(t, stderr, "--redis <addr> is required")
}

// TestNovaTableCellCoverMembersTwoPositionals tests that two positionals are refused.
func TestNovaTableCellCoverMembersTwoPositionals(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "members", "demo", "build")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MEMBERS REFUSED:")
	require.Contains(t, stderr, "wants a table, a row and a column")
}

// TestNovaTableCellCoverMembersFourPositionals tests that four positionals are refused.
func TestNovaTableCellCoverMembersFourPositionals(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "members", "demo", "build", "ready", "extra")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MEMBERS REFUSED:")
	require.Contains(t, stderr, "wants a table, a row and a column")
}

// TestNovaTableCellCoverMembersUnknownFlag tests that an unknown flag is refused.
func TestNovaTableCellCoverMembersUnknownFlag(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "members", "demo", "build", "ready", "--unknown")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MEMBERS REFUSED:")
	require.Contains(t, stderr, "unknown flag")
}

// TestNovaTableCellCoverMembersNoAddress tests that --redis "" is refused.
func TestNovaTableCellCoverMembersNoAddress(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable(at("", "cell", "members", "demo", "build", "ready")...)
	require.EqualValues(t, 2, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "CELL-MEMBERS REFUSED:")
	require.Contains(t, stderr, "--redis <addr> is required")
}
