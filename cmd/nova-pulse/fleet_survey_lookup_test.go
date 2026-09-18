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
	deep := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := readBenchStandardFrom(deep)
	if err == nil {
		t.Fatal("a directory with no checkout above it answered a script")
	}
	for _, want := range []string{deep, "tools/bench-standard.sh", "checkout"} {
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
	script, err := readBenchStandardFrom(deep)
	if err != nil {
		t.Fatalf("the walk did not reach the checkout: %v", err)
	}
	if !strings.Contains(script, "STANDARD OK") {
		t.Fatalf("the script read back is not the one written: %q", script)
	}
}
