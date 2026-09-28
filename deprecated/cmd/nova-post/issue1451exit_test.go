// issue #1451 (follow-up): the door `; run: nova-post help` belongs only to an
// exit-2 refusal -- the tool could not run, or the invocation was wrong. An
// exit-1 refusal is a verdict: the verb RAN and answered NO, and the door tells
// that reader nothing. PR #1756 put the door in refuseLine without regard to the
// engine's exit code, so every exit-1 engine refusal gained an exit-2 door. This
// test drives one real exit-1 engine refusal (the no-approval gate) and one
// exit-2 refusal (a bad --channel) and holds each to the shape its exit owns.
package main

import (
	"strings"
	"testing"
)

func TestOnlyExitTwoRefusalsCarryTheDoor(t *testing.T) {
	const door = "; run: nova-post help"
	const doorWord = "run: nova-post help"

	f := newFixture(t)
	f.writeAllow("ghost\tghost.example")
	f.setCred("ghost", "ghost.example")
	hash := f.draft("ghost", "ghost.example", "a body nobody approved")

	code, _, stderr := f.run("send", "--draft", hash, "--approval", "glenn-0123456789ab",
		"--drafts", f.drafts, "--bus", f.bus, "--allowlist", f.allow)
	if code != 1 {
		t.Fatalf("the exit-1 half: exit = %d, want exactly 1; stderr=%q", code, stderr)
	}
	line := strings.TrimSuffix(stderr, "\n")
	if strings.Contains(line, doorWord) {
		t.Errorf("an exit-1 refusal carries the exit-2 door: %q", line)
	}
	if !strings.Contains(line, "have Glenn send") {
		t.Errorf("an exit-1 refusal no longer names its remedy: %q", line)
	}

	code, _, stderr = f.run("draft", "--channel", "bogus", "--target", "x",
		"--drafts", f.drafts, "--allowlist", f.allow)
	if code != 2 {
		t.Fatalf("the exit-2 half: exit = %d, want exactly 2; stderr=%q", code, stderr)
	}
	line = strings.TrimSuffix(stderr, "\n")
	if !strings.HasSuffix(line, door) {
		t.Errorf("an exit-2 refusal does not END in %q: %q", door, line)
	}
	if n := strings.Count(line, doorWord); n != 1 {
		t.Errorf("%q appears %d times in an exit-2 refusal, want exactly 1: %q", doorWord, n, line)
	}
}
