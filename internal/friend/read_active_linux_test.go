//go:build linux

package friend

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestartHoldsReadWhileUnverifiedChildGroupLives(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }()
	require.True(t, ProcessGroupAlive(cmd.Process.Pid))

	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		readDir := filepath.Join(dir, "reads", "a.w1")
		require.NoError(t, os.MkdirAll(readDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(readDir, "RESULT.md"), []byte(okResult), 0o644))
		require.NoError(t, saveReadActive(readDir, readActive{Read: AskedRead{ID: "a.w1", Epoch: "15", Col: "reading", Packet: ReadPacket{Head: "aaa"}}, PID: cmd.Process.Pid, Identity: "wrong birth"}))
		h := &readHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}}
		sp := &readSprint{queue: askedQueue, state: map[string]string{"a.w1": "begun"}}
		r := readRig(t, h, sp, 1)
		r.run(t, 20)
		assert.Empty(t, sp.verbs("--ok"), "the result cannot be committed while an unverified child group still lives")
		assert.FileExists(t, readActivePath(readDir))
		assert.True(t, ProcessGroupAlive(cmd.Process.Pid), "a mismatched birth must not signal the process")
	})
}
