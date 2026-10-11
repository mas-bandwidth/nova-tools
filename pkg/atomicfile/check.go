package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Check makes every check Write makes before it writes, and one more a write
// learns only by trying: that this process can create a file in the parent. It
// writes nothing. A plan (a --dry-run) calls it in place of Write, so the plan
// refuses exactly where the write would.
func Check(path string, perm os.FileMode, opts ...Option) error {
	_, dir, _, err := validate(path, perm, defaultHooks(), opts)
	if err != nil {
		return err
	}
	if err := canWrite(dir); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	return nil
}

// CheckAfterMkdirAll is Check for a write the caller makes after
// os.MkdirAll(filepath.Dir(path)): a parent that is not there yet is judged as
// MkdirAll would make it, from the nearest ancestor that is there, which must
// be a directory this process can create in; a parent that is there is judged
// by Check.
func CheckAfterMkdirAll(path string, perm os.FileMode, opts ...Option) error {
	if err := checkMkdirAll(filepath.Dir(path)); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, fs.ErrNotExist) {
		return nil // made by MkdirAll, empty: the target is not there and the parent is new
	}
	return Check(path, perm, opts...)
}

// CheckAppend makes the checks an append that creates path when it is absent
// (os.OpenFile with O_CREATE|O_APPEND, after os.MkdirAll of its parent) needs:
// the parent as MkdirAll would make it, and a target that is absent or a regular
// file this process can write.
func CheckAppend(path string) error {
	dir := filepath.Dir(path)
	if err := checkMkdirAll(dir); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if _, derr := os.Stat(dir); derr == nil {
			return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), canWrite(dir))
		}
		return nil
	case err != nil:
		return wrapErr(fmt.Sprintf("atomicfile: stat %q", path), err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("atomicfile: %q is not a regular file", path)
	}
	return wrapErr(fmt.Sprintf("atomicfile: open %q", path), canWriteFile(path, fi))
}

// checkMkdirAll answers whether os.MkdirAll(dir) would succeed without making
// anything: dir is a directory (a link to one is followed, as MkdirAll follows
// it), or the nearest ancestor that exists is a directory this process can
// create in. A file standing anywhere on the way is the not-a-directory error
// MkdirAll would return.
func checkMkdirAll(dir string) error {
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err == nil {
			if !fi.IsDir() {
				return &fs.PathError{Op: "mkdir", Path: p, Err: errors.New("not a directory")}
			}
			if p == filepath.Clean(dir) {
				return nil
			}
			return canWrite(p)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if parent := filepath.Dir(p); parent == p {
			return err
		}
	}
}
