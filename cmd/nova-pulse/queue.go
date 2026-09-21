package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// cmdQueue checks the card queue for dependency cycles and queue invariants.
// Any cycle in dependencies is REFUSED at load / admission with exit code 2
// and an explicit diagnostic: CYCLE REFUSED: card-a -> card-b -> card-a.
func cmdQueue(args []string, stdout, stderr io.Writer) int {
	f := newFlags("queue")
	queueDir := f.fs.String("queue", "", "")
	readyDir := f.fs.String("ready", "", "")
	dir := f.fs.String("dir", "", "")
	if !f.parseAny(args, stderr) {
		return 2
	}

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
	if targetDir == "" && f.fs.NArg() > 0 {
		targetDir = f.fs.Arg(0)
	}
	if targetDir == "" {
		fmt.Fprintf(stderr, "nova-pulse queue: missing --queue or --ready directory\n")
		return 2
	}

	if err := pulse.CheckDirCycles(targetDir); err != nil {
		var cycleErr *pulse.CycleError
		if errors.As(err, &cycleErr) {
			fmt.Fprintln(stderr, cycleErr.Error())
			return 2
		}
		fmt.Fprintf(stderr, "QUEUE ERROR: %s\n", err)
		return 2
	}

	fmt.Fprintf(stdout, "QUEUE OK: %s\n", targetDir)
	return 0
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
