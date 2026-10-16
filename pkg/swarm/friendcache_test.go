package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFriendGoCacheLineNamesHerOwnCache pins FriendGoCacheLine: the cache sentence of a
// friend's card names her own build cache, the warm one that persists across her cards.
func TestFriendGoCacheLineNamesHerOwnCache(t *testing.T) {
	t.Parallel()
	s := FriendGoCacheLine("alice")
	assert.Contains(t, s, "GOCACHE")
	assert.Contains(t, s, "alice")
	assert.Contains(t, s, ".cache/go-build")
	assert.Contains(t, s, "own build cache")
	assert.Contains(t, s, "warm across your cards")
}
