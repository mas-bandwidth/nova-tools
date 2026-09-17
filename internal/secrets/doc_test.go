package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ten rules the 2026-09-16/17 dogfooding added to SPEC-SECRETS.md, each
// carrying the hurt that made it and the red test that guards it. This test
// reads the spec the way internal/decide/doc_test.go reads SPEC-DECIDE.md, so a
// rule renamed out of the document is red in a build.
func TestSpecSecretsNamesDogfoodingAdditions(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SECRETS.md"))
	if err != nil {
		t.Fatalf("the secrets spec is missing: %s", err)
	}
	doc := string(raw)
	if !strings.Contains(doc, "Additions from dogfooding (2026-09-16/17)") {
		t.Errorf("SPEC-SECRETS.md does not name the dogfooding section")
	}
	for _, phrase := range []string{
		"ingest is rotation",
		"one seat per OS user, keys for swarms not people",
		"opened only by its seat key and the recovery key, exactly two recipients",
		"only by `exec --only NAME`, never a file, argv, log or transcript",
		"harness configs reference `{env:NAME}`",
		"exactly one seat key per owner prefix",
		"bench-owned read-only deploy key, never a person's credential",
		"private half lives in the owner's password manager",
		"seal is one step",
		"fail loudly on any plaintext key file",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-SECRETS.md does not name the rule keyed by %q", phrase)
		}
	}
}
