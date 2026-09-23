package swarm

import (
	"strings"
	"testing"
)

// TestIssue1647 verifies that MatchKeyShape detects a key-shaped string in a
// line of text, returns the shape name (never the matched text), and rejects a
// plain source line. The requirement from #1647: `secret` never prints the
// matched text, only path, line and shape name.
func TestIssue1647(t *testing.T) {
	// Positive: a forge-token-shaped string, built by parts so no valid key
	// lands in this repository.
	key := "gh" + "p_" + strings.Repeat("A", 36)
	name, ok := MatchKeyShape("const token = \"" + key + "\"")
	if !ok {
		t.Fatal("a forge token line drew no key-shape match")
	}
	if name == "" {
		t.Fatal("MatchKeyShape returned an empty shape name")
	}
	// The shape name must never carry the matched text.
	if strings.Contains(name, key) {
		t.Fatalf("the shape name carries the key text: %q", name)
	}

	// Negative: a plain source line.
	_, ok = MatchKeyShape("func Sign(n int) int { return 1 }")
	if ok {
		t.Fatal("a plain source line drew a key-shape match")
	}
}
