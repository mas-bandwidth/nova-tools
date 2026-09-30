package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/verbout"
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
	asJSON := verbflag.JSON(fs)

	if !parseFlags(fs, args, stdout, stderr, *asJSON) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stdout, stderr, *asJSON) {
		return 2
	}

	if *dir == "" && len(files) == 0 && len(paths) == 0 {
		return refuseWith(stdout, stderr, *asJSON, " spelling", "give at least one of --dir, --file, or --path; refusing to guess")
	}

	// Validate ignore flags before proceeding.
	if _, err := check.ParseIgnoreSpec(ignore); err != nil {
		return refuseWith(stdout, stderr, *asJSON, " spelling", oneline.Err(err))
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
		Write:   *write,
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
		return refuseWith(stdout, stderr, *asJSON, " spelling", oneline.Err(err))
	}

	if *write {
		if *asJSON {
			out := verbout.OK("spelling")
			out.FactInt("files", res.FilesScanned)
			out.FactInt("misspellings", len(res.Findings))
			out.FactInt("written", res.Corrected)
			for _, f := range res.Findings {
				out.Item("FIXED", fmt.Sprintf("%s:%d:%d: %s -> %s",
					oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
			}
			return out.Emit(stdout, stderr, true)
		}
		for _, f := range res.Findings {
			fmt.Fprintf(stdout, "SPELLING FIXED %s:%d:%d: %s -> %s\n",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement))
		}
		fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=%d written=%d\n",
			res.FilesScanned, len(res.Findings), res.Corrected)
		return 0
	}

	if len(res.Findings) > 0 {
		if *asJSON {
			out := verbout.Failed("spelling", 1)
			out.StatusLast = true
			out.FactInt("files", res.FilesScanned)
			b := out.Bounded(*failMax, "misspelling", failMaxRemedy)
			for _, f := range res.Findings {
				b.Line("FAIL", fmt.Sprintf("%s:%d:%d: %s -> %s",
					oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
			}
			b.Finish()
			out.FactInt("misspellings", b.Total())
			out.FactInt("shown", b.Shown())
			return out.Emit(stdout, stderr, true)
		}
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

	out := verbout.OK("spelling")
	out.FactInt("files", res.FilesScanned)
	out.FactInt("misspellings", 0)
	out.FactInt("excluded", res.Excluded)
	if *asJSON {
		return out.Emit(stdout, stderr, true)
	}
	fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=0 excluded=%d\n", res.FilesScanned, res.Excluded)
	return 0
}
