//go:build !windows

package main

import "os"

// executableMode is the unix rule, unchanged since the refusal was written: a regular file
// with at least one of the three execute bits set. The path is not consulted -- on unix the
// name of a file says nothing about whether it can be run.
func executableMode(_ string, fi os.FileInfo) bool {
	return fi.Mode().Perm()&0o111 != 0
}
