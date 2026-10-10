//go:build windows

package friend

import "os"

// FlockHeld is false on Windows: the writer lock is not probed there, and
// codex itself refuses a resume of an open thread.
func FlockHeld(string) bool { return false }

// tryLock is always taken on Windows: no flock there, so a second daemon beside a stage is
// kept off by the job's mark alone (stage.go).
func tryLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false, err
	}
	return func() { f.Close() }, true, nil // ignored: nothing is held
}
