package main

import (
	"os"
	"path/filepath"
)

// Demanded test 22, the fold's half: A RECORD FILE THAT DOES NOT DECODE IS NEVER SKIPPED.
// The unreadable file may be the hold or the newer red, so its entry is blocked for the
// pass whatever its other records say, and a file whose path names no entry stops the
// pass before any entry is read.

// putRecord writes a file straight into the lane branch at the remote, the way another
// machine's lane would, and returns nothing: what it proves is what the coordinator's
// next pull folds.
func (l *lab) putRecord(path, body string) {
	l.t.Helper()
	side := filepath.Join(l.dir, "side")
	if _, err := os.Stat(side); os.IsNotExist(err) {
		l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	}
	l.git(side, "fetch", "-q", "origin", "nova-merge/lane")
	l.git(side, "reset", "-q", "--hard", "FETCH_HEAD")
	full := filepath.Join(side, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	l.git(side, "add", "--", path)
	l.git(side, "-c", "user.name=other", "-c", "user.email=other@localhost", "commit", "-q", "-m", "a record from another machine")
	l.git(side, "push", "-q", "origin", "HEAD:refs/heads/nova-merge/lane")
}
