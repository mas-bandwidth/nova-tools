//go:build functional || slow || perf

// Helpers the functional, slow and perf tiers share. Every test that calls them starts
// git over a real bus checkout, so the unit tier builds none of them; the constraint is
// wider than functional because slow_test.go and timing_test.go call them too.

package main

import (
	"path/filepath"
	"testing"
)

// draftFrom is one note's whole text, so that two benches sending in the same second get
// two different ids -- the id is a hash over the note, and these differ in the body.
func draftFrom(who, subject, body string) string {
	return "From: " + who + "\nTo: Bo\nSubject: " + subject + "\n\n" + body + "\n"
}

// ---------------------------------------------------------------- helpers

// cloneOf is a second checkout of one bus: the other bench, which cannot see this one.
func cloneOf(t *testing.T, bare string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bench")
	gitIn(t, filepath.Dir(dir), "clone", "--quiet", bare, dir)
	gitIn(t, dir, "checkout", "-q", "-B", "main")
	return dir
}
