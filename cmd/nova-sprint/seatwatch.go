package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

func init() {
	// The Monitor is an inspection of this machine, never a server request
	// (SPEC-SPRINT, "The push proof"). Register beside its implementation.
	verbClasses["seat watch"] = classRead
	verbEffect["seat watch"] = "inspection: watches this machine's directory, prints each complete file path once as it appears, writes nothing; an interrupt stops it"
	notServed = append(notServed, "seat watch")
}

// cmdSeatWatch starts the native folder Monitor (SPEC-SPRINT, "The push proof").
// Libraries considered: os.ReadDir and time.Ticker provide the small directory
// snapshot and bounded polling; no platform watcher or shell is needed.
func (a *app) cmdSeatWatch(args []string, stdout, stderr io.Writer) int {
	const name = "seat watch"
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, name, argErr("wants one directory; run: nova-sprint seat watch <dir> ", err, pos...))
	}
	dir, err := filepath.Abs(pos[0])
	if err != nil {
		return refuse(stderr, name, "the directory cannot be resolved: "+err.Error()+"; give an absolute directory")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return refuse(stderr, name, dir+" is not a directory; make it or give the folder the session watches")
	}
	ctx, stop := a.notify(context.Background())
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	wait := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			return nil
		}
	}
	if err := watchSeatFiles(ctx, dir, c.json, stdout, os.ReadDir, wait); err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s; check the folder and restart the Monitor\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	return 0
}

// watchSeatFiles emits complete regular files already present and then new ones
// (SPEC-SPRINT, "The push proof"). Dot files are unpublished atomic writes. Each
// line is written and flushed before waiting; a removed name can appear again.
func watchSeatFiles(ctx context.Context, dir string, asJSON bool, stdout io.Writer, scan func(string) ([]os.DirEntry, error), wait func(context.Context) error) error {
	seen := map[string]bool{}
	for ctx.Err() == nil {
		entries, err := scan(dir)
		if err != nil {
			return err
		}
		present := make(map[string]bool, len(entries))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || !e.Type().IsRegular() {
				continue
			}
			present[e.Name()] = true
			if seen[e.Name()] {
				continue
			}
			path := filepath.Join(dir, e.Name())
			line := oneline.Escape(path)
			if asJSON {
				b, _ := json.Marshal(map[string]string{"path": path}) // ignored: strings always encode
				line = string(b)
			}
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return err
			}
			if f, ok := stdout.(interface{ Flush() error }); ok {
				if err := f.Flush(); err != nil {
					return err
				}
			}
		}
		seen = present
		if err := wait(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	return nil
}
