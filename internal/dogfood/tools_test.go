package dogfood

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeHelp = `nova-fake: a thin client (see docs/SPEC-FAKE.md)

usage:
  nova-fake session start --session <path> --as <name>
  nova-fake ask           delivers ONE unit to the FRIEND who owns it
  nova-fake asks          the open asks, oldest first
  nova-fake help

example:
  nova-check links --dir ./self
`

func toolsDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a tool, and one that is not executable: neither is run.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nova-notexec"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerbsFromToolsAsksEachBinaryForItsOwnVerbs(t *testing.T) {
	dir := toolsDir(t, "nova-fake")
	var asked []string
	run := func(ctx context.Context, bin string) (string, error) {
		asked = append(asked, filepath.Base(bin))
		return fakeHelp, nil
	}
	verbs, failures, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatalf("VerbsFromTools: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures %+v", failures)
	}
	if strings.Join(asked, ",") != "nova-fake" {
		t.Fatalf("ran %v; only executable nova-* files are run", asked)
	}
	var got []string
	for _, v := range verbs {
		got = append(got, v.Key())
	}
	want := "nova-fake session start|nova-fake ask|nova-fake asks|nova-fake help"
	if strings.Join(got, "|") != want {
		t.Fatalf("verbs %q, want %q", strings.Join(got, "|"), want)
	}
}

// A binary speaks for itself: the example line in its own help naming another
// tool is not a verb of that tool's, and counting it would let one worked
// example invent verbs across the whole family.
func TestVerbsFromToolsIgnoresAnotherToolsLineInAHelpBlock(t *testing.T) {
	dir := toolsDir(t, "nova-fake")
	run := func(ctx context.Context, bin string) (string, error) { return fakeHelp, nil }
	verbs, _, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range verbs {
		if v.Tool != "nova-fake" {
			t.Fatalf("nova-fake's help declared %q for another tool", v.Key())
		}
	}
}

func TestVerbsFromToolsNamesABinaryThatCannotAnswer(t *testing.T) {
	dir := toolsDir(t, "nova-fake", "nova-broken")
	run := func(ctx context.Context, bin string) (string, error) {
		if strings.HasSuffix(bin, "nova-broken") {
			return "", os.ErrPermission
		}
		return fakeHelp, nil
	}
	verbs, failures, err := VerbsFromTools(context.Background(), dir, run, nil)
	if err != nil {
		t.Fatalf("one unreadable binary failed the whole run: %v", err)
	}
	if len(failures) != 1 || !strings.Contains(failures[0].Subject, "nova-broken") {
		t.Fatalf("failures %+v, want the one binary named", failures)
	}
	if len(verbs) == 0 {
		t.Fatal("a half-built directory cost every tool in it, not just the broken one")
	}
}

func TestVerbsFromToolsRefusesWithNoDirectoryAndStopsOnADeadline(t *testing.T) {
	if _, _, err := VerbsFromTools(context.Background(), "  ", nil, nil); err == nil {
		t.Fatal("an empty --tools was accepted; every path comes from a flag")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := toolsDir(t, "nova-fake")
	run := func(ctx context.Context, bin string) (string, error) { return fakeHelp, nil }
	if _, _, err := VerbsFromTools(ctx, dir, run, nil); err == nil {
		t.Fatal("a cancelled read ran on")
	}
}

func TestMergeVerbsLetsTheBinariesWinAndTheReferenceFillIn(t *testing.T) {
	fromTools := verbs("nova-work ask", "nova-work asks")
	fromCLI := verbs("nova-work session start", "nova-check links")
	got := MergeVerbs(fromTools, fromCLI)
	var keys []string
	for _, v := range got {
		keys = append(keys, v.Key())
	}
	want := "nova-work ask|nova-work asks|nova-check links"
	if strings.Join(keys, "|") != want {
		t.Fatalf("merged %q, want %q: a tool that answered for itself is complete", strings.Join(keys, "|"), want)
	}
}

// One end-to-end read against a real executable written here, so the way this
// invokes a binary is the way a binary is actually invoked. Local only.
func TestVerbsFromToolsAgainstARealExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + fakeHelp + "EOF\n"
	if err := os.WriteFile(filepath.Join(dir, "nova-fake"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	verbs, failures, err := VerbsFromTools(ctx, dir, nil, nil)
	if err != nil {
		t.Fatalf("VerbsFromTools: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures %+v", failures)
	}
	if len(verbs) != 4 {
		t.Fatalf("read %d verbs out of a real binary's help, want 4: %+v", len(verbs), verbs)
	}
}
