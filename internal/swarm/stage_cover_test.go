package swarm

// stage_cover_test.go reaches the three stage.go functions the unit tier's
// per-function table showed at 0.0%: mirrorKeepsObjects (stage.go:225),
// WriteStageTimeoutResult (stage.go:271) and stageTimedOut (stage.go:308).
// Nothing here sleeps, reads a real clock, opens a socket or starts a process:
// a git that cannot be found makes exec's own lookup fail before any child is
// forked, so mirrorKeepsObjects' borrowing path (gc.auto already 0) needs a
// real child and is named in the card's report, never hidden. WriteStageTimeoutResult
// and stageTimedOut are covered in full, main path and refusal.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStageCoverMirrorKeepsObjects pins the copy-in refusal: a git whose binary
// cannot be found fails exec's own lookup before a child is forked, so the
// gc.auto read answers empty, the write fails, and the second read still answers
// empty, and the function reports the mirror does not keep every object (false),
// so the clone copies the objects in rather than borrowing them.
func TestStageCoverMirrorKeepsObjects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		bin  string
	}{
		{"missing git binary", "nova-worker-no-such-git"},
		{"empty git binary name", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			git := func(ctx context.Context, args ...string) *exec.Cmd {
				return exec.CommandContext(ctx, tc.bin, args...)
			}
			assert.False(t, mirrorKeepsObjects(context.Background(), git, t.TempDir()),
				"%s: a mirror whose gc.auto cannot be read or written must not be reported as keeping every object", tc.name)
		})
	}
}

// TestStageCoverWriteStageTimeoutResult pins the exact RESULT.md a stage timeout
// writes, and the two refusals: an empty job dir is a no-op, and a job dir that
// is not a directory answers the write error rather than a path.
func TestStageCoverWriteStageTimeoutResult(t *testing.T) {
	t.Parallel()

	t.Run("writes the blocked stage-timeout result", func(t *testing.T) {
		t.Parallel()
		jobDir := t.TempDir()
		dest, err := WriteStageTimeoutResult(jobDir, "bench-a", 120)
		require.NoError(t, err, "writing the stage-timeout result failed")
		assert.Equal(t, filepath.Join(jobDir, "RESULT.md"), dest, "the result must land at <jobDir>/RESULT.md")
		b, err := os.ReadFile(dest)
		require.NoError(t, err, "the result file must be readable")
		assert.Equal(t, "RESULT: BLOCKED stage-timeout bench-a 120\n"+
			"blocked: staging timed out after 120s\n"+
			"written-by: nova-worker native (the card published no report of its own)\n",
			string(b), "the result body must name the bench and the seconds in the card's shape")
	})

	t.Run("refuses an empty job dir", func(t *testing.T) {
		t.Parallel()
		dest, err := WriteStageTimeoutResult("", "bench-a", 120)
		require.NoError(t, err, "an empty job dir is a no-op, not an error")
		assert.Empty(t, dest, "an empty job dir has no RESULT.md path to answer")
	})

	t.Run("refuses a job dir that is a file", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "not-a-dir")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		dest, err := WriteStageTimeoutResult(file, "bench-a", 120)
		require.Error(t, err, "a job dir that is a file cannot hold RESULT.md")
		assert.Empty(t, dest, "a failed write answers no path")
	})
}

// TestStageCoverStageTimedOut pins the main path (a passed deadline, a
// context.DeadlineExceeded error, and exec's own wait delay all report a
// timeout) and the refusals (a live context with no error or an unrelated one,
// and a merely cancelled context, are not timeouts).
func TestStageCoverStageTimedOut(t *testing.T) {
	t.Parallel()

	expired, cancelExpired := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancelExpired()
	cancelled, cancelCancelled := context.WithCancel(t.Context())
	cancelCancelled()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"a passed deadline", expired, nil, true},
		{"a deadline-exceeded error", context.Background(), context.DeadlineExceeded, true},
		{"exec's wait delay", context.Background(), exec.ErrWaitDelay, true},
		{"a live context with no error", context.Background(), nil, false},
		{"a live context with an unrelated error", context.Background(), errors.New("git clone failed"), false},
		{"a cancelled context is not a timeout", cancelled, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, stageTimedOut(tc.ctx, tc.err),
				"%s: stageTimedOut did not answer %v", tc.name, tc.want)
		})
	}
}
