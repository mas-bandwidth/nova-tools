package main

import (
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// F6: add re-reads the state after its update for ADD OK's counts, and that re-read's
// error was assigned to `_`, so a nil state was dereferenced a few lines later. The
// re-read now keeps its error: a re-read that fails is ADD REFUSED and exit 2, never a
// panic. reloadLane is the seam that makes the re-read fail while openLane's first read
// still succeeded -- exactly the window the old code panicked in.
func TestAddRefusesWhenTheRereadFails(t *testing.T) {
	l := newLab(t)
	l.init("main")

	old := reloadLane
	defer func() { reloadLane = old }()
	reloadLane = func(string) (*merge.State, error) {
		return nil, errors.New("the state could not be re-read")
	}

	exit, stdout, stderr := l.run("add", "--lane", l.lane, "--pr", "1")
	if exit != 2 {
		t.Fatalf("a re-read that fails must refuse with exit 2, got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "ADD REFUSED")
	contains(t, stderr, "the state could not be re-read")
	// The entry is queued and the failure is the counts, so the refusal never claims ADD OK.
	absent(t, stdout, "ADD OK")
}
