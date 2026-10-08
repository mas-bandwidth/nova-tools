package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestFriendIdleNotLoaded: friend with no taken cards is not idle-loaded.
func TestFriendIdleNotLoaded(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	// No cards for this friend
	idle, _ := idleLoad(w.s, FriendSeat{Name: "alice", Width: 3, Status: Up})
	assert.False(t, idle, "expected friend with no cards not to be idle-loaded")
}

// TestFriendIdleDown: friend with status down is not idle-loaded.
func TestFriendIdleDown(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	idle, _ := idleLoad(w.s, FriendSeat{Name: "alice", Width: 3, Status: Down})
	assert.False(t, idle, "expected friend with status down not to be idle-loaded")
}

// TestFriendIdleReturnNotData: return returns false with no data.
func TestFriendIdleReturnNotData(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	friend := FriendSeat{Name: "alice", Width: 3, Status: Up}
	returned := ruleFriendIdleReturn(w.s, friend, 120)
	assert.False(t, returned, "expected no return when no data")
}

// TestComposeWidthGoal: width goal message composition.
func TestComposeWidthGoal(t *testing.T) {
	t.Parallel()
	msg := composeWidthGoal("alice", 3, 2, 1, 45)
	assert.NotEmpty(t, msg, "expected non-empty width goal message")
	assert.Contains(t, msg, "alice", "expected message to contain friend name")
}

// TestFriendIdleSetting: friend-idle setting changes the bound.
func TestFriendIdleSetting(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	// Set the property using the settings mechanism
	w.must(Set(w.s, SetReq{FriendIdle: "5m"}))
	assert.Equal(t, 5*time.Minute, w.s.FriendIdleAfter(), "expected 5m idle bound")
}
