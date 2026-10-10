package main

import (
	"strings"
)

// TestNativeBudgetEndsTheCardAndKeepsFindings is demanded test 13d (SPEC-WORKER.md:3324,
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
