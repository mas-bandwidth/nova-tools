//go:build windows

package main

import (
	"syscall"
	"testing"
)

// makeUnreadable makes an EXISTING file refuse os.Open, and returns the release that
// gives it back.
//
// Windows has no mode bits: os.Chmod there clears the read-only ATTRIBUTE and a mode-000
// file is still perfectly readable, so the unix fixture produced a readable `{}` line --
// unreadable=0, unparsed=25 -- and every test of rule 3 measured the wrong property
// (measured in CI 2026-09-11). The mechanism this OS does have is the share mode: a
// handle opened with dwShareMode 0 makes every other open fail with a sharing violation,
// in this process as in any other. That is a file that is listed and cannot be read, which
// is exactly what rule 3 is about.
//
// The handle must be closed before the file can be removed or the temp directory cleaned,
// so release is registered as a cleanup (cleanups run last-registered-first, and t.TempDir
// registered its own before this one) and is returned for the tests that delete the file
// themselves.
func makeUnreadable(t *testing.T, path string) (release func()) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0 /* no sharing */, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("locking %s: %v", path, err)
	}
	done := false
	release = func() {
		if done {
			return
		}
		done = true
		_ = syscall.CloseHandle(h)
	}
	t.Cleanup(release)
	return release
}
