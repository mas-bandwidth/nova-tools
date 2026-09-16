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

// THE SEQUENCE A FRIEND RUNS THROUGH THIS TOOL: snapshot the manifest to count the
// tools they have adopted, then report on that same hand-written --file. Both verbs
// read the one manifest; snapshot counts it, report runs each installed command and
// carries the version forward. The stubs are shell scripts, so this file is unix-only.
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
	stub("nova-bus", "nova-bus v0.15.0 darwin/arm64")
	stub("nova-check", "nova-check 1.2.3")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manifest := filepath.Join(dir, "versions.tsv")
	body := update.Header + "\n" +
		"nova-bus\ttool\tnova-bus version\t-\t-\trowan\n" +
		"nova-check\ttool\tnova-check version\t-\t-\trowan\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	if code := update.Main("nova-version", []string{"snapshot", "--file", manifest}, "", &out, &errs); code != 0 {
		t.Fatalf("snapshot: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "SNAPSHOT OK known=2") {
		t.Fatalf("snapshot did not count both adopted tools:\n%s", out.String())
	}

	out.Reset()
	errs.Reset()
	if code := update.Main("nova-version", []string{"report", "--file", manifest}, "", &out, &errs); code != 0 {
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
