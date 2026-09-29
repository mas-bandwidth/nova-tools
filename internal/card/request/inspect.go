package request

import (
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// MaxStandingBytes bounds a card's evidence standing text.
const MaxStandingBytes = 512

// MaxMissingItems bounds what an inspect lists as missing for the next forward
// move; each is a token of at most MaxIdentityBytes.
const MaxMissingItems = 16

// Pin is the definition identity a card was admitted with, as inspect reports it.
type Pin struct {
	Digest     Digest
	ObjectID   string
	Commit     string
	Repository card.Repository
	Path       string
	Kind       string
}

// InspectCard is what an inspect reports about one card: its place, its pinned
// definition identity, its evidence standing at the current head, what is missing
// for the next forward move, its structural drift, its cycle counters and its
// escalation. Standing and Missing are text of the manager's vocabulary, bounded
// here and interpreted there.
type InspectCard struct {
	ID         ID
	Place      Place
	Revision   string
	Outcome    Outcome
	Pin        Pin
	Head       string
	Standing   string
	Missing    []string
	Drift      []Drift
	Counters   Counters
	Escalation Escalation
	Marks      []Mark
}

// InspectResult is the result of an inspect or a check: the table, the epoch and
// the table revision it read, one entry for every card found, and the named cards
// that were not found. It writes nothing and has no operation ID.
type InspectResult struct {
	Schema   int
	Table    string
	Epoch    string
	Revision string
	Cards    []InspectCard
	Missing  []ID
}

func (c InspectCard) tree() card.Obj {
	m := card.Obj{}
	m.Str("id", string(c.ID))
	m["place"] = card.Obj{"row": c.Place.Row, "col": string(c.Place.Col)}
	m.Str("revision", c.Revision)
	m.Str("outcome", string(c.Outcome))
	pin := card.Obj{}
	pin.Str("digest", string(c.Pin.Digest))
	pin.Str("object_id", c.Pin.ObjectID)
	pin.Str("commit", c.Pin.Commit)
	pin.Str("repository", string(c.Pin.Repository))
	pin.Str("path", c.Pin.Path)
	pin.Str("kind", c.Pin.Kind)
	m["pin"] = pin
	m.Str("head", c.Head)
	m.Str("standing", c.Standing)
	m.OptSet("missing", card.Strings(c.Missing))
	m.OptSet("drift", card.Strings(c.Drift))
	if t := c.Counters.tree(); len(t) > 0 {
		m["counters"] = t
	}
	m.Str("escalation", string(c.Escalation))
	m.OptSet("marks", card.Strings(c.Marks))
	return m
}

// Canonical is the result's deterministic encoding: cards sorted by ID.
func (r *InspectResult) Canonical() []byte {
	if r == nil {
		return []byte("null")
	}
	m := card.Obj{"schema": r.Schema}
	m.Str("table", r.Table)
	m.Str("epoch", r.Epoch)
	m.Str("revision", r.Revision)
	items := make([]any, len(r.Cards))
	for i, c := range r.Cards {
		items[i] = c.tree()
	}
	m["cards"] = card.Set{Key: "id", Items: items}
	m["missing"] = card.Strings(r.Missing)
	return card.Encode(m)
}

// Digest is the SHA-256 of the canonical result.
func (r *InspectResult) Digest() Digest { return sum(r.Canonical()) }

// ValidateInspectResult checks an inspect or check result is well formed: a table,
// an epoch and a revision, each card once with a valid place, an outcome exactly on
// a done card, a pinned identity, a head that is a code head when present, bounded
// standing and missing text, known drift and marks, valid counters, an escalation,
// and escalated exactly when it carries a mark.
func ValidateInspectResult(r *InspectResult) error {
	c := newCollector()
	c.op = OpInspect
	if r == nil {
		c.add(-1, "", "", CauseRequired, "no result", "", "fill the result")
		return c.err()
	}
	v := validator{c}
	if r.Schema != SchemaVersion {
		c.add(-1, "", "schema", CauseInvalidValue, strconv.Itoa(r.Schema), strconv.Itoa(SchemaVersion), "send schema "+strconv.Itoa(SchemaVersion))
	}
	v.name(-1, "", "table", r.Table)
	v.counter(-1, "", "epoch", r.Epoch, false)
	v.counter(-1, "", "revision", r.Revision, false)
	if len(r.Cards) > MaxReceiptCards {
		c.add(-1, "", "cards", CauseTooMany, strconv.Itoa(len(r.Cards)), strconv.Itoa(MaxReceiptCards), "a result holds at most that many cards")
	}
	seen := map[ID]bool{}
	for i := 0; i < len(r.Cards) && i < MaxReceiptCards; i++ {
		ic := &r.Cards[i]
		id := knownID(string(ic.ID))
		v.cardID(i, id, "id", ic.ID)
		if ic.ID != "" && seen[ic.ID] {
			c.add(i, id, "id", CauseRepeatedID, quote(string(ic.ID)), "one entry per card", "list each card once")
		}
		seen[ic.ID] = true
		v.name(i, id, "place.row", ic.Place.Row)
		v.counter(i, id, "revision", ic.Revision, true)
		v.placeAndOutcome(i, id, "place.col", "outcome", ic.Place.Col, ic.Outcome)
		v.digest(i, id, "pin.digest", ic.Pin.Digest)
		v.objectID(i, id, "pin.object_id", ic.Pin.ObjectID)
		v.objectID(i, id, "pin.commit", ic.Pin.Commit)
		v.repository(i, id, "pin.repository", ic.Pin.Repository)
		v.path(i, id, "pin.path", ic.Pin.Path)
		v.text(i, id, "pin.kind", ic.Pin.Kind, 32, card.ValidKind, "lower-case letters, digits and hyphen, starting with a letter")
		if ic.Head != "" {
			v.head(i, id, "head", ic.Head)
		}
		if ic.Standing != "" {
			if cause := card.TextFault(ic.Standing, MaxStandingBytes); cause != "" {
				v.fault(i, id, "standing", ic.Standing, cause, MaxStandingBytes, "one clean line")
			}
		}
		if len(ic.Missing) > MaxMissingItems {
			c.add(i, id, "missing", CauseTooMany, strconv.Itoa(len(ic.Missing)), strconv.Itoa(MaxMissingItems), "list at most that many")
		}
		for j := 0; j < len(ic.Missing) && j < MaxMissingItems; j++ {
			v.token(i, id, "missing["+strconv.Itoa(j)+"]", ic.Missing[j], MaxIdentityBytes)
		}
		for j, d := range ic.Drift {
			if !d.Valid() {
				c.add(i, id, "drift["+strconv.Itoa(j)+"]", CauseInvalidValue, quote(string(d)), "a kind of drift", "use one of the closed set")
			}
		}
		for _, n := range counterNames {
			if val := *n.get(&ic.Counters); val != "" && !card.ValidCounter(val) {
				c.add(i, id, "counters."+n.name, CauseInvalidValue, quote(val), "a decimal integer within uint64", "send a decimal counter")
			}
		}
		for j, m := range ic.Marks {
			if !m.Valid() {
				c.add(i, id, "marks["+strconv.Itoa(j)+"]", CauseInvalidValue, quote(string(m)), "an escalation mark", "use one of the closed set")
			}
		}
		switch ic.Escalation {
		case EscalationNone:
			if len(ic.Marks) > 0 {
				c.add(i, id, "escalation", CauseInvalidValue, "none with marks", "escalated", "a card with a mark is escalated")
			}
		case EscalationEscalated:
			if len(ic.Marks) == 0 {
				c.add(i, id, "escalation", CauseInvalidValue, "escalated with no mark", "none", "a card is escalated because of a mark")
			}
		default:
			c.add(i, id, "escalation", CauseInvalidValue, quote(string(ic.Escalation)), "none or escalated", "set the escalation")
		}
	}
	for i, id := range r.Missing {
		v.cardID(-1, "", "missing["+strconv.Itoa(i)+"]", id)
		if seen[id] {
			c.add(-1, knownID(string(id)), "missing["+strconv.Itoa(i)+"]", CauseInvalidValue, quote(string(id)), "a card not listed as found", "a card is found or missing, not both")
		}
	}
	return c.err()
}
