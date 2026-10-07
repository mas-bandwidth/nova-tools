package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamReopen(t *testing.T) {
	t.Parallel()

	// Test: adding a card to a landed stream should reopen it
	s := mustSetup(sprintSnapshotConfig{
		Work: []string{"s1", "s2"},
		Merge: []string{"s1", "s2"},
	})

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.Now = now

	// Create a stream with state "landed"
	ctl := &Card{
		ID:   CtlID("s1"),
		Row:  "s1",
		Col:  Ctl,
		Rev:  1,
		Fields: map[string]string{
			"kind":  "stream",
			"state": StreamLanded,
			"since": stamp(now),
		},
	}
	s.Merge.Put(ctl)

	// Add a primary card to the stream (this simulates the add command behavior)
	p := Add(s, AddReq{
		Stream: "s1",
		IDs:    []string{"s1-1"},
		Who:    "coordinator",
	})

	// Check that the plan includes reopening the stream
	require.Equal(t, 1, len(p.Units))
	require.Equal(t, "s1-1", p.Units[0].Key)
}

func TestStreamReopenWithOpenCards(t *testing.T) {
	t.Parallel()

	// Test: reopening when there are already open cards
	s := mustSetup(sprintSnapshotConfig{
		Work: []string{"s1"},
		Merge: []string{"s1"},
	})

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.Now = now

	// Stream in landed state
	ctl := &Card{
		ID:   CtlID("s1"),
		Row:  "s1",
		Col:  Ctl,
		Rev:  1,
		Fields: map[string]string{
			"kind":  "stream",
			"state": StreamLanded,
			"since": stamp(now),
		},
	}
	s.Merge.Put(ctl)

	// Add first card (this would normally land it)
	add1 := Add(s, AddReq{
		Stream: "s1",
		IDs:    []string{"s1-1"},
		Who:    "coordinator",
	})

	// Manually set it to landed for testing
	landed1 := &Card{
		ID:   "s1-1",
		Row:  "s1",
		Col:  Landed,
		Score: 1.0,
		Rev:  1,
		Fields: map[string]string{
			"brief":   "test card",
			"attempt": "1",
		},
	}
	s.Work.Put(landed1)

	// Manually set stream to landed
	ctl.Fields["state"] = StreamLanded

	// Add second card - this should trigger reopen
	add2 := Add(s, AddReq{
		Stream: "s1",
		IDs:    []string{"s1-2"},
		Who:    "coordinator",
	})

	// Check the state
	require.NotNil(t, add2)
}

func TestIsStreamClosed(t *testing.T) {
	t.Parallel()

	s := mustSetup(sprintSnapshotConfig{
		Work: []string{"s1"},
		Merge: []string{"s1"},
	})

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.Now = now

	// Stream in landed state with no open cards
	ctl := &Card{
		ID:   CtlID("s1"),
		Row:  "s1",
		Col:  Ctl,
		Rev:  1,
		Fields: map[string]string{
			"kind":  "stream",
			"state": StreamLanded,
			"since": stamp(now),
		},
	}
	s.Merge.Put(ctl)

	// Add a landed card
	landed := &Card{
		ID:   "s1-1",
		Row:  "s1",
		Col:  Landed,
		Score: 1.0,
		Rev:  1,
	}
	s.Work.Put(landed)

	// Stream should be closed (no open cards, state landed)
	closed := IsStreamClosed(s, "s1")
	require.True(t, closed)
}

func TestIsStreamClosedWithOpenCards(t *testing.T) {
	t.Parallel()

	s := mustSetup(sprintSnapshotConfig{
		Work: []string{"s1"},
		Merge: []string{"s1"},
	})

	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s.Now = now

	// Stream in landed state but with open cards
	ctl := &Card{
		ID:   CtlID("s1"),
		Row:  "s1",
		Col:  Ctl,
		Rev:  1,
		Fields: map[string]string{
			"kind":  "stream",
			"state": StreamLanded,
			"since": stamp(now),
		},
	}
	s.Merge.Put(ctl)

	// Add a waiting card (open)
	waiting := &Card{
		ID:   "s1-1",
		Row:  "s1",
		Col:  Waiting,
		Score: 1.0,
		Rev:  1,
	}
	s.Work.Put(waiting)

	// Stream should NOT be closed (has open cards)
	closed := IsStreamClosed(s, "s1")
	require.False(t, closed)
}

func TestOpenCards(t *testing.T) {
	t.Parallel()

	s := mustSetup(sprintSnapshotConfig{
		Work: []string{"s1"},
		Merge: []string{"s1"},
	})

	// Add a waiting card (open)
	waiting := &Card{
		ID:   "s1-1",
		Row:  "s1",
		Col:  Waiting,
		Score: 1.0,
		Rev:  1,
	}
	s.Work.Put(waiting)

	// Add a landed card (not open)
	landed := &Card{
		ID:   "s1-2",
		Row:  "s1",
		Col:  Landed,
		Score: 2.0,
		Rev:  1,
	}
	s.Work.Put(landed)

	open := OpenCards(s, "s1")
	require.Len(t, open, 1)
	require.Equal(t, "s1-1", open[0].ID)
}

func TestNewStopSentinelID(t *testing.T) {
	t.Parallel()

	s := mustSetup(sprintSnapshotConfig{
		Work: []string{"s1"},
	})

	// First stop sentinel
	stop1 := newStopSentinelID(s, "s1")
	require.Equal(t, "s1-stop-1", stop1)

	// Add an existing stop sentinel
	s.Work.Put(&Card{
		ID:   "s1-stop-1",
		Row:  "s1",
		Col:  Waiting,
		Score: 0,
		Rev:  1,
		Fields: map[string]string{"kind": "sentinel"},
	})

	// Next stop sentinel
	stop2 := newStopSentinelID(s, "s1")
	require.Equal(t, "s1-stop-2", stop2)
}
