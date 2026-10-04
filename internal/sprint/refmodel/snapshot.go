package refmodel

import (
	"maps"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Snapshot is the whole state one tick of the scanning machine decides from:
// the four tables and the judgments open on them, and the few facts the tick
// reads beside them, which the store keeps outside the tables. It holds what
// the tick reads and nothing it writes, so a snapshot is the machine's whole
// input, and equal snapshots are decided alike.
type Snapshot struct {
	// Tables is the four tables (unplaced records of the needs of waiting
	// primaries included, as the tick loads them), the open judgments and
	// the acknowledged conditions, the coordinator and the epoch. Its Now is
	// not read: the time is an argument of Decide.
	Tables *sprint.Snapshot
	// Running says the machine is RUNNING. A STOPPED machine moves nothing.
	Running bool
	// Since is when the machine last started: the first tick after it pushes
	// every reminder.
	Since time.Time
	// Stopped is the spans the machine was STOPPED: a deadline compares
	// running time, which leaves them out.
	Stopped []sprint.Span
	// Beats is each fleet member's last beat record; a member that never
	// beat has none. nil is none read, and the presence duty does nothing.
	Beats map[string]sprint.Beat
	// Goals is the sprint's people, their goals and routes.
	Goals sprint.Goals
	// Untold is the unknown machines that beat and whom the coordinator was
	// not told of yet.
	Untold []string
}

// Clone is a copy of the snapshot that shares nothing with it, so what is done
// to one is never seen in the other. Every duty decides on a clone, which is
// why deciding leaves the caller's snapshot as it was, whatever the planners
// do to what they read.
func (s Snapshot) Clone() Snapshot {
	c := s
	c.Tables = cloneTables(s.Tables)
	c.Stopped = slices.Clone(s.Stopped)
	c.Beats = cloneBeats(s.Beats)
	c.Goals = sprint.Goals{People: slices.Clone(s.Goals.People), Noted: maps.Clone(s.Goals.Noted)}
	c.Untold = slices.Clone(s.Untold)
	return c
}

// needTables refuses a snapshot that lacks one of the four tables, in words,
// rather than let a planner find the nil.
func (s Snapshot) needTables() {
	t := s.Tables
	switch {
	case t == nil:
		panic("refmodel: the snapshot has no tables")
	case t.Work == nil || t.Readers == nil || t.Merge == nil || t.Fleet == nil:
		panic("refmodel: the snapshot lacks one of the four tables: the tick reads them all")
	}
}

// stopped is the running-time function the tick is given: the time the
// machine was STOPPED between two clock readings.
func (s Snapshot) stopped() func(from, to time.Time) time.Duration {
	return func(from, to time.Time) time.Duration { return sprint.StoppedBetween(s.Stopped, from, to) }
}

// tickReq is what the tick is given beside its tables.
func (s Snapshot) tickReq() sprint.TickReq {
	return sprint.TickReq{Who: sprint.MachineActor, Stopped: s.stopped(), Beats: s.Beats}
}

func cloneTables(s *sprint.Snapshot) *sprint.Snapshot {
	if s == nil {
		return nil
	}
	c := *s
	c.Work, c.Readers, c.Merge, c.Fleet = cloneTable(s.Work), cloneTable(s.Readers), cloneTable(s.Merge), cloneTable(s.Fleet)
	c.Open, c.Acked = cloneOpen(s.Open), cloneOpen(s.Acked)
	return &c
}

func cloneTable(t *sprint.Table) *sprint.Table {
	if t == nil {
		return nil
	}
	c := sprint.NewTable(t.Name)
	c.Epoch, c.Revision = t.Epoch, t.Revision
	c.SetRows(slices.Clone(t.Rows()))
	for row, texts := range t.Texts {
		c.Texts[row] = maps.Clone(texts)
	}
	for _, card := range t.Cards() {
		c.Put(&sprint.Card{ID: card.ID, Row: card.Row, Col: card.Col, Score: card.Score, Rev: card.Rev, Fields: maps.Clone(card.Fields)})
	}
	return c
}

func cloneOpen(os []sprint.Open) []sprint.Open {
	if os == nil {
		return nil
	}
	out := make([]sprint.Open, len(os))
	for i, o := range os {
		n := o.Note
		n.Primaries, n.Decisions = slices.Clone(n.Primaries), slices.Clone(n.Decisions)
		n.Suspects, n.Needs = slices.Clone(n.Suspects), slices.Clone(n.Needs)
		out[i] = sprint.Open{Key: o.Key, Note: n}
	}
	return out
}

func cloneBeats(bs map[string]sprint.Beat) map[string]sprint.Beat {
	if bs == nil {
		return nil
	}
	out := make(map[string]sprint.Beat, len(bs))
	for m, b := range bs {
		b.Samples = slices.Clone(b.Samples)
		if b.Meter.Ticks != nil {
			t := *b.Meter.Ticks
			b.Meter.Ticks = &t
		}
		out[m] = b
	}
	return out
}
