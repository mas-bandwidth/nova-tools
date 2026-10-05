package sprint

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// A friend's reader room (the owner, 2026-10-05: "Reads are in extra slots
// per-friend! Read slots are different from worker cards."). Her card width
// is how many jobs she works. Her read slots are how many reads reader-<friend>
// runs at once. Friend sync writes the map on the fleet; ReaderWidth reads it.
// The ask gives her nothing when the number is 0.

// PropFriendReadSlots is the fleet property friend sync writes: a JSON object
// of friend name to her read slots. A name absent from the map is not a
// friend, and her reader keeps the machine width (or unbounded room).
const PropFriendReadSlots = "friend_read_slots"

// PropFriendReadWait is the fleet property friend sync writes beside
// PropFriendReadSlots: a JSON object of friend name to her read wait in
// seconds, how long a read asked of her reader may wait not begun before the
// tick raises the readers-behind judgment on that reader (readWaitsConds).
// A name the map lacks, or a value below 1, waits ReadersWindow.
const PropFriendReadWait = "friend_read_wait"

// friendReadSlots is the friend's read slots when reader is reader-<name> and
// the fleet property names her. The boolean is false when she is not in the
// map, the property is absent, or it is not that JSON: 0 in the map is a real
// room, not the default.
func (s *Snapshot) friendReadSlots(reader string) (int, bool) {
	return s.friendProp(PropFriendReadSlots, reader)
}

// friendProp is the friend's number in the fleet property prop, a JSON object
// of friend name to a whole number, when reader is reader-<name>.
func (s *Snapshot) friendProp(prop, reader string) (int, bool) {
	if s == nil || s.Fleet == nil {
		return 0, false
	}
	name, ok := ReaderMachine(reader)
	if !ok {
		return 0, false
	}
	raw, ok := s.Fleet.Prop(prop)
	if !ok || raw == "" {
		return 0, false
	}
	var m map[string]int
	if json.Unmarshal([]byte(raw), &m) != nil {
		return 0, false
	}
	n, ok := m[name]
	return n, ok
}

// readWaitBound is how long a friend reader's asked read waits before
// readWaitsConds raises it: her read wait in seconds (PropFriendReadWait)
// when it is 1 or more, otherwise ReadersWindow.
func readWaitBound(s *Snapshot, reader string) time.Duration {
	if n, ok := s.friendProp(PropFriendReadWait, reader); ok && n >= 1 {
		return time.Duration(n) * time.Second
	}
	return ReadersWindow
}

// IsFriendReader says the reader is reader-<friend> for a friend the fleet's
// friend_read_slots map names: its reads are judged by readWaitsConds, one
// judgment on the reader, and not by the sprint's readers-behind line.
func (s *Snapshot) IsFriendReader(reader string) bool {
	_, ok := s.friendReadSlots(reader)
	return ok
}

// readWaitsConds is one readers-behind condition per friend reader who is up
// and holds a read asked and not begun for readWaitBound or longer: its
// subject is the reader, and its line names her and her oldest such read, so
// the judgment is pushed to the coordinator like any other and kept by the
// tick (TickKept) while the read waits, updated in place, closed when it is
// begun or taken back. A reader the friend map does not name is not here: the
// sprint's own readers-behind judgment (ReadersBehind) keeps it.
func readWaitsConds(s *Snapshot) []cond {
	if s == nil || s.Readers == nil {
		return nil
	}
	type wait struct {
		id string
		at time.Time
		n  int
	}
	oldest := map[string]wait{}
	for _, c := range s.Readers.Column(Asked) {
		if !s.IsFriendReader(c.Row) || !s.ReaderIsUp(c.Row) {
			continue
		}
		at, err := time.Parse(time.RFC3339, c.F("asked"))
		if err != nil || s.Now.Sub(at) < readWaitBound(s, c.Row) {
			continue
		}
		w := oldest[c.Row]
		w.n++
		if w.id == "" || at.Before(w.at) || (at.Equal(w.at) && c.ID < w.id) {
			w.id, w.at = c.ID, at
		}
		oldest[c.Row] = w
	}
	if len(oldest) == 0 {
		return nil
	}
	readers := make([]string, 0, len(oldest))
	for rd := range oldest {
		readers = append(readers, rd)
	}
	slices.Sort(readers)
	out := make([]cond, 0, len(readers))
	for _, rd := range readers {
		w := oldest[rd]
		out = append(out, cond{
			typ:       NReadersBehind,
			primaries: []string{rd},
			// the bound, never the age: the line changes with the facts, not
			// the clock, so the tick rewrites it only when the facts change
			what: fmt.Sprintf("the readers are behind: %s has %d read(s) asked and not begun past %s, the oldest %s; friend sync delivers it to the friend's inbox/reads/%s/: wake the friend",
				rd, w.n, readWaitBound(s, rd), w.id, w.id),
			decisions: []string{"wait 10m"},
		})
	}
	return out
}
