package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
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
			if kind := TimeoutKind(c.F("report")); kind != "" {
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
	ts := MemberTimeouts(s, member)
	if len(ts) < OverloadTimeouts {
		return Overload{}, false
	}
	return Overload{Member: member, Timeouts: ts, Width: s.Width(member)}, true
}

// HalfWidth is the width the judgment offers: half the member's, at least 1.
func (o Overload) HalfWidth() int { return max(1, o.Width/2) }

// What is the judgment's line: the member, the count, each card with its timeout kind.
func (o Overload) What() string {
	var cards []string
	for _, t := range o.Timeouts {
		cards = append(cards, t.Card+" ("+t.Kind+")")
	}
	return fmt.Sprintf("%s is overloaded: %d cards ended on a timeout in the last %s: %s; halve its width: nova-sprint fleet up %s --width %d, or wait 15m",
		o.Member, len(o.Timeouts), OverloadWindow, strings.Join(cards, ", "), o.Member, o.HalfWidth())
}

// Decisions are the judgment's: halve the member's width, or wait the window.
func (o Overload) Decisions() []string {
	return []string{fmt.Sprintf("fleet up %s --width %d", o.Member, o.HalfWidth()), "wait 15m"}
}

// MemberSubject is the stream word a member's judgment is filed under.
func MemberSubject(member string) string { return "member:" + member }

// overloadConds is the tick's overload condition of every member up (NOverloaded).
func overloadConds(s *Snapshot) []cond {
	var out []cond
	for _, m := range s.UpMembers() {
		if o, ok := Overloaded(s, m); ok {
			out = append(out, cond{typ: NOverloaded, stream: MemberSubject(m), streamLevel: true, what: o.What(), decisions: o.Decisions()})
		}
	}
	return out
}
