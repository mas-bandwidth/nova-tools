package sprint

import (
	"maps"
	"time"
)

// A friend's card's deadline by friend (docs/SPEC-SPRINT.md section 5, the one deadline
// rule; section 1, a friend's card's deadline; the owner, 2026-10-04: friends get what
// machines have). A friend's card has no route deadline of its own: its own is
// DeadlineUnfinished, and the deadline it carries while it works on her row is the same
// rule a member's deal gets (rowDeadline): the larger of its own and DeadlineK times her
// median run wall over her last DeadlineSamples ok attempts (RunWall: her take to her
// report, RowMedianWall over her row), set on the card (FieldFriendDeadline) as it goes
// into working on her row, so a friend whose cards take long is not judged late on the
// fleet's number.

// FieldFriendDeadline is a friend's work card's working deadline in seconds, set when it is
// placed on her row (friendDeal, FriendLevel: friendDeadline), as #5300 sets a machine's
// card's when it is dealt: absent while she has no ok attempt, and DeadlineUnfinished holds.
const FieldFriendDeadline = "friend_deadline"

// friendDeadline is the fields a card placed on the friend's row carries for its working
// deadline: FieldFriendDeadline, the one deadline rule (rowDeadline) over her row with her
// own DeadlineUnfinished, when she has an ok attempt; none otherwise (unset says so).
func friendDeadline(s *Snapshot, name string) (set map[string]string, unset []string) {
	row := FriendRow(name)
	if _, n := RowMedianWall(s, row); n == 0 {
		return map[string]string{}, []string{FieldFriendDeadline}
	}
	d := s.rowDeadline(row, int(DeadlineUnfinished/time.Second), 0)
	return map[string]string{FieldFriendDeadline: itoa(d)}, nil
}

// unfinishedLimit is how long a work card taken may run unfinished: its friend's deadline
// when it carries one (FieldFriendDeadline), else DeadlineUnfinished.
func unfinishedLimit(c *Card) time.Duration {
	if d := c.Int(FieldFriendDeadline); d > 0 {
		return time.Duration(d) * time.Second
	}
	return DeadlineUnfinished
}

// friendTaken is the fields of a friend's card going into working on her row now: taken
// stamps (takenStamps) and her deadline (friendDeadline), untaken_since unset.
func friendTaken(s *Snapshot, c *Card, name string) (set map[string]string, unset []string) {
	set, unset = friendDeadline(s, name)
	maps.Copy(set, takenStamps(c, s.Now))
	return set, append(unset, "untaken_since")
}
