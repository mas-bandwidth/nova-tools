package guard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MaxTrackedBytes is the size cap on one tracked file: a source repository
// carries no built executable, no log and no dump (a 4.9 MB Mach-O once
// reached the tree, referenced by nothing). Measured on dev ac1dfd2e the
// largest tracked file is a 3.0 MB png and the next is 0.8 MB, so 4 MiB
// passes the tree and fails that executable.
const MaxTrackedBytes = 4 << 20

// scratchDirs are directories that are a card's or a tool's scratch, never
// content (the .gitignore names them; a tracked file under one slipped past
// an ignore that was added after the file).
var scratchDirs = []string{"scratch/", "tending/", ".tmp/", "bin/"}

// scratchNames are file names that are scratch wherever they are.
var scratchNames = map[string]bool{".DS_Store": true, "notes.txt": true, "Thumbs.db": true}

// scratchSuffixes are extensions no source file of ours has.
var scratchSuffixes = []string{".log", ".tmp", ".orig", ".rej", ".swp", ".bak", "~"}

// trackedFiles lists the tree's tracked files with git and fails on the
// first that is scratch or over MaxTrackedBytes. It is the guard for the
// 2026-09-18 shape (79 junk files reached dev from a card's TMPDIR beside
// its clone) on a merged tree, where an ignore one member added does not
// untrack what another member committed.
func trackedFiles(ctx context.Context, root string) Result {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return Result{Err: fmt.Errorf("git: %w", err)}
	}
	cmd := exec.CommandContext(ctx, gitBin, "-C", root, "ls-files", "-z")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return Result{Err: fmt.Errorf("git ls-files: %v: %s", err, strings.TrimSpace(errOut.String()))}
	}
	files := strings.Split(strings.TrimRight(out.String(), "\x00"), "\x00")
	if len(files) == 1 && files[0] == "" {
		return Result{Err: fmt.Errorf("git ls-files lists nothing under %s", root)}
	}
	for _, f := range files {
		if f == "" {
			continue
		}
		if why := scratchReason(f); why != "" {
			return Result{OK: false, File: f, Why: why + "; git rm it (the .gitignore names the class)"}
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			continue // a tracked path missing from the checkout is git's problem, not size
		}
		if info.Mode().IsRegular() && info.Size() > MaxTrackedBytes {
			return Result{OK: false, File: f, Why: fmt.Sprintf("%d bytes tracked, over the %d-byte cap; a source repository ships no built executable, log or dump", info.Size(), MaxTrackedBytes)}
		}
	}
	return Result{OK: true, Why: fmt.Sprintf("%d tracked files, none scratch, none over %d bytes", len(files), MaxTrackedBytes)}
}

// scratchReason names why a tracked path is scratch, or "". A fixture under
// a testdata directory is what it is on purpose (a .log a parser test reads)
// and is judged by size only.
func scratchReason(f string) string {
	if strings.HasPrefix(f, "testdata/") || strings.Contains(f, "/testdata/") {
		return ""
	}
	for _, d := range scratchDirs {
		if strings.HasPrefix(f, d) || strings.Contains(f, "/"+d) {
			return "tracked under the scratch directory " + d
		}
	}
	base := f[strings.LastIndex(f, "/")+1:]
	if scratchNames[base] {
		return "tracked scratch file " + base
	}
	for _, s := range scratchSuffixes {
		if strings.HasSuffix(base, s) {
			return "tracked scratch file (" + s + ")"
		}
	}
	return ""
}
