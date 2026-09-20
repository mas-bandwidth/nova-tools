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

// ISSUE #1836: a tool that runs a check -- code from the tree under test --
// through a child hands it the caller's environment after only Clean. A
// variable the coordinator holds for gh (GH_TOKEN, GITHUB_TOKEN) or any other
// secret-named variable reached that child, so a member's code could read it.
// Clean is the one place a go command's environment is built, so it drops a
// credential by NAME and covers simulate, batch and review mutate at once.
func TestCleanDropsTheCallersCredentials(t *testing.T) {
	for _, entry := range []string{
		"GH_TOKEN=a-forge-token",
		"GITHUB_TOKEN=a-forge-token",
		"FAKE_SECRET_FOR_PROBE=a-probe-that-is-not-a-real-credential",
		"DEEPSEEK_API_KEY=a-provider-key",
		"aws_secret_access_key=a-lower-case-secret",
	} {
		if got := Clean([]string{entry}); len(got) != 0 {
			t.Errorf("Clean kept the caller's credential %q; a child running a pull request's code can read it", entry)
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
	for _, name := range []string{"GOFLAGS", "GOTEST", "json", "KEY", "TOKEN", "SECRET"} {
		if !strings.Contains(Removed, name) {
			t.Errorf("Removed does not name %s", name)
		}
	}
}
