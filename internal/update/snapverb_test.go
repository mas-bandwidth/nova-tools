package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotWritesRuleTwoManifest(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, line string) {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	stub("nova-bus", "nova-bus v0.12.1-0.20260912-0459069+dirty darwin/arm64")
	stub("nova-check", "v7.8.9")
	stub("nova-wake", "nova-wake 2.3.4")
	stub("nova-bad", "hello world")
	if err := os.WriteFile(filepath.Join(bin, "README"), []byte("not a tool"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.tsv")
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out, "--owner", "rowan"}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK tools=3 unreadable=1 out="+field(out))
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// kind is `tool`, one of the five curated kinds, because this file is read by
	// `nova-version report` and `binary` was refused there (#571).
	want := Header + "\n" +
		"nova-bus\ttool\t0.12.1-0.20260912-0459069+dirty\t-\t-\trowan\n" +
		"nova-check\ttool\t7.8.9\t-\t-\trowan\n" +
		"nova-wake\ttool\t2.3.4\t-\t-\trowan\n"
	if string(b) != want {
		t.Fatalf("manifest mismatch:\ngot:\n%s\nwant:\n%s", b, want)
	}

	// Default owner is "-", never guessed.
	o.Reset()
	code = Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	b, _ = os.ReadFile(out)
	if !strings.Contains(string(b), "nova-check\ttool\t7.8.9\t-\t-\t-\n") {
		t.Fatalf("default owner not '-':\n%s", b)
	}
}

// THE ROUND TRIP, which is the whole point of the verb: the manifest snapshot writes is
// the manifest report reads. Both verbs passed their own tests on 2026-09-15 while the
// sequence between them was broken three fields deep -- `kind=binary` against the five
// curated kinds, an installed column holding a version where report wanted the argv to run,
// and `latest=-` against `<scheme>:<locator>` (#571). Red before the three fixes: REPORT
// REFUSED at exit 2, on the file this tool wrote one command earlier.
func TestSnapshotThenReportRoundTrips(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	for name, line := range map[string]string{
		"nova-bus":   "nova-bus v0.15.0 darwin/arm64",
		"nova-check": "nova-check 1.2.3",
	} {
		if err := os.WriteFile(filepath.Join(bin, name),
			[]byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "versions.tsv")
	var o, e bytes.Buffer
	if code := Run("nova-version", []string{"snapshot", "--bin", bin, "--out", out, "--owner", "rowan"}, "", &o, &e, Environment{}); code != 0 {
		t.Fatalf("snapshot: exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK tools=2 unreadable=0 out="+field(out))

	o.Reset()
	e.Reset()
	if code := Run("nova-version", []string{"report", "--file", out}, "", &o, &e, Environment{}); code != 0 {
		t.Fatalf("report on the manifest snapshot just wrote: exit %d\nstdout: %s\nstderr: %s", code, o.String(), e.String())
	}
	need(t, o.String(), "REPORT OK checked=2 known=2 unknown=0")
	// The version each tool printed, carried through the file rather than read again: the
	// snapshot is a reading taken at a moment, and report says what that reading was.
	need(t, o.String(), "REPORT TOOL name=nova-bus kind=tool version=0.15.0")
	need(t, o.String(), "REPORT TOOL name=nova-check kind=tool version=1.2.3")
}
