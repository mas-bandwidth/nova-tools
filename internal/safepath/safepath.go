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
	rootReal, err := resolveRoot(rootAbs, root)
	if err != nil {
		return err
	}
	// The resolved root is the boundary that will actually be used, so it is the one
	// that has to be a boundary: a root spelled as a symlink to the home or to the
	// whole disk is that directory, whatever the caller called it.
	if err := refuseUnsafeRoot(rootReal); err != nil {
		return err
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
	under, err := strictlyUnder(rootReal, pathReal)
	if err != nil {
		return fmt.Errorf("%w: %q could not be placed under %q: %v", ErrUnsafe, path, root, err)
	}
	if !under {
		return fmt.Errorf("%w: %q is not below %q", ErrUnsafe, path, root)
	}
	return os.RemoveAll(pathAbs)
}

// A PATH IS NOT A NAME FOR A DIRECTORY. IT IS ONE OF ITS NAMES.
//
// Johnny, 2026-09-19: "On APFS, `EvalSymlinks(\"/Users/Glenn\")` stays `/Users/Glenn` and
// string-compare misses `/Users/glenn`. Identify HOME by `os.SameFile`, not by the string."
//
// EvalSymlinks is not a canonicaliser. It resolves symlinks and it cleans, and on a
// case-sensitive volume that happens to be enough, so every check below used to be a string
// compare and passed on linux. On a case-insensitive one it is not: two spellings that
// differ in case, or in Unicode normalisation form, are THE SAME DIRECTORY and compare
// unequal. Case-folding the string is not the fix either, because the normalisation forms
// differ too, and neither is a property of the path the process can read off.
//
// The kernel already answers the question. A directory's identity is its device and inode,
// which is exactly what os.SameFile compares, and os.Stat follows the links and the
// spellings to get there. So every place in this package that used to ask "is this string
// the home / the root / below the root" now asks the file system instead. The sites, all of
// them in this file and all of them changed together:
//
//  1. refuseUnsafeRoot  - is the root the whole disk, or the home?     (was ==)
//  2. refuseUnsafePath  - is the resolved path the whole disk, or the home? (was ==)
//  3. strictlyUnder     - is the path strictly below the root?         (was HasPrefix)
//  4. ResolvedUnder     - the same containment test, the other door.   (was HasPrefix)
//
// They are the only four: a sweep of the repository for os.UserHomeDir found four other
// callers (internal/pulse/statushtmlrows.go, internal/pulse/harvest_working.go,
// cmd/nova-merge/batch.go, cmd/nova-swarm/native.go) and not one of them COMPARES a path to
// the home -- each joins onto it or hands it to a command -- so none of them can make this
// mistake.
//
// AND EVERY ONE OF THEM FAILS CLOSED. A Stat that does not answer is not permission to
// remove: it is the one case where the check could not be made, and an unanswered question
// about the home directory is refused, never waved through. The old code did the opposite:
// it skipped the home comparison entirely when EvalSymlinks returned an error.

// sameDir reports whether two paths name the same directory, by device and inode rather
// than by spelling. A Stat that fails is reported as an error, never as "different": the
// caller refuses on it.
func sameDir(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// strictlyUnder reports whether path is below root and not root itself, deciding every step
// by identity: it walks up from the path's own parent and asks the file system whether each
// ancestor IS the root. A string prefix cannot do this, for the reason above, and it is the
// ancestor relation -- not just the equality -- that has to be identity, or a path whose
// spelling differs from its root's walks straight out of the boundary.
//
// An error anywhere in the walk is a refusal: see FAILS CLOSED above.
func strictlyUnder(root, path string) (bool, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	at := filepath.Clean(path)
	for {
		parent := filepath.Dir(at)
		if parent == at {
			// The volume root, reached without meeting the root: not below it.
			return false, nil
		}
		info, err := os.Stat(parent)
		if err != nil {
			return false, err
		}
		if os.SameFile(info, rootInfo) {
			return true, nil
		}
		at = parent
	}
}

// resolveRoot resolves the root's absolute form through symlinks, so a root that is a
// symlink to an unsafe directory is refused as that directory. It is the one resolution
// shared by both removal doors.
func resolveRoot(rootAbs, root string) (string, error) {
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("%w: the root %q cannot be resolved: %v", ErrUnsafe, root, err)
	}
	return rootReal, nil
}

// refuseUnsafeRoot refuses a root that is the whole disk or the user's home: those are
// not a boundary, they are the absence of one, and a mistake under either is the disk.
func refuseUnsafeRoot(root string) error {
	if isDisk, err := sameDir(root, string(os.PathSeparator)); err != nil {
		return fmt.Errorf("%w: the root %q cannot be identified: %v", ErrUnsafe, root, err)
	} else if isDisk {
		return fmt.Errorf("%w: the root is %q, the whole disk", ErrUnsafe, root)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		isHome, err := sameDir(root, home)
		if err != nil {
			return fmt.Errorf("%w: the root %q could not be compared with the user's home: %v", ErrUnsafe, root, err)
		}
		if isHome {
			return fmt.Errorf("%w: the root is the user's home %q", ErrUnsafe, root)
		}
	}
	return nil
}

// refuseUnsafePath refuses a resolved path that is the whole disk or the user's home,
// even when it is technically below the root: the home directory is never a directory
// this tool computed, and deleting it is the bug that matters most.
func refuseUnsafePath(path string) error {
	if isDisk, err := sameDir(path, string(os.PathSeparator)); err != nil {
		return fmt.Errorf("%w: the path %q cannot be identified: %v", ErrUnsafe, path, err)
	} else if isDisk {
		return fmt.Errorf("%w: the path resolves to %q, the whole disk", ErrUnsafe, path)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		isHome, err := sameDir(path, home)
		if err != nil {
			return fmt.Errorf("%w: the path %q could not be compared with the user's home: %v", ErrUnsafe, path, err)
		}
		if isHome {
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
	// The same containment test as RemoveUnder's, by the same identity rather than by a
	// string prefix: site 4 of the four named at the top of this file.
	under, err := strictlyUnder(resolvedRoot, resolved)
	if err != nil {
		return "", &Refused{Path: path, Reason: "the path could not be placed under " + root + ": " + err.Error()}
	}
	if !under {
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
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			last = fmt.Errorf("%w: the root %q does not resolve: %v", ErrUnsafe, root, err)
			continue
		}
		rootReal, err := resolveRoot(rootAbs, root)
		if err != nil {
			last = err
			continue
		}
		if err := refuseUnsafeRoot(rootReal); err != nil {
			last = err
			continue
		}
		resolved, err := ResolvedUnder(path, root)
		if err != nil {
			last = err
			continue
		}
		if err := refuseUnsafePath(resolved); err != nil {
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
