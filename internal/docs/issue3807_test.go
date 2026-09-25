package docs

import (
	"os"
	"strings"
	"testing"
)

// TestIssue3807CLIPresenceIsTheRow verifies that docs/CLI.md's nova-wake
// presence paragraphs say what the code does since #3447: on the fleet store
// friend:<name> is the friend row, its up and at are the presence, and
// nova-wake beat refuses it (nova-tools #3807). The stale sentence that made
// the beat's TTL the presence on the row is gone.
func TestIssue3807CLIPresenceIsTheRow(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	body := string(raw)

	start := strings.Index(body, "A friend is `up` or `down` and nothing else.")
	if start < 0 {
		t.Fatal("docs/CLI.md: the nova-wake presence paragraph (\"A friend is `up` or `down` and nothing else.\") is missing")
	}
	end := strings.Index(body[start:], "The password is never a flag")
	if end < 0 {
		t.Fatal("docs/CLI.md: the paragraph after the presence paragraph (\"The password is never a flag\") is missing")
	}
	para := body[start : start+end]

	for _, want := range []string{
		"the friend row",
		"`up` and `at`",
		"rowan-tools `friend-row`",
		"#3447",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("docs/CLI.md presence paragraph must say the row is the presence; missing %q", want)
		}
	}
	for _, stale := range []string{
		"presence is not the hash's existence but its TTL",
		"only a beat gives it",
	} {
		if strings.Contains(body, stale) {
			t.Errorf("docs/CLI.md still carries the pre-#3447 presence sentence %q", stale)
		}
	}
	if !strings.Contains(body, "`nova-wake beat` refuses it") {
		t.Error("docs/CLI.md must say nova-wake beat refuses the friend row (#3447)")
	}
}
