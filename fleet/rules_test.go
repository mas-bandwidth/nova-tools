package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// childRulesSHA256 is the sha256 of child-rules.txt. nova-sprint embeds the same file as
// internal/fleetrules/child-rules.txt and pins the same digest: its add refuses a held-name
// rules file whose bytes are not its embedded copy (cmd/nova-sprint/verbs.go heldSet), so the
// two copies change together, in one pull request per repository, or not at all.
const childRulesSHA256 = "28fd38b11be263e28c7fc9167f3a24c8cfc4428c676b06c913fd69450aafbdaf"

// TestChildRulesMatchTheSprintCopy pins child-rules.txt to the digest nova-sprint's copy pins.
func TestChildRulesMatchTheSprintCopy(t *testing.T) {
	t.Parallel()
	raw, err := Rules.ReadFile("child-rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != childRulesSHA256 {
		t.Fatalf("child-rules.txt sha256 %s, want %s: change nova-sprint's internal/fleetrules/child-rules.txt byte for byte with it and update the digest in both repositories", got, childRulesSHA256)
	}
}

// TestChildRulesNeverRewriteHistory pins the worker brief's history rule: a child that
// amends, rebases or resets onto origin rewrites the staged commit, and the finish refuses
// its head as one that does not descend from it.
func TestChildRulesNeverRewriteHistory(t *testing.T) {
	t.Parallel()
	raw, err := Rules.ReadFile("child-rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "[no-rewrite-history] Never amend, rebase or reset onto origin") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want the history rule exactly once in child-rules.txt, got %d", n)
	}
}
