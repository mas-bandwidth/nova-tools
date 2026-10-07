package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// seatWatchEvery is how often seat watch looks at the folder. It is the
// interval the old shell monitor slept.
const seatWatchEvery = 5 * time.Second

func init() {
	verbClasses["seat watch"] = classRead
	notServed = append(notServed, "seat watch")
	verbExit["seat watch"] = "exit codes: 0 interrupted, 1 the folder is not a directory or is not readable, 2 usage"
	verbEffect["seat watch"] = "inspection: prints each new file's path in <dir>, one flushed line, and runs no shell; writes nothing"
}

// cmdSeatWatch is nova-sprint seat watch <dir>. Each new file is one flushed
// line, its path absolute. A file already there at the start is not printed.
// A name that starts with a dot is a file still being written, and a directory
// is not a push. It runs no shell and writes nothing. An interrupt ends it
// (exit 0). A folder that is not a directory is exit 1, one line, not a usage
// refusal: the example names a folder that is not there, and it has to finish.
func (a *app) cmdSeatWatch(args []string, stdout, stderr io.Writer) int {
	const name = "seat watch"
	fs, _ := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one word, the folder it watches, ", err, pos...))
	}
	dir, err := filepath.Abs(pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "%s is not a directory\n", pos[0])
		return 1
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		fmt.Fprintf(stderr, "%s is not a directory\n", dir)
		return 1
	}
	have, err := seatWatchNames(dir)
	if err != nil {
		fmt.Fprintf(stderr, "%s is not readable\n", dir)
		return 1
	}
	seen := map[string]struct{}{}
	for _, n := range have {
		seen[n] = struct{}{}
	}
	ctx := context.Background()
	stop := func() {}
	if a.notify != nil {
		ctx, stop = a.notify(ctx)
	}
	defer stop()
	out := bufio.NewWriter(stdout)
	for {
		var tick <-chan time.Time
		if a.after != nil {
			tick = a.after(seatWatchEvery)
		} else {
			tick = time.After(seatWatchEvery)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-tick:
		}
		// A timer that is already ready and a context that is already done can
		// both be chosen. Returning here keeps the next wait from running.
		if ctx.Err() != nil {
			return 0
		}
		names, err := seatWatchNames(dir)
		if err != nil {
			fmt.Fprintf(stderr, "%s is not readable\n", dir)
			return 1
		}
		for _, n := range names {
			if _, ok := seen[n]; ok {
				continue
			}
			fmt.Fprintln(out, filepath.Join(dir, n))
			if err := out.Flush(); err != nil {
				return 1
			}
			seen[n] = struct{}{}
		}
	}
}

// seatWatchNames is the files in dir a watch prints: not a dot name, not a
// directory, sorted so two that appear together come out in one order.
func seatWatchNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if n == "" || n[0] == '.' || e.IsDir() {
			continue
		}
		names = append(names, n)
	}
	slices.Sort(names)
	return names, nil
}
