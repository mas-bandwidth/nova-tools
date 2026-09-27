/*
Package atomicfile writes a file atomically: a reader opening or reading the file
sees either the old content or the new content in full, never a part of either.

# Contract and Guarantees

Every write follows a strict, bounded sequence:
 1. The target path and its parent directory are validated. The parent directory
    must already exist; atomicfile never creates parent directories silently.
    If the target is an existing directory or symlink, or if the parent directory
    is read-only or does not exist, the operation is refused immediately with an
    error naming the path.
 2. A temporary file is created exclusively in the SAME directory as the target
    file, using a unique name ("." + base + "-*.tmp") that never collides with
    concurrent writers or existing files.
 3. The temporary file's permissions are set to the caller's requested FileMode.
 4. The data is written to the temporary file in full.
 5. The temporary file is flushed to storage media using fsync (f.Sync()).
 6. The temporary file is closed.
 7. The temporary file is renamed over the target path (os.Rename).

On any failure at any step (create, chmod, write, sync, close, or rename), the
temporary file is removed immediately and the target file is left completely
untouched. Nothing outside the target's directory is touched.

# Power-Loss Durability (What is and is not promised)

What is promised:
  - Atomicity for readers: Concurrent readers in this process or other processes
    will observe either the pre-existing file or the newly written file, never a
    truncated file, zero-filled pages, or a partially overwritten mixture of the two.
  - Data and file-metadata durability: The temporary file data and inode metadata
    are synced to persistent storage via fsync prior to rename. Once Write returns
    nil, the contents are safely on disk.

What is NOT promised:
  - Parent directory durability across power loss: The parent directory is not
    fsynced after the rename operation. On POSIX filesystems without full metadata
    journaling, an abrupt system crash or power loss occurring immediately after
    Write returns could leave the parent directory's directory entry pointing to the
    old inode or in an uncommitted state, even though the file data itself was synced.
    Callers that require absolute crash-durability of the directory entry across
    sudden power loss must fsync the parent directory themselves according to their
    platform and filesystem requirements.
*/
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// hooks provides injection seams for step failure testing.
type hooks struct {
	stat       func(name string) (os.FileInfo, error)
	lstat      func(name string) (os.FileInfo, error)
	createTemp func(dir, pattern string) (*os.File, error)
	chmod      func(f *os.File, mode os.FileMode) error
	write      func(f *os.File, data []byte) (int, error)
	sync       func(f *os.File) error
	close      func(f *os.File) error
	rename     func(oldpath, newpath string) error
}

func defaultHooks() *hooks {
	return &hooks{
		stat:       os.Stat,
		lstat:      os.Lstat,
		createTemp: os.CreateTemp,
		chmod:      func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) },
		write:      func(f *os.File, data []byte) (int, error) { return f.Write(data) },
		sync:       func(f *os.File) error { return f.Sync() },
		close:      func(f *os.File) error { return f.Close() },
		rename:     os.Rename,
	}
}

// Write writes data to path atomically with the specified file mode permissions.
// If path does not exist, Write creates it with permissions perm; if path already
// exists, Write replaces it atomically.
//
// The write is performed by creating a temporary file in the same directory,
// setting permissions to perm, flushing data and metadata to disk with fsync,
// closing the file, and renaming it over path. On any failure, the temporary file
// is removed and the target file remains untouched.
//
// Write refuses to replace directories, refuses to follow or replace symlinks,
// and returns an error naming path if the parent directory does not exist or
// is read-only.
//
// Durability note: Write syncs the file's data and inode to storage before
// renaming, but does not fsync the parent directory after rename. Refer to the
// package documentation for power-loss durability guarantees.
func Write(path string, data []byte, perm os.FileMode) error {
	return writeWithHooks(path, data, perm, nil)
}

// WriteFile is an alias for Write, matching os.WriteFile's signature and behavior
// with atomic replacement guarantees.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Write(path, data, perm)
}

func writeWithHooks(path string, data []byte, perm os.FileMode, h *hooks) error {
	if path == "" {
		return fmt.Errorf("atomicfile: path is empty")
	}

	if h == nil {
		h = defaultHooks()
	}

	dir := filepath.Dir(path)
	dirInfo, err := h.stat(dir)
	if err != nil {
		return fmt.Errorf("atomicfile: parent directory for %q: %w", path, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("atomicfile: parent directory %q for %q is not a directory", dir, path)
	}

	targetInfo, err := h.lstat(path)
	if err == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("atomicfile: target %q is a symlink", path)
		}
		if targetInfo.IsDir() {
			return fmt.Errorf("atomicfile: target %q is a directory", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("atomicfile: stat %q: %w", path, err)
	}

	pattern := "." + filepath.Base(path) + "-*.tmp"
	f, err := h.createTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("atomicfile: create temporary file for %q: %w", path, err)
	}

	tmpName := f.Name()
	closed := false
	cleaned := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()

	if err := h.chmod(f, perm); err != nil {
		return fmt.Errorf("atomicfile: chmod %q: %w", path, err)
	}

	if _, err := h.write(f, data); err != nil {
		return fmt.Errorf("atomicfile: write %q: %w", path, err)
	}

	if err := h.sync(f); err != nil {
		return fmt.Errorf("atomicfile: sync %q: %w", path, err)
	}

	closed = true
	if err := h.close(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomicfile: close %q: %w", path, err)
	}

	if err := h.rename(tmpName, path); err != nil {
		return fmt.Errorf("atomicfile: rename %q: %w", path, err)
	}

	cleaned = true
	return nil
}
