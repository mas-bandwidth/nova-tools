package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ten rules from dogfooding in SPEC-SECRETS.md, each carrying the mistake it
// prevents and what holds it. This test reads the spec's text, so a rule renamed
// out of the document is red in a build.
func TestSpecSecretsNamesDogfoodingAdditions(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SECRETS.md"))
	require.NoError(t, err, "the secrets spec is missing: %s", err)
	doc := string(raw)
	assert.Contains(t, doc, "## Rules from dogfooding", "SPEC-SECRETS.md does not name the dogfooding section")
	for _, phrase := range []string{
		"ingest is rotation",
		"one seat per OS user, keys for swarms not people",
		"opened only by its seat key and the recovery key, exactly two recipients",
		"only by `exec --only NAME`, never a file, argv, log or transcript",
		"harness configs reference `{env:NAME}`",
		"exactly one seat key per owner prefix",
		"the bench's own SSH key, generated on that bench",
		"`TestStorePullUsesBenchOwnedKey`",
		"private half lives in the owner's password manager",
		"seal is one step",
		"fails loudly on a plaintext key file",
	} {
		assert.Contains(t, doc, phrase, "SPEC-SECRETS.md does not name the rule keyed by %q", phrase)
	}
}
