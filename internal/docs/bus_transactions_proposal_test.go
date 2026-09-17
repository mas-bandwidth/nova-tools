package docs

import (
	"os"
	"strings"
	"testing"
)

// TestBusTransactionsProposalBoundaries pins the nova-bus bounded
// refresh/read/reply transaction of nova-tools #246 where it lives, in the
// "Bounded transactions" section of docs/SPEC.md. The section already composes
// the read half, the reply half and the delivery identity; this test holds the
// two things the issue names and the composed section must not lose: the
// ownership boundary (bus carries and receipts, wake owns subscriptions, work
// owns assignments, and no permission is inferred from content or Git author)
// and the seven deciding cases (stale checkout, concurrent sends, a lost push
// response, quoted text, an invalid recipient, a CC-only update and interrupted
// draft handling). A missing file or a dropped term is a bug.
func TestBusTransactionsProposalBoundaries(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC.md")
	if err != nil {
		t.Fatalf("docs/SPEC.md: %v", err)
	}
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
		"BUS FINDING",
		"BUS SUMMARY",
		"shared collector",
		"no savings percentage is claimed yet",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC.md missing %q", want)
		}
	}

	// The ownership boundary the issue draws, in the issue's own terms.
	for _, want := range []string{
		"bus owns message transport and receipts",
		"`nova-wake` owns subscriptions",
		"`nova-work` owns assignments",
		"does not infer permission from message content or Git author",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC.md missing %q", want)
		}
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
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC.md missing %q", want)
		}
	}
}
