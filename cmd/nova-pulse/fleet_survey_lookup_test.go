package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LESSON 9 of docs/SPEC-RELEASE.md, found on the fourth release dogfood:
// `fleet survey` refused with `tools/bench-standard.sh not found` and said
// nothing about WHERE it had looked, so a person running it from ~ read it as
// "the script is missing" rather than as "this verb wants a nova-tools
// checkout as its working directory". A lookup that walks upwards says what it
// walked: the first directory and the last, and what it wanted to find there.
func TestTheStandardScriptLookupSaysWhereItLooked(t *testing.T) {
	// The hulk gate (16aq, 2026-09-20): TMPDIR sat under ~/nova-bench, which held
	// tools/bench-standard.sh, so this test's t.TempDir() was not a closed world.
	// The tree below is that layout, owned by this test: a stray script above the
	// scratch root must not answer, and the walk must not consult ambient ancestors.
	owned := t.TempDir()
	if err := os.MkdirAll(filepath.Join(owned, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "tools", "bench-standard.sh"), []byte("STRAY FROM THE BENCH\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(owned, "scratch")
	deep := filepath.Join(scratch, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := readBenchStandardFrom(deep, scratch)
	if err == nil {
		t.Fatal("a directory with no checkout above it answered a script")
	}
	for _, want := range []string{deep, "up to " + scratch, "tools/bench-standard.sh", "checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %s", want, err.Error())
		}
	}
}

// And it finds the script from anywhere inside a checkout, which is the whole
// reason the walk exists.
func TestTheStandardScriptLookupWalksUpToTheCheckout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "bench-standard.sh"), []byte("#!/bin/bash\necho STANDARD OK\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "internal", "pulse")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := readBenchStandardFrom(deep, root)
	if err != nil {
		t.Fatalf("the walk did not reach the checkout: %v", err)
	}
	if !strings.Contains(script, "STANDARD OK") {
		t.Fatalf("the script read back is not the one written: %q", script)
	}
}

// Production's ceiling is $HOME when the start is inside it: a stray script
// above $HOME must not answer. The 16-step walk still reached a file on the
// bench; the volume root is the same surprise one more hop up (#2057).
func TestTheStandardScriptLookupStopsAtHome(t *testing.T) {
	owned := t.TempDir()
	home := filepath.Join(owned, "home")
	if err := os.MkdirAll(filepath.Join(owned, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "tools", "bench-standard.sh"), []byte("ABOVE HOME\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(home, "tmp", "x")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := benchStandardStop(deep); got != home {
		t.Fatalf("benchStandardStop(%q) = %q, want $HOME %q", deep, got, home)
	}
	got, err := readBenchStandardFrom(deep, benchStandardStop(deep))
	if err == nil {
		t.Fatalf("a script above $HOME answered: %q", got)
	}
	if !strings.Contains(err.Error(), "up to "+home) {
		t.Errorf("the walk did not stop at $HOME %q: %s", home, err.Error())
	}
}
