package main

import (
	"strings"
	"testing"
)

// inbox --wait (errata 3 amendment 8): with no tick end to come it waits out
// its --timeout on the store's clock, says so, and shows the inbox; a
// timeout of zero is refused.
func TestInboxWaitRunsOutItsTimeout(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	out := ta.ok("inbox --wait --timeout 2s")
	if !strings.Contains(out, "inbox --wait: no tick end in 2s") {
		t.Fatalf("inbox --wait with nothing to come:\n%s", out)
	}
	if code, _, errs := ta.do("inbox --wait --timeout 0s"); code == 0 || !strings.Contains(errs, "--timeout above zero") {
		t.Fatalf("a timeout of zero: exit %d\n%s", code, errs)
	}
}
