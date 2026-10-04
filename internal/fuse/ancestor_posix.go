//go:build darwin || linux

package fuse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type ancestorLstat func(string) (os.FileInfo, error)
type ancestorReadlink func(string) (string, error)
type ancestorOwner func(string, os.FileInfo) (uint32, bool)

// checkBoxAncestors implements security finding 74.6: before a box write or
// its plan, user-owned symlink ancestors are refused while root-owned platform
// links such as /tmp and /var remain valid.
func checkBoxAncestors(path string) error {
	return checkBoxAncestorsWith(path, os.Lstat, os.Readlink, posixLinkOwner)
}

func checkBoxAncestorsWith(path string, lstat ancestorLstat, readlink ancestorReadlink, owner ancestorOwner) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for followed := 0; ; followed++ {
		if followed > 255 {
			return fmt.Errorf("box path has too many symlink ancestors: %s", abs)
		}
		next, changed, err := inspectBoxAncestorPath(abs, lstat, readlink, owner)
		if err != nil || !changed {
			return err
		}
		abs = next
	}
}

func inspectBoxAncestorPath(path string, lstat ancestorLstat, readlink ancestorReadlink, owner ancestorOwner) (string, bool, error) {
	root, parts := splitAncestorPath(filepath.Dir(path))
	current := root
	for i, name := range parts {
		current = filepath.Join(current, name)
		info, err := lstat(current)
		if os.IsNotExist(err) {
			return path, false, nil
		}
		if err != nil {
			return "", false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return inspectBoxAncestorLink(path, current, parts[i+1:], info, readlink, owner)
		}
	}
	return path, false, nil
}

func inspectBoxAncestorLink(path, link string, remaining []string, info os.FileInfo, readlink ancestorReadlink, owner ancestorOwner) (string, bool, error) {
	uid, ok := owner(link, info)
	if !ok || uid != 0 {
		resolved := resolvedBoxPath(link, append(remaining, filepath.Base(path)))
		return "", false, fmt.Errorf("box path has a symlink ancestor %s owned by a non-root user; use the resolved path %s", link, resolved)
	}
	target, err := readlink(link)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	return filepath.Join(append([]string{target}, append(remaining, filepath.Base(path))...)...), true, nil
}

func splitAncestorPath(path string) (string, []string) {
	volume := filepath.VolumeName(path)
	root := volume + string(filepath.Separator)
	rel := strings.TrimPrefix(path, root)
	if rel == "" {
		return root, nil
	}
	return root, strings.Split(rel, string(filepath.Separator))
}

func resolvedBoxPath(link string, remaining []string) string {
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "unresolved: " + err.Error()
	}
	return filepath.Join(append([]string{resolved}, remaining...)...)
}

func posixLinkOwner(_ string, info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}
