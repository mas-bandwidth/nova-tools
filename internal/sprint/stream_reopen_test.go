package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSentinelID verifies sentinel ID generation.
func TestSentinelID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "stream-1-stop-1", SentinelID("stream-1", 1))
	assert.Equal(t, "stream-name-stop-5", SentinelID("stream-name", 5))
}

// TestSentinelName verifies sentinel name extraction from ID.
func TestSentinelName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "stream-1", SentinelName("stream-1-stop-1"))
	assert.Equal(t, "stream-name", SentinelName("stream-name-stop-5"))
	assert.Equal(t, "not-a-sentinel", SentinelName("not-a-sentinel"))
}

// TestIsStopSentinel verifies stop sentinel identification.
func TestIsStopSentinel(t *testing.T) {
	t.Parallel()

	assert.True(t, IsStopSentinel("stream-1-stop-1", "stream-1"))
	assert.True(t, IsStopSentinel("stream-name-stop-5", "stream-name"))
	assert.False(t, IsStopSentinel("stream-1-stop-1", "stream-2"))
	assert.False(t, IsStopSentinel("not-a-sentinel", "stream-1"))
}

// TestIsPushedUnreported verifies pushed-unreported detection.
func TestIsPushedUnreported(t *testing.T) {
	t.Parallel()

	assert.True(t, IsPushedUnreported("pushed-unreported abc123"))
	assert.False(t, IsPushedUnreported("regular-card-id"))
}

// TestPushedUnreportedID verifies pushed-unreported ID extraction.
func TestPushedUnreportedID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "abc123", PushedUnreportedID("pushed-unreported abc123"))
}

// TestStreamStateTextClosed verifies stream state display for closed streams.
func TestStreamStateTextClosed(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "closed", StreamStateTextClosed(map[string]string{"state": StreamLanded}))
	assert.Equal(t, "stopped", StreamStateTextClosed(map[string]string{"state": StreamStopped}))
	assert.Equal(t, "waiting", StreamStateTextClosed(map[string]string{"state": StreamWaiting}))
	assert.Equal(t, "merging", StreamStateTextClosed(map[string]string{"state": StreamMerging}))
}

// TestStreamState verifies stream state display.
func TestStreamState(t *testing.T) {
	t.Parallel()

	// Landed state with no open cards
	ctl := &Card{
		ID: CtlID("stream"),
		Row: "stream",
		Col: Ctl,
		Fields: map[string]string{
			"state": StreamLanded,
		},
	}
	assert.Equal(t, "closed", StreamState(ctl, "stream"))

	// Stopped state
	ctl = &Card{
		ID: CtlID("stream"),
		Row: "stream",
		Col: Ctl,
		Fields: map[string]string{
			"state": StreamStopped,
		},
	}
	assert.Equal(t, "stopped", StreamState(ctl, "stream"))

	// Nil control card
	assert.Equal(t, "", StreamState(nil, "stream"))
}

// TestStreamLandedWithOpen verifies detection of landed streams with open cards.
func TestStreamLandedWithOpen(t *testing.T) {
	t.Parallel()

	// Create a snapshot with a landed stream
	s := &Snapshot{
		Work: NewTable(Work),
		Merge: NewTable(Merge),
		Fleet: NewTable(Fleet),
		Readers: NewTable(Readers),
		Open: []Open{},
	}

	// Stream with landed state but no open cards
	ctl := &Card{
		ID: CtlID("stream"),
		Row: "stream",
		Col: Ctl,
		Fields: map[string]string{
			"state": StreamLanded,
		},
	}
	s.Merge.Put(ctl)
	assert.False(t, StreamLandedWithOpen(s, "stream"))

	// Stream with landed state and open cards
	s.Work.Put(&Card{
		ID: "card-1",
		Row: "stream",
		Col: Waiting,
		Fields: map[string]string{},
	})
	assert.True(t, StreamLandedWithOpen(s, "stream"))

	// Stream with stopped state and open cards
	ctl = &Card{
		ID: CtlID("stream2"),
		Row: "stream2",
		Col: Ctl,
		Fields: map[string]string{
			"state": StreamStopped,
		},
	}
	s.Merge.Put(ctl)
	s.Work.Put(&Card{
		ID: "card-2",
		Row: "stream2",
		Col: Waiting,
		Fields: map[string]string{},
	})
	assert.False(t, StreamLandedWithOpen(s, "stream2"))
}

// TestStreamReopenReq verifies the request structure.
func TestStreamReopenReq(t *testing.T) {
	t.Parallel()

	r := StreamReopenReq{
		Stream: "test-stream",
		Card:   "card-1",
		Who:    "test-who",
	}
	assert.Equal(t, "test-stream", r.Stream)
	assert.Equal(t, "card-1", r.Card)
	assert.Equal(t, "test-who", r.Who)
}

// TestFieldStop verifies the stop field constant.
func TestFieldStop(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "stop", FieldStop)
}
