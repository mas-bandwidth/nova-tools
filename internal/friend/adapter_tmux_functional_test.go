//go:build functional

package friend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A hosted `cat` behind a fake prompt: Host starts it in a real tmux, Deliver
// types a line into the idle pane, and the line arrives (docs/SPEC-FRIEND.md,
// "Hosted in tmux"). Skipped, with the reason, when tmux is not installed.
func TestTmuxHostedCatReceivesTheTypedLine(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed here: " + err.Error())
	}
	name := fmt.Sprintf("fn%d", os.Getpid())
	dir := t.TempDir()
	_, err := Host(context.Background(), RealExec, HostSpec{Name: name, Dir: dir, Command: []string{"sh", "-c", "printf '> '; cat"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", TmuxSession(name)).Run() }) // the session this test started

	d := &Tmux{Dir: dir, Session: TmuxSession(name), Prompt: PromptPattern(`^>\s*$`), Run: RealExec}
	require.Eventually(t, func() bool {
		busy, err := d.Busy(context.Background())
		return err == nil && !busy
	}, 5*time.Second, 50*time.Millisecond, "the fake prompt is drawn")

	exit, err := d.Deliver(context.Background(), "hello\nfrom the bus")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	screen, err := d.capture(context.Background())
	require.NoError(t, err)
	assert.Contains(t, strings.Join(strings.Fields(screen), " "), "hello ⏎ from the bus")
	busy, err := d.Busy(context.Background())
	require.NoError(t, err)
	assert.True(t, busy, "the line is in, the prompt is gone")
}
