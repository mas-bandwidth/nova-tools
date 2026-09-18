package goenv

import (
	"slices"
	"strings"
	"testing"
)

// The bug this package exists for: GOFLAGS=-json in the parent turns an inner
// `go test` into a JSON stream, and a parser reading `--- PASS:` lines sees a
// green run as red.
func TestCleanDropsGOFLAGS(t *testing.T) {
	got := Clean([]string{"PATH=/usr/bin", "GOFLAGS=-json", "HOME=/home/rowan"})
	want := []string{"PATH=/usr/bin", "HOME=/home/rowan"}
	if !slices.Equal(got, want) {
		t.Fatalf("Clean = %q, want %q", got, want)
	}
}

func TestCleanDropsTheWholeDocumentedList(t *testing.T) {
	for _, entry := range []string{
		"GOFLAGS=-json",
		"GOFLAGS=-count=1 -json -race",
		"GOFLAGS=-mod=vendor",
		"GOTESTFLAGS=-v",
		"GOTESTSUM_FORMAT=standard-verbose",
		"GOCOVERFLAGS=--json",
		"GORUNFLAGS=-json=on",
	} {
		if got := Clean([]string{entry}); len(got) != 0 {
			t.Errorf("Clean kept %q", entry)
		}
	}
}

// Everything else survives, in the order it came in. A GOTMPDIR a bench set is
// a location, not an output shape, and a tool that wants its own appends it
// after Clean.
func TestCleanKeepsEverythingElseInOrder(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GOTMPDIR=/scratch",
		"GOCACHE=/cache",
		"GOPATH=/home/rowan/go",
		"GOOS=darwin",
		"MY_ARGS=--json", // not a GO variable: the tool's own, left alone
		"NOEQUALSIGN",
	}
	got := Clean(env)
	if !slices.Equal(got, env) {
		t.Fatalf("Clean = %q, want the input unchanged", got)
	}
}

// Windows environment names are case-insensitive, and os.Environ there hands
// them back however they were written.
func TestCleanFoldsTheName(t *testing.T) {
	if got := Clean([]string{"GoFlags=-json"}); len(got) != 0 {
		t.Fatalf("Clean kept %q", got)
	}
}

func TestCleanDoesNotModifyItsInput(t *testing.T) {
	env := []string{"GOFLAGS=-json", "PATH=/usr/bin"}
	_ = Clean(env)
	if env[0] != "GOFLAGS=-json" || len(env) != 2 {
		t.Fatalf("the caller's env was modified: %q", env)
	}
}

// The documented list is what a reader is pointed at when the class test says
// no, so it names the variables the matcher actually drops.
func TestRemovedNamesWhatItDrops(t *testing.T) {
	for _, name := range []string{"GOFLAGS", "GOTEST", "json"} {
		if !strings.Contains(Removed, name) {
			t.Errorf("Removed does not name %s", name)
		}
	}
}
