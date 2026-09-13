//go:build !windows

package update

import (
	"testing"
	"time"
)

// A child that spawns a grandchild holding stdout open in its own process group
// (so the group kill cannot reach it) and then hangs must still end inside the
// budget plus a small fixed slack: the deadline closes the held pipe instead of
// letting a fixed drain grace run past it.
func TestDeadlineEscapedPipeGrandchildReturnsInsideBudget(t *testing.T) {
	p := manifest(t, row("x", "tool", command(t, "escaped", "30s"), "npm:unused", "none"))
	started := time.Now()
	c, out, errs := run(t, Environment{}, "check", "--file", p, "--budget", "300ms", "--timeout", "200ms")
	if c != 1 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("a 300ms budget with an escaped grandchild took %s", took)
	}
}
