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
//
// The permission half asks the kernel on Unix (canWrite) and checks only the
// read-only attribute on Windows, where access lists are not consulted.
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
// os.MkdirAll(filepath.Dir(path)), in the caller's order: MkdirAll first (a
// parent that is not there is judged as MkdirAll would make it, from the
// nearest ancestor that is there, which must be a directory this process can
// create in), then every check of the write. A parent MkdirAll would make is
// new and empty, so the write's checks that read the disk have nothing to find
// there; the ones that do not (the path, the mode, the name's length) run all
// the same.
func CheckAfterMkdirAll(path string, perm os.FileMode, opts ...Option) error {
	if err := checkMkdirAll(filepath.Dir(path)); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	if _, _, _, err := validateName(path, perm, opts); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return Check(path, perm, opts...)
}

// CheckAppend makes the checks an append that creates path when it is absent
// (os.MkdirAll of its parent, then os.OpenFile with O_WRONLY|O_CREATE|O_APPEND)
// needs, in that order: the parent as MkdirAll would make it, then the file the
// open reaches. The open follows a symbolic link, so a link is judged by what it
// leads to: an existing file must be a regular file this process can write; a
// name that leads nowhere is created by the open, in a directory that must
// already be there (the open makes no directory) and that this process can
// create in. A link whose referent's directory is missing is the ENOENT the
// open would meet.
func CheckAppend(path string) error {
	dir := filepath.Dir(path)
	if err := checkMkdirAll(dir); err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: parent directory for %q", path), err)
	}
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil // a new directory MkdirAll makes holds nothing for the open to meet
	}
	target, err := referent(path)
	if err != nil {
		return wrapErr(fmt.Sprintf("atomicfile: open %q", path), err)
	}
	fi, err := os.Stat(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		parent := filepath.Dir(target)
		pi, perr := os.Stat(parent)
		if perr != nil {
			return wrapErr(fmt.Sprintf("atomicfile: open %q", path), perr)
		}
		if !pi.IsDir() {
			return wrapErr(fmt.Sprintf("atomicfile: open %q", path), &fs.PathError{Op: "open", Path: parent, Err: errors.New("not a directory")})
		}
		return wrapErr(fmt.Sprintf("atomicfile: open %q", path), canWrite(parent))
	case err != nil:
		return wrapErr(fmt.Sprintf("atomicfile: open %q", path), err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("atomicfile: %q is not a regular file", path)
	}
	return wrapErr(fmt.Sprintf("atomicfile: open %q", path), canWriteFile(target, fi))
}

// referent follows path through its symbolic links, as an open does, to the
// name the open would create or reach: path itself when it is no link.
func referent(path string) (string, error) {
	for hops := 0; hops < 40; hops++ {
		li, err := os.Lstat(path)
		if err != nil || li.Mode()&fs.ModeSymlink == 0 {
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			return path, nil
		}
		to, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(to) {
			to = filepath.Join(filepath.Dir(path), to)
		}
		path = to
	}
	return "", &fs.PathError{Op: "open", Path: path, Err: errors.New("too many levels of symbolic links")}
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
