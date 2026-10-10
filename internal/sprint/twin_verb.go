package sprint

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// The twin verb (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin";
// tla/SprintRules.tla, Replace). One step admits the twin and drops the card.

// TwinCarry is the attempt a twin carries (twin --carry): its number, the branch and head it
// pushed, and its CARRY: header line (member.CarryLine), where the twin's first attempt starts.
type TwinCarry struct {
	Attempt      int
	Branch, Head string
	Line         string
}

// TwinReq twins a card (nova-sprint twin): one step that admits the twin and drops the card.
type TwinReq struct {
	ID          string
	Paths       []string // globs joined to every PATHS: line of the brief
	Needs       []string // needs of the twin besides the card's own
	Before      string   // the card the twin stands in front of; "" is the card twinned
	Tier        string   // the twin's pinned tier; "" keeps the card's pin
	Instruction string   // written verbatim at the head of THE TASK
	Carry       *TwinCarry
	Who         string
}

// TwinReturnedFirst is what a twin of a merging card says: the step returned it to review
// (Return), and the twin is made by the same verb's next step (cmd/nova-sprint twin).
const TwinReturnedFirst = "returned first: a merging card is taken off the merge queue before it is twinned"

// maxTwinIDs is how many raised numbers TwinNumberIDs offers.
const maxTwinIDs = 32

// TwinNumberIDs is the ids the twin of id chooses among, in order: id with its trailing
// number raised (cards3 -> cards4, cards4, ...), or with 2 after it when it ends in none.
func TwinNumberIDs(id string) []string {
	stem := strings.TrimRight(id, "0123456789")
	n, err := strconv.Atoi(id[len(stem):])
	if err != nil {
		n = 1
	}
	out := make([]string, 0, maxTwinIDs)
	for i := 1; i <= maxTwinIDs; i++ {
		out = append(out, stem+strconv.Itoa(n+i))
	}
	return out
}

// TwinBrief is brief with every PATHS: line widened by paths (each glob once, in order),
// the carry's CARRY: line in its header, and THE TASK prefixed by the correction: the
// carried branch and head, then the instruction verbatim. A brief with no THE TASK
// paragraph gets one after its header (cardhdr.KeyValue lines up to the first other line).
func TwinBrief(brief, old string, paths []string, carry *TwinCarry, instruction string) string {
	lines := strings.Split(brief, "\n")
	if len(paths) > 0 {
		var union []string
		for _, l := range lines {
			if k, v, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == cardhdr.KeyPaths {
				union = joinGlobs(union, strings.Split(v, ","))
			}
		}
		set := cardhdr.KeyPaths + ": " + strings.Join(joinGlobs(union, paths), ",")
		found := false
		for i, l := range lines {
			if k, _, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == cardhdr.KeyPaths {
				lines[i], found = l[:len(l)-len(strings.TrimLeft(l, " \t"))]+set, true
			}
		}
		if !found {
			lines = slices.Insert(lines, min(1, len(lines)), set)
		}
	}
	var said []string
	if carry != nil {
		said = append(said, fmt.Sprintf("This card is the twin of %s: its attempt %d pushed branch %s at head %s, and this attempt starts from that head.",
			old, carry.Attempt, orDash(carry.Branch), carry.Head))
	}
	if instruction != "" {
		said = append(said, instruction)
	}
	if len(said) > 0 {
		correction := "THE TASK. " + strings.Join(said, " ")
		at := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "THE TASK") })
		if at >= 0 {
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[at]), "THE TASK"), "."))
			lines[at] = strings.TrimSpace(correction + " " + rest)
		} else {
			end := slices.IndexFunc(lines, func(l string) bool {
				_, _, ok := cardhdr.KeyValue(strings.TrimSpace(l))
				return !ok
			})
			if end < 0 {
				end = len(lines)
			}
			lines = slices.Insert(lines, end, "", correction)
		}
	}
	if carry != nil && carry.Line != "" {
		lines = slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "CARRY:") })
		lines = slices.Insert(lines, min(1, len(lines)), carry.Line)
	}
	return strings.Join(lines, "\n")
}

// joinGlobs is have with every glob of add not in it after it, trimmed, each once.
func joinGlobs(have, add []string) []string {
	for _, g := range add {
		if g = strings.TrimSpace(g); g != "" && !slices.Contains(have, g) {
			have = append(have, g)
		}
	}
	return have
}

// Twin twins a card in one step (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin";
// tla/SprintRules.tla, Replace): the twin's id is the card's with its trailing number raised
// (TwinNumberIDs), its brief the card's with PATHS widened and THE TASK prefixed by the
// correction (TwinBrief), its needs the card's (less those it waived) and r.Needs, held if it
// was held, pinned to r.Tier or the card's pin, in the card's stream in front of it or of
// r.Before, from its first attempt, as a fresh card is admitted. It is add --replaces of the
// card (Replace): every waiting card that needs the card needs the twin, in the same place, and
// the card is dropped "twinned as <id>", raising no blocked judgment; every open judgment on
// the card is answered "twinned as <id>". A merging card is returned first (Return), after the
// twin is checked on the card as returned: this step is then the return alone, and says
// TwinReturnedFirst. Refused whole, writing nothing, for a card not on the table, a sentinel
// or landed, a tier that is no class, a glob that climbs out, every twin id taken, and any
// refusal of the replace, a cycle among them, naming its edges.
func Twin(s *Snapshot, r TwinReq) Plan {
	var p Plan
	refuse := func(why string) Plan {
		p.refuse(r.ID, why+"; nothing was changed")
		return p
	}
	if why := notCoordinator(s, r.Who, "twin"); why != "" {
		p.refuse(r.ID, strings.Replace(why, "answers a judgment, which is", "is", 1))
		return p
	}
	c := s.Work.Placed(r.ID)
	var bad []string
	for _, g := range r.Paths {
		if path.IsAbs(g) || slices.Contains(strings.Split(g, "/"), "..") {
			bad = append(bad, "--paths "+g+" climbs out of the repository")
		}
	}
	switch {
	case c == nil:
		return refuse("no primary " + r.ID + " on the work table (" + placeWord(orEmpty(s.Work.Card(r.ID), r.ID)) + ")")
	case IsSentinel(c):
		return refuse(r.ID + " is a sentinel, which no twin replaces")
	case c.Col == Landed:
		return refuse(r.ID + " landed: landed is final, and the change is a new card")
	case r.Tier != "" && !cardhdr.IsRoute(r.Tier):
		return refuse("--tier wants " + cardhdr.RouteList + ", found " + r.Tier)
	case len(bad) > 0:
		return refuse(strings.Join(bad, "; "))
	}
	nw := ""
	for _, id := range TwinNumberIDs(c.ID) {
		if s.Work.Card(id) == nil {
			nw = id
			break
		}
	}
	if nw == "" {
		ids := TwinNumberIDs(c.ID)
		return refuse("every twin id of " + r.ID + " is taken, " + ids[0] + " to " + ids[len(ids)-1])
	}
	if c.Col == Merging {
		// checked on the card as returned, so a twin that cannot hold returns nothing
		after := *s
		after.Work = s.Work.Frozen()
		rc := *c
		rc.Col = Review
		after.Work.Put(&rc)
		if tp := twinOf(&after, r, &rc, nw); len(tp.Refused) > 0 {
			return tp
		}
		rp := Return(s, ReturnReq{Sel: Sel{IDs: []string{c.ID}}, Reason: "twinned as " + nw, Who: r.Who})
		if len(rp.Refused) == 0 {
			rp.Said = append(rp.Said, TwinReturnedFirst)
		}
		return rp
	}
	return twinOf(s, r, c, nw)
}

// twinOf is Twin of the open card c, not merging, as nw. Replace drops the card "replaced by";
// the plan is rewritten to "twinned as", the pin is set, and every open judgment on the card
// is answered. twins.go stays as add --replaces left it.
func twinOf(s *Snapshot, r TwinReq, c *Card, nw string) Plan {
	waived := Split(c.F("waived"))
	var needs []string
	for _, n := range append(Split(c.F("needs")), r.Needs...) {
		if !contains(waived, n) && !contains(needs, n) {
			needs = append(needs, n)
		}
	}
	brief := TwinBrief(c.F("brief"), c.ID, r.Paths, r.Carry, r.Instruction)
	p := Replace(s, AddReq{Stream: c.Row, IDs: []string{nw}, Needs: needs, Brief: brief, Rules: c.F(FieldRules),
		Before: cmp.Or(r.Before, c.ID), Held: IsHeld(c), Replaces: []string{c.ID}, Who: r.Who})
	if len(p.Refused) > 0 {
		return p
	}
	tier := cmp.Or(r.Tier, c.F(FieldTier))
	said := "twinned as " + nw
	from := "replaced by " + nw
	for i := range p.Units {
		u := &p.Units[i]
		dropped := false
		for j, ch := range u.Changes {
			e := ch.Entry
			if ch.Table != Work {
				continue
			}
			switch {
			case e.ID == c.ID && e.Remove:
				dropped = true
				set := map[string]string{}
				for k, v := range e.Set {
					if k == "reason" && v == from {
						v = said
					}
					set[k] = v
				}
				u.Changes[j].Entry.Set = set
				u.Moved = strings.ReplaceAll(u.Moved, from, said)
			case e.ID == nw && e.Create != nil && tier != "":
				set := map[string]string{}
				for k, v := range e.Set {
					set[k] = v
				}
				set[FieldTier] = tier
				u.Changes[j].Entry.Set = set
				if !strings.Contains(u.Moved, "tier "+tier) {
					u.Moved += "; tier " + tier
				}
			}
		}
		if !dropped {
			continue
		}
		for _, o := range s.Open {
			if o.Subject() == c.ID || (o.Note.StreamLevel && (o.Note.Card == c.ID || contains(o.Note.Primaries, c.ID))) {
				if !answeredIn(u.Notes, o.Note.ID) {
					u.Notes = append(u.Notes, decided(o, said, r.Who, s.Now, c.ID))
				}
			}
		}
	}
	return p
}
