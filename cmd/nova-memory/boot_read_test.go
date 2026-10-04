package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boot READS the bytes it reports. docs/SPEC.md, boot: "Boot reads exactly
// those files ... The load is the named files' byte total, never the
// directory's" — a total only a read can produce, so a pinned file that
// passes every validation but cannot be read refuses the boot (exit 2, no
// BOOT OK) instead of counting bytes it never loaded. The defect this pins is
// docs/ratings/snapshots/0c5803c2de40/memory-read.md finding 4: loadPin
// validated each entry with os.Lstat and summed fi.Size() without opening
// anything, so a boot claimed loaded bytes it had only stat'd.

// TestBootReadsPinnedBytes pins the reads end to end: the byte total is what
// the reads returned (never the entries' sizes, never an unpinned file's), a
// pinned file that validates and then cannot be opened, read or closed
// refuses the boot naming that entry with a remedy, and arbitrary nonempty
// bytes stay valid input.
func TestBootReadsPinnedBytes(t *testing.T) {
	t.Parallel()

	t.Run("bytes are what the reads returned", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a := []byte("\x00\x01not markdown \xff\xfe: arbitrary bytes load\n")
		b := []byte("plain paragraph\n\nsecond paragraph\n")
		un := []byte("unpinned paragraph\n\nnever named by the pin\n")
		write := func(name string, content []byte) {
			t.Helper()
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0o644))
		}
		write("a.md", a)
		write("b.md", b)
		write("unpinned.md", un)
		pin := filepath.Join(dir, "pin")
		require.NoError(t, os.WriteFile(pin, []byte("# the two memories this session loads\na.md\nb.md\n"), 0o644))

		exit, stdout, stderr := runCLI(t, "", "boot", "--root", dir, "--pin", pin)
		require.Equalf(t, 0, exit, "exit = %d, stdout: %q stderr: %q", exit, stdout, stderr)
		// The total is the bytes the reads returned, and only the pinned
		// files' bytes join it: a walk or an unpinned byte would change the
		// number. Arbitrary nonempty bytes stay valid input — nothing in the
		// load validates markdown.
		want := strconv.Itoa(len(a) + len(b))
		assert.Containsf(t, stdout, "BOOT OK files=2 bytes="+want, "stdout = %q, want the read total bytes=%s", stdout, want)
		assert.NotContainsf(t, stdout, "bytes="+strconv.Itoa(len(a)+len(b)+len(un)), "the unpinned file's bytes must not join the total")
		kept, err := os.ReadFile(filepath.Join(dir, "unpinned.md"))
		require.NoError(t, err)
		assert.Equal(t, un, kept, "boot loads the pin, not the directory: the unpinned file is untouched")
	})

	t.Run("a pinned file that cannot be read refuses the boot", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("a mode-000 file is unreadable only to a caller without read permission")
		}
		dir := t.TempDir()
		a := []byte("readable paragraph\n\nsecond paragraph\n")
		b := []byte("validates on metadata, fails on read\n\nsecond paragraph\n")
		write := func(name string, content []byte) {
			t.Helper()
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0o644))
		}
		write("a.md", a)
		write("b.md", b)
		require.NoError(t, os.Chmod(filepath.Join(dir, "b.md"), 0o000))
		// The fixture is the defect's shape, proven here as nova-tokens'
		// unreadable helper proves its own: metadata still validates (a
		// regular, nonzero file Lstat accepts) while the read refuses.
		fi, err := os.Lstat(filepath.Join(dir, "b.md"))
		require.NoError(t, err)
		require.True(t, fi.Mode().IsRegular(), "the fixture must still pass the regular-file rule")
		require.NotZero(t, fi.Size(), "the fixture must still pass the nonempty rule")
		if f, err := os.Open(filepath.Join(dir, "b.md")); err == nil {
			f.Close()
			require.FailNowf(t, "unreadable fixture was readable", "mode 000 did not make b.md refuse a read")
		}

		pin := filepath.Join(dir, "pin")
		require.NoError(t, os.WriteFile(pin, []byte("# two memories\na.md\nb.md\n"), 0o644))

		exit, stdout, stderr := runCLI(t, "", "boot", "--root", dir, "--pin", pin)
		require.Equalf(t, 2, exit, "a pinned file that validates but cannot be read must refuse; stdout = %q stderr = %q", stdout, stderr)
		assert.NotContains(t, stdout, "BOOT OK", "no success line on a failed read")
		assert.Contains(t, stderr, "b.md", "the refusal names the offending entry")
		assert.Contains(t, stderr, "could not be opened for reading", "the refusal says what failed")
		assert.Contains(t, stderr, "re-run", "the refusal carries a remedy")
	})

	t.Run("a read that fails mid-file refuses the boot", func(t *testing.T) {
		t.Parallel()

		dir, pin := validatedEntry(t)
		boom := errors.New("read: input/output error")
		var opened []string
		open := func(name string) (io.ReadCloser, error) {
			opened = append(opened, name)
			return &stubPinFile{data: []byte("0123456789"), readErr: boom}, nil
		}
		var stderr strings.Builder
		n, total, ok := loadPinOpen(dir, pin, &stderr, open)
		require.False(t, ok, "a read that fails after validation must refuse")
		require.Zero(t, n)
		require.Zero(t, total)
		assert.Contains(t, stderr.String(), "a.md", "the refusal names the offending entry")
		assert.Contains(t, stderr.String(), "read 10 of 40 bytes", "the refusal reports how far the read got")
		assert.Contains(t, stderr.String(), boom.Error(), "the refusal carries the read's error")
		assert.Contains(t, stderr.String(), "re-run", "the refusal carries a remedy")
		assert.Equal(t, []string{filepath.Join(dir, "a.md")}, opened, "boot opens exactly the pinned entry it validated, nothing else")
	})

	t.Run("a close that fails after a clean read refuses the boot", func(t *testing.T) {
		t.Parallel()

		dir, pin := validatedEntry(t)
		open := func(string) (io.ReadCloser, error) {
			return &stubPinFile{data: []byte("0123456789"), readErr: io.EOF, closeErr: errors.New("close: storage error")}, nil
		}
		var stderr strings.Builder
		n, total, ok := loadPinOpen(dir, pin, &stderr, open)
		require.False(t, ok, "a close that fails after a clean read must refuse")
		require.Zero(t, n)
		require.Zero(t, total)
		assert.Contains(t, stderr.String(), "a.md", "the refusal names the offending entry")
		assert.Contains(t, stderr.String(), "could not be closed", "the refusal says what failed")
		assert.Contains(t, stderr.String(), "re-run", "the refusal carries a remedy")
	})

	t.Run("the total counts bytes consumed, not the entry's size", func(t *testing.T) {
		t.Parallel()

		dir, pin := validatedEntry(t)
		// The entry holds 40 bytes, but the read returns 10 and then a clean
		// end — the shape of a file that shrinks between validation and read.
		// The boot's total is the bytes the read returned, never the 40 the
		// directory entry promised.
		open := func(string) (io.ReadCloser, error) {
			return &stubPinFile{data: []byte("0123456789"), readErr: io.EOF}, nil
		}
		var stderr strings.Builder
		n, total, ok := loadPinOpen(dir, pin, &stderr, open)
		require.Truef(t, ok, "a clean short read loads what it got; stderr: %s", stderr.String())
		require.Equal(t, 1, n)
		assert.Equal(t, int64(10), total, "the total is the bytes the reads returned, not the entry's 40")
	})
}

// stubPinFile is the io.ReadCloser a seam test's opener returns: reads come
// from data until it is spent, then readErr arrives (io.EOF for a clean end),
// and Close reports closeErr — the read and close failures an Lstat-only boot
// never observed.
type stubPinFile struct {
	data     []byte
	readErr  error
	closeErr error
}

func (s *stubPinFile) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, s.readErr
	}
	n := copy(p, s.data)
	s.data = s.data[n:]
	return n, nil
}

func (s *stubPinFile) Close() error { return s.closeErr }

// validatedEntry writes one pinned file that passes every metadata check (a
// regular, nonzero 40-byte file) and the pin naming it, so a seam test
// controls what the read returns without touching what the validation sees.
func validatedEntry(t *testing.T) (dir, pin string) {
	t.Helper()
	dir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("0123456789012345678901234567890123456789"), 0o644))
	pin = filepath.Join(dir, "pin")
	require.NoError(t, os.WriteFile(pin, []byte("a.md\n"), 0o644))
	return dir, pin
}
