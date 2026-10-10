//go:build linux && functional

package swarm

// This file's tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). They run under -tags functional.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/require"
)

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// waitStopped reports whether pid reaches state T (stopped by a signal) within d,
// read from /proc/<pid>/stat: the state is the first field after the ")" that
// closes the command name.
func waitStopped(pid int, d time.Duration) bool {
	stat := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(time.Millisecond) {
		b, err := os.ReadFile(stat)
		if err != nil {
			continue
		}
		if i := bytes.LastIndexByte(b, ')'); i >= 0 && i+2 < len(b) && b[i+2] == 'T' {
			return true
		}
	}
	return false
}

func TestPausePointThreadDirected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	markPath := filepath.Join(dir, "mark.txt")
	afterPath := filepath.Join(dir, "after.txt")

	repoRoot, err := findRepoRoot()
	require.NoError(t, err)

	// The probe is committed at pkg/swarm/testprobe (build tag
	// swarmtest); the test never writes into the repository tree.
	binFile := filepath.Join(dir, "probe_bin")
	cmd := exec.Command("go", "build", "-tags", "swarmtest", "-o", binFile, "./pkg/swarm/testprobe")
	cmd.Dir = repoRoot
	cmd.Env = append(goenv.Clean(os.Environ()), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "build probe: %v\n%s", err, out)

	for i := 0; i < 200; i++ {
		err := os.Remove(markPath)
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}
		err = os.Remove(afterPath)
		if !os.IsNotExist(err) {
			require.NoError(t, err)
		}

		child := exec.Command(binFile)
		child.Env = append(os.Environ(),
			"NOVA_SWARM_PAUSEPOINT=test-pause",
			"NOVA_SWARM_PAUSE_MARK="+markPath,
			"PROBE_DIR="+dir,
		)
		require.NoError(t, child.Start())

		for waited := 0; waited < 3000; waited++ {
			if _, err := os.Stat(markPath); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}

		// The mark is written just before the child's tgkill, so seeing it does
		// not mean the child has stopped. A SIGCONT sent before the stop is lost,
		// the child then stops for good and Wait never returns: the functional
		// shard's 1m40s timeout under load. Wait for the stop itself (state T).
		if !waitStopped(child.Process.Pid, 5*time.Second) {
			_ = child.Process.Kill()
			_ = child.Wait()
			require.Fail(t, "the child never stopped at its pause point", "iteration %d", i)
		}

		_, err = os.Stat(afterPath)
		if err == nil {
			child.Process.Kill()
			child.Wait()
		}
		require.Error(t, err, "iteration %d: after.txt existed while child should be stopped (proof of process-directed SIGSTOP race)", i)

		_ = child.Process.Signal(syscall.SIGCONT)
		child.Wait()
	}
}
