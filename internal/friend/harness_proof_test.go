package friend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAProofOfAClaudeThatCannotAnswerFailsWithItsTerminalReason pins the
// finding of 2026-10-11: a lane whose config dir was never signed in answers
// terminal_reason api_error with duration_api_ms 0 in about a second, and the
// proof must fail with the reason the daemon beats down (docs/SPEC-FRIEND.md,
// the harness proof).
func TestAProofOfAClaudeThatCannotAnswerFailsWithItsTerminalReason(t *testing.T) {
	t.Parallel()
	var gotName string
	var gotArgs []string
	run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		gotName, gotArgs = name, args
		return `{"type":"result","terminal_reason":"api_error","duration_api_ms":0}`, 0, nil
	}
	p := ProveHarness(context.Background(), t.TempDir(), "claude", "", "/home/ada/.claude", run, time.Unix(0, 0).UTC())
	require.False(t, p.OK)
	assert.Equal(t, "api_error", p.TerminalReason)
	assert.Equal(t, "harness claude cannot answer: api_error (config dir /home/ada/.claude)", p.Reason())
	assert.Equal(t, "env", gotName)
	assert.Equal(t, []string{"CLAUDE_CONFIG_DIR=/home/ada/.claude", "claude", "-p", "ok", "--output-format", "json", "--max-turns", "1"}, gotArgs)
}

// TestAProofOfAClaudeThatAnswersIsOK pins the other side: the same one-turn
// run with terminal_reason success proves the harness, so cards are taken as
// today.
func TestAProofOfAClaudeThatAnswersIsOK(t *testing.T) {
	t.Parallel()
	run := func(_ context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		return `{"type":"result","terminal_reason":"success","duration_api_ms":812}`, 0, nil
	}
	p := ProveHarness(context.Background(), t.TempDir(), "claude", "", "/home/ada/.claude", run, time.Unix(0, 0).UTC())
	require.True(t, p.OK, p.Reason())
	assert.Empty(t, p.Reason())
	assert.Contains(t, p.Line(), "harness=claude ok")
}

// TestTheClaudeProofIsOneNoToolTurn pins both the argv and the config dir the
// proof runs with: the same config dir the lane's cards run with, so a never
// signed-in dir fails here.
func TestTheClaudeProofIsOneNoToolTurn(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"claude", "-p", "ok", "--output-format", "json", "--max-turns", "1"}, ProofArgv("claude"))
	assert.Equal(t, []string{"dsh", "-p", "ok"}, ProofArgv("dsh"))
	assert.Equal(t, []string{"gemini", "-p", "ok"}, ProofArgv("gemini"))
	assert.Nil(t, ProofArgv("codex"))
	assert.Equal(t, []string{"CLAUDE_CONFIG_DIR=/x"}, ProofEnv("claude", "/x"))
	assert.Nil(t, ProofEnv("claude", ""))
	assert.Nil(t, ProofEnv("dsh", "/x"))
}

// TestAProofOfAnUnknownHarnessIsNotOK pins that the proof never passes a
// harness it cannot run: the daemon must not take cards on an unproven
// harness.
func TestAProofOfAnUnknownHarnessIsNotOK(t *testing.T) {
	t.Parallel()
	run := func(_ context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		t.Fatal("an unknown harness must not be run")
		return "", 0, nil
	}
	p := ProveHarness(context.Background(), t.TempDir(), "codex", "", "/c", run, time.Unix(0, 0).UTC())
	require.False(t, p.OK)
	assert.Contains(t, p.Reason(), "harness codex cannot answer:")
}

// TestAProofRunThatFailsToStartIsNotOK pins the exec failure: the harness did
// not answer, so the proof says so.
func TestAProofRunThatFailsToStartIsNotOK(t *testing.T) {
	t.Parallel()
	run := func(_ context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		return "", -1, errors.New("exec: claude: executable file not found")
	}
	p := ProveHarness(context.Background(), t.TempDir(), "claude", "", "/c", run, time.Unix(0, 0).UTC())
	require.False(t, p.OK)
	assert.Contains(t, p.Reason(), "executable file not found")
}
