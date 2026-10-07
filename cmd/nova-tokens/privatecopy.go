package main

// The private copy a run that writes nothing reads OpenCode from: made under --scratch for
// the run, removed before it returns. These are the only removals in this tool, and each
// removes an entry the run itself made (TestNothingInThisToolRemovesAFile allows this file
// and no other).

import (
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// copyNotes adds each private copy note a read could not remove to o.
func copyNotes(o *tool.Out, notes []string) {
	for _, n := range notes {
		o.Note(n)
	}
}

// removePrivateCopy removes the directory a private read made under --scratch: the
// opencode-<label> directories in it, the copies and whatever sqlite3 left beside them,
// one entry at a time, then the directory itself. It removes nothing it did not make: the
// directory is new to this run and holds one level of copy directories.
func removePrivateCopy(dir string) error {
	labels, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, l := range labels {
		sub := filepath.Join(dir, l.Name())
		if l.IsDir() {
			files, err := os.ReadDir(sub)
			if err != nil {
				return err
			}
			for _, f := range files {
				if err := os.Remove(filepath.Join(sub, f.Name())); err != nil {
					return err
				}
			}
		}
		if err := os.Remove(sub); err != nil {
			return err
		}
	}
	return os.Remove(dir)
}
