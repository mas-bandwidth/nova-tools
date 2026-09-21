package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// cmdLint checks cards for dependency cycles and structural defects.
// Any cycle in dependencies is REFUSED at load / admission with exit code 2
// and an explicit diagnostic: CYCLE REFUSED: card-a -> card-b -> card-a.
func cmdLint(args []string, stdout, stderr io.Writer) int {
	f := newFlags("lint")
	cardPath := f.fs.String("card", "", "")
	readyDir := f.fs.String("ready", "", "")
	queueDir := f.fs.String("queue", "", "")
	dir := f.fs.String("dir", "", "")
	if !f.parseAny(args, stderr) {
		return 2
	}

	var cardPaths []string

	targetDir := *readyDir
	if targetDir == "" && *queueDir != "" {
		readySub := filepath.Join(*queueDir, "ready")
		if isDir(readySub) {
			targetDir = readySub
		} else {
			targetDir = *queueDir
		}
	}
	if targetDir == "" && *dir != "" {
		targetDir = *dir
	}

	if targetDir != "" {
		matches, _ := filepath.Glob(filepath.Join(targetDir, "card-*.md"))
		if len(matches) == 0 {
			matches, _ = filepath.Glob(filepath.Join(targetDir, "*.md"))
		}
		cardPaths = append(cardPaths, matches...)
	}

	if *cardPath != "" {
		cardPaths = append(cardPaths, *cardPath)
		if targetDir == "" {
			parent := filepath.Dir(*cardPath)
			matches, _ := filepath.Glob(filepath.Join(parent, "card-*.md"))
			if len(matches) == 0 {
				matches, _ = filepath.Glob(filepath.Join(parent, "*.md"))
			}
			cardPaths = append(cardPaths, matches...)
		}
	}

	for _, arg := range f.fs.Args() {
		if isDir(arg) {
			matches, _ := filepath.Glob(filepath.Join(arg, "card-*.md"))
			if len(matches) == 0 {
				matches, _ = filepath.Glob(filepath.Join(arg, "*.md"))
			}
			cardPaths = append(cardPaths, matches...)
		} else if strings.HasSuffix(arg, ".md") {
			cardPaths = append(cardPaths, arg)
		}
	}

	if len(cardPaths) == 0 {
		fmt.Fprintf(stderr, "nova-pulse lint: missing --card <file> or --ready <dir>\n")
		return 2
	}

	seen := make(map[string]bool)
	var uniquePaths []string
	for _, p := range cardPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			uniquePaths = append(uniquePaths, p)
		}
	}

	if err := pulse.CheckCardCycles(uniquePaths); err != nil {
		var cycleErr *pulse.CycleError
		if errors.As(err, &cycleErr) {
			fmt.Fprintln(stderr, cycleErr.Error())
			return 2
		}
		fmt.Fprintf(stderr, "LINT ERROR: %s\n", err)
		return 2
	}

	fmt.Fprintf(stdout, "LINT OK: %d cards checked, no dependency cycles\n", len(uniquePaths))
	return 0
}
