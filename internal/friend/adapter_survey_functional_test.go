//go:build functional

package friend

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The frames reach the real binaries and are refused as documented, without
// a model turn: no token is spent. Each half skips where its harness is not
// installed.
func TestDSHAndGeminiFramesReachTheRealBinaries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := os.Stat(DSHProgram); err == nil {
		d := &DSH{Dir: dir, Session: "session-nope", Run: RealExec}
		exit, err := d.Deliver(context.Background(), "x")
		require.NoError(t, err)
		assert.Equal(t, 1, exit, "dsh headless refuses an unknown session with exit 1")
	} else {
		t.Log("dsh is not installed here; its half is skipped")
	}
	if _, err := exec.LookPath("gemini"); err == nil {
		g := &Gemini{Dir: dir, Session: "00000000-0000-0000-0000-000000000000", Run: RealExec}
		exit, err := g.Deliver(context.Background(), "x")
		require.NoError(t, err)
		assert.Equal(t, 42, exit, "gemini --resume refuses an unknown session with exit 42")
	} else {
		t.Log("gemini is not installed here; its half is skipped")
	}
}
