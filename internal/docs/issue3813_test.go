package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue3813FriendServeDocDoesNotNameFriendRow verifies that docs/CLI.md's
// nova-sprint friend serve paragraph no longer names friend:<f> as a hash
// serve writes (nova-tools #3813).
func TestIssue3813FriendServeDocDoesNotNameFriendRow(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	body := string(raw)

	start := strings.Index(body, "### `nova-sprint friend serve`")
	if start < 0 {
		t.Fatal("docs/CLI.md: ### `nova-sprint friend serve` section is missing")
	}
	end := strings.Index(body[start:], "The harness argv is the seat's declaration:")
	if end < 0 {
		t.Fatal("docs/CLI.md: end of friend serve paragraph is missing")
	}
	para := body[start : start+end]

	if strings.Contains(para, "the presence hashes `friend:<f>:beat` and `friend:<f>`") {
		t.Error("docs/CLI.md friend serve paragraph must not name friend:<f> as a presence hash serve writes")
	}
	if !strings.Contains(para, "the presence hash `friend:<f>:beat`") {
		t.Error("docs/CLI.md friend serve paragraph must name friend:<f>:beat as the presence hash")
	}
}
