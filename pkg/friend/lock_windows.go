//go:build windows

package friend

// FlockHeld is false on Windows: the writer lock is not probed there, and
// codex itself refuses a resume of an open thread.
func FlockHeld(string) bool { return false }
