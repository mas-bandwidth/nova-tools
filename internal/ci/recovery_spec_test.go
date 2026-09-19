package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRecoverWhenRemoteServiceReturnsIsSpecified pins the coordination recovery
// requirement from nova-tools#187 on docs/SPEC-WORK.md. Glenn's refinement
// (2026-09-13) is that a temporary GitHub or connectivity outage must not require a
// human to say "resume" after the service returns: transient failures are classified
// apart from auth/validation refusals, retried under a capped backoff that honors the
// service's own Retry-After signal, reconciled through a stable operation identity so
// a successful-but-unanswered write is never replayed, and resumed automatically once
// the service returns -- and it is explicitly future implementation scope, not a claim
// that a recovery watcher is deployed today. The package's charter (doc.go) is exactly
// this: a check about the repo, read as text, so the spec cannot silently drop the
// requirement.
func TestRecoverWhenRemoteServiceReturnsIsSpecified(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-WORK.md"))

	required := []string{
		"Recover automatically when a remote service returns",
		"transient",
		"Retry-After",
		"health probes",
		"stable operation identity",
		"without a human",
		"future implementation scope",
	}
	for _, want := range required {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md does not carry the recovery-when-the-remote-returns requirement %q", want)
		}
	}
}
