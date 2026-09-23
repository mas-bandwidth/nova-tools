// Package sprinttable is the sprint table's restart behaviour: publish a
// finished render, and leave the previous table untouched until one is ready.
//
// A launchd kickstart -k of the sprint-table unit used to kill the refresh
// that was still in the unit's process group, then the next start painted an
// empty table over the last good one and left it there. Publish never opens
// the published path until the next render is non-empty, so a second start
// with the refresh still running keeps the bytes already on disk.
package sprinttable

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// ReasonRefreshNotReady is why a start kept the previous table: the
	// refresh that would replace it has not finished.
	ReasonRefreshNotReady = "refresh-not-ready"
	// ReasonEmptyRender is why a start kept the previous table: the render
	// that arrived was empty, and an empty table is a blank screen.
	ReasonEmptyRender = "empty-render"
)

// Result is one publish. Body is what a reader should see. Wrote is true
// only when Out was replaced with a finished render. Kept is true when Out
// was not opened for write.
type Result struct {
	Body   []byte
	Wrote  bool
	Kept   bool
	Reason string
}

// Publish replaces out with next only when ready is true and next has a
// non-whitespace byte. Otherwise it reads out, if it exists, and returns
// those bytes without creating or truncating the file. An empty out path
// publishes nowhere: a ready next is returned as Body, and an unready call
// returns an empty Body.
func Publish(out string, next []byte, ready bool) (Result, error) {
	if ready && len(bytes.TrimSpace(next)) > 0 {
		if out == "" {
			return Result{Body: next}, nil
		}
		if err := writeAtomic(out, next); err != nil {
			return Result{}, err
		}
		return Result{Body: next, Wrote: true}, nil
	}
	reason := ReasonRefreshNotReady
	if ready {
		reason = ReasonEmptyRender
	}
	prev, err := readPrevious(out)
	if err != nil {
		return Result{}, err
	}
	return Result{Body: prev, Kept: true, Reason: reason}, nil
}

func readPrevious(out string) ([]byte, error) {
	if out == "" {
		return nil, nil
	}
	body, err := os.ReadFile(out)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sprint table: %w", err)
	}
	return body, nil
}

// writeAtomic lands body at path by renaming a complete temp file in the
// same directory. The published path is not opened until that rename, so a
// reader never sees a truncated table and a crash mid-write leaves the
// previous file in place.
func writeAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("sprint table: %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("sprint table: %s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".sprint-table-*")
	if err != nil {
		return fmt.Errorf("sprint table: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sprint table: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sprint table: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("sprint table: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("sprint table: %w", err)
	}
	return nil
}
