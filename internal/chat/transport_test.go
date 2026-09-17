package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-CHAT rule 7 is the page transport's whole specification: it names the
// Tailscale-served private page as the second transport, spells rule 5's six
// capabilities against it — no message size limit, no rate limit but the
// line's own, no member list because the mesh is the membership, and identity
// as the Tailscale node and user — and keeps `--transport page` refused until
// `internal/chat/transport.go` has carried two implementations in tests
// (issue #94). A capability dropped from the section, or the refusal renamed
// away, is red.
func TestTheSecondTransportPageSectionIsSpecified(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-CHAT.md"))
	if err != nil {
		t.Fatalf("SPEC-CHAT.md: %v", err)
	}
	doc := string(raw)

	_, after, ok := strings.Cut(doc, "\n## 7. The second transport is named and not built\n")
	if !ok {
		t.Fatal("SPEC-CHAT.md has no rule 7 heading")
	}
	section := after
	if end := strings.Index(after, "\n## 8."); end >= 0 {
		section = after[:end]
	}

	for _, want := range []string{
		"tailscale-for-glenn-access",
		"no truncation forced by the transport",
		"no rate limit but the line's own",
		"the mesh is the membership",
		"the tailnet is the boundary",
		"the response carries the page's own message id",
		"CHAT REFUSED: not in this build",
		"#94",
		"carried two implementations in tests",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-CHAT rule 7 does not name %q", want)
		}
	}
}
