//go:build unix && !linux && swarmtest

package swarm

import (
	"os"
	"syscall"
)

// CheckPausePoint checks if NOVA_SWARM_PAUSEPOINT matches point, and if so,
// stops the process with SIGSTOP until resumed with SIGCONT.
func CheckPausePoint(point string) {
	if pp := os.Getenv("NOVA_SWARM_PAUSEPOINT"); pp != "" && pp == point {
		if os.Getenv("NOVA_SWARM_PAUSE_AFTER_ORPHAN") != "" {
			waitForOrphan(injectedWait)
		}
		if mark := os.Getenv("NOVA_SWARM_PAUSE_MARK"); mark != "" {
			// ignored: a test-only kill point; the mark is a hint for the test harness, which times out without it
			_ = os.WriteFile(mark, []byte("about to stop\n"), 0o644)
		}
		// ignored: a test-only kill point stopping itself; a failed stop leaves the run going, which the test reads
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
	}
}
