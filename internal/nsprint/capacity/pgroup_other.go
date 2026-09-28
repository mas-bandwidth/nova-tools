//go:build !unix

package capacity

// reapGroup cannot signal a process group off unix, so it never confirms one
// gone: the debit stays until a unix bench reaps it (fail closed).
func reapGroup(pgid int) bool {
	_ = pgid
	return false
}
