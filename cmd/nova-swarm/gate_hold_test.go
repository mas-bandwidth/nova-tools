package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// STELLA'S HOLD ON #1478 (comment 5737662335), P1 AND P2. Both are real, and both are about
// the same mistake: the first version read the shell's line as if it were a sentence about a
// program, when it is a sentence about a PATH and says nothing about what was done to it.
//
// P1: the grammar rejected any path holding a space, so `/opt/sdk tool/bin/go` -- an ordinary
// absolute path -- recreated the exact silent green of #1465 through the tool's own fixture.
//
// P2: a plain redirection to an unwritable path produces the same words, and the card
// RECOVERS from it and exits 0. The line cannot tell an exec denial from a write denial, and
// under --no-wall there is no wall to attribute either to.

// TestDeniedPathWithASpaceStillRefuses is P1 as a CLI regression, at the full path. A
// whitespace-bearing absolute path is not evidence of prose.
func TestDeniedPathWithASpaceStillRefuses(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	for _, refused := range []string{
		"/opt/sdk tool/bin/go",
		"/opt/sdk (old)/bin/go",
		"/opt/my sdk/go 1.26/bin/go",
	} {
		t.Run(refused, func(t *testing.T) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			if err := os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED "+refused+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"native", "--harness", bin, "--model", "fake/fake-model",
				"--label", "go-card", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s", "--no-wall"}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if strings.Contains(stdout.String(), "NATIVE OK") || rc == 0 {
				t.Fatalf("a denied path holding a space is still a denial; the run reported OK (rc=%d):\n%s", rc, stdout.String())
			}
			// AND IT IS NAMED WHOLE. A path truncated at its first space is a path the
			// coordinator cannot act on, and read_roots would take the wrong directory.
			if !strings.Contains(stderr.String(), refused) {
				t.Errorf("the refusal names the complete path %q; it reads:\n%s", refused, stderr.String())
			}
		})
	}
}

// TestRefusalClaimsNoCauseItCannotProve is P2. A shell's `Permission denied` on a path is
// evidence that SOMETHING was denied and nothing more: Stella's own witness is a bash
// redirection to an unwritable output that printed exactly this shape and then RECOVERED,
// exit 0, having attempted no program at all. The refusal may not call that path a program,
// may not assert that a gate never ran or that nothing was compiled, and may not prescribe
// read_roots as the cure for a failed write.
func TestRefusalClaimsNoCauseItCannotProve(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-DENY-AND-RECOVER /opt/out/report.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "redirect-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	// The disposition is still refused -- a denial nobody read is not an OK -- but the WORDS
	// must be honest about what is known.
	if rc == 0 || strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("a denial in the capture is never OK, whatever it was: rc=%d\n%s", rc, stdout.String())
	}
	line := stderr.String()
	for _, forbidden := range []string{
		"never executed", "nothing compiled", "never compiled",
		"the program", "the gate never",
	} {
		if strings.Contains(line, forbidden) {
			t.Errorf("the refusal asserts %q, which this line cannot establish -- a redirection to an unwritable path prints the same words and the card recovers:\n%s", forbidden, line)
		}
	}
	// It must say what it does not know, in so many words.
	if !strings.Contains(line, "unverified") {
		t.Errorf("the refusal labels the denied operation unverified; it reads:\n%s", line)
	}
	// It must carry the line itself, which is the only thing a person can act on.
	if !strings.Contains(line, "Permission denied") {
		t.Errorf("the refusal quotes the capture's own line verbatim; it reads:\n%s", line)
	}
	// AND IT MUST NOT BLAME A WALL THAT WAS NEVER THERE. This run was --no-wall.
	for _, forbidden := range []string{"the wall refused", "read_roots"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("an unwalled run attributes nothing to a wall or its read set (%q):\n%s", forbidden, line)
		}
	}
}

// TestWalledRefusalOffersTheReadRootsAsOnePossibility: on a WALLED run the read set is a
// real candidate and the refusal may name it -- as one possible cause among the others, never
// as the diagnosis. The roots are still worked out for the coordinator rather than left to a
// guess.
func TestWalledRefusalOffersTheReadRootsAsOnePossibility(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED /opt/sdk tool/bin/go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "walled-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", nativeSandbox(t)}
	var stdout, stderr bytes.Buffer
	if rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now()); rc == 0 {
		t.Fatalf("the walled run is refused, got rc=0:\n%s", stdout.String())
	}
	line := stderr.String()
	if !strings.Contains(line, "read_roots") {
		t.Errorf("a walled run's refusal offers the read set as one candidate; it reads:\n%s", line)
	}
	if !strings.Contains(line, "/opt/sdk tool/bin") {
		t.Errorf("the candidate root is the complete directory of the denied path; it reads:\n%s", line)
	}
	if !strings.Contains(line, "unverified") {
		t.Errorf("a walled run's refusal is still an unverified cause; it reads:\n%s", line)
	}
}
