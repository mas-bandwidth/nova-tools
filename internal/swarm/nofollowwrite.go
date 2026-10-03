package swarm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileNoFollow writes data to path, which must sit lexically inside root.
//
// The data home is a write root of the wall, so a card can plant a symlink where
// this process later writes auth.json or opencode.json (security#47). os.WriteFile
// and os.MkdirAll follow that link, including a directory symlink above the final
// component. A symlink at root or at any component of path below root is an error
// and is not followed. Missing directories below root are created as real
// directories. The file is published with writeAtomic: the temporary is opened
// O_NOFOLLOW|O_EXCL, and a final component that is already not a regular file is
// refused before that open.
func WriteFileNoFollow(root, path string, data []byte, mode os.FileMode) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if _, err := relWithin(root, path); err != nil {
		return err
	}
	rst, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rst.Mode()&os.ModeSymlink != 0 {
		return refuseSymlink("write", root)
	}
	if !rst.IsDir() {
		return &fs.PathError{Op: "write", Path: root, Err: fmt.Errorf("not a directory: %w", errNotRegular)}
	}
	if err := mkdirBelow(root, filepath.Dir(path)); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		return refuseSymlink("write", path)
	case err == nil && !fi.Mode().IsRegular():
		return &fs.PathError{Op: "write", Path: path, Err: fmt.Errorf("not a regular file (%s); this write follows no symlink: %w", kindOf(fi.Mode()), errNotRegular)}
	case err != nil && !missing(err):
		return err
	}
	return writeAtomic(path, data, mode)
}

// relWithin is the lexical path of path relative to root, refused when path
// is root itself or sits outside it. It does not touch the filesystem.
func relWithin(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &fs.PathError{Op: "write", Path: path, Err: fmt.Errorf("outside %s: %w", root, errNotRegular)}
	}
	return rel, nil
}

// mkdirBelow creates dir and any missing parents strictly below root. An existing
// component is taken only when Lstat says it is a real directory. A symlink is
// refused here, before any create, so MkdirAll cannot follow it.
func mkdirBelow(root, dir string) error {
	if dir == root {
		return nil
	}
	rel, err := relWithin(root, dir)
	if err != nil {
		return err
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if missing(err) {
			if mkErr := os.Mkdir(cur, 0o755); mkErr == nil {
				continue
			} else if !errors.Is(mkErr, fs.ErrExist) {
				return mkErr
			}
			fi, err = os.Lstat(cur)
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return refuseSymlink("mkdir", cur)
		}
		if !fi.IsDir() {
			return &fs.PathError{Op: "mkdir", Path: cur, Err: fmt.Errorf("not a directory: %w", errNotRegular)}
		}
	}
	return nil
}

func refuseSymlink(op, path string) error {
	return &fs.PathError{Op: op, Path: path, Err: fmt.Errorf("symlink; this write follows no symlink: %w", errNotRegular)}
}
