//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// makeUnreadable makes an EXISTING file refuse os.Open, and returns the release that gives
// it back.
//
// Windows has no mode bits: os.Chmod clears the read-only ATTRIBUTE and a mode-000 file is
// still perfectly readable, so the unix fixture measured nothing there and six tests of
// rule 3 passed over a property that was not present (CI, 2026-09-11).
//
// There is more than one way to refuse a read on this OS and not one of them holds on
// every runner: an exclusive handle is the cheapest, a deny ACE is what a permission
// actually is, and a dangling symlink is what is left when neither takes. So this helper
// TRIES them in that order and PROVES each one before returning -- a fixture that cannot
// be observed to work is the bug it is here to catch, and the one thing it must never do
// is quietly hand back a readable file.
func makeUnreadable(t *testing.T, path string) (release func()) {
	t.Helper()
	for _, m := range []struct {
		how   string
		apply func(*testing.T, string) (func(), bool)
	}{
		{"an exclusive handle (dwShareMode 0)", lockExclusive},
		{"a deny ACE for Everyone (icacls /deny)", denyEveryone},
		{"a dangling symlink in its place", danglingSymlink},
	} {
		undo, ok := m.apply(t, path)
		if !ok {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			t.Logf("unreadable by %s", m.how)
			return once(t, undo)
		}
		// It did not take: give the file back and try the next one.
		f.Close()
		undo()
	}
	t.Fatalf("no mechanism on this Windows made %s refuse a read: an exclusive handle, a deny ACE and a symlink were each tried and the file stayed readable", path)
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

// danglingSymlink replaces the file with a link to a name that does not exist. The bytes
// are kept so that release gives back the file the test wrote.
func danglingSymlink(t *testing.T, path string) (func(), bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if err := os.Remove(path); err != nil {
		return nil, false
	}
	if err := os.Symlink(path+".nowhere", path); err != nil {
		// Put it back: a test that never locked the file still has its fixture.
		_ = os.WriteFile(path, raw, 0o644)
		return nil, false
	}
	return func() {
		_ = os.Remove(path)
		_ = os.WriteFile(path, raw, 0o644)
	}, true
}
