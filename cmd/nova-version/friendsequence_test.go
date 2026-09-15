//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// THE SEQUENCE A FRIEND RUNS THROUGH THIS TOOL, in the order they run it: snapshot the
// tools they have installed, then report on the manifest snapshot just wrote. Both verbs
// pass their own tests -- snapshot writes the file it says it writes
// (TestSnapshotWritesRuleTwoManifest), report reads the manifests in testdata -- and the
// sequence is still broken, because snapshot writes `kind=binary` and report's manifest
// parser accepts only the five curated kinds:
//
//	REPORT REFUSED: <path>: line 2: unknown kind binary (use harness,engine,model,tool,pin)
//
// exit 2, on the file this tool wrote one command earlier. That is the whole of the
// dogfood finding, and it is the shape no single-verb test can see: the two verbs are
// each right about themselves and disagree about the file between them.
//
// The stubs are shell scripts, so this file is unix-only, as the fixture in
// internal/update's own snapshot test is.
func TestFriendSequenceSnapshotReport(t *testing.T) {
	t.Skip("red on main: snapshot writes kind=binary and report refuses it as an unknown kind (nova-tools#571); un-skip with the PR that fixes it")

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, line string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("nova-bus", "nova-bus v0.15.0 darwin/arm64")
	stub("nova-check", "nova-check 1.2.3")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manifest := filepath.Join(dir, "versions.tsv")
	var out, errs bytes.Buffer
	if code := update.Main("nova-version", []string{"snapshot", "--bin", bin, "--out", manifest, "--owner", "rowan"}, "", &out, &errs); code != 0 {
		t.Fatalf("snapshot: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "SNAPSHOT OK tools=2 unreadable=0") {
		t.Fatalf("snapshot did not write both stubs:\n%s", out.String())
	}

	out.Reset()
	errs.Reset()
	if code := update.Main("nova-version", []string{"report", "--file", manifest}, "", &out, &errs); code != 0 {
		t.Fatalf("report on the manifest snapshot just wrote: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "REPORT OK checked=2 known=2 unknown=0") {
		t.Fatalf("report did not read the file snapshot wrote:\n%s", out.String())
	}
	for _, tool := range []string{"nova-bus", "nova-check"} {
		if !strings.Contains(out.String(), "REPORT TOOL name="+tool) {
			t.Fatalf("report did not name %s:\n%s", tool, out.String())
		}
	}
}
