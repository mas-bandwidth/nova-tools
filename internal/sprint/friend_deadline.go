package sprint

import (
	"maps"
	"math"
	"sort"
	"time"
)

// A friend's card's deadline by friend (docs/SPEC-SPRINT.md section 1, a friend's card's
// deadline; the owner, 2026-10-04: friends get what machines have). The member rule of
// nova-tools#5300 (not on dev when this was written, so the same rule is written here for
// friends, under its own names) gives a card dealt to a machine the larger of its own
// deadline and DeadlineK times the machine's median run wall over its last DeadlineSamples
// ok attempts. A friend's card has no route deadline of its own: its own is
// DeadlineUnfinished, and the friend's is FriendDeadlineK times her median run wall over
// her last FriendDeadlineSamples ok attempts (RunWall: her take to her report), set on the
// card (FieldFriendDeadline) as it goes into working on her row, so a friend whose cards
// take long is not judged late on the fleet's number.

const (
	// FriendDeadlineK is the multiple of the friend's median run wall her card's working
	// deadline is at least (nova-tools#5300's DeadlineK).
	FriendDeadlineK = 3
	// FriendDeadlineSamples is how many of her latest ok attempts the median is over
	// (nova-tools#5300's DeadlineSamples).
	FriendDeadlineSamples = 50
)

// FriendMedianWall is the friend's median run wall in seconds over her last
// FriendDeadlineSamples ok attempts (RunWall of the ok work cards on her row, newest
// finished first), and how many samples it is over; 0 and 0 with none.
func FriendMedianWall(s *Snapshot, name string) (median float64, n int) {
	if s.Fleet == nil {
		return 0, 0
	}
	cards := append([]*Card(nil), s.Fleet.Cell(FriendRow(name), DoneOK)...)
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].F("finished") > cards[j].F("finished") })
	var walls []float64
	for _, c := range cards {
		if w, ok := wallSeconds(RunWall(c)); ok {
			walls = append(walls, w)
		}
		if len(walls) == FriendDeadlineSamples {
			break
		}
	}
	m := measure(walls)
	return m.Median, m.N
}

// FieldFriendDeadline is a friend's work card's working deadline in seconds, set when it is
// placed on her row (friendDeal, FriendLevel: friendDeadline), as #5300 sets a machine's
// card's when it is dealt: absent while she has no ok attempt, and DeadlineUnfinished holds.
const FieldFriendDeadline = "friend_deadline"

// friendDeadline is the fields a card placed on the friend's row carries for its working
// deadline: FieldFriendDeadline, the larger of DeadlineUnfinished and FriendDeadlineK times
// her median run wall, when she has an ok attempt; none otherwise (unset says so).
func friendDeadline(s *Snapshot, name string) (set map[string]string, unset []string) {
	median, n := FriendMedianWall(s, name)
	if n == 0 {
		return map[string]string{}, []string{FieldFriendDeadline}
	}
	d := max(DeadlineUnfinished, time.Duration(math.Ceil(FriendDeadlineK*median))*time.Second)
	return map[string]string{FieldFriendDeadline: itoa(int(d / time.Second))}, nil
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
