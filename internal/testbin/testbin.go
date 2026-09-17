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
	return os.WriteFile(dst, raw, 0o755)
}
