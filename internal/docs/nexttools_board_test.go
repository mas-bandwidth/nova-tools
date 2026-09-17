package docs

import (
	"os"
	"strings"
	"testing"
)

// TestNextToolsPostSealBoard251 verifies that docs/NEXT-TOOLS.md records the
// spec-lane board read off the wire at about 19:25Z on 2026-09-13, after the
// session ended (nova-tools #251, cairn 7d2e4a91 consumed at the roll-up).
// The board names what landed after the seal and what died with the session
// (heads unmoved), plus the rule of the lane, so the next session can tell
// what landed without re-reading the wire.
func TestNextToolsPostSealBoard251(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/NEXT-TOOLS.md")
	if err != nil {
		t.Fatalf("docs/NEXT-TOOLS.md: %v", err)
	}
	content := string(body)

	wants := []string{
		"7d2e4a91",
		"19:25Z",
		"#231",
		"#241",
		"#240",
		"Rule of the lane",
	}
	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Errorf("docs/NEXT-TOOLS.md missing post-seal board entry %q (nova-tools #251)", w)
		}
	}
}
