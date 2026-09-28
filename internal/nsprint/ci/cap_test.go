package ci_test

// The 2026-09-25 retry storm: hulk logged `CLAIMED nova-tools@47e596c6
// attempt=22` and later attempt=244 on another head, while space and vision
// (no GitHub key) claimed and released it back to the pool every tick.
// Attempts are capped at cfg:ci max_attempts (default 2, one run plus one
// retry for flake): a head is claimed at most that many times, then it ends
// FAIL with the why on the record and leaves the pool for good; only
// `ci request --again` puts it back.

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

func TestFirstFail(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"ok a\n--- FAIL: TestX (0.1s)\n    x_test.go:3: boom\nFAIL\n": "--- FAIL: TestX (0.1s)",
		"building\nFAIL\tgithub.com/x/y [build failed]\n":             "FAIL github.com/x/y [build failed]",
		"fatal: Needed a single revision\n\n":                         "fatal: Needed a single revision",
		"":                                                            "",
	}
	for in, want := range cases {
		if got := ci.FirstFail([]byte(in)); got != want {
			t.Fatalf("FirstFail(%q) = %q, want %q", in, got, want)
		}
	}
}
