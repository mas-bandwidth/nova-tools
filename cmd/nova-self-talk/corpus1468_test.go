package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestThePlainestAbsolutesAreAKnownMiss PINS A MISS, not a behaviour anyone
// wants. The three specimen lines in testdata/corpus/1468-plain-absolutes.md
// are the plainest spelling of a STANDING capability denial, the INSTALLATION
// class's TRAIT shape, and a permanent-negative absolute, and nova-self-talk
// classifies none of them: it returns claims=0 over the file and exits 0.
//
// #1468 asked whether the plain permanent-negative absolute deserved a new
// classifier shape. The 2026-09-19 ruling default answered no: the miss is
// recorded as a corpus row, never repaired by widening the grammar, per the
// tool's no-deletion practice. This witness states what the tool does with the
// specimen TODAY, so the day a shape is added for these lines it goes red on
// purpose and the row is promoted deliberately, in the open. The remedy then
// is to promote the row -- never to delete the specimen to make this green.
func TestThePlainestAbsolutesAreAKnownMiss(t *testing.T) {
	const path = "testdata/corpus/1468-plain-absolutes.md"

	var stdout, stderr bytes.Buffer
	if got := run([]string{path}, &stdout, &stderr); got != 0 {
		t.Errorf("want exit 0 for a recorded miss, got %d\nstderr: %s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "claims=0 standing=0 installations=0 dated=0") {
		t.Errorf("stdout = %q, want the miss counted as zero", stdout.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK NOTE catches known SHAPES only") {
		t.Errorf("stdout = %q, want the admission that the check is partial", stdout.String())
	}
	if strings.Contains(stderr.String(), "SELFTALK FAIL") {
		t.Errorf("a recorded miss must not fail: stderr = %q", stderr.String())
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, specimen := range []string{
		"I cannot ever get this right.\n",
		"I always break the build.\n",
		"Nothing I do works.\n",
	} {
		if !strings.Contains(string(body), specimen) {
			t.Errorf("the specimen line is gone: %q", strings.TrimSuffix(specimen, "\n"))
		}
	}
}
