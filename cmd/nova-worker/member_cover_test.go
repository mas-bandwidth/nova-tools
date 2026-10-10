package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// TestMemberCoverWaitReturnsTheChildsDoneChannel pins nativeChild.Wait
// (cmd/nova-worker/member.go:676): the channel it returns is the child's own done
// channel, the one the launch closes at the child's end, and the child answers
// member.Waiter through it, so a Background member's loop blocks there and wakes
// at the end and not later. The child is the struct itself: no process, no store,
// no wall, no socket.
func TestMemberCoverWaitReturnsTheChildsDoneChannel(t *testing.T) {
	t.Parallel()
	c := &nativeChild{card: "c1", done: make(chan struct{})}
	require.Implements(t, (*member.Waiter)(nil), c, "Wait is the child's member.Waiter")
	assert.Equal(t, reflect.ValueOf(c.done).Pointer(), reflect.ValueOf(c.Wait()).Pointer(),
		"Wait is the child's own done channel")
	close(c.done)
	select {
	case <-c.Wait():
		assert.True(t, c.Done(), "the ended child is Done")
	default:
		t.Fatal("Wait does not wake after the child's end")
	}
}

// TestMemberCoverWaitRefusesAnUnendedChild is the refusal: while the child still
// runs, the channel Wait returns is open, so a member blocking on it does not
// wake. The check is one non-blocking receive, never a fixed wait.
func TestMemberCoverWaitRefusesAnUnendedChild(t *testing.T) {
	t.Parallel()
	c := &nativeChild{card: "c1", done: make(chan struct{})}
	select {
	case <-c.Wait():
		t.Fatal("Wait is closed before the child has ended")
	default:
	}
	assert.False(t, c.Done(), "a child that has not ended is not Done")
}
