//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// THE SEQUENCE A FRIEND RUNS THROUGH THIS TOOL: snapshot the binaries in a bin
// directory to record what they report, then report on the hand-written
// manifest of installed commands. The stubs are shell scripts, so this file is
// unix-only.
func TestFriendSequenceSnapshotReport(t *testing.T) {
	t.Parallel()
	binExe := buildNovaVersion(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, line string) {
		t.Helper()
		if err := testbin.WriteExecutable(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("nova-bus", "nova-bus v0.15.0 darwin/arm64 go1.26.0")
	stub("nova-check", "nova-check v0.15.0 darwin/arm64 go1.26.0")

	manifest := filepath.Join(dir, "versions.tsv")
	body := update.Header + "\n" +
		"nova-bus\ttool\tnova-bus version\t-\t-\trowan\n" +
		"nova-check\ttool\tnova-check version\t-\t-\trowan\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := filepath.Join(dir, "snapshot.tsv")
	cmdSnap := exec.Command(binExe, "snapshot", "--bin", bin, "--out", snap)
	cmdSnap.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	outSnap, err := cmdSnap.CombinedOutput()
	if err != nil {
		t.Fatalf("snapshot: %v\noutput: %s", err, outSnap)
	}
	if !strings.Contains(string(outSnap), "SNAPSHOT OK bin=") || !strings.Contains(string(outSnap), "tools=2") {
		t.Fatalf("snapshot did not record both binaries:\n%s", outSnap)
	}

	// Generous probe and run deadlines: the report shells out to every adopted
	// tool, and the default five-second probe timeout asserts the machine's
	// load under a shared runner rather than the manifest's contents.
	cmdReport := exec.Command(binExe, "report", "--file", manifest, "--timeout", "30s", "--budget", "60s")
	cmdReport.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	outReport, err := cmdReport.CombinedOutput()
	if err != nil {
		t.Fatalf("report on the adopted manifest: %v\noutput: %s", err, outReport)
	}
	if !strings.Contains(string(outReport), "REPORT OK checked=2 known=2 unknown=0") {
		t.Fatalf("report did not read the adopted manifest:\n%s", outReport)
	}
	for _, tool := range []string{"nova-bus", "nova-check"} {
		if !strings.Contains(string(outReport), "REPORT TOOL name="+tool) {
			t.Fatalf("report did not name %s:\n%s", tool, outReport)
		}
	}
}
