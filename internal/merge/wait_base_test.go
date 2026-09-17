package merge

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// nova-tools #229 point 5: a stacked PR lands onto its stack, not main.
// #144 was rebased onto the docs-migration branch and its base retargeted
// there; when it was called merged, it had merged onto the stack, not main.
// A merge line names its base, and only a base of main is a landing.
// MERGE WAIT MERGED must therefore carry base=<branch> so a coordinator can
// tell a stack merge from a landing.
func TestWaitMergedNamesItsBase(t *testing.T) {
	host := NewFakeHost()
	head := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	mergeSHA := "ffffffffffffffffffffffffffffffffffffffff"
	host.OnPR = func(n, call int) (PR, bool) {
		return PR{Number: 144, HeadOID: head, Base: "docs-migration", Merged: true, Closed: true, MergeSHA: mergeSHA}, true
	}
	host.SetChecks(head, 1, 0)
	start := time.Date(2026, 9, 12, 19, 54, 0, 0, time.UTC)
	nowFn, sleepFn, _ := waitClock(start)
	var out bytes.Buffer
	exit := Wait(host, 144, 5*time.Minute, time.Millisecond, nowFn, sleepFn, &out)
	if exit != 0 {
		t.Fatalf("merged wait wants exit 0, got %d in %q", exit, out.String())
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "base=docs-migration") {
		t.Fatalf("a merge line names its base, and only a base of main is a landing: MERGED must carry base=docs-migration, got %q", line)
	}
}
