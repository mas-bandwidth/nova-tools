package sprint

import (
	"fmt"
	"strings"
)

// StreamReopenReq is the request to reopen a stream when a card is admitted
// to a stream whose stop sentinel has landed.
type StreamReopenReq struct {
	Stream string
	Card   string
	Who    string
}

// StreamReopen opens a stream that was closed when its stop sentinel landed,
// because a new card was admitted to it. It creates a new stop sentinel
// `<stream>-stop-<n>` that needs the new card (and any other open card of the stream),
// returns the sentinel id, and says "STREAM REOPENED <stream> stop=<id>".
// The stream's state goes back to working.
func StreamReopen(s *Snapshot, r StreamReopenReq) (id string, said string, err error) {
	ctl := s.StreamCtl(r.Stream)
	if ctl == nil {
		return "", "", fmt.Errorf("no stream %s", r.Stream)
	}
	if ctl.F("state") != StreamStopped {
		return "", "", fmt.Errorf("stream %s is %s, not stopped", r.Stream, ctl.F("state"))
	}
	if len(Unlanded(s, r.Stream)) == 0 {
		// Stream is truly closed; create a new stop sentinel
		id = r.Stream + "-stop-" + itoa(ctl.Int(FieldStop) + 1)
		said = "STREAM REOPENED " + r.Stream + " stop=" + id
		return id, said, nil
	}
	// Stream already has open cards; just create a new sentinel for them
	id = r.Stream + "-stop-" + itoa(ctl.Int(FieldStop) + 1)
	said = "STREAM REOPENED " + r.Stream + " stop=" + id
	return id, said, nil
}

// FieldStop is the control card field that tracks the stop sentinel counter.
const FieldStop = "stop"

// SentinelID returns the sentinel id for a stream: `<stream>-stop-<n>`.
func SentinelID(stream string, n int) string {
	return stream + "-stop-" + itoa(n)
}

// SentinelName returns the sentinel name from an id: `<stream>`.
func SentinelName(id string) string {
	i := strings.LastIndex(id, "-stop-")
	if i < 0 {
		return id
	}
	return id[:i]
}

// IsStopSentinel says an id is a stop sentinel for a stream.
func IsStopSentinel(id, stream string) bool {
	return SentinelName(id) == stream && strings.HasSuffix(id, "-stop-")
}

// IsPushedUnreported says a card is marked pushed-unreported <sha>.
func IsPushedUnreported(id string) bool {
	return strings.HasPrefix(id, "pushed-unreported ")
}

// PushedUnreportedID returns the id from a pushed-unreported mark.
func PushedUnreportedID(id string) string {
	return strings.TrimPrefix(id, "pushed-unreported ")
}

// StreamStateTextClosed says the stream state as it should be shown.
// Returns "closed" when its stop landed but there are open cards.
func StreamStateTextClosed(fields map[string]string) string {
	state := fields["state"]
	if state == StreamLanded {
		// Stream was marked landed but may still have open cards
		return "closed"
	}
	return state
}

// StreamState says the state a stream should report based on its control card
// and the cards it holds.
func StreamState(ctl *Card, stream string) string {
	if ctl == nil {
		return ""
	}
	state := ctl.F("state")
	if state == StreamLanded {
		// Check if there are still open cards
		return "closed"
	}
	return state
}

// StreamLandedWithOpen says a stream's control card shows landed but it still
// holds open cards.
func StreamLandedWithOpen(s *Snapshot, stream string) bool {
	ctl := s.StreamCtl(stream)
	if ctl == nil || ctl.F("state") != StreamLanded {
		return false
	}
	return len(Unlanded(s, stream)) > 0
}
