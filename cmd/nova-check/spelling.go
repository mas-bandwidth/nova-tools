package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdSpelling(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spelling", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory tree to scan for misspellings")
	var files repeatable
	fs.Var(&files, "file", "one file to check, narrowing the check to just these (repeatable)")
	var paths repeatable
	fs.Var(&paths, "path", "file or glob pattern to check (repeatable)")
	var ignore repeatable
	fs.Var(&ignore, "ignore", "allowlisted word or @file (repeatable, or comma-separated)")
	write := fs.Bool("write", false, "apply spelling corrections to files in place")
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

	opts := check.SpellingOptions{
		Ignore:  ignore,
		Write:   *write,
		Exclude: exclude,
	}

	var (
		res check.SpellingResult
		err error
	)
	if len(files) > 0 && len(paths) == 0 {
		res, err = check.CheckSpellingFiles(*dir, files, opts)
	} else if len(paths) > 0 || (len(files) > 0 && len(paths) > 0) {
		allTargets := append([]string(nil), files...)
		allTargets = append(allTargets, paths...)
		if *dir != "" {
			for i, p := range allTargets {
				if !filepath.IsAbs(p) {
					allTargets[i] = filepath.Join(*dir, p)
				}
			}
		}
		res, err = check.CheckSpelling(allTargets, opts)
	} else {
		res, err = check.CheckSpellingDir(*dir, opts)
	}
	if err != nil {
		return refuse(stderr, " spelling", oneline.Err(err))
	}

	if *write {
		for _, f := range res.Findings {
			fmt.Fprintf(stdout, "SPELLING FIXED %s:%d:%d: %s -> %s\n",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement))
		}
		fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=%d written=%d\n",
			res.FilesScanned, len(res.Findings), res.Corrected)
		return 0
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
