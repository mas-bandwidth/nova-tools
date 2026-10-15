package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A friend's card names her own build cache, by path under her own working directory, and
// says it is hers and warm across her cards (FriendGoCacheLine, ported from nova-sprint's
// templates.go; cmd/nova-sprint's friendBrief writes it into the working-directory line).
func TestFriendGoCacheLineNamesHerOwnCache(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "GOCACHE=~/amy-working/.cache/go-build is your own build cache, warm across your cards: keep it", FriendGoCacheLine("amy"))
	assert.Contains(t, FriendGoCacheLine("stella"), "~/stella-working/.cache/go-build")
	assert.NotEqual(t, GoCacheLine, FriendGoCacheLine("amy"), "a friend's cache is not the machine's shared one")
}
