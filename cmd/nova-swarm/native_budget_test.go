package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeBudgetEndsTheCardAndKeepsFindings is demanded test 13d (SPEC-SWARM.md:3324,
// issue #1545). It is cut in slices, and this file holds the FIRST: the word is required
// on every native launch, and every in-repo constructor of a native argv passes it along.
//
// THE CLAUSES OF 13d THIS FILE HOLDS:
//
//	"`native` without `--tokens` is exit 2 naming the flag and makes no directory, and
//	 `--tokens 0` is refused"
//	"`--tokens unmetered` prints `budget=unmetered`"
//
// The rest of 13d -- the sampler, the stop, the two-launch accounting, the packet -- comes
// in the slices after this one, and each is red from its own clause before it is green.

// budgetNativeArgs is one complete native argv with the budget word the caller names, and
// with it omitted entirely when the word is empty: the two shapes every case here wants.
func budgetNativeArgs(t *testing.T, bin, card, slot, root, tokens string) []string {
	t.Helper()
	args := []string{"native",
		"--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", card, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	if tokens != "" {
		args = append(args, "--tokens", tokens)
	}
	return args
}

// budgetCard writes a card the fake harness will run through without spending anything.
func budgetCard(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(path, []byte("a card\nFAKE-FINDINGS 1\n"), 0o644))
	return path
}

// fieldOf is one `k=v` field of a line, or "" when the line does not carry it. It lives in
// this file, which no build tag guards, because every budget test reads a field of the
// NATIVE verdict line and one of them is unix-only.
//
//lint:ignore U1000 used by the functional and slow tagged budget tests, which staticcheck reads without build tags
func fieldOf(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// nativeOKLine is the one NATIVE verdict line in a capture, failed for if there is none.
//
// THE VERDICT WORD IS NOT THIS HELPER'S SUBJECT (nova-tools #1844, which landed on dev
// while this branch was open). `native` now prints `NATIVE OK` only for a run that earned
// it and `NATIVE INCOMPLETE ... why=<harness-silent|no-result|rc>` otherwise, and #1844's
// own sentence is that "every other field is byte-for-byte the same, so a reader that
// parses fields still reads them all". Every caller here asserts a FIELD -- budget=,
// stopped=, rc=, harness= -- so the helper matches the line by its `NATIVE ` prefix and
// lets the verdict be whatever the run earned. A card the budget stopped has rc=-1 and so
// is INCOMPLETE by #1844's rule, which is the truth about it: it did not finish. The
// verdict's OWN assertions live in native_verdict_test.go and are untouched.
func nativeOKLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "NATIVE OK ") || strings.HasPrefix(line, "NATIVE INCOMPLETE ") {
			return line
		}
	}
	t.Fatalf("no NATIVE verdict line in:\n%s", out)
	return ""
}
