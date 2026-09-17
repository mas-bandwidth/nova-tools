package docs

import (
	"os"
	"strings"
	"testing"
)

// TestNovaRedisSpecFirstSlice pins the first slice of the nova-redis proposal
// (nova-tools #130): an internal ephemeral package used by wake, swarm slots
// and locks, nova-go budgets and plan state, each with a file fallback, and a
// `nova-redis` binary owning the instance (status, spill/recall scratch with
// TTL and owner prefixes, presence, check) bound to localhost and the tailnet
// with auth from nova-secrets and persistence off. Git stays the record; a
// missing file or a section missing one of the named contract terms is a bug.
func TestNovaRedisSpecFirstSlice(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REDIS.md: %v", err)
	}
	content := string(body)

	for _, want := range []string{
		"# nova-redis",
		"`nova-redis`",
		"`status`",
		"`spill`",
		"`recall`",
		"TTL",
		"owner prefix",
		"`presence`",
		"`check`",
		"localhost",
		"tailnet",
		"nova-secrets",
		"persistence",
		"file fallback",
		"wake",
		"swarm slots and locks",
		"budgets",
		"plan state",
		"Git stays the record",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("docs/SPEC-REDIS.md missing %q", want)
		}
	}
}
