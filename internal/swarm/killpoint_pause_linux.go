//go:build linux && swarmtest

package swarm

import (
	"os"
	"syscall"
)

// CheckPausePoint checks if NOVA_SWARM_PAUSEPOINT matches point, and if so,
// stops the calling thread with a thread-directed SIGSTOP via tgkill.
// On linux syscall.Kill(getpid(), SIGSTOP) is process-directed and the kernel
// wakes the thread-group leader to take it, so the sending thread runs on until
// the leader stops the group. Using tgkill(pid, tid, SIGSTOP) directs the stop
// to the calling thread itself, which takes it on the way out of the syscall.
func CheckPausePoint(point string) {
	if pp := os.Getenv("NOVA_SWARM_PAUSEPOINT"); pp != "" && pp == point {
		if os.Getenv("NOVA_SWARM_PAUSE_AFTER_ORPHAN") != "" {
			waitForOrphan(injectedWait)
		}
		if mark := os.Getenv("NOVA_SWARM_PAUSE_MARK"); mark != "" {
			_ = os.WriteFile(mark, []byte("about to stop\n"), 0o644)
		}
		_ = syscall.Tgkill(os.Getpid(), syscall.Gettid(), syscall.SIGSTOP)
	}
}
