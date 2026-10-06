package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A pin behind a friend down (docs/SPEC-SPRINT.md section 1, a hard pin behind a friend
// down or held; the owner, 2026-10-05: 31 ready cards sat pinned to friends down for the
// week or held, some for 20 hours, with no judgment, and the coordinator unpinned ten by
// hand; 2026-10-04: "pins are the exception; hard-pin only true ownership"). A ready
// primary hard-pinned (WHO: only friend <name>) to a friend who is not up is stamped
// (FieldPinWaits) at the first deal that finds her so; once she has been down or held past
// the friend-idle setting (FriendIdleAfter) behind it, the deal either unpins it by rule,
// logged (NUnpinnedByRule), when its pin is a preference, or names it in the one judgment
// for her (NPinDown) with the unpin printed complete, when the pin is ownership
// (PinOwned) or the unpin would be refused (unpinWhy). The judgment closes when she is up
// or no card waits behind her; its text moves only when the cards it names do.

const (
	// NPinDown is ready cards hard-pinned to a friend down or held past the friend-idle
	// setting that the machine does not unpin: one judgment per friend, on her row.
	NPinDown = "ready cards wait pinned to a friend down or held"
	// NUnpinnedByRule is the log of a preference pin the machine dropped because its
	// friend was down or held past the friend-idle setting.
	NUnpinnedByRule = "WHO unpinned by rule: its friend is down or held"
	// FieldPinWaits is when the deal first found a ready hard pin's friend not up: the
	// clock of the friend-idle setting, unset when she is up and when the card is dealt.
	FieldPinWaits = "pin_waits"
)

func init() { TickDecisions[NPinDown] = []string{"ack", "wait"} }

// PinOwned says the card's pin to the friend is true ownership, never a preference: its
// brief's WHO line carries the `(owner)` mark for her (cardhdr.ReadWho; the generator
// writes that mark only for ratings and a friend's own tool), or it is her rating card,
// rate-<name>-*.
func PinOwned(c *Card, name string) bool {
	if name != "" && strings.HasPrefix(c.ID, "rate-"+name+"-") {
		return true
	}
	w, why := cardhdr.ReadWho(c.F("brief"))
	return why == "" && w.Owner && w.Name == name
}

// pinWait is one ready hard pin waiting behind its friend past the friend-idle setting
// that the machine leaves pinned, and why.
type pinWait struct{ id, why string }

// friendPinWaits is the deal's units for the hard pins behind friends not up: the stamps
// written and cleared, the preferences unpinned by rule, and the one judgment per friend
// raised, replaced when the cards it names change, and closed. dealt is the primaries the
// friends' deal placed in this plan: their own move clears the stamp. With no friends
// roster it does nothing.
func friendPinWaits(s *Snapshot, seats []FriendSeat, dealt map[string]bool) []Unit {
	if len(seats) == 0 || s.Work == nil {
		return nil
	}
	seat := map[string]FriendSeat{}
	for _, f := range seats {
		seat[f.Name] = f
	}
	idle := s.FriendIdleAfter()
	var units []Unit
	waits := map[string][]pinWait{}
	since := map[string]time.Time{}
	for _, c := range s.Work.Column(Ready) {
		if IsSentinel(c) || !OnlyFriend(c) || StreamHeld(s, c.Row) {
			continue
		}
		name, _ := FriendCard(c)
		f, known := seat[name]
		if known && f.Status == Up {
			if c.F(FieldPinWaits) != "" && !dealt[c.ID] {
				units = append(units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, nil, FieldPinWaits))},
					Moved: fmt.Sprintf("%s pin clock cleared: friend %s is up", c.ID, name)})
			}
			continue
		}
		at := stampAt(c, FieldPinWaits)
		if at.IsZero() {
			units = append(units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, map[string]string{FieldPinWaits: stamp(s.Now)}))},
				Moved: fmt.Sprintf("%s waits pinned to friend %s, who is %s: clock started", c.ID, name, pinFriendState(f, known))})
			continue
		}
		if s.Now.Sub(at) <= idle {
			continue
		}
		if t, ok := since[name]; !ok || at.Before(t) {
			since[name] = at
		}
		state := pinFriendState(f, known)
		if PinOwned(c, name) {
			waits[name] = append(waits[name], pinWait{id: c.ID, why: "owned"})
			continue
		}
		if why := unpinWhy(s, c.ID); why != "" {
			waits[name] = append(waits[name], pinWait{id: c.ID, why: why})
			continue
		}
		n := happened(NUnpinnedByRule, c.Row, s.Now, c.ID)
		n.Who = MachineActor
		n.What = fmt.Sprintf("%s dropped WHO: only friend %s: friend %s has been %s past the friend-idle setting %s (since %s); the pin was a preference, not ownership",
			c.ID, name, name, state, idle, stamp(at))
		units = append(units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, nil, FieldWho, FieldPinWaits))},
			Notes: []Note{n}, Moved: n.What})
	}
	open := map[string][]Open{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NPinDown {
			open[o.Subject()] = append(open[o.Subject()], o)
		}
	}
	quiet := map[string]bool{} // an acknowledgement, or a wait not run out, holds the judgment
	for _, o := range s.Acked {
		if o.Note.Type == NPinDown && (o.Note.Review.IsZero() || s.Now.Before(o.Note.Review)) {
			quiet[o.Subject()+"\x00"+o.Note.What] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(waits)) {
		row := FriendRow(name)
		f, known := seat[name]
		what, decisions := pinDownWhat(name, pinFriendState(f, known), idle, since[name], waits[name])
		var closes []Open
		same := false
		for _, o := range open[row] {
			if o.Note.What == what {
				same = true
				continue
			}
			closes = append(closes, o)
		}
		u := Unit{Key: row, Closes: closes}
		if !same && !quiet[row+"\x00"+what] {
			u.Notes = []Note{{Kind: Judgment, Type: NPinDown, Primaries: []string{row}, Count: 1, What: what,
				Who: MachineActor, At: s.Now, Marked: true, Decisions: decisions}}
			u.Moved = "judged: " + what
		}
		if len(u.Closes) > 0 || len(u.Notes) > 0 {
			units = append(units, u)
		}
	}
	for _, row := range slices.Sorted(maps.Keys(open)) {
		if name, _ := FriendOfRow(row); len(waits[name]) == 0 {
			units = append(units, Unit{Key: row, Closes: open[row], Moved: "closed: no ready card waits pinned behind friend " + name})
		}
	}
	return units
}

// pinFriendState is how a friend not up is: held, down, or not on the friends roster.
func pinFriendState(f FriendSeat, known bool) string {
	switch {
	case !known:
		return "off the friends roster"
	case f.Status == Held:
		return "held"
	}
	return "down"
}

// pinDownWhat is the judgment's text and decisions: the friend, how long, and the cards,
// with the one unpin that frees them all printed complete first.
func pinDownWhat(name, state string, idle time.Duration, since time.Time, waits []pinWait) (string, []string) {
	ids := make([]string, len(waits))
	var notes []string
	for i, w := range waits {
		ids[i] = w.id
		if w.why != "owned" {
			notes = append(notes, w.id+": "+w.why)
		}
	}
	unpin := "nova-sprint unpin " + strings.Join(ids, " ") + " --reason " + oneline.ShellWord("friend "+name+" "+state+" past friend-idle")
	what := fmt.Sprintf("friend %s has been %s past the friend-idle setting %s (since %s) with %d ready cards pinned to her alone that the machine does not unpin: %s; run: %s",
		name, state, idle, stamp(since), len(ids), strings.Join(ids, ", "), unpin)
	if len(notes) > 0 {
		what += "; not unpinnable now: " + strings.Join(notes, "; ")
	}
	decisions := []string{unpin}
	if state == "held" {
		decisions = append(decisions, "friend up "+name)
	}
	return what, append(decisions, "ack", "wait")
}
