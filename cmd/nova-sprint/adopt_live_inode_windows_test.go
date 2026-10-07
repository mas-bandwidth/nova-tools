package main

import "testing"

// liveInode has no inode to read on Windows, where inodeOf says 0 (unknown);
// the assertion on the server's inode is skipped there.
func liveInode(t *testing.T, _ string) uint64 {
	t.Helper()
	return 0
}
