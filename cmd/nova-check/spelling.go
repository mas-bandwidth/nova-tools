package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdSpelling(args []string, stdout, stderr io.Writer) int {
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
	failMax := addFailMax(fs)

	if !parseFlags(fs, args, stderr) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
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

	root := *dir
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(abs)
		}
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
	} else if cwd, err := os.Getwd(); err == nil {
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
		Write:   *write && !*dryRun,
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
	v := spellingVerdictOf(res, *write, *dryRun, *failMax)
	if asJSON {
		return renderSpelling(stdout, root, res, v)
	}
	if v.planned {
		list := bounded.Capped(stdout, v.max, "SPELLING", "misspelling", failMaxRemedy)
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
		list := bounded.Capped(stderr, *failMax, "SPELLING", "misspelling", failMaxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FAIL %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(stderr, "SPELLING FAIL files=%d misspellings=%d shown=%d\n",
			res.FilesScanned, list.Total(), list.Shown())
		return 1
	}

	fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=0 excluded=%d\n", res.FilesScanned, res.Excluded)
	return 0
}

// spellingVerdict is one spelling run's answer, computed once and printed by
// both renderings: a check of prose says FAIL when it finds a misspelling; a
// write, real or planned by --dry-run, says OK with what it wrote (or would).
type spellingVerdict struct {
	planned bool // --write --dry-run: corrections listed, nothing written
	failed  bool
	exit    int
	max     int // the listing's bound: --fail-max, except a real write lists every correction it made
}

func spellingVerdictOf(res check.SpellingResult, write, dryRun bool, failMax int) spellingVerdict {
	v := spellingVerdict{planned: write && dryRun, max: failMax}
	if write && !dryRun {
		v.max = 0
	}
	if !write && len(res.Findings) > 0 {
		v.failed, v.exit = true, 1
	}
	return v
}
