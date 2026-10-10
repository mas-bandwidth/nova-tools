package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSuccessAndFileModes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	testCases := []struct {
		name     string
		filename string
		initial  []byte
		updated  []byte
		mode     os.FileMode
		useAlias bool
	}{
		{"new_file_0644", "file1.txt", []byte("hello world\n"), nil, 0o644, false},
		{"overwrite_0600", "file2.txt", []byte("initial content\n"), []byte("updated private content\n"), 0o600, false},
		{"overwrite_0755", "file3.sh", []byte("#!/bin/sh\necho old\n"), []byte("#!/bin/sh\necho new\n"), 0o755, false},
		{"read_only_0444", "file4.ro", []byte("read only data\n"), nil, 0o444, false},
		{"empty_content_via_writefile_alias", "file5.empty", []byte{}, nil, 0o644, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := filepath.Join(dir, tc.filename)

			writeFn := Write
			if tc.useAlias {
				writeFn = WriteFile
			}

			require.NoError(t, writeFn(target, tc.initial, tc.mode))

			got, err := os.ReadFile(target)
			require.NoError(t, err)
			require.True(t, bytes.Equal(got, tc.initial))

			info, err := os.Stat(target)
			require.NoError(t, err)
			require.Equal(t, referencePerm(dir, tc.filename, tc.mode), info.Mode().Perm())

			if tc.updated != nil {
				require.NoError(t, writeFn(target, tc.updated, tc.mode))
				gotUpdated, err := os.ReadFile(target)
				require.NoError(t, err)
				require.True(t, bytes.Equal(gotUpdated, tc.updated))
				infoUpdated, err := os.Stat(target)
				require.NoError(t, err)
				require.Equal(t, referencePerm(dir, tc.filename+".updated", tc.mode), infoUpdated.Mode().Perm())
			}
		})
	}
}

func referencePerm(dir, name string, perm os.FileMode) os.FileMode {
	ref := filepath.Join(dir, "."+name+".ref")
	f, err := os.OpenFile(ref, os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return perm
	}
	_ = f.Close()
	defer func() { _ = os.Remove(ref) }() // ignored: a reference file removed on return; it may already be gone
	st, err := os.Stat(ref)
	if err != nil {
		return perm
	}
	return st.Mode().Perm()
}

func requireErrNamesPath(t *testing.T, err error, path string) {
	t.Helper()
	require.Contains(t, err.Error(), path, "error %q does not name path %q", err, path)
}

// requireRefused pins a refusal: a non-nil error that names the path and contains
// the expected substring.
func requireRefused(t *testing.T, err error, path, want string) {
	t.Helper()
	require.Error(t, err, "Write %q should have been refused", path)
	requireErrNamesPath(t, err, path)
	if want != "" {
		require.Contains(t, err.Error(), want, "error %q does not mention %q", err, want)
	}
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("empty_path", func(t *testing.T) {
		t.Parallel()
		requireRefused(t, Write("", []byte("data"), 0o644), "", "empty")
	})

	t.Run("base_name_too_long", func(t *testing.T) {
		t.Parallel()
		tooLongBase := strings.Repeat("a", maxBaseNameLen+1)
		target := filepath.Join(dir, tooLongBase)
		requireRefused(t, Write(target, []byte("data"), 0o644), target, "exceeds maximum length")
	})

	t.Run("target_is_directory", func(t *testing.T) {
		t.Parallel()
		sub := filepath.Join(dir, "subdir")
		require.NoError(t, os.Mkdir(sub, 0o755))
		requireRefused(t, Write(sub, []byte("data"), 0o644), sub, "")
		st, err := os.Stat(sub)
		require.NoError(t, err)
		require.True(t, st.IsDir())
	})

	t.Run("target_is_symlink", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "target.txt")
		require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))
		link := filepath.Join(dir, "link.txt")
		require.NoError(t, os.Symlink(target, link))

		requireRefused(t, Write(link, []byte("replacement\n"), 0o644), link, "")

		lst, err := os.Lstat(link)
		require.NoError(t, err)
		require.NotZero(t, lst.Mode()&os.ModeSymlink)
		targetBytes, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, "original\n", string(targetBytes))
	})

	t.Run("target_is_broken_symlink", func(t *testing.T) {
		t.Parallel()
		broken := filepath.Join(dir, "dangling.txt")
		require.NoError(t, os.Symlink(filepath.Join(dir, "nonexistent.txt"), broken))

		requireRefused(t, Write(broken, []byte("data\n"), 0o644), broken, "")

		lst, err := os.Lstat(broken)
		require.NoError(t, err)
		require.NotZero(t, lst.Mode()&os.ModeSymlink)
	})

	t.Run("target_in_readonly_directory", func(t *testing.T) {
		t.Parallel()
		roDir := filepath.Join(dir, "ro-dir")
		require.NoError(t, os.Mkdir(roDir, 0o755))
		target := filepath.Join(roDir, "file.txt")
		require.NoError(t, os.Chmod(roDir, 0o555))
		defer func() { _ = os.Chmod(roDir, 0o755) }()

		requireRefused(t, Write(target, []byte("data"), 0o644), target, "")
	})

	t.Run("parent_does_not_exist", func(t *testing.T) {
		t.Parallel()
		missingParent := filepath.Join(dir, "no-such-parent", "file.txt")
		requireRefused(t, Write(missingParent, []byte("data"), 0o644), missingParent, "")
		_, err := os.Stat(filepath.Dir(missingParent))
		require.True(t, os.IsNotExist(err))
	})

	t.Run("parent_is_not_a_directory", func(t *testing.T) {
		t.Parallel()
		regParent := filepath.Join(dir, "file_as_parent")
		require.NoError(t, os.WriteFile(regParent, []byte("regular file\n"), 0o644))
		target := filepath.Join(regParent, "child.txt")
		requireRefused(t, Write(target, []byte("data"), 0o644), target, "")
	})

	t.Run("not_clean_path", func(t *testing.T) {
		t.Parallel()
		dirtyPath := dir + "/sub/../file.txt"
		requireRefused(t, Write(dirtyPath, []byte("data"), 0o644), dirtyPath, "not clean")
	})

	t.Run("unsupported_mode_bits", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "setuid_file.txt")
		requireRefused(t, Write(target, []byte("data"), 0o4755), target, "unsupported file mode")
	})

	t.Run("eval_symlinks_error", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "eval_err.txt")
		injectedErr := errors.New("injected eval symlinks failure")
		h := defaultHooks()
		h.evalSymlinks = func(path string) (string, error) { return "", injectedErr }
		err := writeWithHooks(target, []byte("data"), 0o644, h)
		requireRefused(t, err, target, "")
		require.ErrorIs(t, err, injectedErr)
	})

	t.Run("unexpected_lstat_error", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "lstat_err.txt")
		injectedErr := errors.New("injected lstat failure")
		h := defaultHooks()
		h.lstat = func(name string) (os.FileInfo, error) { return nil, injectedErr }
		err := writeWithHooks(target, []byte("data"), 0o644, h)
		requireRefused(t, err, target, "")
		require.ErrorIs(t, err, injectedErr)
	})
}

// TestParentSymlink is the witness layout: intended/linkdir points at outside/,
// and a write of intended/linkdir/note.txt must not change outside/note.txt.
func TestParentSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	intended := filepath.Join(root, "intended")
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.Mkdir(intended, 0o755))
	note := filepath.Join(outside, "note.txt")
	const original = "original-outside\n"
	require.NoError(t, os.WriteFile(note, []byte(original), 0o644))
	linkdir := filepath.Join(intended, "linkdir")
	require.NoError(t, os.Symlink(outside, linkdir))

	target := filepath.Join(linkdir, "note.txt")
	err := WriteFile(target, []byte("replaced\n"), 0o644)
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "atomicfile")
	require.Contains(t, msg, "symlink")
	requireErrNamesPath(t, err, target)
	require.Contains(t, msg, "pass the real directory")

	got, rerr := os.ReadFile(note)
	require.NoError(t, rerr)
	require.Equal(t, original, string(got))
	entries, rerr := os.ReadDir(outside)
	require.NoError(t, rerr)
	require.Len(t, entries, 1)
	require.Equal(t, "note.txt", entries[0].Name())
	lst, rerr := os.Lstat(linkdir)
	require.NoError(t, rerr)
	require.NotZero(t, lst.Mode()&os.ModeSymlink)
}

func TestSanitizedError(t *testing.T) {
	t.Parallel()

	rawErr := errors.New("raw message\nwith newline and \x1b[31mescape sequence")
	wrapped := wrapErr("atomicfile: test operation", rawErr)

	require.Error(t, wrapped)
	require.NotContains(t, wrapped.Error(), "\n")
	require.NotContains(t, wrapped.Error(), "\x1b")
	require.ErrorIs(t, wrapped, rawErr)

	nilWrapped := wrapErr("prefix", nil)
	require.NoError(t, nilWrapped)
}

// Calling sync on a closed file descriptor MUST return an error.
// If defaultHooks().sync was mutated to a dummy `return nil`, this test fails immediately!
func TestDefaultHooksSyncWithTeeth(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "teeth-*.tmp")
	require.NoError(t, err)
	name := f.Name()
	require.NoError(t, f.Close())
	defer func() { _ = os.Remove(name) }()

	syncErr := defaultHooks().sync(f)
	require.Error(t, syncErr)
}

func TestFsyncExecuted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "synced_file.txt")

	syncCalled := false
	h := defaultHooks()
	h.sync = func(f *os.File) error {
		syncCalled = true
		return f.Sync()
	}

	require.NoError(t, writeWithHooks(target, []byte("sync test\n"), 0o644, h))
	require.True(t, syncCalled)

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "sync test\n", string(got))
}

func TestDefaultSyncDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, defaultSyncDir(dir))
	require.NoError(t, defaultSyncDir(filepath.Join(dir, "does_not_exist")))
}

func TestSyncDirExecuted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "synced_dir_file.txt")

	var syncedDir string
	h := defaultHooks()
	h.syncDir = func(d string) error { syncedDir = d; return nil }

	require.NoError(t, writeWithHooks(target, []byte("dir sync test\n"), 0o644, h))
	require.Equal(t, dir, syncedDir)
}

func TestSyncDirFailureIgnored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "synced_dir_ignore_err.txt")

	h := defaultHooks()
	h.syncDir = func(d string) error { return errors.New("simulated dir sync error") }

	require.NoError(t, writeWithHooks(target, []byte("dir sync ignore err test\n"), 0o644, h))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "dir sync ignore err test\n", string(got))
}

func TestCleanupFailureJoined(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "cleanup_fail.txt")

	writeErr := errors.New("simulated write failure")
	removeErr := errors.New("simulated remove failure")

	h := defaultHooks()
	h.write = func(f *os.File, data []byte) (int, error) { return 0, writeErr }
	h.remove = func(name string) error {
		_ = os.Remove(name)
		return removeErr
	}

	err := writeWithHooks(target, []byte("data"), 0o644, h)
	require.Error(t, err)
	require.ErrorIs(t, err, writeErr)
	require.ErrorIs(t, err, removeErr)
	require.Contains(t, err.Error(), "cleanup failed for")
}

func TestCreateTempCollisionAndExhaustion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("collision_resolved_on_retry", func(t *testing.T) {
		t.Parallel()
		preName := filepath.Join(dir, fmt.Sprintf(".test.tmp-%08x", 0x12345678))
		require.NoError(t, os.WriteFile(preName, []byte("existing"), 0o600))
		defer func() { _ = os.Remove(preName) }()

		attempts := 0
		randFn := func() (uint32, error) {
			attempts++
			if attempts == 1 {
				return 0x12345678, nil
			}
			return 0x87654321, nil
		}

		f, err := createTempFile(dir, "test", 0o600, randFn)
		require.NoError(t, err)
		_ = f.Close()
		_ = os.Remove(f.Name())

		require.Equal(t, 2, attempts)
	})

	t.Run("attempts_exhausted", func(t *testing.T) {
		t.Parallel()
		collidingName := filepath.Join(dir, fmt.Sprintf(".exhaust.tmp-%08x", 0xabcdef01))
		require.NoError(t, os.WriteFile(collidingName, []byte("existing"), 0o600))
		defer func() { _ = os.Remove(collidingName) }()

		randFn := func() (uint32, error) { return 0xabcdef01, nil }

		f, err := createTempFile(dir, "exhaust", 0o600, randFn)
		if err == nil {
			_ = f.Close()
			require.FailNow(t, "createTempFile succeeded despite constant collision; want exhaustion")
		}
		wantMsg := fmt.Sprintf("after %d attempts", maxCreateTempAttempts)
		require.Contains(t, err.Error(), wantMsg)
	})

	t.Run("rand_error", func(t *testing.T) {
		t.Parallel()
		randErr := errors.New("entropy exhausted")
		randFn := func() (uint32, error) { return 0, randErr }
		_, err := createTempFile(dir, "randerr", 0o600, randFn)
		require.ErrorIs(t, err, randErr)
	})
}

func TestStepFailureInjection(t *testing.T) {
	t.Parallel()

	steps := []string{"create", "write", "chmod", "sync", "close", "rename"}

	for _, step := range steps {
		t.Run(step+"_failure_target_not_preexisting", func(t *testing.T) {
			t.Parallel()
			testStepFailure(t, step, false)
		})
		t.Run(step+"_failure_target_preexisting", func(t *testing.T) {
			t.Parallel()
			testStepFailure(t, step, true)
		})
	}
}

func testStepFailure(t *testing.T, failStep string, preexisting bool) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	const initialContent = "PRE-EXISTING CONTENT\n"
	const newContent = "NEW CONTENT THAT SHOULD FAIL\n"

	if preexisting {
		require.NoError(t, os.WriteFile(target, []byte(initialContent), 0o644))
	}

	injectedErr := fmt.Errorf("injected failure at %s", failStep)
	h := defaultHooks()

	switch failStep {
	case "create":
		h.createTemp = func(dir, base string, perm os.FileMode) (*os.File, error) { return nil, injectedErr }
	case "write":
		h.write = func(f *os.File, data []byte) (int, error) { return 0, injectedErr }
	case "chmod":
		h.chmod = func(f *os.File, mode os.FileMode) error { return injectedErr }
	case "sync":
		h.sync = func(f *os.File) error { return injectedErr }
	case "close":
		h.close = func(f *os.File) error { _ = f.Close(); return injectedErr }
	case "rename":
		h.rename = func(oldpath, newpath string) error { return injectedErr }
	default:
		require.FailNow(t, fmt.Sprintf("unknown step %q", failStep))
	}

	opts := []Option{}
	if failStep == "chmod" {
		opts = append(opts, ExactMode())
	}
	err := writeWithHooks(target, []byte(newContent), 0o644, h, opts...)
	require.Error(t, err)
	requireErrNamesPath(t, err, target)
	require.ErrorIs(t, err, injectedErr)

	if preexisting {
		got, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, initialContent, string(got))
	} else {
		_, err := os.Stat(target)
		require.True(t, os.IsNotExist(err))
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.Equal(t, "target.txt", e.Name())
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) {
	return 0, errors.New("simulated entropy read failure")
}

func TestRandomUint32Error(t *testing.T) {
	t.Parallel()
	_, err := randomUint32(errReader{})
	require.Error(t, err)
}

// The final file sync must cover the metadata ExactMode changes as well as data.
func TestExactModeIsSetBeforeSync(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "note")
	h := defaultHooks()
	chmod := h.chmod
	changed := false
	h.chmod = func(f *os.File, mode os.FileMode) error {
		if err := chmod(f, mode); err != nil {
			return err
		}
		changed = true
		return nil
	}
	h.sync = func(f *os.File) error {
		if !changed {
			return errors.New("file sync precedes ExactMode chmod")
		}
		return f.Sync()
	}
	require.NoError(t, writeWithHooks(target, []byte("complete\n"), 0o644, h, ExactMode()))
}

// Check refuses exactly what Write refuses before it writes, with the same error, and
// makes nothing: a dry run that calls it plans the write it skips.
func TestCheckRefusesWhatWriteRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	for _, tc := range []struct {
		name string
		path string
		perm os.FileMode
		ok   bool
	}{
		{"empty", "", 0o644, false},
		{"not clean", dir + "/x/../y", 0o644, false},
		{"bad mode", filepath.Join(dir, "m"), 0o1777, false},
		{"missing parent", filepath.Join(dir, "missing", "f"), 0o644, false},
		{"parent is a file", filepath.Join(file, "f"), 0o644, false},
		{"target is a directory", dir, 0o644, false},
		{"a new file", filepath.Join(dir, "new"), 0o644, true},
		{"an existing file", file, 0o644, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := os.ReadDir(dir)
			require.NoError(t, err)
			checked := Check(tc.path, tc.perm)
			after, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Equal(t, len(before), len(after), "Check made an entry")
			if tc.ok {
				assert.NoError(t, checked)
				return
			}
			require.Error(t, checked)
			written := Write(tc.path, []byte("y"), tc.perm)
			require.Error(t, written)
			assert.Equal(t, written.Error(), checked.Error())
		})
	}
}
