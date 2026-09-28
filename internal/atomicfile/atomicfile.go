/*
Package atomicfile writes a file atomically: a reader opening or reading the file
sees either the old content or the new content in full, never a part of either.

# Contract and Guarantees

Every write follows a strict, bounded sequence:
 1. Path and parent directory validation: The target path must be in clean canonical
    form (filepath.Clean(path) == path); non-clean paths (e.g. holding lexical ".."
    traversals or redundant slashes) are refused immediately to prevent lexical
    versus physical directory divergence. The parent directory must already exist;
    atomicfile never creates parent directories silently. The base filename must
    not exceed 241 bytes so that the temporary file's fixed 14-byte overhead does
    not exceed the filesystem NAME_MAX (255 bytes). Only standard file permissions
    0000-0777 are supported; mode bits outside this mask (such as setuid 04755,
    setgid, sticky, or file-type bits) are refused. If the target is an existing
    directory or symlink, or if the parent directory is read-only, does not exist,
    or fails to resolve via EvalSymlinks, the operation is refused immediately with
    an error naming the path.
 2. Exclusive temporary file creation: A temporary file is created exclusively in the
    SAME physical directory as the target file, using a fixed-length name of the form
    ".<base>.tmp-%08x" where the suffix is drawn from crypto/rand. This guarantees
    that concurrent writers never collide and temporary files never escape their
    parent directory.
 3. Explicit permissions: The temporary file is created with and explicitly chmoded
    to the caller's requested os.FileMode.
 4. Full write: The data payload is written to the temporary file in full.
 5. Media flush (fsync): The file contents and inode metadata are flushed to durable
    storage media using fsync (f.Sync()).
 6. Clean closure: The temporary file descriptor is closed.
 7. Atomic rename: The temporary file is renamed over the target path (os.Rename).

# Failure and Cleanup Guarantees

On any failure during creation, chmod, write, sync, close, or rename, atomicfile
attempts to remove the temporary file immediately (os.Remove), leaving the target
file completely untouched. If removing the temporary file fails (for example due
to sudden permission loss or filesystem error), the cleanup error is joined to the
returned error via errors.Join, explicitly naming the leftover temporary path so
callers can detect and inspect any uncollected file.

Temporary files left behind by abrupt process termination outside runtime control
(such as SIGKILL, power loss, or kernel panic) cannot be swept by defer and are
not automatically removed on subsequent invocations.

# Directory Boundary and Concurrency Note

The initial symlink and directory check is a safeguard against accidental caller
mistakes (e.g. attempting to overwrite a symlink or directory path). It is designed
for caller-owned directories; it is not an adversarial race-free lock against
malicious actors concurrently swapping directory contents on untrusted trees.

# Differences from os.WriteFile

Callers migrating from os.WriteFile should note five key differences:
 1. Atomicity: os.WriteFile truncates and overwrites in-place, allowing concurrent
    readers to observe truncated or empty intermediate states. atomicfile writes
    to a sibling temporary file first and renames, ensuring readers always see
    a complete version.
 2. Permissions & umask: os.WriteFile creates new files masked by the process umask
    and preserves permissions of existing files without updating them. atomicfile
    only accepts standard permissions 0000-0777 and applies the caller's perm
    directly to the target via chmod, replacing prior permissions and bypassing
    umask reduction.
 3. Inodes and hard links: Because atomicfile replaces the directory entry via
    rename(2), the target receives a new inode. Existing hard links to the target
    path continue pointing to the previous inode and will not reflect new writes.
 4. Special files and FIFOs: If the target is an existing FIFO, socket, or device
    node, atomicfile does not write into the stream; atomic rename replaces the
    directory entry with a regular file.
 5. fsync: atomicfile flushes file data and metadata with fsync before renaming;
    os.WriteFile performs no fsync.

# Power-Loss Durability (What is and is not promised)

What is promised:
  - Atomicity for readers: Concurrent readers in this process or other processes
    will observe either the pre-existing file or the newly written file, never a
    truncated file, zero-filled pages, or a partially overwritten mixture of the two.
  - Data and file-metadata durability: The temporary file data and inode metadata
    are synced to persistent storage via fsync prior to rename. Once Write returns
    nil, the file contents are safely flushed to media.

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
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	maxFilenameLen = 255
	// tempOverhead is the length of "." prefix (1) + ".tmp-%08x" suffix (13) = 14 bytes.
	tempOverhead          = 14
	maxBaseNameLen        = maxFilenameLen - tempOverhead // 241
	maxCreateTempAttempts = 1000
)

// hooks provides injection seams for step failure testing.
type hooks struct {
	stat         func(name string) (os.FileInfo, error)
	lstat        func(name string) (os.FileInfo, error)
	evalSymlinks func(path string) (string, error)
	createTemp   func(dir, base string, perm os.FileMode) (*os.File, error)
	chmod        func(f *os.File, mode os.FileMode) error
	write        func(f *os.File, data []byte) (int, error)
	sync         func(f *os.File) error
	close        func(f *os.File) error
	rename       func(oldpath, newpath string) error
	remove       func(name string) error
}

func defaultHooks() *hooks {
	return &hooks{
		stat:         os.Stat,
		lstat:        os.Lstat,
		evalSymlinks: filepath.EvalSymlinks,
		createTemp:   defaultCreateTemp,
		chmod:        func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) },
		write:        func(f *os.File, data []byte) (int, error) { return f.Write(data) },
		sync:         func(f *os.File) error { return f.Sync() },
		close:        func(f *os.File) error { return f.Close() },
		rename:       os.Rename,
		remove:       os.Remove,
	}
}

func defaultCreateTemp(dir, base string, perm os.FileMode) (*os.File, error) {
	return createTempFile(dir, base, perm, func() (uint32, error) {
		return randomUint32(crand.Reader)
	})
}

func createTempFile(dir, base string, perm os.FileMode, randFn func() (uint32, error)) (*os.File, error) {
	for i := 0; i < maxCreateTempAttempts; i++ {
		r, err := randFn()
		if err != nil {
			return nil, err
		}
		name := filepath.Join(dir, fmt.Sprintf(".%s.tmp-%08x", base, r))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	return nil, fmt.Errorf("atomicfile: could not create unique temporary file after %d attempts", maxCreateTempAttempts)
}

func randomUint32(r io.Reader) (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

type sanitizedError struct {
	msg string
	err error
}

func (e *sanitizedError) Error() string {
	return e.msg
}

func (e *sanitizedError) Unwrap() error {
	return e.err
}

func wrapErr(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return &sanitizedError{
		msg: prefix + ": " + oneline.Escape(err.Error()),
		err: err,
	}
}

// Write writes data to path atomically with the specified file mode permissions.
// If path does not exist, Write creates it with permissions perm; if path already
// exists, Write replaces it atomically.
//
// The write is performed by creating a temporary file in the same directory,
// setting permissions to perm, flushing data and metadata to disk with fsync,
// closing the file, and renaming it over path. On any failure, removal of the
// temporary file is attempted, leaving the target file untouched; if removal
// fails, the cleanup error is joined to the returned error naming the leftover file.
//
// Write refuses paths that are not clean (filepath.Clean(path) != path), refuses
// base names longer than 241 bytes, refuses mode bits outside 0000-0777, refuses
// to replace directories or symlinks, and returns an error naming path if the
// parent directory does not exist, is read-only, or fails to resolve.
//
// Durability note: Write syncs the file's data and inode to storage before
// renaming, but does not fsync the parent directory after rename. Refer to the
// package documentation for power-loss durability guarantees.
func Write(path string, data []byte, perm os.FileMode) error {
	return writeWithHooks(path, data, perm, nil)
}

// WriteFile is an alias for Write, matching os.WriteFile's signature with atomic
// replacement guarantees.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return Write(path, data, perm)
}

func writeWithHooks(path string, data []byte, perm os.FileMode, h *hooks) (err error) {
	if path == "" {
		return fmt.Errorf("atomicfile: path is empty")
	}

	if filepath.Clean(path) != path {
		return fmt.Errorf("atomicfile: path %q is not clean: use filepath.Clean", path)
	}

	if perm&^0o777 != 0 {
		return fmt.Errorf("atomicfile: unsupported file mode %04o for %q: only permissions 0000-0777 supported", perm, path)
	}

	base := filepath.Base(path)
	if len(base) > maxBaseNameLen {
		return fmt.Errorf("atomicfile: base name of %q exceeds maximum length %d: name too long", path, maxBaseNameLen)
	}

	if h == nil {
		h = defaultHooks()
	}

	dir := filepath.Dir(path)
	dirInfo, err := h.stat(dir)
	if err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("atomicfile: parent directory %q for %q is not a directory", dir, path)
	}

	if _, err := h.evalSymlinks(dir); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: resolve parent directory for %q", path), err)
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
		return wrapErr(fmt.Sprintf("atomicfile: stat %q", path), err)
	}

	f, err := h.createTemp(dir, base, perm)
	if err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: create temporary file for %q", path), err)
	}

	tmpName := f.Name()
	closed := false
	cleaned := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !cleaned {
			if cleanErr := h.remove(tmpName); cleanErr != nil {
				cleanWrapped := wrapErr(fmt.Sprintf("atomicfile: cleanup failed for %q", tmpName), cleanErr)
				err = errors.Join(err, cleanWrapped)
			}
		}
	}()

	if err := h.chmod(f, perm); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: chmod %q", path), err)
	}

	if _, err := h.write(f, data); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: write %q", path), err)
	}

	if err := h.sync(f); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: sync %q", path), err)
	}

	closed = true
	if err := h.close(f); err != nil {
		_ = f.Close()
		return wrapErr(fmt.Sprintf("atomicfile: close %q", path), err)
	}

	if err := h.rename(tmpName, path); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: rename %q", path), err)
	}

	cleaned = true
	return nil
}
