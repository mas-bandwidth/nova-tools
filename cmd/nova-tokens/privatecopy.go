package main

// The private copy a run that writes nothing reads OpenCode from: made under --scratch for
// the run, removed before it returns. These are the only removals in this tool, and each
// removes an entry the run itself made (TestNothingInThisToolRemovesAFile allows this file
// and no other).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// copyNote prints each private copy a read could not remove, as a NOTE on stderr: the run
// meant to leave --scratch as it was, and says where it did not.
func copyNote(s *sink, token string, notes []string) {
	for _, n := range notes {
		fmt.Fprintf(s.err(), "%s NOTE %s\n", oneline.Field(token), oneline.Escape(n))
		s.note(n)
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
