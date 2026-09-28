package main

import (
	"bytes"
	"strings"
	"testing"
)

// nova-tools #2090: "decision record: nova-work absorb mode (E09-F04). Not
// scheduled. The three triggers, which side wins, and what must be true
// before it is built."
//
// The issue is a record for later -- "so the reasoning of 2026-09-20 does not
// have to be reconstructed" -- and it says itself that nothing in it is work
// to start: E09-F04's three criteria stay unverified on purpose until a
// trigger reopens the issue. What was missing on base was the record reaching
// the one surface a caller has. A caller who asked nova-work for absorb was
// told `unknown verb`, a sentence that says nobody ever decided anything --
// which is false: the decision is made, it is just NO. The binary now answers
// `absorb` with the decision record itself, as one refusal line, and this
// test is the reproduction: on base it fails because the answer is `unknown
// verb`, and it fails again the moment the record is unwired from run().
//
// The record says the four things the title names, in the issue's own words:
//
//   - Not scheduled: link is the intake mode today and GitHub stays the
//     source of truth for issues; we dogfood.
//   - The three triggers that reopen it: a RADICAL saving in tokens and wall
//     clock shown by the dogfood tables (#2089); more than one issue
//     tracker; sync pain, meaning drift that needs a person or a decision
//     made on a stale copy.
//   - Which side wins, already decided: if one side must be primary it is
//     nova-work's Lisp data structure, and the GitHub copy is what stops
//     being maintained.
//   - What must be true before it is built: all three E09-F04 criteria --
//     absorb separate from the default link with selected scope and
//     authority; source identity, provenance and content archived before
//     removal with the deletion outcome receipt appended; deletion left
//     pending on missing content, a source change or an uncertain network
//     result.
func TestIssue2090(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"absorb", "--session", "work.sock"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("absorb exit = %d, want 2 (not scheduled is a decision, and a decision this client can only refuse)", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("absorb wrote stdout: %q; the decision record travels on the refusal, which belongs on stderr", stdout.String())
	}
	line := strings.TrimSuffix(stderr.String(), "\n")
	if strings.Count(stderr.String(), "\n") != 1 || !strings.HasSuffix(line, "run: nova-work help") {
		t.Fatalf("absorb refusal = %q, want one line ending \"run: nova-work help\"", stderr.String())
	}

	// "decision record: nova-work absorb mode (E09-F04). Not scheduled."
	for _, want := range []string{"not scheduled", "E09-F04", "nova-tools#2090"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not name %q, so the record is not findable:\n%s", want, line)
		}
	}
	// TODAY: link mode is the default and GitHub is the source of truth.
	for _, want := range []string{"link", "source of truth"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not say what TODAY is, and %q is a word of it:\n%s", want, line)
		}
	}
	// "The three triggers": said to be three and named one by one.
	for _, want := range []string{"three triggers", "tokens and wall clock", "dogfood tables", "second issue tracker", "sync pain"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not carry the trigger %q:\n%s", want, line)
		}
	}
	// "which side wins": the direction is already decided, and the winner is named.
	for _, want := range []string{"Lisp data structure", "stops being maintained"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not say which side wins, and %q is a word of it:\n%s", want, line)
		}
	}
	// "what must be true before it is built": all three E09-F04 criteria.
	for _, want := range []string{
		"before it is built",
		"separate from the default link",
		"selected scope and authority",
		"archived before removal",
		"deletion outcome receipt",
		"pending on missing content",
		"source change",
		"uncertain network result",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal does not carry the precondition %q:\n%s", want, line)
		}
	}
}
