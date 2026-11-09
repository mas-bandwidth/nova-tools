//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reservation directory is shared by every user of the machine, so an
// entry in it may be anything another user put there. These tests put links
// and special files where the tool's own files go. They are Unix tests, as
// os_unix.go is: nova-reserve has no other build.

// odd is the refusal of an entry that is not a regular file.
func odd(path string) string {
	return path + " is not a regular file; nova-reserve opens no link or special file in its directory; run: sudo rm -f " + shellQuote(path)
}

// private is a file outside the reservation directory that only its owner may
// read or write; same checks it is as it was made.
func private(t *testing.T) (path string, same func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.WriteFile(path, []byte("export PATH=/usr/bin\n"), 0o600))
	return path, func() {
		t.Helper()
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "export PATH=/usr/bin\n", string(b), "the link's target keeps its content")
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the link's target keeps its mode")
	}
}

func requireOdd(t *testing.T, err error, path string) {
	t.Helper()
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, exitError{2, odd(path)}, *e)
}

// TestCapacityThroughALinkIsRefused: with capacity a link to another file, a
// run, set and status refuse, and the file is as it was.
func TestCapacityThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target, same := private(t)
	link := filepath.Join(h.dir, "capacity")
	require.NoError(t, os.Symlink(target, link))
	assert.Equal(t, 2, run([]string{"--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: "+odd(link)+"\n", h.err.String())
	assert.Nil(t, h.ran)
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"set", "--slots", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+odd(link)+"\n", h.err.String())
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"status"}, h.w))
	assert.Equal(t, "nova-reserve status: "+odd(link)+"\n", h.err.String())
	assert.Empty(t, h.out.String())
	assert.True(t, h.f.idle())
	same()
}

// TestNewCapacityThroughALinkIsRefused: the temporary file a first write
// renames into place is a link to nothing: nothing is made where it points,
// and no capacity is stored.
func TestNewCapacityThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(t.TempDir(), "made")
	link := filepath.Join(h.dir, "capacity.42.tmp")
	require.NoError(t, os.Symlink(target, link))
	assert.Equal(t, 2, run([]string{"set", "--slots", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+odd(link)+"\n", h.err.String())
	assert.NoFileExists(t, target)
	assert.NoFileExists(t, filepath.Join(h.dir, "capacity"))
	assert.True(t, h.f.idle())
}

// TestHolderThroughALinkIsRefused: a holder path this run cannot remove
// (another user's in the sticky directory; here the directory is read-only)
// that is a link, to nothing or to a file: nothing is made or written where
// it points, and the hold is refused.
func TestHolderThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	for _, dangling := range []bool{true, false} {
		h := newHarness(t)
		h.setCap(t, 1)
		target, same := private(t)
		if dangling {
			require.NoError(t, os.Remove(target))
		}
		link := filepath.Join(h.dir, "slot1.holder")
		require.NoError(t, os.Symlink(target, link))
		require.NoError(t, os.Chmod(h.dir, 0o555))
		t.Cleanup(func() { _ = os.Chmod(h.dir, 0o755) })
		tk, err := acquire(h.w, h.opts(0, false, 0))
		require.NoError(t, err)
		requireOdd(t, tk.mark(tk.name()), link)
		if dangling {
			assert.NoFileExists(t, target)
		} else {
			same()
		}
	}
}

// TestReleaseEmptiesTheFileItWrote: a holder path that became a link while the
// command ran: release empties the file the hold wrote, never the link's target.
func TestReleaseEmptiesTheFileItWrote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 1)
	tk := h.take(t, h.opts(0, false, 0))
	target, same := private(t)
	p := filepath.Join(h.dir, "slot1.holder")
	require.NoError(t, os.Remove(p))
	require.NoError(t, os.Symlink(target, p))
	tk.release()
	same()
	assert.True(t, h.f.idle())
}

// TestLockThroughALinkIsRefused: a lock path that is a link, to nothing or to
// a file: no lock is taken, and nothing is made where it points.
func TestLockThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	for _, dangling := range []bool{true, false} {
		target, same := private(t)
		if dangling {
			require.NoError(t, os.Remove(target))
		}
		link := filepath.Join(t.TempDir(), "gate")
		require.NoError(t, os.Symlink(target, link))
		_, ok, err := osLock(link, true)
		assert.False(t, ok)
		requireOdd(t, err, link)
		if dangling {
			assert.NoFileExists(t, target)
		} else {
			same()
		}
	}
}

// TestDirectoryAsALockFileIsRefused: what is open is checked, not only the
// path: a directory where a lock file goes is no lock.
func TestDirectoryAsALockFileIsRefused(t *testing.T) {
	t.Parallel()
	gate := filepath.Join(t.TempDir(), "gate")
	require.NoError(t, os.Mkdir(gate, 0o755))
	_, ok, err := osLock(gate, true)
	assert.False(t, ok)
	requireOdd(t, err, gate)
}

// TestFIFOAsCapacityIsRefusedNotWaitedOn: a FIFO has no writer, so a read
// that waited for one would never return.
func TestFIFOAsCapacityIsRefusedNotWaitedOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fifo := filepath.Join(h.dir, "capacity")
	require.NoError(t, syscall.Mkfifo(fifo, 0o666))
	_, _, err := readCapacity(h.dir)
	requireOdd(t, err, fifo)
}

// TestMadeFilesAreForEveryUser: under a umask of 077, every file a run makes in
// the directory can still be read and written by every user, the lock files
// too: no user can take a lock on a file it may not open. Not parallel: the
// umask is the process's, so this runs while no other test does.
func TestMadeFilesAreForEveryUser(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	h := newHarness(t)
	h.w.lock = osLock
	mode := func(name string) os.FileMode {
		fi, err := os.Stat(filepath.Join(h.dir, name))
		require.NoError(t, err)
		return fi.Mode().Perm()
	}
	h.w.exec = func(string, []string) int {
		assert.Equal(t, os.FileMode(0o666), mode("slot1.holder"), "slot1.holder")
		return 0
	}
	require.Equal(t, 0, run([]string{"--slots", "2", "--", "make"}, h.w), h.err.String())
	for _, name := range []string{"capacity", "gate", "all", "slot1"} {
		assert.Equal(t, os.FileMode(0o666), mode(name), name)
	}
}

// linked is the refusal of an entry that is one of two names of one file.
func linked(path string) string {
	return path + " has 2 names (a hard link); nova-reserve opens no file in its directory that has another name; run: sudo rm -f " + shellQuote(path)
}

// hardLink makes path a second name of a file outside the reservation
// directory holding a valid capacity; same checks it is as it was made.
func hardLink(t *testing.T, path string) (same func()) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(target, []byte("4\n"), 0o600))
	require.NoError(t, os.Link(target, path))
	return func() {
		t.Helper()
		b, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.Equal(t, "4\n", string(b), "the other name's file keeps its content")
		fi, err := os.Stat(target)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the other name's file keeps its mode")
	}
}

// TestHardLinkedCapacityIsRefused: capacity, or the temporary file a first
// write renames into place, is a second name of a file elsewhere: a run, set
// and status refuse, and that file is as it was.
func TestHardLinkedCapacityIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := filepath.Join(h.dir, "capacity")
	same := hardLink(t, p)
	assert.Equal(t, 2, run([]string{"set", "--slots", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+linked(p)+"\n", h.err.String())
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: "+linked(p)+"\n", h.err.String())
	assert.Nil(t, h.ran)
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"status"}, h.w))
	assert.Equal(t, "nova-reserve status: "+linked(p)+"\n", h.err.String())
	assert.True(t, h.f.idle())
	same()

	h = newHarness(t)
	tmp := filepath.Join(h.dir, "capacity.42.tmp")
	same = hardLink(t, tmp)
	assert.Equal(t, 2, run([]string{"set", "--slots", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+linked(tmp)+"\n", h.err.String())
	assert.NoFileExists(t, filepath.Join(h.dir, "capacity"))
	same()
}

// TestHardLinkedHolderIsRefused: a holder this run cannot remove (the
// directory is read-only here) that is a second name of a file elsewhere: the
// hold is refused and that file is as it was.
func TestHardLinkedHolderIsRefused(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	h := newHarness(t)
	h.setCap(t, 1)
	p := filepath.Join(h.dir, "slot1.holder")
	same := hardLink(t, p)
	require.NoError(t, os.Chmod(h.dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(h.dir, 0o755) })
	tk, err := acquire(h.w, h.opts(0, false, 0))
	require.NoError(t, err)
	err = tk.mark(tk.name())
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, exitError{2, linked(p)}, *e)
	same()
}

// TestHardLinkedLockIsRefused: a lock file that is a second name of a file
// elsewhere: no lock is taken and that file is as it was.
func TestHardLinkedLockIsRefused(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "gate")
	same := hardLink(t, p)
	_, ok, err := osLock(p, true)
	assert.False(t, ok)
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, exitError{2, linked(p)}, *e)
	same()
}

// TestDirectoryLinkIsRefused: the directory's own path is a link to another
// directory: a run is refused, and that directory keeps its mode and is empty.
func TestDirectoryLinkIsRefused(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	require.NoError(t, os.Chmod(target, 0o700))
	link := filepath.Join(t.TempDir(), "reserve")
	require.NoError(t, os.Symlink(target, link))
	h := newHarness(t)
	assert.Equal(t, 2, run([]string{"--dir", link, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: --dir "+link+" is a symbolic link; nova-reserve uses no link as its directory: give the directory it names, or remove the link; run: ls -ld "+shellQuote(link)+"\n", h.err.String())
	assert.Nil(t, h.ran)
	fi, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.ModeDir|0o700, fi.Mode(), "the link's target keeps its mode")
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
