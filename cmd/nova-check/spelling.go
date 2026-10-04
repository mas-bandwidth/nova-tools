package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func spellingFlags(f *tool.Flags) {
	f.Prints()
	f.Bool("json", false, "print typed findings and totals as one JSON object")
	f.String("dir", "", "directory tree to scan for misspellings")
	f.Var(&repeatable{}, "file", "one file to check, narrowing the check to just these (repeatable)")
	f.Var(&repeatable{}, "path", "file or glob pattern to check (repeatable)")
	f.Var(&repeatable{}, "ignore", "allowlisted word or @file (repeatable, or comma-separated)")
	f.Bool("write", false, "apply spelling corrections to files in place")
	f.Var(&repeatable{}, "exclude", "path prefix not scanned (repeatable; empty by default)")
	addMax(f)
}

func spelling(c *tool.Call) *tool.Out {
	dryRun := c.DryRun()
	dir := c.Str("dir")
	files := c.Get("file").([]string)
	paths := c.Get("path").([]string)
	ignore := c.Get("ignore").([]string)
	write := c.Bool("write")
	exclude := c.Get("exclude").([]string)
	maxFlag := c.Int("max")

	if dir == "" && len(files) == 0 && len(paths) == 0 {
		return tool.Refuse("give at least one of --dir, --file, or --path; refusing to guess")
	}
	if _, err := check.ParseIgnoreSpec(ignore); err != nil {
		return tool.Refuse(oneline.Err(err))
	}

	root := dir
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

	opts := check.SpellingOptions{Ignore: ignore, Write: write, Plan: dryRun, Exclude: exclude, Dir: root}

	var (
		res check.SpellingResult
		err error
	)
	switch {
	case len(files) > 0 && len(paths) == 0:
		absFiles := make([]string, len(files))
		for i, f := range files {
			if !filepath.IsAbs(f) {
				absFiles[i] = filepath.Join(root, f)
			} else {
				absFiles[i] = filepath.Clean(f)
			}
		}
		res, err = check.CheckSpellingFiles(root, absFiles, opts)
	case len(paths) > 0:
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
	default:
		res, err = check.CheckSpellingDir(root, opts)
	}
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}

	v := spellingVerdictOf(res, write, dryRun, maxFlag)
	if c.Bool("json") {
		o := spellingJSON(root, res, v)
		o.Render(c.Stdout, true)
		return tool.Exit(v.exit)
	}

	if v.planned {
		list := bounded.Capped(c.Stdout, v.max, "SPELLING", "misspelling", tool.MaxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FIX %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(c.Stdout, "SPELLING OK files=%d misspellings=%d written=0 dry_run=true\n", res.FilesScanned, len(res.Findings))
		return tool.Exit(v.exit)
	}
	if write {
		for _, f := range res.Findings {
			fmt.Fprintf(c.Stdout, "SPELLING FIXED %s:%d:%d: %s -> %s\n",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement))
		}
		fmt.Fprintf(c.Stdout, "SPELLING OK files=%d misspellings=%d written=%d\n",
			res.FilesScanned, len(res.Findings), res.Corrected)
		return tool.Exit(v.exit)
	}

	if len(res.Findings) > 0 {
		list := bounded.Capped(c.Stderr, maxFlag, "SPELLING", "misspelling", tool.MaxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FAILED %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(c.Stderr, "SPELLING FAILED files=%d misspellings=%d shown=%d\n",
			res.FilesScanned, list.Total(), list.Shown())
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "SPELLING OK files=%d misspellings=0 excluded=%d\n", res.FilesScanned, res.Excluded)
	return tool.Exit(0)
}

// spellingVerdict is one spelling run's answer, computed once and printed by
// both renderings.
type spellingVerdict struct {
	planned bool
	failed  bool
	exit    int
	max     int
}

func spellingVerdictOf(res check.SpellingResult, write, dryRun bool, maxFlag int) spellingVerdict {
	v := spellingVerdict{planned: write && dryRun, max: maxFlag}
	if write && !dryRun {
		v.max = 0
	}
	if !write && len(res.Findings) > 0 {
		v.failed, v.exit = true, 1
	}
	return v
}

// spellingJSON is the --json rendering, one Out carrying the same facts.
func spellingJSON(dir string, res check.SpellingResult, v spellingVerdict) *tool.Out {
	written := res.Corrected
	if v.planned {
		written = 0
	}
	o := tool.Done()
	o.Fact("dir", dir).Fact("files", res.FilesScanned).Fact("misspellings", len(res.Findings)).Fact("written", written).Fact("excluded", res.Excluded)
	if v.planned {
		o.Fact("dry_run", true)
	}
	for _, f := range res.Findings {
		o.Item("misspelling", "file", f.File, "line", f.Line, "column", f.Column, "original", f.Original, "replacement", f.Replacement)
	}
	if v.max > 0 {
		o.Cap(v.max)
	}
	if v.failed {
		o.Status = tool.Failed
	}
	o.Exit = v.exit
	return o
}
