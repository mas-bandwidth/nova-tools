package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdSpelling(e env, args []string, stdout, stderr io.Writer) int {
	var asJSON bool
	stdout, stderr = jsonWriters(stdout, stderr, &asJSON)
	defer stderr.(*jsonOutput).finish()
	fs := flag.NewFlagSet("spelling", flag.ContinueOnError)
	fs.BoolVar(&asJSON, "json", false, "print typed findings and totals as one JSON object")
	dir := fs.String("dir", "", "directory tree to scan for misspellings")
	var files repeatable
	fs.Var(&files, "file", "one file to check, narrowing the check to just these (repeatable)")
	var paths repeatable
	fs.Var(&paths, "path", "file or glob pattern to check (repeatable)")
	var ignore repeatable
	fs.Var(&ignore, "ignore", "allowlisted word or @file (repeatable, or comma-separated)")
	write := fs.Bool("write", false, "apply spelling corrections to files in place")
	dryRun := fs.Bool("dry-run", false, "with --write, print the corrections it would make and write nothing")
	var exclude repeatable
	fs.Var(&exclude, "exclude", "path prefix not scanned (repeatable; empty by default)")
	allowEmpty := fs.Bool("allow-empty", false, "answer OK when zero files are read; without it, a run that read nothing is FAILED")
	maxFlag := addMax(fs)

	if !parseFlags(fs, args, stderr) {
		return 2
	}
	if !checkMax(fs, *maxFlag, stderr) {
		return 2
	}

	if *dir == "" && len(files) == 0 && len(paths) == 0 {
		refuse(stderr, " spelling", "give at least one of --dir, --file, or --path; refusing to guess")
		return 2
	}

	// Validate ignore flags before proceeding.
	if _, err := check.ParseIgnoreSpec(ignore); err != nil {
		return refuse(stderr, " spelling", oneline.Err(err))
	}

	// The tree a relative --dir names is resolved against the invocation's own
	// directory, and with no --dir at all the invocation's directory IS the
	// tree; neither is read off the process (docs/STANDARD.md section 8).
	root := e.path(*dir)
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(abs)
		}
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
	} else if cwd, err := e.workdir(); err == nil {
		if abs, err := filepath.Abs(cwd); err == nil {
			cwd = filepath.Clean(abs)
		}
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			root = resolved
		} else {
			root = cwd
		}
	}

	opts := check.SpellingOptions{
		Ignore:  ignore,
		Write:   *write,
		Plan:    *dryRun, // the plan of the write: every check it makes, nothing written
		Exclude: exclude,
		Dir:     root,
	}

	var (
		res check.SpellingResult
		err error
	)
	if len(files) > 0 && len(paths) == 0 {
		absFiles := make([]string, len(files))
		for i, f := range files {
			if !filepath.IsAbs(f) {
				absFiles[i] = filepath.Join(root, f)
			} else {
				absFiles[i] = filepath.Clean(f)
			}
		}
		res, err = check.CheckSpellingFiles(root, absFiles, opts)
	} else if len(paths) > 0 || (len(files) > 0 && len(paths) > 0) {
		allTargets := append([]string(nil), files...)
		allTargets = append(allTargets, paths...)
		absTargets := make([]string, len(allTargets))
		for i, p := range allTargets {
			if !filepath.IsAbs(p) {
				absTargets[i] = filepath.Join(root, p)
			} else {
				absTargets[i] = filepath.Clean(p)
			}
		}
		res, err = check.CheckSpelling(absTargets, opts)
	} else {
		res, err = check.CheckSpellingDir(root, opts)
	}
	if err != nil {
		return refuse(stderr, " spelling", oneline.Err(err))
	}

	// One verdict for both renderings: what was asked (a check, a write, or a
	// write planned by --dry-run) decides the status, the exit, the facts and
	// the listing's bound; the line form and the JSON print the same value.
	v := spellingVerdictOf(res, *write, *dryRun, *maxFlag, *allowEmpty)
	if v.empty {
		// The way out names the same flags that chose what this run read, so the
		// command in the FAILED line runs as printed (docs/STANDARD.md section 2,
		// a result names the next command).
		v.remedy = "nova-check spelling" + spellingSelector(*dir, files, paths) + " --allow-empty"
	}
	if asJSON {
		return renderSpelling(stdout, root, res, v)
	}
	if v.empty {
		// No green over nothing: the run read no file, so it read nothing, and
		// an OK over nothing is not green (docs/STANDARD.md section 2, exit
		// codes tell the truth). The FAILED names the count and the flag that
		// accepts the empty set.
		fmt.Fprintf(stderr, "SPELLING FAILED %s=0 misspellings=0: looked at nothing; run: %s\n",
			oneline.Field(looks["spelling"]), oneline.Escape(v.remedy))
		return 1
	}
	if v.planned {
		list := bounded.Capped(stdout, v.max, "SPELLING", "misspelling", maxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FIX %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=%d written=0 dry_run=true\n", res.FilesScanned, len(res.Findings))
		return v.exit
	}
	if *write {
		for _, f := range res.Findings {
			fmt.Fprintf(stdout, "SPELLING FIXED %s:%d:%d: %s -> %s\n",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement))
		}
		fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=%d written=%d\n",
			res.FilesScanned, len(res.Findings), res.Corrected)
		return v.exit
	}

	if len(res.Findings) > 0 {
		list := bounded.Capped(stderr, *maxFlag, "SPELLING", "misspelling", maxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FAILED %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(stderr, "SPELLING FAILED files=%d misspellings=%d shown=%d\n",
			res.FilesScanned, list.Total(), list.Shown())
		return 1
	}

	fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=0 excluded=%d\n", res.FilesScanned, res.Excluded)
	return 0
}

// spellingVerdict is one spelling run's answer, computed once and printed by
// both renderings: a check of prose says FAILED when it finds a misspelling; a
// write, real or planned by --dry-run, says OK with what it wrote (or would).
type spellingVerdict struct {
	planned bool   // --write --dry-run: corrections listed, nothing written
	failed  bool   // the run said no: misspellings found, or nothing read
	empty   bool   // the run read no file and --allow-empty did not accept that
	exit    int    // 0 ok, 1 failed
	max     int    // the listing's bound: --max, except a real write lists every correction it made
	remedy  string // the empty-set FAILED line's way out, the flags as given
}

func spellingVerdictOf(res check.SpellingResult, write, dryRun bool, maxFlag int, allowEmpty bool) spellingVerdict {
	v := spellingVerdict{planned: write && dryRun, max: maxFlag}
	if write && !dryRun {
		v.max = 0
	}
	if !write && len(res.Findings) > 0 {
		v.failed, v.exit = true, 1
	}
	if res.FilesScanned == 0 && !allowEmpty {
		// A verb that declares Looks is not green over a read of nothing: the
		// count the OK line would carry is zero (docs/STANDARD.md section 2,
		// exit codes tell the truth).
		v.empty, v.failed, v.exit = true, true, 1
	}
	return v
}

// spellingSelector echoes the flags that chose what a spelling run read, so the
// empty-set FAILED names a command that runs with --allow-empty appended
// (docs/STANDARD.md section 2, a result names the next command). Each value is
// one escaped shell word, so a path with a blank still pastes.
func spellingSelector(dir string, files, paths []string) string {
	var b strings.Builder
	if dir != "" {
		fmt.Fprintf(&b, " --dir %s", oneline.Escape(oneline.ShellWord(dir)))
	}
	for _, f := range files {
		fmt.Fprintf(&b, " --file %s", oneline.Escape(oneline.ShellWord(f)))
	}
	for _, p := range paths {
		fmt.Fprintf(&b, " --path %s", oneline.Escape(oneline.ShellWord(p)))
	}
	return b.String()
}
