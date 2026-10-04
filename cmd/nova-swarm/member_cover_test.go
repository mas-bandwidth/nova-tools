// cmd/nova-swarm's cover for nativeChild.Wait (member.go:676): the Waiter
// seam the member loop wakes on (internal/member, `go func() { <-w.Wait(); m.woken() }`),
// which no unit test reached before, leaving Wait at 0.0% in go tool cover -func.
// These reach it by constructing a nativeChild directly, closing (or leaving open)
// its done channel, with no process, no clock and no store.
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// compile-time check: a *nativeChild satisfies member.Waiter, the seams the
// member loop holds.
var _ member.Waiter = (*nativeChild)(nil)

// TestMemberCoverWait pins nativeChild.Wait (cmd/nova-swarm/member.go:676): it
// returns the child's done channel, which the member loop waits on to learn a
// child has ended. The main path is a child that has ended: its done channel is
// closed, so Wait returns a channel that is already closed. The refusal is a
// child still running: its done channel is open, so Wait returns a channel that
// cannot be received from without the child ending.
func TestMemberCoverWait(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		closed bool // whether the child's done channel is closed (the child ended)
	}{
		{"child ended, Wait returns a closed channel", true},
		{"child not ended, Wait returns an open channel", false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			done := make(chan struct{})
			if tc.closed {
				close(done)
			}
			c := &nativeChild{card: "c1", logPath: "log", results: "res", job: "j", done: done}
			// reach Wait through the member.Waiter interface a *nativeChild satisfies
			var w member.Waiter = c
			ch := w.Wait()
			require.NotNil(t, ch, "Wait returns the done channel")
			isClosed := false
			select {
			case <-ch:
				isClosed = true
			default:
			}
			assert.Equal(t, tc.closed, isClosed, "Wait's channel is %s when %s",
				map[bool]string{true: "closed", false: "open"}[isClosed], tc.name)
		})
	}
}
