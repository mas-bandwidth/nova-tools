package release

import (
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// KeepBesides is how many release versions a root keeps BESIDES the ones it
// must: the version just built or installed, and the version the machine was
// running before it. A root that keeps every version forever, one directory per
// dev build, runs a machine out of disk; the measurement below is that root,
// not a claim every machine reaches it.
// Measurement (2026-10-01): 36 versions, 9 GB on one root; 34 GB on another.
//
// Three is enough to put back any of the last few builds by re-installing it
// without a rebuild, which is the only thing an old version directory is for,
// and it bounds a root at five versions whatever the day's build rate.
const KeepBesides = 3

// prune removes the version directories under root that keep does not name, and reports what it did.
// It is the LAST step of a build or an install that has already succeeded, and
// it never fails one: a directory that cannot be removed is said on errs and
// counted, and the verb's exit code is the build's or the install's.
//
// WHAT IT WILL REMOVE IS NARROW. Only a directory directly under root (never a
// symlink, never a file) whose name is a version this tooling would write
// (ValidVersion, the check every --version passes before anything is named
// after it); anything else in the root is somebody's and is left alone. Never a
// name keep says is protected. Of the rest, the KeepBesides newest by
// modification time stay. Each removal goes through safepath.RemoveUnder, which
// refuses a path that is not strictly below root.
func prune(root string, keep func(name string) bool, remove func(root, path string) error, errs io.Writer) (removed, failed int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		progress(errs, "cannot list %s to remove old releases: %v (nothing removed; nothing else is affected)", root, err)
		return 0, 1
	}
	type candidate struct {
		name string
		mod  int64
	}
	var old []candidate
	for _, e := range entries {
		if !e.IsDir() || ValidVersion(e.Name()) != nil || keep(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// Not stat-able is not evidence of age: it stays.
			progress(errs, "leaving %s alone: %v", filepath.Join(root, e.Name()), err)
			continue
		}
		old = append(old, candidate{e.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].mod != old[j].mod {
			return old[i].mod > old[j].mod
		}
		return old[i].name > old[j].name
	})
	for i, c := range old {
		if i < KeepBesides {
			continue
		}
		path := filepath.Join(root, c.name)
		progress(errs, "removing the old release %s", path)
		if err := remove(root, path); err != nil {
			progress(errs, "cannot remove the old release %s: %v (remove it by hand; nothing else is affected)", path, err)
			failed++
			continue
		}
		removed++
	}
	return removed, failed
}

// pruneDefault is prune with the one production removal.
func pruneDefault(root string, keep func(name string) bool, errs io.Writer) (int, int) {
	return prune(root, keep, safepath.RemoveUnder, errs)
}
