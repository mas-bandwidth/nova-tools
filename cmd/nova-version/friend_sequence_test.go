package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// TestFriendSequenceSnapshotReport is the friend sequence a line runs through the
// version inventory: snapshot a directory of nova-* executables into a rule-2 manifest,
// then report reads that manifest back. The regression Glenn called out is that snapshot
// wrote kind=binary and latest=- placeholders that report's manifest loader refused as
// "unknown kind" and "invalid latest", so the second verb refused a file the first verb
// had just written. The one property asserted is that report does not refuse: it reads the
// manifest snapshot wrote.
func TestFriendSequenceSnapshotReport(t *testing.T) {
	t.Skip("pending #559 (nova-version snapshot manifest is refused by report; accept binary kind and - placeholders)")
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, line string) {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("nova-bus", "nova-bus v0.12.1-0.20260912-0459069+dirty darwin/arm64")
	stub("nova-check", "v7.8.9")

	manifest := filepath.Join(dir, "out.tsv")
	var out, errs bytes.Buffer
	if code := update.Main("nova-version", []string{"snapshot", "--bin", bin, "--out", manifest, "--owner", "rowan"}, "", &out, &errs); code != 0 {
		t.Fatalf("snapshot exit=%d err=%q", code, errs.String())
	}
	if !strings.Contains(out.String(), "SNAPSHOT OK tools=2 unreadable=0") {
		t.Fatalf("snapshot receipt %q", out.String())
	}

	var rOut, rErr bytes.Buffer
	code := update.Main("nova-version", []string{"report", "--file", manifest, "--host", "air"}, "", &rOut, &rErr)
	if code == 2 {
		t.Fatalf("report refused the manifest snapshot wrote:\n%s", rErr.String())
	}
	if !strings.Contains(rOut.String(), "REPORT at=") || !strings.Contains(rOut.String(), "entries=") {
		t.Fatalf("report did not read the manifest:\n%s\n%s", rOut.String(), rErr.String())
	}
}
