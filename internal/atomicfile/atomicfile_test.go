package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		{
			name:     "new_file_0644",
			filename: "file1.txt",
			initial:  []byte("hello world\n"),
			mode:     0o644,
		},
		{
			name:     "overwrite_0600",
			filename: "file2.txt",
			initial:  []byte("initial content\n"),
			updated:  []byte("updated private content\n"),
			mode:     0o600,
		},
		{
			name:     "overwrite_0755",
			filename: "file3.sh",
			initial:  []byte("#!/bin/sh\necho old\n"),
			updated:  []byte("#!/bin/sh\necho new\n"),
			mode:     0o755,
		},
		{
			name:     "read_only_0444",
			filename: "file4.ro",
			initial:  []byte("read only data\n"),
			mode:     0o444,
		},
		{
			name:     "empty_content_via_writefile_alias",
			filename: "file5.empty",
			initial:  []byte{},
			mode:     0o644,
			useAlias: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := filepath.Join(dir, tc.filename)

			writeFn := Write
			if tc.useAlias {
				writeFn = WriteFile
			}

			err := writeFn(target, tc.initial, tc.mode)
			require.NoError(t, err, "Write(%q) failed: %v", target, err)

			got, err := os.ReadFile(target)
			require.NoError(t, err, "ReadFile(%q) failed: %v", target, err)
			require.True(t, bytes.Equal(got, tc.initial), "ReadFile(%q) = %q, want %q", target, got, tc.initial)

			info, err := os.Stat(target)
			require.NoError(t, err, "Stat(%q) failed: %v", target, err)
			wantPerm := referencePerm(dir, tc.filename, tc.mode)
			require.Equal(t, wantPerm, info.Mode().Perm(), "file perm = %04o, want %04o", info.Mode().Perm(), wantPerm)

			if tc.updated != nil {
				err := writeFn(target, tc.updated, tc.mode)
				require.NoError(t, err, "second Write(%q) failed: %v", target, err)
				gotUpdated, err := os.ReadFile(target)
				require.NoError(t, err, "ReadFile(%q) after update failed: %v", target, err)
				require.True(t, bytes.Equal(gotUpdated, tc.updated), "ReadFile(%q) = %q, want %q", target, gotUpdated, tc.updated)
				infoUpdated, err := os.Stat(target)
				require.NoError(t, err, "Stat(%q) after update failed: %v", target, err)
				wantPermUpdated := referencePerm(dir, tc.filename+".updated", tc.mode)
				require.Equal(t, wantPermUpdated, infoUpdated.Mode().Perm(), "file perm after update = %04o, want %04o", infoUpdated.Mode().Perm(), wantPermUpdated)
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
	defer os.Remove(ref)
	st, err := os.Stat(ref)
	if err != nil {
		return perm
	}
	return st.Mode().Perm()
}

// requireErrNamesPath pins a refusal: an error whose text names the refused path.
func requireErrNamesPath(t *testing.T, err error, path string) {
	t.Helper()
	require.Contains(t, err.Error(), path, "error %q does not name path %q", err, path)
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("empty_path", func(t *testing.T) {
		t.Parallel()
		err := Write("", []byte("data"), 0o644)
		require.Error(t, err, "Write with empty path succeeded; want error")
		require.Contains(t, err.Error(), "empty", "error %q does not mention empty", err)
	})

	t.Run("base_name_too_long", func(t *testing.T) {
		t.Parallel()
		tooLongBase := strings.Repeat("a", maxBaseNameLen+1)
		target := filepath.Join(dir, tooLongBase)
		err := Write(target, []byte("data"), 0o644)
		require.Error(t, err, "Write with base name exceeding limit succeeded; want refusal")
		requireErrNamesPath(t, err, target)
		require.Contains(t, err.Error(), "exceeds maximum length", "error %q does not mention maximum length", err)
	})

	t.Run("target_is_directory", func(t *testing.T) {
		t.Parallel()
		sub := filepath.Join(dir, "subdir")
		err := os.Mkdir(sub, 0o755)
		require.NoError(t, err)
		err = Write(sub, []byte("data"), 0o644)
		require.Error(t, err, "Write over existing directory succeeded; want refusal")
		requireErrNamesPath(t, err, sub)
		st, err := os.Stat(sub)
		require.NoError(t, err, "directory %q was corrupted: %v", sub, err)
		require.True(t, st.IsDir(), "directory %q was corrupted: %v", sub, err)
	})

	t.Run("target_is_symlink", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "target.txt")
		err := os.WriteFile(target, []byte("original\n"), 0o644)
		require.NoError(t, err)
		link := filepath.Join(dir, "link.txt")
		err = os.Symlink(target, link)
		require.NoError(t, err)

		err = Write(link, []byte("replacement\n"), 0o644)
		require.Error(t, err, "Write over symlink succeeded; want refusal")
		requireErrNamesPath(t, err, link)

		lst, err := os.Lstat(link)
		require.NoError(t, err, "Lstat(%q) failed: %v", link, err)
		require.NotZero(t, lst.Mode()&os.ModeSymlink, "link %q is no longer a symlink", link)
		targetBytes, err := os.ReadFile(target)
		require.NoError(t, err, "target was modified: %s (%v)", string(targetBytes), err)
		require.Equal(t, "original\n", string(targetBytes), "target was modified: %s (%v)", string(targetBytes), err)
	})

	t.Run("target_is_broken_symlink", func(t *testing.T) {
		t.Parallel()
		broken := filepath.Join(dir, "dangling.txt")
		err := os.Symlink(filepath.Join(dir, "nonexistent.txt"), broken)
		require.NoError(t, err)

		err = Write(broken, []byte("data\n"), 0o644)
		require.Error(t, err, "Write over broken symlink succeeded; want refusal")
		requireErrNamesPath(t, err, broken)
		lst, err := os.Lstat(broken)
		require.NoError(t, err, "broken link was modified or removed: %v", err)
		require.NotZero(t, lst.Mode()&os.ModeSymlink, "broken link was modified or removed: %v", err)
	})

	t.Run("target_in_readonly_directory", func(t *testing.T) {
		t.Parallel()
		roDir := filepath.Join(dir, "ro-dir")
		err := os.Mkdir(roDir, 0o755)
		require.NoError(t, err)
		target := filepath.Join(roDir, "file.txt")
		err = os.Chmod(roDir, 0o555)
		require.NoError(t, err)
		defer func() { _ = os.Chmod(roDir, 0o755) }()

		err = Write(target, []byte("data"), 0o644)
		require.Error(t, err, "Write in read-only directory succeeded; want refusal")
		requireErrNamesPath(t, err, target)
	})

	t.Run("parent_does_not_exist", func(t *testing.T) {
		t.Parallel()
		missingParent := filepath.Join(dir, "no-such-parent", "file.txt")
		err := Write(missingParent, []byte("data"), 0o644)
		require.Error(t, err, "Write with non-existent parent succeeded; want refusal")
		requireErrNamesPath(t, err, missingParent)
		_, err = os.Stat(filepath.Dir(missingParent))
		require.True(t, os.IsNotExist(err), "parent directory was created silently: %v", err)
	})

	t.Run("parent_is_not_a_directory", func(t *testing.T) {
		t.Parallel()
		regParent := filepath.Join(dir, "file_as_parent")
		err := os.WriteFile(regParent, []byte("regular file\n"), 0o644)
		require.NoError(t, err)
		target := filepath.Join(regParent, "child.txt")
		err = Write(target, []byte("data"), 0o644)
		require.Error(t, err, "Write with non-dir parent succeeded; want refusal")
		requireErrNamesPath(t, err, target)
	})

	t.Run("not_clean_path", func(t *testing.T) {
		t.Parallel()
		dirtyPath := dir + "/sub/../file.txt"
		err := Write(dirtyPath, []byte("data"), 0o644)
		require.Error(t, err, "Write with non-clean path succeeded; want refusal")
		require.Contains(t, err.Error(), "not clean", "error %q does not mention clean", err)
		requireErrNamesPath(t, err, dirtyPath)
	})

	t.Run("unsupported_mode_bits", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "setuid_file.txt")
		err := Write(target, []byte("data"), 0o4755)
		require.Error(t, err, "Write with setuid 04755 succeeded; want refusal")
		require.Contains(t, err.Error(), "unsupported file mode", "error %q does not mention unsupported file mode", err)
		requireErrNamesPath(t, err, target)
	})

	t.Run("eval_symlinks_error", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "eval_err.txt")
		injectedErr := errors.New("injected eval symlinks failure")
		h := defaultHooks()
		h.evalSymlinks = func(path string) (string, error) {
			return "", injectedErr
		}
		err := writeWithHooks(target, []byte("data"), 0o644, h)
		require.Error(t, err, "writeWithHooks succeeded; want error")
		requireErrNamesPath(t, err, target)
		require.ErrorIs(t, err, injectedErr, "error %v does not wrap injected error %v", err, injectedErr)
	})

	t.Run("unexpected_lstat_error", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(dir, "lstat_err.txt")
		injectedErr := errors.New("injected lstat failure")
		h := defaultHooks()
		h.lstat = func(name string) (os.FileInfo, error) {
			return nil, injectedErr
		}
		err := writeWithHooks(target, []byte("data"), 0o644, h)
		require.Error(t, err, "writeWithHooks succeeded; want error")
		requireErrNamesPath(t, err, target)
		require.ErrorIs(t, err, injectedErr, "error %v does not wrap injected error %v", err, injectedErr)
	})
}

// TestParentSymlink is the witness layout: intended/linkdir points at outside/,
// and a write of intended/linkdir/note.txt must not change outside/note.txt.
func TestParentSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	intended := filepath.Join(root, "intended")
	err := os.Mkdir(outside, 0o755)
	require.NoError(t, err)
	err = os.Mkdir(intended, 0o755)
	require.NoError(t, err)
	note := filepath.Join(outside, "note.txt")
	const original = "original-outside\n"
	err = os.WriteFile(note, []byte(original), 0o644)
	require.NoError(t, err)
	linkdir := filepath.Join(intended, "linkdir")
	err = os.Symlink(outside, linkdir)
	require.NoError(t, err)

	target := filepath.Join(linkdir, "note.txt")
	err = WriteFile(target, []byte("replaced\n"), 0o644)
	require.Error(t, err, "WriteFile through a symlink parent succeeded; want refusal")
	msg := err.Error()
	require.Contains(t, msg, "atomicfile", "error %q does not name atomicfile", msg)
	require.Contains(t, msg, "symlink", "error %q does not say the parent is a symlink", msg)
	requireErrNamesPath(t, err, target)
	require.Contains(t, msg, "pass the real directory", "error %q does not name the next action", msg)

	got, rerr := os.ReadFile(note)
	require.NoError(t, rerr)
	require.Equal(t, original, string(got), "outside/note.txt = %q, want unchanged %q", got, original)
	entries, rerr := os.ReadDir(outside)
	require.NoError(t, rerr)
	require.Len(t, entries, 1, "outside/ has %d entries, want only note.txt", len(entries))
	require.Equal(t, "note.txt", entries[0].Name(), "outside/ has %d entries, want only note.txt", len(entries))
	lst, rerr := os.Lstat(linkdir)
	require.NoError(t, rerr)
	require.NotZero(t, lst.Mode()&os.ModeSymlink, "linkdir %q is no longer a symlink", linkdir)
}

func TestSanitizedError(t *testing.T) {
	t.Parallel()

	rawErr := errors.New("raw message\nwith newline and \x1b[31mescape sequence")
	wrapped := wrapErr("atomicfile: test operation", rawErr)

	require.Error(t, wrapped, "wrapErr returned nil")
	require.NotContains(t, wrapped.Error(), "\n", "wrapped Error() contains raw newline: %q", wrapped.Error())
	require.NotContains(t, wrapped.Error(), "\x1b", "wrapped Error() contains raw ESC byte: %q", wrapped.Error())
	require.ErrorIs(t, wrapped, rawErr, "wrapped error does not satisfy errors.Is for rawErr")

	nilWrapped := wrapErr("prefix", nil)
	require.NoError(t, nilWrapped, "wrapErr with nil err = %v, want nil", nilWrapped)
}

func TestDefaultHooksSyncWithTeeth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "teeth-*.tmp")
	require.NoError(t, err)
	name := f.Name()
	err = f.Close()
	require.NoError(t, err)
	defer func() { _ = os.Remove(name) }()

	// Calling sync on a closed file descriptor MUST return an error.
	// If defaultHooks().sync was mutated to a dummy `return nil`, this test fails immediately!
	syncErr := defaultHooks().sync(f)
	require.Error(t, syncErr, "defaultHooks().sync on closed file succeeded; want error (fsync tooth failed)")
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

	err := writeWithHooks(target, []byte("sync test\n"), 0o644, h)
	require.NoError(t, err, "writeWithHooks failed: %v", err)

	require.True(t, syncCalled, "sync hook was not executed during successful write")

	got, err := os.ReadFile(target)
	require.NoError(t, err, "file content invalid: %s (%v)", string(got), err)
	require.Equal(t, "sync test\n", string(got), "file content invalid: %s (%v)", string(got), err)
}

func TestDefaultSyncDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := defaultSyncDir(dir)
	require.NoError(t, err, "defaultSyncDir on valid dir failed: %v", err)

	// defaultSyncDir must ignore errors on non-existent or inaccessible dirs (best effort)
	nonExistent := filepath.Join(dir, "does_not_exist")
	err = defaultSyncDir(nonExistent)
	require.NoError(t, err, "defaultSyncDir on non-existent dir returned error: %v", err)
}

func TestSyncDirExecuted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "synced_dir_file.txt")

	var syncedDir string
	h := defaultHooks()
	h.syncDir = func(d string) error {
		syncedDir = d
		return nil
	}

	err := writeWithHooks(target, []byte("dir sync test\n"), 0o644, h)
	require.NoError(t, err, "writeWithHooks failed: %v", err)

	require.Equal(t, dir, syncedDir, "syncDir called with %q, want %q", syncedDir, dir)
}

func TestSyncDirFailureIgnored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "synced_dir_ignore_err.txt")

	h := defaultHooks()
	h.syncDir = func(d string) error {
		return errors.New("simulated dir sync error")
	}

	err := writeWithHooks(target, []byte("dir sync ignore err test\n"), 0o644, h)
	require.NoError(t, err, "writeWithHooks failed despite syncDir error being best-effort: %v", err)

	got, err := os.ReadFile(target)
	require.NoError(t, err, "file content invalid: %s (%v)", string(got), err)
	require.Equal(t, "dir sync ignore err test\n", string(got), "file content invalid: %s (%v)", string(got), err)
}

func TestCleanupFailureJoined(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "cleanup_fail.txt")

	writeErr := errors.New("simulated write failure")
	removeErr := errors.New("simulated remove failure")

	h := defaultHooks()
	h.write = func(f *os.File, data []byte) (int, error) {
		return 0, writeErr
	}
	h.remove = func(name string) error {
		_ = os.Remove(name) // remove real file so no temp leaks on disk
		return removeErr
	}

	err := writeWithHooks(target, []byte("data"), 0o644, h)
	require.Error(t, err, "writeWithHooks succeeded; want error")

	require.ErrorIs(t, err, writeErr, "error does not wrap writeErr: %v", err)
	require.ErrorIs(t, err, removeErr, "error does not wrap removeErr: %v", err)
	require.Contains(t, err.Error(), "cleanup failed for", "error %q does not report cleanup failure", err)
}

func TestCreateTempCollisionAndExhaustion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	t.Run("collision_resolved_on_retry", func(t *testing.T) {
		t.Parallel()
		// Pre-create file with fixed nonce 0x12345678
		preName := filepath.Join(dir, fmt.Sprintf(".test.tmp-%08x", 0x12345678))
		err := os.WriteFile(preName, []byte("existing"), 0o600)
		require.NoError(t, err)
		defer func() { _ = os.Remove(preName) }()

		attempts := 0
		randFn := func() (uint32, error) {
			attempts++
			if attempts == 1 {
				return 0x12345678, nil // will collide
			}
			return 0x87654321, nil // will succeed
		}

		f, err := createTempFile(dir, "test", 0o600, randFn)
		require.NoError(t, err, "createTempFile failed on retry: %v", err)
		_ = f.Close()
		_ = os.Remove(f.Name())

		require.Equal(t, 2, attempts, "expected 2 attempts, got %d", attempts)
	})

	t.Run("attempts_exhausted", func(t *testing.T) {
		t.Parallel()
		collidingName := filepath.Join(dir, fmt.Sprintf(".exhaust.tmp-%08x", 0xabcdef01))
		err := os.WriteFile(collidingName, []byte("existing"), 0o600)
		require.NoError(t, err)
		defer func() { _ = os.Remove(collidingName) }()

		randFn := func() (uint32, error) {
			return 0xabcdef01, nil // always collides
		}

		f, err := createTempFile(dir, "exhaust", 0o600, randFn)
		if err == nil {
			_ = f.Close()
			require.FailNow(t, "createTempFile succeeded despite constant collision; want exhaustion")
		}
		wantMsg := fmt.Sprintf("after %d attempts", maxCreateTempAttempts)
		require.Contains(t, err.Error(), wantMsg, "error %q does not mention %s", err, wantMsg)
	})

	t.Run("rand_error", func(t *testing.T) {
		t.Parallel()
		randErr := errors.New("entropy exhausted")
		randFn := func() (uint32, error) {
			return 0, randErr
		}
		_, err := createTempFile(dir, "randerr", 0o600, randFn)
		require.ErrorIs(t, err, randErr, "error does not wrap randErr: %v", err)
	})
}

func TestStepFailureInjection(t *testing.T) {
	t.Parallel()

	steps := []string{"create", "write", "chmod", "sync", "close", "rename"}

	for _, step := range steps {
		step := step
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
		err := os.WriteFile(target, []byte(initialContent), 0o644)
		require.NoError(t, err, "setup pre-existing file failed: %v", err)
	}

	injectedErr := fmt.Errorf("injected failure at %s", failStep)
	h := defaultHooks()

	switch failStep {
	case "create":
		h.createTemp = func(dir, base string, perm os.FileMode) (*os.File, error) {
			return nil, injectedErr
		}
	case "write":
		h.write = func(f *os.File, data []byte) (int, error) {
			return 0, injectedErr
		}
	case "chmod":
		h.chmod = func(f *os.File, mode os.FileMode) error { return injectedErr }
	case "sync":
		h.sync = func(f *os.File) error {
			return injectedErr
		}
	case "close":
		h.close = func(f *os.File) error {
			_ = f.Close()
			return injectedErr
		}
	case "rename":
		h.rename = func(oldpath, newpath string) error {
			return injectedErr
		}
	default:
		require.FailNow(t, fmt.Sprintf("unknown step %q", failStep))
	}

	var opts []Option
	if failStep == "chmod" {
		opts = append(opts, ExactMode())
	}
	err := writeWithHooks(target, []byte(newContent), 0o644, h, opts...)
	require.Error(t, err, "writeWithHooks unexpectedly succeeded at %s step", failStep)
	require.Contains(t, err.Error(), target, "error %q does not name target path %q", err, target)
	require.ErrorIs(t, err, injectedErr, "error %v does not wrap injected error %v", err, injectedErr)

	// Verify target file state
	if preexisting {
		got, err := os.ReadFile(target)
		require.NoError(t, err, "target was deleted on %s failure: %v", failStep, err)
		require.Equal(t, initialContent, string(got), "target was corrupted on %s failure: got %q, want %q", failStep, string(got), initialContent)
	} else {
		_, err := os.Stat(target)
		require.True(t, os.IsNotExist(err), "target was created despite %s failure: %v", failStep, err)
	}

	// Verify NO temporary files remain in the target directory
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "ReadDir failed: %v", err)
	for _, e := range entries {
		require.Equal(t, "target.txt", e.Name(), "leaked file %q in dir after %s failure", e.Name(), failStep)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) {
	return 0, errors.New("simulated entropy read failure")
}

func TestRandomUint32Error(t *testing.T) {
	t.Parallel()
	_, err := randomUint32(errReader{})
	require.Error(t, err, "randomUint32 succeeded on errReader; want error")
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
	err := writeWithHooks(target, []byte("complete\n"), 0o644, h, ExactMode())
	require.NoError(t, err)
}
