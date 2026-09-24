package main

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The coordinator's DECISION (Stella, 5648500966), point 4: rule 15's arm is derived from
// the remote's OWN default branch, not the literal "main", and the derivation is
// conservative -- a failed discovery never downgrades a lane. These drive the ACTUAL run
// path against local bare repositories.

// setDefault points the fixture remote's HEAD at name, the way a repository whose default
// branch is trunk or master answers `git ls-remote --symref`.
func (l *lab) setDefault(name string) {
	l.t.Helper()
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/"+name)
	l.git(l.remote, "symbolic-ref", "HEAD", "refs/heads/"+name)
}

// initArgs is init on the given base with any extra flags (--default-branch, --hosted-red).
func (l *lab) initArgs(base string, extra ...string) {
	l.t.Helper()
	args := []string{"init", "--lane", l.lane, "--repo", "o/n", "--base", base, "--lane-branch", "nova-merge/lane"}
	args = append(args, extra...)
	if exit, _, errb := l.run(args...); exit != 0 {
		l.t.Fatalf("init: exit %d\n%s", exit, errb)
	}
}

// failLSRemote is a runner that answers an error to `git ls-remote` and passes every other
// command through, so a test can make discovery fail while the fold, the fetch and the
// push all still work.
type failLSRemote struct{ inner merge.Runner }

func (r failLSRemote) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" && len(args) > 0 && args[0] == "ls-remote" {
		return "", fmt.Errorf("the remote did not answer")
	}
	return r.inner.Run(ctx, dir, name, args...)
}
