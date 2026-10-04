package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A member's overload (docs/SPEC-SPRINT.md, "a member is overloaded"; the owner, 2026-10-03:
// "overloads should bubble up to you much quicker than they just did now", and "the overload
// is defined as -- cards are timing out. not any CPU%"). One member ran an hour of stagings
// refused on stage-timeout and cards failed on the usage source not answering, and no
// judgment was raised: it was found by reading its log. The decision is one pure function
// over the snapshot, Overloaded, which the tick and the seat check both call: within
// OverloadWindow a member has had OverloadTimeouts or more cards end on a timeout of any
// kind, counted from the finishes the member reported. No load number is in it: the beat's
// load stays a fact for the table.
//
// It covers friends (the owner: "trust but VERIFY", "Are they actually doing the
// work that is shown in the friend table? Really?"): a friend up whose cards end on a
// timeout raises the same judgment with the same numbers, counted from her finishes on her
// own row FriendRow(<name>), her width the roster's (FriendSeat), and the remedy her width
// in nova-config, which friend sync applies.

// OverloadWindow is how far back the timeouts are counted.
const OverloadWindow = 15 * time.Minute

// OverloadTimeouts is how many timeouts within the window make a member overloaded.
const OverloadTimeouts = 3

// The timeout kinds a member's finish reports (member.Judge): a launch refused at staging
// on the stage's own timeout, a child its deadline ended, and a child the budget rule ended
// because the usage source stopped answering within its bound.
const (
	TimeoutStaging  = "stage-timeout"
	TimeoutDeadline = "deadline"
	TimeoutUsage    = "usage source"
)

// TimeoutKind is the timeout a member's finish report names, "" for a report that is no
// timeout: `staging refused: stage-timeout` (or a staging take's own line, `stage-timeout`),
// `deadline: ...`, and `budget: unverifiable: the usage source stopped answering, ...`.
func TimeoutKind(report string) string {
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(report), cardhdr.EndStaging), ":"))
	switch {
	case strings.HasPrefix(line, TimeoutStaging):
		return TimeoutStaging
	case strings.HasPrefix(line, TimeoutDeadline+":"):
		return TimeoutDeadline
	case strings.Contains(line, "the usage source stopped answering"):
		return TimeoutUsage
	}
	return ""
}

// Timeout is one card of a member that ended on a timeout: the work card, the kind, and when.
type Timeout struct {
	Card string
	Kind string
	At   time.Time
}

// MemberTimeouts is the member's cards that ended on a timeout within the window ending at
// s.Now, oldest first: its staging refusals on stage-timeout (the work cards' staging takes,
// whichever row they sit on now) and its failed finishes whose report names a timeout.
func MemberTimeouts(s *Snapshot, member string) []Timeout {
	if s.Fleet == nil {
		return nil
	}
	since := s.Now.Add(-OverloadWindow)
	within := func(stamp string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, stamp)
		return t, err == nil && !t.Before(since) && !t.After(s.Now)
	}
	var out []Timeout
	for _, c := range s.Fleet.Column(Ready, Working, Withdrawn, DoneFailed) {
		if takes, _ := StagingTakes(c); len(takes) > 0 {
			for _, t := range takes {
				if t.Member == member && TimeoutKind(t.Error) == TimeoutStaging {
					if at, ok := within(t.Finished); ok {
						out = append(out, Timeout{Card: c.ID, Kind: TimeoutStaging, At: at})
					}
				}
			}
		}
		if c.Col == DoneFailed && c.Row == member {
			if kind := TimeoutKind(friendReportBody(member, c.F("report"))); kind != "" {
				if at, ok := within(c.F("finished")); ok {
					out = append(out, Timeout{Card: c.ID, Kind: kind, At: at})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].Card < out[j].Card
	})
	return out
}

// Overload is a member overloaded: its timeouts within the window and its width now.
type Overload struct {
	Member   string
	Timeouts []Timeout
	Width    int
}

// Overloaded is whether the member is overloaded (OverloadTimeouts or more timeouts within
// OverloadWindow), and the facts.
func Overloaded(s *Snapshot, member string) (Overload, bool) {
	return overloadOf(s, member, s.Width(member))
}

// FriendOverloaded is Overloaded for a friend: the same rule over her row FriendRow(<name>),
// her width the roster's.
func FriendOverloaded(s *Snapshot, f FriendSeat) (Overload, bool) {
	return overloadOf(s, FriendRow(f.Name), f.Width)
}

// overloadOf is the one rule of Overloaded, a machine's or a friend's: the row's timeouts
// within the window, OverloadTimeouts or more, and the width the judgment halves.
func overloadOf(s *Snapshot, member string, width int) (Overload, bool) {
	ts := MemberTimeouts(s, member)
	if len(ts) < OverloadTimeouts {
		return Overload{}, false
	}
	return Overload{Member: member, Timeouts: ts, Width: width}, true
}

// friendReportPrefix begins a friend's finish report, `friend <name> <VERDICT>: <paragraph>`
// (friend sync, the inbox/outbox standard).
const friendReportPrefix = "friend "

// friendReportBody is what a finish on the row reports as a member would: on a friend's row,
// the paragraph after `friend <name> <VERDICT>: `, else the report itself.
func friendReportBody(row, report string) string {
	if !IsFriendRow(row) {
		return report
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(report), friendReportPrefix)
	if f := strings.SplitN(rest, " ", 3); ok && len(f) == 3 && strings.HasSuffix(f[1], ":") {
		return f[2]
	}
	return report
}

// HalfWidth is the width the judgment offers: half the member's, at least 1.
func (o Overload) HalfWidth() int { return max(1, o.Width/2) }

// What is the judgment's line: the member, the count, each card with its timeout kind.
func (o Overload) What() string {
	var cards []string
	for _, t := range o.Timeouts {
		cards = append(cards, t.Card+" ("+t.Kind+")")
	}
	remedy := fmt.Sprintf("nova-sprint fleet up %s --width %d", o.Member, o.HalfWidth())
	if name, ok := FriendOfRow(o.Member); ok {
		remedy = fmt.Sprintf("nova-config friend set %s --width %d, then nova-sprint friend sync", name, o.HalfWidth())
	}
	return fmt.Sprintf("%s is overloaded: %d cards ended on a timeout in the last %s: %s; halve its width: %s, or wait 15m",
		o.Member, len(o.Timeouts), OverloadWindow, strings.Join(cards, ", "), remedy)
}

// Decisions are the judgment's: halve the member's width (a friend's in the roster), or
// wait the window.
func (o Overload) Decisions() []string {
	if name, ok := FriendOfRow(o.Member); ok {
		return []string{fmt.Sprintf("friend set %s --width %d", name, o.HalfWidth()), "wait 15m"}
	}
	return []string{fmt.Sprintf("fleet up %s --width %d", o.Member, o.HalfWidth()), "wait 15m"}
}

// MemberSubject is the stream word a member's judgment is filed under.
func MemberSubject(member string) string { return "member:" + member }

// overloadConds is the tick's overload condition of every member up and every friend up
// the binding read (NOverloaded).
func overloadConds(s *Snapshot, friends []FriendSeat) []cond {
	var out []cond
	add := func(o Overload) {
		out = append(out, cond{typ: NOverloaded, stream: MemberSubject(o.Member), streamLevel: true, what: o.What(), decisions: o.Decisions()})
	}
	for _, m := range s.UpMembers() {
		if o, ok := Overloaded(s, m); ok {
			add(o)
		}
	}
	for _, f := range friends {
		if f.Status != Up {
			continue // a friend held or down raises none, as a member not up
		}
		if o, ok := FriendOverloaded(s, f); ok {
			add(o)
		}
	}
	return out
}
