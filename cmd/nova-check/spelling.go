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

// spellingVerb prints its own lines (Prints): the SPELLING FIX line of a
// planned correction, the SPELLING FIXED line of a written one, the
// SPELLING FAILED line of a misspelling found, and the OK or FAILED line that
// closes each run. They carry a file:line:column and an original -> replacement
// the skeleton's typed rows do not render. It takes --json itself, through the
// same facts and rows the lines carry.
func spellingVerb() tool.Verb {
	return tool.Verb{
		Name:      "spelling",
		Usage:     "spelling (--dir <dir> | --file <path> | --path <pattern>) [--ignore <word|@file>] [--write] [--exclude <prefix>] [--max <n>] [--dry-run]",
		Effect:    tool.Effect("local write: --write edits the files in place (--dry-run, or no --write, writes nothing)"),
		Detail:    "Checks markdown or prose for misspellings against a list of common misspellings; it is not a dictionary; fenced code blocks and inline code spans are blanked so code is not prose; --write fixes misspellings in place.",
		ExitTable: exitCodes,
		DryRun:    true,
		Flags:     spellingFlags,
		Run:       spelling,
	}
}

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
	aliasNote(c)
	dir, write, maxFlag := c.Str("dir"), c.Bool("write"), c.Int("max")
	dryRun := c.DryRun() // with --write, print the corrections it would make and write nothing
	files, paths := c.Get("file").([]string), c.Get("path").([]string)
	ignore, exclude := c.Get("ignore").([]string), c.Get("exclude").([]string)
	stdout, stderr := c.Stdout, c.Stderr
	asJSON := c.Bool("json")

	if dir == "" && len(files) == 0 && len(paths) == 0 {
		return tool.Refuse("give at least one of --dir, --file, or --path; refusing to guess")
	}

	// Validate ignore flags before proceeding.
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

	opts := check.SpellingOptions{
		Ignore:  ignore,
		Write:   write,
		Plan:    dryRun, // the plan of the write: every check it makes, nothing written
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
		return tool.Refuse(oneline.Err(err))
	}

	// One verdict for both renderings: what was asked (a check, a write, or a
	// write planned by --dry-run) decides the status, the exit, the facts and
	// the listing's bound; the line form and the JSON print the same value.
	v := spellingVerdictOf(res, write, dryRun, maxFlag)
	if asJSON {
		o := spellingJSON(root, res, v)
		o.Render(stdout, true)
		return tool.Exit(v.exit)
	}
	if v.planned {
		list := bounded.Capped(stdout, v.max, "SPELLING", "misspelling", tool.MaxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FIX %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=%d written=0 dry_run=true\n", res.FilesScanned, len(res.Findings))
		return tool.Exit(v.exit)
	}
	if write {
		for _, f := range res.Findings {
			fmt.Fprintf(stdout, "SPELLING FIXED %s:%d:%d: %s -> %s\n",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement))
		}
		fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=%d written=%d\n",
			res.FilesScanned, len(res.Findings), res.Corrected)
		return tool.Exit(v.exit)
	}

	if len(res.Findings) > 0 {
		list := bounded.Capped(stderr, maxFlag, "SPELLING", "misspelling", tool.MaxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("SPELLING FAILED %s:%d:%d: %s -> %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Original), oneline.Escape(f.Replacement)))
		}
		list.More()
		fmt.Fprintf(stderr, "SPELLING FAILED files=%d misspellings=%d shown=%d\n",
			res.FilesScanned, list.Total(), list.Shown())
		return tool.Exit(1)
	}

	fmt.Fprintf(stdout, "SPELLING OK files=%d misspellings=0 excluded=%d\n", res.FilesScanned, res.Excluded)
	return tool.Exit(0)
}

// spellingVerdict is one spelling run's answer, computed once and printed by
// both renderings: a check of prose says FAILED when it finds a misspelling; a
// write, real or planned by --dry-run, says OK with what it wrote (or would).
type spellingVerdict struct {
	planned bool // --write --dry-run: corrections listed, nothing written
	failed  bool
	exit    int
	max     int // the listing's bound: --max, except a real write lists every correction it made
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

// spellingJSON is the --json rendering: one Out carrying the facts and rows the
// lines carry, with its verb named here because it is rendered inside the verb,
// before the skeleton fills the field in.
func spellingJSON(dir string, res check.SpellingResult, v spellingVerdict) *tool.Out {
	written := res.Corrected
	if v.planned {
		written = 0
	}
	o := tool.Done()
	o.Verb = "spelling"
	o.Fact("dir", dir).Fact("files", res.FilesScanned).Fact("misspellings", len(res.Findings)).Fact("written", written).Fact("excluded", res.Excluded)
	if v.planned {
		o.Fact("dry_run", true)
	}
	for _, f := range res.Findings {
		o.Item("misspelling", "file", f.File, "line", f.Line, "column", f.Column, "original", f.Original, "replacement", f.Replacement)
	}
	o.Cap(v.max)
	if v.failed {
		o.Status = tool.Failed
	}
	o.Exit = v.exit
	return o
}
