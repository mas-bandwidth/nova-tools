package sprint

import (
	"cmp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// RecutReq re-cuts a card as its twin (nova-sprint recut): the same card for another tier
// or another scope, under a new id.
type RecutReq struct {
	ID    string // the card re-cut
	New   string // the twin's id; "" names it by TwinID
	Tier  string // the tier the twin is pinned to (FieldTier); "" keeps the old card's pin
	Brief string // the twin's brief; "" keeps the old card's brief and its rules
	Rules string // with Brief: the held rules file the brief is held to (FieldRules)
	// Needs, with Brief: the needs the new brief names on its DEPENDS-ON: line (briefNeeds),
	// taken with the old card's.
	Needs []string `json:",omitempty"`
	Who   string
}

// TwinIDs is the ids TwinID chooses among for c's twin, in order: the old id with a letter
// after it, b to z, or, for a twin that replaced one card under the old id and one letter
// ("lint-pkg-cairn-tb" for "lint-pkg-cairn-t"), that id with the letters after the twin's.
func TwinIDs(c *Card) []string {
	base, from := c.ID, byte('b')
	if prev := Split(c.F(FieldReplaces)); len(prev) == 1 && len(c.ID) == len(prev[0])+1 && strings.HasPrefix(c.ID, prev[0]) {
		if l := c.ID[len(c.ID)-1]; l >= 'b' && l < 'z' {
			base, from = prev[0], l+1
		}
	}
	var out []string
	for l := from; l <= 'z'; l++ {
		out = append(out, base+string(rune(l)))
	}
	return out
}

// TwinID is the first of TwinIDs that no card on the table or off it has, "" when every
// one is taken.
func TwinID(s *Snapshot, c *Card) string {
	for _, id := range TwinIDs(c) {
		if s.Work.Card(id) == nil {
			return id
		}
	}
	return ""
}

// Recut is a card re-cut for another tier or another scope (docs/SPEC-SPRINT.md section 2,
// "A card replaced by its twin"; tla/SprintRules.tla, Replace): add --replaces of the old
// card (Replace), the twin in the old card's stream in front of it, with its needs (those
// it waived left out) and the new brief's, held as it was, its brief and rules unless a
// brief is named, its pinned tier unless a tier is named, and its attempts from the first.
// Every waiting card that needed the old id needs the twin, the old card is dropped
// "replaced by <new>", no blocked judgment is raised, and the twin records the id it
// replaces (FieldReplaces), in one step. Refused whole, writing nothing, for no tier and no
// brief, a tier that is no class or the one the card is pinned to with no brief, a card
// not on the table, a sentinel or landed, a brief that pins a model with a tier, and any
// refusal of the replace.
func Recut(s *Snapshot, r RecutReq) Plan {
	var p Plan
	refuse := func(why string) Plan {
		p.refuse(r.ID, why+"; nothing was changed")
		return p
	}
	c := s.Work.Placed(r.ID)
	switch {
	case r.Tier == "" && r.Brief == "":
		return refuse("recut wants --tier <" + cardhdr.RouteList + "> or --brief-file <path>: what the twin changes")
	case c == nil:
		return refuse("no primary " + r.ID + " on the work table")
	case IsSentinel(c):
		return refuse(r.ID + " is a sentinel, not a primary")
	case c.Col == Landed:
		return refuse(r.ID + " is landed: the change is a new card")
	case r.Tier != "" && !cardhdr.IsRoute(r.Tier):
		return refuse("--tier wants " + cardhdr.RouteList + ", found " + r.Tier)
	case r.Brief == "" && c.F(FieldTier) == r.Tier:
		return refuse(r.ID + " is pinned to tier " + r.Tier + " already, and no brief is named")
	}
	brief, rules := c.F("brief"), c.F(FieldRules)
	if r.Brief != "" {
		brief, rules = r.Brief, r.Rules
	}
	tier := cmp.Or(r.Tier, c.F(FieldTier))
	if _, why := PriorityOfBrief(brief); why != "" {
		return refuse(why) // a PRIORITY line naming no level is never recut at the card's (priority.go)
	}
	if m, _ := cardhdr.ReadModel(brief); m.Pin != "" && r.Tier != "" {
		return refuse("the brief pins model " + m.Pin + ", which it runs on whatever its tier")
	}
	nw := r.New
	if nw == "" {
		if nw = TwinID(s, c); nw == "" {
			ids := TwinIDs(c)
			return refuse("every twin id of " + r.ID + " is taken, " + ids[0] + " to " + ids[len(ids)-1] + "; name one: --new <id>")
		}
	}
	waived := Split(c.F("waived"))
	var needs []string
	for _, n := range append(Split(c.F("needs")), r.Needs...) {
		if !contains(waived, n) && !contains(needs, n) {
			needs = append(needs, n)
		}
	}
	p = Add(s, AddReq{Stream: c.Row, IDs: []string{nw}, Needs: needs, Brief: brief, Rules: rules, Before: c.ID, Held: IsHeld(c),
		Replaces: []string{c.ID}, Who: r.Who})
	// the card's own level goes to the twin as its tier does (priority.go): a hand-set or a
	// seeded level is the card's, not the brief's, unless a new brief names its own
	level := c.F(FieldPriority)
	if own, _ := PriorityOfBrief(brief); own != "" && r.Brief != "" { // why was refused above
		level = ""
	}
	if len(p.Refused) > 0 || (tier == "" && level == "") {
		return p
	}
	for i := range p.Units {
		u := &p.Units[i]
		for j, ch := range u.Changes {
			if ch.Table == Work && ch.Entry.ID == nw && ch.Entry.Create != nil {
				if u.Changes[j].Entry.Set == nil {
					u.Changes[j].Entry.Set = map[string]string{}
				}
				if tier != "" {
					u.Changes[j].Entry.Set[FieldTier] = tier
					u.Moved += "; tier " + tier
				}
				if level != "" {
					u.Changes[j].Entry.Set[FieldPriority] = level
					u.Moved += "; priority " + level
				}
			}
		}
	}
	return p
}
