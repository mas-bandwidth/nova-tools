package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestASpecPulseLinkWithAnAnchorIsStillChecked builds its own fixture: the
// live docs/spec-pulse/ tree carries no target with a `#` today, so the
// latent skip can only be caught by a directory this test writes.
func TestASpecPulseLinkWithAnAnchorIsStillChecked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "02-the-rules-numbered.md"), []byte("# The rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		"[a](missing-section.md#a-heading)",
		"[b](02-the-rules-numbered.md#a-heading)",
		"[c](#anchor-only)",
		"[d](https://example.com/x.md#f)",
		"[e](02-the-rules-numbered.md)",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "99-fixture.md"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	broken, err := specPulseBrokenLinks(dir)
	if err != nil {
		t.Fatalf("specPulseBrokenLinks(%s): %v", dir, err)
	}
	if len(broken) != 1 {
		t.Fatalf("got %d findings, want exactly 1 (the missing-section.md target): %v", len(broken), broken)
	}
	if !strings.Contains(broken[0], "missing-section.md#a-heading") {
		t.Errorf("finding does not name the written target %q: %s", "missing-section.md#a-heading", broken[0])
	}
}
