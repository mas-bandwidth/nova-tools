//go:build windows

package fuse

// checkBoxAncestors is a POSIX ownership boundary. Windows has no syscall.Stat_t
// UID with which to distinguish root-owned platform links from user-owned links.
func checkBoxAncestors(string) error { return nil }
