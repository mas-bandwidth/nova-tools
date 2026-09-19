package main

import "testing"

// #1496: parse ranges over a Go map, so a bare `nova-bus send` -- missing --bus,
// --remote and --branch at once -- names whichever flag map order reaches first,
// a different one from run to run. The repetition is the assertion: a single run
// names --bus about a third of the time, so a one-shot check passes against the
// unfixed tool by luck. Asserting the set of three would still allow the order to
// vary between runs; the exact ordered string pins the fixed order the repair promises.

func TestIssue1496ABareVerbNamesEveryMissingRequiredFlagInAFixedOrder(t *testing.T) {
	const want = "nova-bus send: --branch is required; refusing to guess; run: nova-bus help\n" +
		"nova-bus send: --bus is required; refusing to guess; run: nova-bus help\n" +
		"nova-bus send: --remote is required; refusing to guess; run: nova-bus help\n"
	first := ""
	for i := 0; i < 20; i++ {
		r := invoke(t, "", "send")
		if r.code != 2 {
			t.Fatalf("run %d: exit = %d, want 2\nstderr: %s", i, r.code, r.stderr)
		}
		if first == "" {
			first = r.stderr
		} else if r.stderr != first {
			t.Fatalf("run %d stderr differs from run 0:\n run 0: %q\n run %d: %q", i, first, i, r.stderr)
		}
	}
	if first != want {
		t.Fatalf("stderr = %q, want %q", first, want)
	}
}
