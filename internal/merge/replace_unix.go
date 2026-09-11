//go:build unix

package merge

// replaceRefusal is never true here. On unix a rename is atomic FOR READERS TOO: an open
// of the state file either finds the old inode or the new one, and no reader is ever told
// no because a replace is in flight. There is nothing to wait out, so Load does not wait.
func replaceRefusal(error) bool { return false }
