// Package testbin places a built program into a test's directory.
//
// On macOS every fresh copy of an executable is a never-seen binary that the
// system policy scanner assesses on its first exec. One is quick; a package run
// that copies the same built helper into dozens of fixtures queues them behind
// the scanner for longer than a test waits. A hard link shares the inode the
// scanner has already assessed, so the rule is to link first and copy only
// where a link is impossible: a temp directory on another filesystem, and
// Windows.
package testbin

import (
	"os"
	"runtime"
	"syscall"
)

// link is os.Link by default. A test swaps it to exercise the copy fallback
// without needing a filesystem where Link fails.
var link = os.Link

// Place puts the program at src at dst. It removes dst first, then hard-links
// src to dst where the platform and filesystem allow it and returns nil; the
// fallback is a byte copy with the executable bit set (mode 0o755). Windows
// always takes the copy, because a link there needs a privilege the CI path
// does not have.
func Place(src, dst string) error {
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := link(src, dst); err == nil {
			return nil
		}
	}
	return copyFile(src, dst)
}

// copyFile writes src's bytes to dst with the executable bit set, the fallback
// Place uses when a hard link is impossible.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return WriteExecutable(dst, raw, 0o755)
}

// PlaceCopy puts a real byte copy of src at dst, never a link, and is the one
// entry point a test should use when the SUBJECT of the test is that the file
// is not the same binary. A hard link shares src's inode, so a guard that asks
// "is my parent this executable?" answers yes through a link and the test
// silently stops testing anything. cmd/nova-sandbox's probe-step guard is that
// test. Everywhere else, Place: a copy there only costs macOS's policy scanner
// time (see the package comment).
func PlaceCopy(src, dst string) error {
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return copyFile(src, dst)
}

// WriteExecutable is os.WriteFile for a file a test is about to exec: a fake
// tool, a stand-in script, a stub on PATH. It holds syscall.ForkLock for
// reading while the file is open for writing, so no other goroutine can fork
// in that window. Without it, a parallel test's fork inherits the write
// descriptor for the instant before its exec, and this test's exec of the file
// fails on Linux with ETXTBSY, "text file busy" (golang/go#22315): the flake
// internal/secrets' seat test hit the first time its package ran in parallel.
func WriteExecutable(path string, data []byte, perm os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, data, perm)
}
