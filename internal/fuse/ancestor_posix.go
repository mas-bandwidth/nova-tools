//go:build !windows

package fuse

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkBoxAncestors implements security finding 74.6: before a box write or
// its plan, user-owned symlink ancestors are refused while root-owned platform
// links such as /tmp and /var remain valid.
func checkBoxAncestors(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for part := filepath.Dir(abs); part != filepath.Dir(part); part = filepath.Dir(part) {
		info, err := os.Lstat(part)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok && stat.Uid == 0 {
			continue
		}
		resolved, resolveErr := filepath.EvalSymlinks(part)
		if resolveErr != nil {
			resolved = "unresolved: " + resolveErr.Error()
		} else if rest, relErr := filepath.Rel(part, abs); relErr == nil {
			resolved = filepath.Join(resolved, rest)
		}
		return fmt.Errorf("box path has a symlink ancestor %s owned by a non-root user; use the resolved path %s", part, resolved)
	}
	return nil
}
