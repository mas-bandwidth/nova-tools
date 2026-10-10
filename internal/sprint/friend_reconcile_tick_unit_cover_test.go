package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Unit tests for friend_reconcile_tick.go functions: PropFriendDisagree,
// FriendQueueWorking, FriendCollected, and FriendDisagree. The Snapshot is
// built by hand with a fixed Now and the fleet through NewTable(Fleet) and
// SetProps, as presence_cover_test.go does. No real time, no network, no
// subprocess, no live store.

func TestSprintFriendReconcileTickCoverPropFriendDisagree(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "friend_disagree_f1", PropFriendDisagree("f1"))
}

func TestSprintFriendReconcileTickCoverFriendQueueWorking(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		acc  FriendAccount
		want int
	}{
		{
			name: "nil Tasks map",
			acc:  FriendAccount{},
			want: 0,
		},
		{
			name: "mix of working, queued and done",
			acc: FriendAccount{
				Tasks: map[string]string{
					"c1": FriendTaskWorking,
					"c2": FriendTaskQueued,
					"c3": FriendTaskDone,
					"c4": FriendTaskWorking,
				},
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, FriendQueueWorking(tt.acc))
		})
	}
}

func TestSprintFriendReconcileTickCoverFriendCollected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := &Snapshot{
		Now:         now,
		Coordinator: "coord",
	}
	req := FriendCollectedReq{
		Friend:  "f1",
		Card:    "card123",
		Primary: "p1",
		Stream:  "stream1",
		Who:     "user1",
		Why:     "because",
	}
	got := FriendCollected(s, req)
	assert.Len(t, got.Notes, 1)
	n := got.Notes[0]
	assert.Equal(t, NFriendCollected, n.Type)
	assert.Equal(t, "coord", n.To)
	assert.Equal(t, "user1", n.Who)
	assert.Equal(t, "card123 collected from friend f1 by the run loop's reconcile: because", n.What)
	assert.Equal(t, "run: nova-sprint card p1", n.Hint)
}

func TestSprintFriendReconcileTickCoverFriendDisagree(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		s         *Snapshot
		req       FriendDisagreeReq
		wantUnits int
		wantProps int
	}{
		{
			name: "nil Fleet",
			s:    &Snapshot{Now: now},
			req:  FriendDisagreeReq{Friend: "f1", Row: 1, Queue: 2},
		},
		{
			name: "row != queue with no property",
			s: func() *Snapshot {
				f := NewTable(Fleet)
				return &Snapshot{Now: now, Fleet: f}
			}(),
			req:       FriendDisagreeReq{Friend: "f1", Row: 1, Queue: 2, Who: "user1"},
			wantUnits: 1,
			wantProps: 1,
		},
		{
			name: "row != queue with the property already set",
			s: &Snapshot{
				Now: now,
				Fleet: func() *Table {
					f := NewTable(Fleet)
					f.SetProps(map[string]string{"friend_disagree_f1": "20261004T120000"})
					return f
				}(),
			},
			req: FriendDisagreeReq{Friend: "f1", Row: 1, Queue: 2, Who: "user1"},
		},
		{
			name: "row == queue with the property set",
			s: &Snapshot{
				Now: now,
				Fleet: func() *Table {
					f := NewTable(Fleet)
					f.SetProps(map[string]string{"friend_disagree_f1": "20261004T120000"})
					return f
				}(),
			},
			req:       FriendDisagreeReq{Friend: "f1", Row: 1, Queue: 1, Who: "user1"},
			wantUnits: 1,
			wantProps: 1,
		},
		{
			name: "row == queue with no property",
			s: &Snapshot{
				Now: now,
				Fleet: func() *Table {
					f := NewTable(Fleet)
					return f
				}(),
			},
			req: FriendDisagreeReq{Friend: "f1", Row: 1, Queue: 1, Who: "user1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FriendDisagree(tt.s, tt.req)
			assert.Equal(t, tt.wantUnits, len(got.Units))
			assert.Equal(t, tt.wantProps, len(got.Props))
		})
	}
}
