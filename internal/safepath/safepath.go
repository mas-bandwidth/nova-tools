// Package safepath is the one way a path this tool COMPUTED is removed. It exists
// because a reaper glob, a lane flag, a slot directory and a job directory are all
// paths the tool derived rather than paths a person is deleting by hand, and a wrong
// derivation must not be able to remove an arbitrary directory. Glenn, 2026-09-17:
// "It is just one mistake away from deleting the whole disk."
//
// RemoveUnder removes a path only when it is STRICTLY below a root the caller names.
// It refuses an empty root or path, a root that is the whole disk or the user's home, a
// path equal to its root, a path that resolves outside its root once symlinks are
// followed, and a path that is itself a symlink. The removal is the caller's one
// allowed os.RemoveAll; no other package removes a computed path directly.
//
// The hygiene verbs use the second door, RemoveUnderRoots: it is the same removal
// check reached through a small set of literal roots, with a ".." element refused
// before anything resolves and no element left non-writable. It is the Go half of
// bin/bench-hygiene.sh's `under_root` and `remove`: the path is never built from
// user text, it is the join of a literal root and a name, and the check is by
// construction rather than by a caller's care.
package safepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsafe wraps every refusal, so a caller can say "this was a refusal, not an I/O
// failure" without reading the message.
var ErrUnsafe = errors.New("refusing to remove an unsafe path")

// RemoveUnder removes path, which must sit strictly below root. It is os.RemoveAll
// with the one question that matters answered first: can this path escape the root a
// person named? A path that does not exist is nothing to remove and returns nil.
//
// root and path may be relative; they are made absolute against the process's own
// directory, the same directory the caller would have opened them from. Symlinks are
// followed on BOTH sides before the containment test, so a link cannot smuggle a path
// outside its root, and the path itself may not be a link: removing a link removes
// only the link, but a link where a directory was expected is a derivation the tool
// must not act on.
func RemoveUnder(root, path string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("%w: the root is empty", ErrUnsafe)
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: the path is empty", ErrUnsafe)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("%w: the root %q does not resolve: %v", ErrUnsafe, root, err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: the path %q does not resolve: %v", ErrUnsafe, path, err)
	}
	if err := refuseUnsafeRoot(rootAbs); err != nil {
		return err
	}
	if filepath.Clean(rootAbs) == filepath.Clean(pathAbs) {
		return fmt.Errorf("%w: %q is the root itself", ErrUnsafe, path)
	}
	// The path may not be a symlink, whether or not its target is inside the root.
	if info, err := os.Lstat(pathAbs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q is a symlink", ErrUnsafe, path)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("%w: the root %q cannot be resolved: %v", ErrUnsafe, root, err)
	}
	pathReal, err := filepath.EvalSymlinks(pathAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: the path %q cannot be resolved: %v", ErrUnsafe, path, err)
	}
	if err := refuseUnsafePath(pathReal); err != nil {
		return err
	}
	if !strictlyUnder(rootReal, pathReal) {
		return fmt.Errorf("%w: %q is not below %q", ErrUnsafe, path, root)
	}
	return os.RemoveAll(pathAbs)
}

// strictlyUnder reports whether path is below root and not root itself. Both are
// expected to be the clean output of EvalSymlinks.
func strictlyUnder(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return false
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

// refuseUnsafeRoot refuses a root that is the whole disk or the user's home: those are
// not a boundary, they are the absence of one, and a mistake under either is the disk.
func refuseUnsafeRoot(root string) error {
	if filepath.Clean(root) == string(os.PathSeparator) {
		return fmt.Errorf("%w: the root is %q, the whole disk", ErrUnsafe, root)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		homeAbs, err := filepath.Abs(home)
		if err == nil && filepath.Clean(homeAbs) == filepath.Clean(root) {
			return fmt.Errorf("%w: the root is the user's home %q", ErrUnsafe, root)
		}
	}
	return nil
}

// refuseUnsafePath refuses a resolved path that is the whole disk or the user's home,
// even when it is technically below the root: the home directory is never a directory
// this tool computed, and deleting it is the bug that matters most.
func refuseUnsafePath(path string) error {
	if filepath.Clean(path) == string(os.PathSeparator) {
		return fmt.Errorf("%w: the path resolves to %q, the whole disk", ErrUnsafe, path)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		homeReal, err := filepath.EvalSymlinks(home)
		if err == nil && filepath.Clean(homeReal) == filepath.Clean(path) {
			return fmt.Errorf("%w: the path resolves to the user's home %q", ErrUnsafe, path)
		}
	}
	return nil
}

// Refused is why a path was not removed: the path and the one reason.
type Refused struct {
	Path   string
	Reason string
}

func (r *Refused) Error() string { return fmt.Sprintf("%s: %s", r.Path, r.Reason) }

// NameOK reports whether s is one safe path element: not empty, not "." or
// "..", no slash, not starting with "-", and only [A-Za-z0-9._-].
func NameOK(s string) bool {
	if s == "" || s == "." || s == ".." || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// HasDotDot reports whether any element of p is "..".
func HasDotDot(p string) bool {
	for _, e := range strings.Split(filepath.ToSlash(p), "/") {
		if e == ".." {
			return true
		}
	}
	return false
}

// ResolvedUnder returns path with symlinks resolved when it is strictly below
// root (also resolved for symlinks). A path with a ".." element, a path that is
// itself a symlink, and a path that resolves outside root are all refused.
func ResolvedUnder(path, root string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", &Refused{Path: path, Reason: "the path is empty"}
	}
	if HasDotDot(path) {
		return "", &Refused{Path: path, Reason: `the path contains ".."`}
	}
	li, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return "", &Refused{Path: path, Reason: "the path is a symlink"}
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if resolved == resolvedRoot || !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) {
		return "", &Refused{Path: path, Reason: "the path is not strictly below " + root}
	}
	return resolved, nil
}

// RemoveUnderRoots removes path when it is strictly below one of roots. It is the
// only rm in the hygiene verbs; a refusal names the path and the reason.
func RemoveUnderRoots(path string, roots ...string) error {
	if len(roots) == 0 {
		return &Refused{Path: path, Reason: "no root to remove under"}
	}
	var last error
	for _, root := range roots {
		resolved, err := ResolvedUnder(path, root)
		if err != nil {
			last = err
			continue
		}
		addUserWrite(resolved)
		return os.RemoveAll(resolved)
	}
	return last
}

// addUserWrite makes the tree writable, best effort, the way the old script's
// `chmod -R u+w` ran before its `rm -rf`.
func addUserWrite(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		_ = os.Chmod(p, info.Mode().Perm()|0o200)
		return nil
	})
}
