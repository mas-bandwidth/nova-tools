//go:build windows

package main

// inodeOf has no inode to read on Windows; 0 means unknown, as on a stat error.
func inodeOf(path string) uint64 { return 0 }
