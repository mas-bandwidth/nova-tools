//go:build unix

package merge

import "os"

// replaceRefusal is never true here. On unix a rename is atomic FOR READERS TOO: an open
// of the state file either finds the old inode or the new one, and no reader is ever told
// no because a replace is in flight. There is nothing to wait out, so Load does not wait.
func replaceRefusal(error) bool { return false }

// readShared is os.ReadFile: on unix there is no share mode, an open never refuses a
// rename, and a rename never refuses an open. The name exists so that state.go has one
// read call on every platform and no build tag of its own.
func readShared(path string) ([]byte, error) { return os.ReadFile(path) }
