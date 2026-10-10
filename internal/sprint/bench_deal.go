package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A card's bench (docs/SPEC-SPRINT.md section 5, the deal: a card's BENCH line). A brief
// whose header carries `BENCH: <member>` (cardhdr.KeyValue reads the line, in any case)
// names the member that has what the card needs: a tool only that member's machine holds.
// Such a card is dealt only to that member, or to the members a comma-separated line
// names, by every placement of a work card (the deal, a redeal, an escalation, a rework,
// a member going down and the level); while no member of its bench is up (down, or held)
// it waits in ready and the no-stall rule says why (held.go), so a card that would fail
// on a missing tool is never dealt to a member that has not got it. A BENCH naming no
// fleet member is refused at add, with the members there are. A card with no BENCH line
// is dealt round the fleet as every card before it was.

// FieldBench is a primary's bench as its brief's BENCH line names it: the members' names,
// comma-separated, written by add and brief with the brief; absent on a card with none.
const FieldBench = "bench"

// BenchOfBrief is the bench a brief gives its card (FieldBench), read from its header
// block (the `key: value` lines under line 1, as cardhdr.ReadWho reads WHO): nil and ""
// when the brief names no bench, else the members it names. why is "" or the one line
// naming what is wrong with the line and what to write
// (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func BenchOfBrief(brief string) (bench []string, why string) {
	_, rest, _ := strings.Cut(brief, "\n")
	for rest != "" {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		k, v, ok := cardhdr.KeyValue(l)
		if !ok {
			break
		}
		if !strings.EqualFold(k, "bench") {
			continue
		}
		for _, m := range Split(v) {
			if !ValidID(m) {
				return nil, "BENCH: " + v + " wants a comma-separated list of fleet members, each of letters, digits, _ and -; a card with no BENCH line is dealt round the fleet"
			}
			bench = append(bench, m)
		}
		if len(bench) == 0 {
			return nil, "BENCH: names no member: write `BENCH: <member>[,<member>...]`, or leave the line out and the card is dealt round the fleet"
		}
		return bench, ""
	}
	return nil, ""
}

// Bench is the primary's bench (FieldBench): the members its brief's BENCH line names,
// nil when it names none (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func Bench(c *Card) []string {
	if c == nil {
		return nil
	}
	return Split(c.F(FieldBench))
}

// BenchKnown is the names of bench that are no fleet member of s (Snapshot.Members): what
// add refuses a brief for, so a card is never admitted waiting for a member the fleet has
// not got (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func BenchKnown(s *Snapshot, bench []string) []string {
	members := s.Members()
	var out []string
	for _, m := range bench {
		if !contains(members, m) {
			out = append(out, m)
		}
	}
	return out
}

// BenchRefused is add's refusal of a brief whose BENCH line names no fleet member, with
// the members there are and the remedy (docs/SPEC-SPRINT.md section 5, a card's BENCH
// line).
func BenchRefused(s *Snapshot, bench, unknown []string) string {
	members := s.Members()
	have := "the fleet has no member"
	if len(members) > 0 {
		have = "the fleet's members are " + Preview(members, ", ")
	}
	return fmt.Sprintf("BENCH: %s names no fleet member (%s): %s; run: nova-sprint fleet list, and name a member of it, or start the member the card needs with nova-sprint fleet beat %s on its machine",
		strings.Join(bench, ","), Preview(unknown, ", "), have, unknown[0])
}

// onlyBench is the members of up the bench names, in up's order: up itself when the bench
// is empty (a card with no BENCH line is dealt round the fleet), and none when no member
// of its bench is up, so the card waits in ready for it (docs/SPEC-SPRINT.md section 5, a
// card's BENCH line).
func onlyBench(up, bench []string) []string {
	if len(bench) == 0 {
		return up
	}
	var out []string
	for _, m := range up {
		if contains(bench, m) {
			out = append(out, m)
		}
	}
	return out
}

// notBench is the members of up the bench does not name: the level's avoid (round.levelTo),
// so a bench card is never moved off the bench its deal put it on; nil when the bench is
// empty (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func notBench(up, bench []string) []string {
	if len(bench) == 0 {
		return nil
	}
	return without(up, bench)
}

// benchOfWork is the bench of the work card's primary: the one place the bench lives is the
// primary its brief carries (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func benchOfWork(s *Snapshot, wc *Card) []string {
	if s.Work == nil || wc == nil {
		return nil
	}
	return Bench(s.Work.Placed(wc.F("primary")))
}

// benchWaits is why a card whose bench has no member up waits in ready: no placement deals
// it to another member (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func benchWaits(bench []string) string {
	return "waits for its bench " + Preview(bench, ", ") + ": no member of it is up, and a card whose brief says BENCH: is dealt only to the members it names"
}

// benchRefusal is the deal's refusal of that card, with the remedy (docs/SPEC-SPRINT.md
// section 5, a card's BENCH line).
func benchRefusal(bench []string) string {
	return benchWaits(bench) + "; run: nova-sprint fleet beat " + bench[0] + " on its machine, or release its hold with nova-sprint fleet up " + bench[0]
}

// benchRoom is the refusal of a card whose bench is up and every member of it at its room
// (DealAhead times its width, width.go): the tick deals it when one has room
// (docs/SPEC-SPRINT.md section 5, a card's BENCH line).
func benchRoom(bench []string) string {
	return "every member of its bench " + Preview(bench, ", ") + " is at its room (DealAhead times its width): the tick deals it when one has room"
}
