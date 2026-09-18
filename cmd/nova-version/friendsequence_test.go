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

// THE SEQUENCE A FRIEND RUNS THROUGH THIS TOOL: snapshot the binaries in a bin
// directory to record what they report, then report on the hand-written
// manifest of installed commands. The stubs are shell scripts, so this file is
// unix-only.
func TestFriendSequenceSnapshotReport(t *testing.T) {
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
	stub("nova-bus", "nova-bus v0.15.0 darwin/arm64 go1.26.0")
	stub("nova-check", "nova-check v0.15.0 darwin/arm64 go1.26.0")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manifest := filepath.Join(dir, "versions.tsv")
	body := update.Header + "\n" +
		"nova-bus\ttool\tnova-bus version\t-\t-\trowan\n" +
		"nova-check\ttool\tnova-check version\t-\t-\trowan\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := filepath.Join(dir, "snapshot.tsv")
	var out, errs bytes.Buffer
	if code := update.Main("nova-version", []string{"snapshot", "--bin", bin, "--out", snap}, "", &out, &errs); code != 0 {
		t.Fatalf("snapshot: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "SNAPSHOT OK bin=") || !strings.Contains(out.String(), "tools=2") {
		t.Fatalf("snapshot did not record both binaries:\n%s", out.String())
	}

	out.Reset()
	errs.Reset()
	// Generous probe and run deadlines: the report shells out to every adopted
	// tool, and the default five-second probe timeout asserts the machine's
	// load under a shared runner rather than the manifest's contents.
	if code := update.Main("nova-version", []string{"report", "--file", manifest, "--timeout", "30s", "--budget", "60s"}, "", &out, &errs); code != 0 {
		t.Fatalf("report on the adopted manifest: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "REPORT OK checked=2 known=2 unknown=0") {
		t.Fatalf("report did not read the adopted manifest:\n%s", out.String())
	}
	for _, tool := range []string{"nova-bus", "nova-check"} {
		if !strings.Contains(out.String(), "REPORT TOOL name="+tool) {
			t.Fatalf("report did not name %s:\n%s", tool, out.String())
		}
	}
}
