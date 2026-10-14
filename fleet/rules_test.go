package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childRulesSHA256 is the sha256 of child-rules.txt. nova-sprint embeds the same file as
// internal/fleetrules/child-rules.txt and pins the same digest: its add refuses a held
// rules file whose bytes are not its embedded copy (cmd/nova-sprint/verbs.go heldSet), so the
// two copies change together, in one pull request per repository, or not at all.
const childRulesSHA256 = "28fd38b11be263e28c7fc9167f3a24c8cfc4428c676b06c913fd69450aafbdaf"

// TestChildRules reads child-rules.txt once and pins both promises it makes: its
// bytes match the digest nova-sprint's embedded copy pins (so the two change
// together, in one pull request per repository, or not at all), and the worker
// brief's history rule stands exactly once — a child that amends, rebases or
// resets onto origin rewrites the staged commit, and the finish refuses its head
// as one that does not descend from it.
func TestChildRules(t *testing.T) {
	t.Parallel()
	raw, err := Rules.ReadFile("child-rules.txt")
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	assert.Equal(t, childRulesSHA256, hex.EncodeToString(sum[:]),
		"child-rules.txt sha256 must match nova-sprint's internal/fleetrules/child-rules.txt byte for byte; update the digest in both repositories")

	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "[no-rewrite-history] Never amend, rebase or reset onto origin") {
			n++
		}
	}
	assert.Equal(t, 1, n, "want the history rule exactly once in child-rules.txt")
}
