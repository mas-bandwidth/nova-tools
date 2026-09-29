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

func cmdExecDeadline(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("exec-deadline", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory tree to scan for exec.CommandContext calls")
	var files repeatable
	fs.Var(&files, "file", "one Go file to scan, narrowing the check (repeatable; --dir is resolution root if given)")
	var exclude repeatable
	fs.Var(&exclude, "exclude", "path prefix not scanned (repeatable; empty by default)")
	strict := fs.Bool("strict", false, "flag context parameters without local WithTimeout/WithDeadline or Deadline check")
	includeTests := fs.Bool("include-tests", false, "scan _test.go files when walking a directory (default false)")
	failMax := addFailMax(fs)

	if !parseFlags(fs, args, stderr) {
		return 2
	}
	if !checkFailMax(fs, *failMax, stderr) {
		return 2
	}

	if *dir == "" && len(files) == 0 {
		refuse(stderr, " exec-deadline", "give at least one of --dir or --file; refusing to guess")
		return 2
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

	opts := check.ExecDeadlineOptions{
		Exclude:      exclude,
		IncludeTests: *includeTests,
		Strict:       *strict,
	}

	var (
		res check.ExecDeadlineResult
		err error
	)

	if len(files) > 0 {
		absFiles := make([]string, len(files))
		for i, f := range files {
			if !filepath.IsAbs(f) {
				absFiles[i] = filepath.Join(root, f)
			} else {
				absFiles[i] = filepath.Clean(f)
			}
		}
		res, err = check.CheckExecDeadlineFiles(root, absFiles, opts)
	} else {
		res, err = check.CheckExecDeadlineDir(root, opts)
	}

	if err != nil {
		return refuse(stderr, " exec-deadline", oneline.Err(err))
	}

	if len(res.Findings) > 0 {
		list := bounded.Capped(stderr, *failMax, "EXEC-DEADLINE", "violation", failMaxRemedy)
		for _, f := range res.Findings {
			list.Line(fmt.Sprintf("EXEC-DEADLINE FAIL %s:%d:%d: %s",
				oneline.Escape(f.File), f.Line, f.Column, oneline.Escape(f.Detail)))
		}
		list.More()
		fmt.Fprintf(stderr, "EXEC-DEADLINE FAIL files=%d exec-calls=%d violations=%d shown=%d excluded=%d\n",
			res.FilesScanned, res.ExecCalls, list.Total(), list.Shown(), res.Excluded)
		return 1
	}

	fmt.Fprintf(stdout, "EXEC-DEADLINE OK files=%d exec-calls=%d excluded=%d\n",
		res.FilesScanned, res.ExecCalls, res.Excluded)
	return 0
}
