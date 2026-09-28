package main

// forestwrite.go is the ONE place this binary writes a work set back to disk,
// and the boundary that keeps it off the forest (#3340, stage 1 of #3309).
//
// The forest is every file under a docs/roadmaps/ directory: nova-work.sexp,
// sprint-fixes-2026-09-22.sexp and the storage split's work/ and blobs/ beside
// them. SPEC-WORK rule 6 says a mutation outside the kernel's command loop is a
// defect, and the forest's one writer is the kernel: its journal is the source
// of truth and the .sexp is its render. So a verb that edits a work set --
// `set check --write-status`, `attempt record`, `next --take` -- writes through
// writeWorkSet, and writeWorkSet refuses a forest path before a byte moves. The
// verb refuses it earlier still, at exit 3 (no kernel session: nothing written,
// nothing sent), so the refusal costs no network and no lock.
//
// A work set anywhere else (a plan in job storage, a fixture in a temp dir) is
// still written here, beside itself through one temporary file and one rename,
// so a reader never sees half a set and the file keeps its mode.
//
// internal/ci's TestForestWrittenOnlyByTheKernel holds the rest of the tree to
// the same line: a file write in any package that reads work sets lives here or
// on that rule's shrink-only list, and no function anywhere writes a file while
// naming a docs/roadmaps/ path.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// forestDir is the directory whose files only the kernel writes.
const forestDir = "docs/roadmaps"

// errForest is what writeWorkSet returns for a forest path: nothing was written.
var errForest = errors.New("the forest (" + forestDir + "/) is written only by the nova-work kernel (#3340); nothing was written")

// isForestPath reports whether path names a file in the forest. It reads the
// path as written and, when it resolves, through its symlinks, so neither a
// relative spelling nor a link into or out of docs/roadmaps/ gets around it.
func isForestPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	spellings := []string{path}
	if abs, err := filepath.Abs(path); err == nil {
		spellings = append(spellings, abs)
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		spellings = append(spellings, real)
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		spellings = append(spellings, filepath.Join(real, filepath.Base(path)))
	}
	for _, s := range spellings {
		s = "/" + filepath.ToSlash(filepath.Clean(s)) + "/"
		if strings.Contains(s, "/"+forestDir+"/") {
			return true
		}
	}
	return false
}

// forestRefused prints the one refusal line a verb gives for a forest path and
// returns exit 3: no kernel session took the change, so nothing was written and
// nothing was sent.
func forestRefused(stderr io.Writer, where, path string) int {
	fmt.Fprintf(stderr, "nova-work%s: file=%s: %s\n", oneline.Escape(where), oneline.Field(path), oneline.Escape(errForest.Error()))
	return 3
}

// writeWorkSet replaces the work set at path with data: a temporary file beside
// it, the mode it had (0644 for a new one), and one rename. A forest path is
// refused with errForest before anything is created.
func writeWorkSet(path string, data []byte) error {
	if isForestPath(path) {
		return errForest
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.WriteFile(name, data, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
