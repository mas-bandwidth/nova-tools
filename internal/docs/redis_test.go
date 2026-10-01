package docs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaRedisSpecFirstSlice pins the nova-redis specification's contract
// terms: the `nova-redis` binary owning the instance (serve, and spill/recall
// scratch with TTL and owner prefixes) bound to localhost and the tailnet with
// auth from nova-secrets and the AOF on. Git stays the record; a missing file or
// a section missing one of the named contract terms is a bug.
func TestNovaRedisSpecFirstSlice(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	require.NoError(t, err, "docs/SPEC-REDIS.md: %v", err)
	content := string(body)

	for _, want := range []string{
		"# nova-redis",
		"`nova-redis`",
		"`serve`",
		"`spill`",
		"`recall`",
		"TTL",
		"owner prefix",
		"localhost",
		"tailnet",
		"nova-secrets",
		"Persistence",
		"Git stays the record",
	} {
		assert.Contains(t, content, want, "docs/SPEC-REDIS.md missing %q", want)
	}
}
