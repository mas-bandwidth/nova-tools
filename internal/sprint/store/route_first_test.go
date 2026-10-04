package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRouteOfReadsFirst is the hash nova-config apply writes (docs/SPEC-CONFIG.md,
// the route field first): "true" sets the field the deal reads, and a row from
// before the field parses as not first.
func TestRouteOfReadsFirst(t *testing.T) {
	t.Parallel()
	got := RouteOf("pro-a", map[string]string{"tier": "pro", "enabled": "true", "first": "true"})
	assert.True(t, got.First, "first true did not round-trip")
	old := RouteOf("pro-a", map[string]string{"tier": "pro", "enabled": "true"})
	assert.False(t, old.First, "a row with no first field parsed as first")
}
