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

// PropReadWait is the work property of how long an asked read of a friend
// waits before the tick raises one judgment for that reader. Empty, or not a
// positive duration, is ReadersWindow. nova-sprint set does not grow a flag
// for it: the property is written on the work table by a step that names it.
const PropReadWait = "read_wait"

// NReadWaits is one judgment per friend reader whose asked read has waited
// past the bound. It is not the sprint's single "the readers are behind"
// judgment, and it is not in TickDecisions: the condition carries its own
// decision, and the tick updates it in place while the read stays asked.
const NReadWaits = "a read asked of a reader waits"

// friendReadSlots is the friend's read slots when reader is reader-<name> and
// the fleet property names her. The boolean is false when she is not in the
// map, the property is absent, or it is not that JSON: 0 in the map is a real
// room, not the default.
func (s *Snapshot) friendReadSlots(reader string) (int, bool) {
	if s == nil || s.Fleet == nil {
		return 0, false
	}
	name, ok := ReaderMachine(reader)
	if !ok {
		return 0, false
	}
	raw, ok := s.Fleet.Prop(PropFriendReadSlots)
	if !ok || raw == "" {
		return 0, false
	}
	var slots map[string]int
	if json.Unmarshal([]byte(raw), &slots) != nil {
		return 0, false
	}
	n, ok := slots[name]
	return n, ok
}

// readWaitBound is how long an asked read waits before NReadWaits. The work
// property PropReadWait wins when it is a positive duration; otherwise
// ReadersWindow.
func readWaitBound(s *Snapshot) time.Duration {
	if s == nil || s.Work == nil {
		return ReadersWindow
	}
	raw, ok := s.Work.Prop(PropReadWait)
	if !ok || raw == "" {
		return ReadersWindow
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return ReadersWindow
	}
	return d
}

// readWaitsConds is one condition per friend reader who is up with an asked
// read older than readWaitBound. The judgment names that reader and her
// oldest such read. A reader the friend map does not name is not here: the
// sprint's own readers-behind judgment is unchanged.
func readWaitsConds(s *Snapshot) []cond {
	if s == nil || s.Readers == nil {
		return nil
	}
	bound := readWaitBound(s)
	type wait struct {
		id string
		at time.Time
	}
	oldest := map[string]wait{}
	for _, c := range s.Readers.Column(Asked) {
		if _, ok := s.friendReadSlots(c.Row); !ok || !s.ReaderIsUp(c.Row) {
			continue
		}
		at, err := time.Parse(time.RFC3339, c.F("asked"))
		if err != nil || s.Now.Sub(at) < bound {
			continue
		}
		w := oldest[c.Row]
		if w.id == "" || at.Before(w.at) {
			oldest[c.Row] = wait{id: c.ID, at: at}
		}
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
			typ:       NReadWaits,
			primaries: []string{rd},
			card:      w.id,
			what: fmt.Sprintf("%s has a read asked and not begun past %s: %s",
				rd, bound, w.id),
			decisions: []string{"wait"},
		})
	}
	return out
}
