package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCoordinatorHandoverAndWorkerLossIsSpecified pins the coordination
// requirement from nova-tools#180 on docs/SPEC-WORK.md. The issue is a proposed
// integration outcome, not a claim that an unattended failover deployment
// exists: a team configures its coordinator set, eligibility, succession and
// authority scope; coordinator redundancy must cross independent providers and
// name shared failure domains; explicit exhaustion and expiry of liveness are
// detected separately; a durable, validated lease fences the one effective
// owner across partitions; and a replacement acts only within previously
// configured authority, never overriding a rest/stop decision and never
// treating silence as consent. This package's charter (doc.go) is exactly a
// check about the repo, read as text, so the spec cannot silently drop the
// requirement.
func TestCoordinatorHandoverAndWorkerLossIsSpecified(t *testing.T) {
	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-WORK.md"))

	required := []string{
		// The configured set and its authority, with no embedded friends.
		"Automatic handover and worker-loss recovery",
		"coordinator set, eligibility, succession and authority scope",
		"no particular friend is embedded",
		// Redundancy is real only across independent providers.
		"independent providers",
		"shared failure domain",
		// Exhaustion and liveness expiry are separate facts.
		"explicit exhaustion",
		"expiry of liveness",
		// Durable, fenced ownership across partitions.
		"validated lease",
		"one effective owner",
		"no conflicting publication",
		// What transfers, and the in-flight reconciliation.
		"exact revisions",
		"ready queue",
		"cost reservations",
		"deferred decisions",
		"persistent stop controls",
		"reconcile in-flight workers",
		// Shared capacity reserved for coordination and recovery.
		"reserve configured shared account capacity",
		// Partial failure and stale state are never trusted.
		"late usage",
		"duplicate notifications",
		"returning friends",
		"stale state",
		// Authority limits: silence is not consent.
		"rest or stop decision",
		"create credentials",
		"expand access",
		"silence is not consent",
		// The honest unavailable case.
		"recoverable state",
		"concrete unavailable function",
		// The fault-injection acceptance evidence.
		"fault-injection",
		"during dispatch",
		"partitions the old coordinator",
		"late result",
		"preserved stop state",
		"sleeping worker",
		"shared-state service loss",
		"all coordinators unavailable",
	}
	for _, want := range required {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md does not carry the coordinator handover and worker-loss requirement %q (#180)", want)
		}
	}
}
