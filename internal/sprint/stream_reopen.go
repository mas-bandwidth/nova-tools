package sprint

import (
	"fmt"
	"strconv"
	"strings"
)

// StreamReopen admits a card to a stream whose stop sentinel has landed, reopening the stream.
// When a card is admitted to a stream with a landed stop sentinel, a new stop sentinel is created,
// the stream state is set back to working, and a note is emitted.
func StreamReopen(s *Snapshot, r AddReq) Plan {
	var p Plan
	p.on(s)

	if r.Sentinel {
		return p // sentinel adds don't reopen streams
	}

	ctl := s.Merge.Card(CtlID(r.Stream))
	if ctl == nil {
		return p // stream doesn't exist, nothing to reopen
	}

	if ctl.F("state") != StreamLanded {
		return p // stream not in landed state
	}

	// Check if there are open cards in this stream
	openCards := Unlanded(s, r.Stream)
	if len(openCards) == 0 {
		return p // no open cards, nothing to reopen
	}

	// Create a new stop sentinel for this stream
	newStopID := newStopSentinelID(s, r.Stream)
	if newStopID == "" {
		return p // could not create stop sentinel id
	}

	// Add the new stop sentinel that needs the new card(s)
	needs := []string{r.IDs[0]}
	if len(r.IDs) > 1 {
		needs = append(needs, r.IDs[1:]...)
	}

	// Build the plan to create the stop sentinel and reopen the stream
	p.Units = append(p.Units, Unit{
		Key: newStopID,
		Stream: r.Stream,
		Changes: []Change{
			change(Work, createEntry(newStopID, r.Stream, Waiting, 0, map[string]string{
				"kind": "sentinel",
				"needs": strings.Join(needs, ","),
				"attempt": "0",
			})),
			change(Merge, setEntry(ctl, map[string]string{
				"state": StreamWaiting,
				"since": stamp(s.Now),
			})),
		},
		Notes: []Note{
			{
				Kind: Happened,
				Stream: r.Stream,
				Time: s.Now,
				What: fmt.Sprintf("STREAM REOPENED %s stop=%s", r.Stream, newStopID),
				Who: r.Who,
			},
		},
		Moved: fmt.Sprintf("stream %s: created sentinel %s, set state to %s", r.Stream, newStopID, StreamWaiting),
	})

	return p
}

// newStopSentinelID generates a new stop sentinel id for the stream.
func newStopSentinelID(s *Snapshot, stream string) string {
	// Count existing stop sentinels for this stream
	stopCount := 0
	prefix := stream + "-stop-"
	for _, c := range s.Work.Cards() {
		if c.Row == stream && c.F("kind") == "sentinel" && strings.HasPrefix(c.ID, prefix) {
			n := strings.TrimPrefix(c.ID, prefix)
			if _, err := strconv.Atoi(n); err == nil {
				if n > stopCount {
					stopCount = n
				}
			}
		}
	}
	return fmt.Sprintf("%s-stop-%d", stream, stopCount+1)
}

// IsStreamClosed says the stream's stop has landed and there are no open cards.
// This is used by where and streams to determine the stream's display state.
func IsStreamClosed(s *Snapshot, stream string) bool {
	ctl := s.Merge.Card(CtlID(stream))
	if ctl == nil {
		return false
	}

	ctlState := ctl.F("state")
	if ctlState != StreamLanded {
		return false
	}

	// Check if there are open cards - if so, the stream is NOT closed yet
	// (it has been reopened but cards remain open)
	open := Unlanded(s, stream)
	return len(open) == 0
}

// OpenCards says whether the stream has any cards not yet landed.
func OpenCards(s *Snapshot, stream string) []*Card {
	return Unlanded(s, stream)
}
