package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// TestTheBusReadsTheSessionApplyWrites holds pkg/bus's copy of a friend's applied
// session equal to the one apply writes: the hash, the field and the one-shot word
// nova-bus reads to refuse a send to a one-shot friend (cmd/nova-bus refuseOneShots).
// The bus keeps its own copy so it does not link this package's Postgres driver.
func TestTheBusReadsTheSessionApplyWrites(t *testing.T) {
	t.Parallel()
	assert.Equal(t, FriendDesiredKey("fay"), bus.FriendDesiredKey("fay"))
	assert.Equal(t, FriendSessionOneShot, bus.SessionOneShot)
	assert.Equal(t, "session", bus.SessionField)
	assert.Contains(t, FriendSessionWords, bus.SessionOneShot)
}
