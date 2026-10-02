package docs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBusTransactionsProposalBoundaries pins the nova-bus bounded
// refresh/read/reply transaction of nova-tools #246 where it lives, in the
// "Bounded transactions" section of docs/SPEC.md. The section already composes
// the read half, the reply half and the delivery identity; this test holds the
// two things the composed section must not lose: the ownership boundary (the
// bus carries and receipts, owns no subscription and no assignment, and infers
// no permission from content or Git author)
// and the seven deciding cases (stale checkout, concurrent sends, a lost push
// response, quoted text, an invalid recipient, a CC-only update and interrupted
// draft handling). A missing file or a dropped term is a bug.
func TestBusTransactionsProposalBoundaries(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC.md")
	require.NoError(t, err, "docs/SPEC.md: %v", err)
	content := string(body)

	// The transaction itself, and the one-identity rule that binds its parts.
	for _, want := range []string{
		"### Bounded transactions — one snapshot, from refresh to receipt",
		"one snapshot from refresh",
		"INBOX NOTE addr=<to|cc>",
		"state=published",
		"state=already-published",
		"second identity is how a message is delivered twice",
		"docs/SPEC-BUS-DELIVERY.md",
		"no savings percentage is claimed yet",
		// the bounded check: the per-class cap and count that ship, held to the common
		// cap rule
		"`check` holds the common cap rule",
		"BUS MORE kind=<class> shown=<n> total=<t>",
	} {
		assert.Contains(t, content, want, "docs/SPEC.md missing %q", want)
	}

	// The ownership boundary, in the section's own terms.
	for _, want := range []string{
		"bus owns message transport and receipts",
		"owns no subscription and no assignment",
		"does not infer permission from message content or Git author",
	} {
		assert.Contains(t, content, want, "docs/SPEC.md missing %q", want)
	}

	// The deciding cases, each named so the section cannot shrink to prose.
	for _, want := range []string{
		"stale checkout",
		"concurrent sends",
		"a lost push response",
		"quoted text",
		"an invalid recipient",
		"a CC-only update",
		"interrupted draft handling",
	} {
		assert.Contains(t, content, want, "docs/SPEC.md missing %q", want)
	}
}
