package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Work list 10 puts the no-/tmp, no-process-scan tripwire in cmd/nova-merge/*_test.go and
// it existed only for internal/merge -- so THIS package, which writes `stop`, `.gitignore`
// and every lane path a flag names, was unscanned. The subject of this test is the source,
// which is the one reason anything here reaches outside t.TempDir(), and it is stated.

func mainPackageSource(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	if len(out) == 0 {
		t.Fatal("no source files found; this test was looking in the wrong place and would have passed by checking nothing")
	}
	return out
}

// Rule 13: nothing under /tmp, and the tool never matches a process by its own command
// line. Every path this binary writes is under --lane, which a person gave it.
func TestTheBinaryReachesNoTmpAndNoProcessTable(t *testing.T) {
	t.Parallel()
	for name, src := range mainPackageSource(t) {
		for _, forbidden := range []string{`"/tmp`, "os.TempDir", "pgrep", `"ps"`, "/proc/", "TMPDIR"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s carries %q; every path this tool writes is under --lane, and a loop that matches a process by its own command line matches itself (19 orphaned shells, 2026-09-09)", name, forbidden)
			}
		}
	}
}

// Rule 7, from this side: the binary writes the lane's own files and nothing in a clone's
// work tree. A new writing site here is a decision rather than a drive-by.
func TestTheBinaryWritesOnlyTheLanesOwnFiles(t *testing.T) {
	t.Parallel()
	allowed := map[string]string{
		"verbs.go": "the lane branch's .gitignore, which init writes, under --lane",
		"pass.go":  "the lane's stop file, which the stop verb writes, under --lane",
	}
	for name, src := range mainPackageSource(t) {
		for i, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "os.WriteFile") && !strings.Contains(line, "os.OpenFile") && !strings.Contains(line, "os.Create") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if _, ok := allowed[name]; !ok {
				t.Errorf("%s:%d writes a file: %s\nthe lane never edits an entry's content, and a new writing site is a decision", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
