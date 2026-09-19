package main

import (
	"strings"
	"testing"
)

// TestIssue1518WaitTakesMaxCommitsAndSaysLoudlyWhenTheWalkIsBlind is card #1518.
//
// inbox takes --max-commits and wait does not, so a waiter whose cursor is more than
// defaultMaxCommits behind never sees new mail: the walk is bounded, the bound returns an
// empty reading, and the loop reports WAIT TIMEOUT on a healthy-looking schedule forever.
// A bounded walk that reads as routine is worse than one that fails, because the waiter
// has no reason to go looking. So wait takes the same flag, and when the wait path hits
// the bound it says so in one loud WAIT BLIND line naming the count it could not cross and
// the remedy. The existing INBOX WALK bounded line survives untouched: other tools parse it.
//
// The test builds a bus whose cursor stands 120 commits behind HEAD, then runs wait with
// --max-commits 100: the flag must be accepted, the walk must be blind, and both the old
// and the new line must be on stderr.
func TestIssue1518WaitTakesMaxCommitsAndSaysLoudlyWhenTheWalkIsBlind(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout := longBus(t, 120)
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	// The flag is ACCEPTED, not guessed at: a bound the walk fits under must run to its
	// deadline rather than die as a bad invocation. The unknown-flag sentence is checked
	// first so the failure names the defect rather than an exit code.
	accepted := invoke(t, "", "wait", "--bus", checkout, "--as", "Ada",
		"--receipt-max-words", "40", "--timeout", "300ms", "--interval", "100ms",
		"--remote", "origin", "--branch", "main", "--attempts", "3",
		"--max-commits", "2000")
	if strings.Contains(accepted.stderr, "flag provided but not defined: -max-commits") {
		t.Fatalf("wait did not accept --max-commits: %s", strings.TrimSpace(accepted.stderr))
	}
	if accepted.code != 0 {
		t.Fatalf("wait --max-commits 2000 must run to its deadline, exit=%d\nstderr:\n%s", accepted.code, accepted.stderr)
	}

	// The bound the walk does NOT fit under. The cursor stands 120 commits behind and the
	// caller allows 100, so the walk is blind: the old line still goes out, and the new
	// loud one says the wait cannot see, names the count and the way out.
	blind := invoke(t, "", "wait", "--bus", checkout, "--as", "Ada",
		"--receipt-max-words", "40", "--timeout", "300ms", "--interval", "100ms",
		"--remote", "origin", "--branch", "main", "--attempts", "3",
		"--max-commits", "100")
	if blind.code != 0 {
		t.Fatalf("a bounded wait exits 0 by contract, got %d\nstderr:\n%s", blind.code, blind.stderr)
	}
	// EXACTLY ONE, counted. The card asks for "one loud WAIT BLIND line", and a wait
	// POLLS: a bound hit on every poll would print the same line twice, which is the
	// noise this line exists to cut through. Comparing a second occurrence against the
	// first would tolerate an identical duplicate, so the occurrences are counted.
	var blindLine string
	blindCount := 0
	for _, line := range strings.Split(blind.stderr, "\n") {
		if !strings.HasPrefix(line, "WAIT BLIND ") {
			continue
		}
		blindCount++
		blindLine = line
	}
	if blindCount != 1 {
		t.Fatalf("want exactly 1 WAIT BLIND line, got %d\nstderr:\n%s", blindCount, blind.stderr)
	}
	wantBlind := `WAIT BLIND commits=100 remedy="raise --max-commits or close --before <instant>"`
	if blindLine != wantBlind {
		t.Fatalf("the wait path did not say loudly that the walk is blind:\nwant: %s\ngot stderr:\n%s", wantBlind, blind.stderr)
	}
	if !strings.Contains(blind.stderr, `INBOX WALK bounded commits=100 remedy="raise --max-commits or close --before <instant>"`) {
		t.Fatalf("the existing INBOX WALK bounded line was changed or dropped:\n%s", blind.stderr)
	}
}
