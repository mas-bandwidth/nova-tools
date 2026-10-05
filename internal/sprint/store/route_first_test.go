package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// RouteOf reads first from the route hash nova-config's apply writes
// (docs/SPEC-CONFIG.md, route). A hash with first set is drawn before the
// others of its tier; a hash with no first key is not.
func TestRouteOfReadsFirstFromTheHash(t *testing.T) {
	t.Parallel()
	assert.True(t, RouteOf("r", map[string]string{"first": "true"}).First, "first=true is drawn before the others of its tier")
	assert.False(t, RouteOf("r", map[string]string{}).First, "a hash with no first key is not first")
	assert.False(t, RouteOf("r", map[string]string{"first": "false"}).First, "first=false leaves the walk as it is")
}
