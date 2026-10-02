//go:build windows

package main

import (
	"os"

	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeUnreadable makes an EXISTING file refuse os.Open, and returns the release that gives
// it back.
//
// Windows has no mode bits: os.Chmod clears the read-only ATTRIBUTE and a mode-000 file is
// still perfectly readable, so the unix fixture measured nothing there and six tests of
// rule 3 passed over a property that was not present (CI, 2026-09-11).
//
// There is more than one way to refuse a read on this OS: an exclusive handle is the cheapest
// and a deny ACE is what a permission actually is. Dangling symlinks are avoided because
// opening a dangling symlink produces ENOENT, which tests that distinguish absence from
// unreadable files (such as session) intentionally allow. So this helper tries these mechanisms
// in order and proves each one before returning.
func makeUnreadable(t *testing.T, path string) (release func()) {
	t.Helper()
	for _, m := range []struct {
		how   string
		apply func(*testing.T, string) (func(), bool)
	}{
		{"an exclusive handle (dwShareMode 0)", lockExclusive},
		{"a deny ACE for Everyone (icacls /deny)", denyEveryone},
	} {
		undo, ok := m.apply(t, path)
		if !ok {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				undo()
				continue
			}
			t.Logf("unreadable by %s", m.how)
			return once(t, undo)
		}
		// It did not take: give the file back and try the next one.
		f.Close()
		undo()
	}
	require.FailNowf(t, "Windows unreadable fixture remained readable", "no mechanism on this Windows made %s refuse a read: an exclusive handle and a deny ACE were each tried and the file stayed readable", path)
	return func() {}
}

// once wraps an undo so that a test may call it early -- before deleting the file, which
// an open handle or a deny ACE would otherwise prevent -- and the cleanup is still safe.
func once(t *testing.T, undo func()) func() {
	done := false
	release := func() {
		if done {
			return
		}
		done = true
		undo()
	}
	t.Cleanup(release)
	return release
}

// lockExclusive opens the file with no sharing, so every other open is a sharing violation.
func lockExclusive(t *testing.T, path string) (func(), bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, false
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0 /* no sharing */, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, false
	}
	return func() { _ = syscall.CloseHandle(h) }, true
}

// denyEveryone puts a deny ACE on the file. The SID rather than the group NAME, because
// the name is localised and a runner is not necessarily English.
func denyEveryone(t *testing.T, path string) (func(), bool) {
	if err := exec.Command("icacls", path, "/deny", "*S-1-1-0:(R)").Run(); err != nil {
		return nil, false
	}
	return func() { _ = exec.Command("icacls", path, "/remove:d", "*S-1-1-0").Run() }, true
}
