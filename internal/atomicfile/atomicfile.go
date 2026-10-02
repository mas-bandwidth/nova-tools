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
    directory or symlink, or if the parent directory is a symlink, is read-only,
    does not exist, or fails to resolve via EvalSymlinks, the operation is refused
    immediately with an error naming the path. A symlink parent is refused before
    anything is written; the error tells the caller to pass the real directory.
 2. Exclusive temporary file creation: A temporary file is created exclusively in the
    SAME physical directory as the target file, using a fixed-length name of the form
    ".<base>.tmp-%08x" where the suffix is drawn from crypto/rand. This guarantees
    that concurrent writers never collide and temporary files never escape their
    parent directory.
 3. Umask-honoring permissions: The temporary file is created with the caller's
    requested os.FileMode, allowing the process umask to apply naturally (perm & ^umask),
    matching the permission semantics of os.OpenFile and os.WriteFile. No explicit
    chmod is performed by default, so the process umask is preserved.
 4. Full write and final permissions: The data payload is written to the temporary
    file in full. With ExactMode, chmod then sets the requested permission bits
    exactly, overriding the umask before the file is synced.
 5. Media flush (fsync): The file contents and inode metadata are flushed to durable
    storage media using fsync (f.Sync()).
 6. Clean closure: The temporary file descriptor is closed.
 7. Atomic publication: The temporary file is renamed over the target path
    (os.Rename). With NoReplace, an exclusive hard link installs it only where
    the target is absent, and the temporary name is removed.
 8. Parent directory fsync: The parent directory is fsynced (best-effort) after the
    publication so that the directory entry is durable on filesystems requiring directory
    flushes. Errors from directory fsync are ignored on platforms or filesystems where
    directory fsync is unsupported.

# Failure and Cleanup Guarantees

On any failure before publication (creation, write, chmod, sync, close, rename
or exclusive link), atomicfile
attempts to remove the temporary file immediately (os.Remove), leaving the target
file completely untouched. If removing the temporary file fails (for example due
to sudden permission loss or filesystem error), the cleanup error is joined to the
returned error via errors.Join, explicitly naming the leftover temporary path so
callers can detect and inspect any uncollected file. If NoReplace publishes
successfully but removal of its temporary name fails, the returned error names
both the created target and the leftover name. The complete target remains
published; the parent-directory sync is still attempted.

Temporary files left behind by abrupt process termination outside runtime control
(such as SIGKILL, power loss, or kernel panic) cannot be swept by defer and are
not automatically removed on subsequent invocations.

# Directory Boundary and Concurrency Note

The initial symlink and directory check is a safeguard against accidental caller
mistakes (e.g. attempting to overwrite a symlink or directory path, or writing
through a symlink parent). It is designed for caller-owned directories; it is not
an adversarial race-free lock against malicious actors concurrently swapping
directory contents on untrusted trees.

# Differences from os.WriteFile

Callers migrating from os.WriteFile should note five key differences:
 1. Atomicity: os.WriteFile truncates and overwrites in-place, allowing concurrent
    readers to observe truncated or empty intermediate states. atomicfile writes
    to a sibling temporary file first and renames, ensuring readers always see
    a complete version.
 2. Permissions & umask: os.WriteFile preserves the permissions of existing files
    when overwriting them, and applies the process umask to newly created files.
    atomicfile creates a new sibling temporary file with perm masked naturally by
    the process umask (perm & ^umask) and renames it over the target; thus, the
    resulting file mode reflects perm & ^umask regardless of whether the target
    already existed. With ExactMode, chmod sets perm exactly before fsync;
    callers preserving an existing mode must explicitly select that option.
 3. Inodes and hard links: Because atomicfile replaces the directory entry via
    rename(2), the target receives a new inode. Existing hard links to the target
    path continue pointing to the previous inode and will not reflect new writes.
 4. Special files and FIFOs: If the target is an existing FIFO, socket, or device
    node, atomicfile does not write into the stream; atomic rename replaces the
    directory entry with a regular file.
 5. fsync: atomicfile flushes file data and metadata with fsync before renaming,
    and performs a best-effort fsync on the parent directory after rename;
    os.WriteFile performs no fsync.

# Power-Loss Durability (What is and is not promised)

What is promised:
  - Atomicity for readers: Concurrent readers in this process or other processes
    will observe either the pre-existing file or the newly written file, never a
    truncated file, zero-filled pages, or a partially overwritten mixture of the two.
  - Data and file-metadata durability: The temporary file data and inode metadata
    are synced to persistent storage via fsync prior to rename. Once Write returns
    nil, the file contents are safely flushed to media.
  - Directory entry durability: The parent directory is fsynced after rename on a
    best-effort basis, making directory entry creation durable across sudden power loss
    on filesystems that support directory fsync.

What is NOT promised:
  - Platforms without directory sync: On platforms or filesystems that do not support
    directory fsync (e.g. Windows or filesystems where syncing a directory descriptor
    returns an error or is unsupported), parent directory sync is best-effort and
    silently ignored.
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
	lstat        func(name string) (os.FileInfo, error)
	evalSymlinks func(path string) (string, error)
	createTemp   func(dir, base string, perm os.FileMode) (*os.File, error)
	write        func(f *os.File, data []byte) (int, error)
	sync         func(f *os.File) error
	chmod        func(f *os.File, mode os.FileMode) error
	close        func(f *os.File) error
	rename       func(oldpath, newpath string) error
	link         func(oldpath, newpath string) error
	remove       func(name string) error
	syncDir      func(dir string) error
}

func defaultHooks() *hooks {
	return &hooks{
		lstat:        os.Lstat,
		evalSymlinks: filepath.EvalSymlinks,
		createTemp:   defaultCreateTemp,
		write:        func(f *os.File, data []byte) (int, error) { return f.Write(data) },
		sync:         func(f *os.File) error { return f.Sync() },
		chmod:        func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) },
		close:        func(f *os.File) error { return f.Close() },
		rename:       os.Rename,
		link:         os.Link,
		remove:       os.Remove,
		syncDir:      defaultSyncDir,
	}
}

func defaultSyncDir(dir string) error {
	dirFile, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer dirFile.Close()
	// ignored: a directory fsync is best effort where the platform does not support it; the rename already landed
	_ = dirFile.Sync()
	return nil
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

// Option configures atomic write behavior.
type Option func(*options)

type options struct {
	exactMode bool
	noReplace bool
}

// ExactMode configures atomicfile to explicitly chmod the temporary file to
// perm before the final file sync and rename, even when the process umask
// would otherwise restrict it.
func ExactMode() Option {
	return func(o *options) {
		o.exactMode = true
	}
}

// NoReplace installs the complete, synced file only if path is absent, using
// an exclusive hard link instead of rename. An existing entry, including one
// created concurrently, is preserved and the error matches os.ErrExist.
// The filesystem must support hard links. If removing the temporary link after
// publication fails, the error names it; path already holds the complete data.
func NoReplace() Option {
	return func(o *options) { o.noReplace = true }
}

// Write writes data to path atomically with the specified file mode permissions,
// subject to the process umask unless ExactMode is selected. If path does not
// exist, Write creates it; otherwise, Write replaces it atomically. NoReplace
// instead refuses an existing path and publishes by exclusive hard link.
//
// The write is performed by creating a temporary file in the same directory with
// mode perm (perm & ^umask), writing the data payload, flushing data and metadata
// to disk with fsync, closing the file, renaming it over path, and performing a
// best-effort fsync of the parent directory. On any failure prior to rename,
// removal of the temporary file is attempted, leaving the target file untouched;
// if removal fails, the cleanup error is joined to the returned error naming the
// leftover file.
//
// Permissions note: by default the process umask applies when the temporary file
// is created. ExactMode explicitly sets perm after writing and before fsync.
//
// Durability note: Write flushes both the file data/metadata (before rename) and
// the parent directory (after rename, best-effort) to ensure durable directory entry
// creation across power loss.
//
// Write refuses paths that are not clean (filepath.Clean(path) != path), refuses
// base names longer than 241 bytes, refuses mode bits outside 0000-0777, refuses
// to replace directories or symlinks, and returns an error naming path if the
// parent directory does not exist, is a symlink, is read-only, or fails to resolve.
// A symlink parent is refused before anything is written; pass the real directory.
func Write(path string, data []byte, perm os.FileMode, opts ...Option) error {
	return writeWithHooks(path, data, perm, nil, opts...)
}

// WriteFile is an alias for Write. Its optional ExactMode setting overrides the
// default umask behavior; atomic replacement and directory durability match Write.
func WriteFile(path string, data []byte, perm os.FileMode, opts ...Option) error {
	return Write(path, data, perm, opts...)
}

func writeWithHooks(path string, data []byte, perm os.FileMode, h *hooks, opts ...Option) (err error) {
	if h == nil {
		h = defaultHooks()
	}
	opt, dir, base, err := validate(path, perm, h, opts)
	if err != nil {
		return err
	}
	return publish(path, data, perm, h, opt, dir, base)
}

// validateName is the half of validate that reads nothing on disk: the path is
// given and clean, the mode is one Write supports, and the base name leaves room
// for the temporary file's name beside it.
func validateName(path string, perm os.FileMode, opts []Option) (opt options, dir, base string, err error) {
	for _, fn := range opts {
		if fn != nil {
			fn(&opt)
		}
	}
	if path == "" {
		return opt, dir, base, fmt.Errorf("atomicfile: path is empty")
	}

	if filepath.Clean(path) != path {
		return opt, dir, base, fmt.Errorf("atomicfile: path %q is not clean: use filepath.Clean", path)
	}

	if perm&^0o777 != 0 {
		return opt, dir, base, fmt.Errorf("atomicfile: unsupported file mode %04o for %q: only permissions 0000-0777 supported", perm, path)
	}

	base = filepath.Base(path)
	if len(base) > maxBaseNameLen {
		return opt, dir, base, fmt.Errorf("atomicfile: base name of %q exceeds maximum length %d: name too long", path, maxBaseNameLen)
	}
	dir = filepath.Dir(path)
	return opt, dir, base, nil
}

// validate is every check Write makes before it touches the disk, shared with
// Check so a plan refuses exactly where the write would: the lexical checks
// (validateName), then the parent and the target as they stand.
func validate(path string, perm os.FileMode, h *hooks, opts []Option) (opt options, dir, base string, err error) {
	if opt, dir, base, err = validateName(path, perm, opts); err != nil {
		return opt, dir, base, err
	}

	// Lstat, not Stat. Stat follows a symlink parent, so the directory looks real
	// and the temporary file is created in the link target. Do not compare
	// EvalSymlinks to the lexical path: on macOS /tmp is /private/tmp.
	dirInfo, err := h.lstat(dir)
	if err != nil {
		return opt, dir, base, wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return opt, dir, base, fmt.Errorf("atomicfile: parent directory %q for %q is a symlink: pass the real directory", dir, path)
	}
	if !dirInfo.IsDir() {
		return opt, dir, base, fmt.Errorf("atomicfile: parent directory %q for %q is not a directory", dir, path)
	}

	if _, err := h.evalSymlinks(dir); err != nil {
		return opt, dir, base, wrapErr(fmt.Sprintf("atomicfile: resolve parent directory for %q", path), err)
	}

	targetInfo, err := h.lstat(path)
	if err == nil {
		if opt.noReplace {
			return opt, dir, base, wrapErr(fmt.Sprintf("atomicfile: create %q", path), os.ErrExist)
		}
		if targetInfo.Mode()&os.ModeSymlink != 0 {
			return opt, dir, base, fmt.Errorf("atomicfile: target %q is a symlink", path)
		}
		if targetInfo.IsDir() {
			return opt, dir, base, fmt.Errorf("atomicfile: target %q is a directory", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return opt, dir, base, wrapErr(fmt.Sprintf("atomicfile: stat %q", path), err)
	}

	return opt, dir, base, nil
}

// publish is Write after validate: the temporary file, its sync, and the rename
// or the exclusive link.
func publish(path string, data []byte, perm os.FileMode, h *hooks, opt options, dir, base string) (err error) {
	f, err := h.createTemp(dir, base, perm)
	if err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: create temporary file for %q", path), err)
	}

	tmpName := f.Name()
	closed := false
	cleaned := false
	defer func() {
		if !closed {
			// ignored: a deferred close on the failure path; the error that got here is the one returned
			_ = f.Close()
		}
		if !cleaned {
			if cleanErr := h.remove(tmpName); cleanErr != nil {
				cleanWrapped := wrapErr(fmt.Sprintf("atomicfile: cleanup failed for %q", tmpName), cleanErr)
				err = errors.Join(err, cleanWrapped)
			}
		}
	}()

	if _, err := h.write(f, data); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: write %q", path), err)
	}

	if opt.exactMode {
		if err := h.chmod(f, perm); err != nil {
			return wrapErr(fmt.Sprintf("atomicfile: chmod %q", path), err)
		}
	}

	if err := h.sync(f); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: sync %q", path), err)
	}

	closed = true
	if err := h.close(f); err != nil {
		// ignored: a second close after the hook's close failed; the hook's error is the one returned
		_ = f.Close()
		return wrapErr(fmt.Sprintf("atomicfile: close %q", path), err)
	}

	var publishedErr error
	if opt.noReplace {
		if err := h.link(tmpName, path); err != nil {
			return wrapErr(fmt.Sprintf("atomicfile: create %q", path), err)
		}
		// Publication succeeded. Remove the temporary name before syncing the
		// directory; report a failed cleanup without pretending publication failed.
		if err := h.remove(tmpName); err != nil {
			publishedErr = wrapErr(fmt.Sprintf("atomicfile: created %q but cleanup failed for %q", path, tmpName), err)
		}
	} else if err := h.rename(tmpName, path); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: rename %q", path), err)
	}
	cleaned = true

	// Parent directory fsync (best-effort): flush directory entry to media so that
	// rename is durable across power loss on filesystems that require it.
	// Ignore errors on platforms or filesystems where directory fsync is unsupported.
	// ignored: a directory fsync is best effort where the platform does not support it (see the comment above)
	_ = h.syncDir(dir)

	return publishedErr
}
