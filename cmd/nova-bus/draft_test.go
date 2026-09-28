package main

import (
	"strings"
	"testing"
)

// TestDraftUsageShowsRedirectSynopsis holds the synopsis promise: the draft line of
// `nova-bus help` must say the skeleton goes to stdout, so a reader Redirects it to a file
// instead of expecting draft to write one. The released synopsis spells the redirect
// `> <file>`.
func TestDraftUsageShowsRedirectSynopsis(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "help").mustCode(t, 0)
	if !strings.Contains(r.stdout, "> <file>") {
		t.Fatalf("draft synopsis does not show the redirect `> <file>`:\n%s", r.stdout)
	}
}
