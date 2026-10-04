package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Unit tests for StrangerNotes (presence.go), the plan that tells the
// coordinator of each unknown machine that beats: the note's main path and its
// refusal (a machine that has a row in the fleet by now). The Snapshot is
// built by hand, the fleet rows through the table's own seam: no store, no
// clock, no network, no subprocess, no sleep.

func TestPresenceCoverStrangerNotes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	fleet := func(rows ...string) *Table {
		f := NewTable(Fleet)
		f.SetRows(rows)
		return f
	}
	note := func(machine string) Note {
		return Note{Kind: Happened, Type: NUnknownMachine, Who: MachineActor, At: now,
			What: "an unknown machine is beating: " + machine + "; add it with nova-sprint fleet up " + machine}
	}
	tests := []struct {
		name  string
		s     *Snapshot
		names []string
		want  []Note
	}{
		{
			name:  "an unknown machine that beats gets one happened note",
			s:     &Snapshot{Now: now, Fleet: fleet()},
			names: []string{"m2"},
			want:  []Note{note("m2")},
		},
		{
			name:  "several strangers come out in name order",
			s:     &Snapshot{Now: now, Fleet: fleet()},
			names: []string{"m3", "m2"},
			want:  []Note{note("m2"), note("m3")},
		},
		{
			name:  "a machine that has a row in the fleet by now gets no note",
			s:     &Snapshot{Now: now, Fleet: fleet("m1", "m2")},
			names: []string{"m2", "m3"},
			want:  []Note{note("m3")},
		},
		{
			name:  "every name known: the plan holds no note",
			s:     &Snapshot{Now: now, Fleet: fleet("m1", "m2")},
			names: []string{"m2", "m1"},
			want:  nil,
		},
		{
			name:  "no names: the plan holds no note",
			s:     &Snapshot{Now: now, Fleet: fleet("m1")},
			names: nil,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, StrangerNotes(tt.s, tt.names).Notes)
		})
	}
}