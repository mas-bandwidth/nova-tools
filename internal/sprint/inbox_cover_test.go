package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Unit tests for the pure inbox helpers of internal/sprint/inbox.go that no unit
// test reaches yet: Stalled, StaleStream and NoteCommands. Each covers the
// function's main path and one refusal, with no sleeps, no real time and no
// network: the inputs are plain time values and the outputs are plain values.
//
// Libraries considered: only the Go standard library (testing, time) and
// testify/assert, already in the tree.

// TestInboxCoverStalled covers StreamClock.Stalled (inbox.go:33): a stream that
// has not landed and whose last progress is older than stale is stalled; a
// landed stream, a zero stale, a zero progress, or fresh progress is refused
// the staleness.
func TestInboxCoverStalled(t *testing.T) {
	t.Parallel()
	now := t0
	progress := t0.Add(-2 * time.Hour)
	tests := []struct {
		name  string
		clk   StreamClock
		stale time.Duration
		want  bool
	}{
		{
			name:  "not landed and past the stale mark",
			clk:   StreamClock{State: StreamWaiting, Progress: progress},
			stale: time.Hour,
			want:  true,
		},
		{
			name:  "the stream landed: not stalled",
			clk:   StreamClock{State: StreamLanded, Progress: progress},
			stale: time.Hour,
			want:  false,
		},
		{
			name:  "a zero stale: not stalled",
			clk:   StreamClock{State: StreamWaiting, Progress: progress},
			stale: 0,
			want:  false,
		},
		{
			name:  "no progress yet: not stalled",
			clk:   StreamClock{State: StreamWaiting},
			stale: time.Hour,
			want:  false,
		},
		{
			name:  "progress is fresh: not stalled",
			clk:   StreamClock{State: StreamWaiting, Progress: now.Add(-time.Minute)},
			stale: time.Hour,
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.clk.Stalled(now, tt.stale))
		})
	}
}

// TestInboxCoverStaleStream covers StaleStream (inbox.go:142): a stale:<card id>
// group id yields the card id it names; anything else is refused.
func TestInboxCoverStaleStream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		id     string
		wantID string
		wantOk bool
	}{
		{
			name:   "a stale group id yields its card id",
			id:     StaleGroupID("s1", 7),
			wantID: "s1",
			wantOk: true,
		},
		{
			name:   "a regular note id is refused",
			id:     "n1",
			wantID: "",
			wantOk: false,
		},
		{
			name:   "the stale prefix alone is refused",
			id:     "stale:",
			wantID: "",
			wantOk: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := StaleStream(tt.id)
			assert.Equal(t, tt.wantID, got)
			assert.Equal(t, tt.wantOk, ok)
		})
	}
}

// TestInboxCoverNoteCommands covers NoteCommands (inbox.go:350): an open
// judgment's decisions render as the commands that make them, one command each;
// a note with no decisions renders none.
func TestInboxCoverNoteCommands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		note    Note
		members []string
		want    []Command
	}{
		{
			name:    "a blocked judgment renders its ack command",
			note:    Note{ID: "n1", Kind: Judgment, Type: NBlocked, Stream: "s1", Decisions: []string{"ack"}},
			members: []string{"p1"},
			want: []Command{
				{Decision: "ack", Lines: []string{"nova-sprint ack n1 --reason " + noneText}},
			},
		},
		{
			name:    "a note with no decisions renders no commands",
			note:    Note{ID: "n2", Kind: Judgment, Type: NBlocked},
			members: []string{"p1"},
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NoteCommands(tt.note, tt.members))
		})
	}
}
