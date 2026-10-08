package main

// firstrun_recipe_test.go pins the banner's first-run recipe: a throwaway store
// by hand, the same shape nova-table's help gives, so a reader on a shared
// machine never starts against the default port a real store may hold.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirstRunRecipeIsAThrowawayStore: the first run sends the reader to the
// throwaway recipe the banner's note carries, and that recipe names a temp dir,
// a Unix socket and how to stop it, not the default port.
func TestFirstRunRecipeIsAThrowawayStore(t *testing.T) {
	t.Parallel()
	line := ""
	for _, l := range strings.Split(usage, "\n") {
		if strings.HasPrefix(l, "first run:") {
			line = l
			break
		}
	}
	require.NotEmpty(t, line, "the banner has no first-run line:\n%s", usage)
	assert.Contains(t, line, "--dry-run needs no store", line)
	assert.NotContains(t, line, "127.0.0.1:6379", "the first run still points at the default port a real store holds: %s", line)
	assert.Contains(t, usage, "mktemp -d", usage)
	assert.Contains(t, usage, "--unixsocket", usage)
	assert.Contains(t, usage, "shutdown nosave", usage)
}
