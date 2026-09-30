package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The derived indexes of the event-driven design (EVENT-DRIVEN-TICK-v2.md,
// layer 5, section 1.3.1 "The indexes and the sprint's own keys" and section
// 1.3.2 "How X derives indexes and due entries", with the due set of section
// 1.2), as pure Go: no store, no socket. IndexOps says, from one card's state
// before a step and after it, what to remove from and add to every index that
// is a function of the card; IndexDefinition (indexdef.go) says what every
// index is from scratch over a set of cards, straight from the definitions.
// The first is what a step applies, the second is what the first must always
// equal.
//
// The derivation is one table (IndexDefs, DueKinds), so a change to a
// definition is a changed row. It is the Go twin of the derivation the store's
// function runs on the real before-state; the two are compared on the same
// steps. What it does not cover is what the design writes from something
// other than one card's own state: the judgment keys (askwait, jopen, jall),
// the note list, the counters, the drop marks and the strangers, and the adds
// of wait:<n>, which the intents waitfor, needmet and needgone make.

// The names of the indexes and of the due set: the part of a stored key
// before its colon.
const (
	IndexSent  = "sent"  // sent:<s>, the sentinels of stream s
	IndexElig  = "elig"  // elig:<s>, the primaries of s free to go
	IndexFresh = "fresh" // fresh:<s>, the ready primaries never dealt
	IndexAgain = "again" // again:<s>, the ready primaries dealt before
	IndexWait  = "wait"  // wait:<n>, the waiting cards still counting the need n
	IndexDue   = "due"   // due, the due set: member <kind>:<id>, score the running ms
)

// IndexPiece is the most members of one key a step writes in one piece
// (section 1.3.2: "in pieces of at most 1,000 members").
const IndexPiece = 1000

// IndexKey names one index: its name and its argument, the stream of sent,
// elig, fresh and again, the need of wait, and none for the due set.
type IndexKey struct {
	Index string
	Arg   string
}

// String is the key without its deployment prefix and epoch: "elig:s1",
// "wait:s1-4", "due".
func (k IndexKey) String() string {
	if k.Arg == "" {
		return k.Index
	}
	return k.Index + ":" + k.Arg
}

// Stored is the key as the store holds it in a deployment at an epoch.
func (k IndexKey) Stored(n Names, epoch uint64) string { return n.KeyAt(k.String(), epoch) }

// IndexCard is a card as the indexes see it: the table it is placed in, its
// place, its score and its fields, the same things S.before reads from the
// store. A card with no column is a kept record with no place, and is in no
// index; a nil *IndexCard is a card that does not exist.
type IndexCard struct {
	Table    string // Work, Readers, Merge or Fleet
	ID       string
	Row, Col string
	Score    float64
	Fields   map[string]string
}

// NewIndexCard is the card as the indexes see it, from a card of the table
// named; nil when the card is nil.
func NewIndexCard(table string, c *Card) *IndexCard {
	if c == nil {
		return nil
	}
	return &IndexCard{Table: table, ID: c.ID, Row: c.Row, Col: c.Col, Score: c.Score, Fields: c.Fields}
}

func (c *IndexCard) placed() bool { return c != nil && c.Col != "" }

// whole is a field that holds a whole number: 0 when it is absent or empty, an
// error when it holds anything else.
func (c *IndexCard) whole(name string) (int64, error) {
	v := c.Fields[name]
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s card %s: field %s is %q, not a whole number", c.Table, c.ID, name, v)
	}
	return n, nil
}

// errNoRow is the refusal of a card that is placed and has no row: the key of
// an index is made of its row (the stream), and no key is made without one.
func errNoRow(c *IndexCard) error {
	return fmt.Errorf("%s card %s is placed at %s with no row", c.Table, c.ID, c.Col)
}

// IndexEntry is one member of one index.
type IndexEntry struct {
	Key    IndexKey
	Member string
	Score  float64
}

// Scored is a member of a sorted set with its score.
type Scored struct {
	Member string
	Score  float64
}

// IndexOp is what one step does to one key: the members it removes (ZREM) and
// the members it adds or re-scores (ZADD). A member is in one list only.
type IndexOp struct {
	Key IndexKey
	Rem []string
	Add []Scored
}

// String is the op on one line, "elig:s1 -p1 +p2@20": the key, each member
// removed after a minus, each member added after a plus with its score.
func (o IndexOp) String() string {
	var b strings.Builder
	b.WriteString(o.Key.String())
	for _, m := range o.Rem {
		b.WriteString(" -" + m)
	}
	for _, a := range o.Add {
		b.WriteString(" +" + a.Member + "@" + fmtScore(a.Score))
	}
	return b.String()
}

// CondTest is how an IndexCond compares a field to its value.
type CondTest string

// The tests an IndexCond makes.
const (
	FieldIs      CondTest = "is"     // the field is the value
	FieldIsNot   CondTest = "is not" // the field is anything else, absent included
	FieldNum     CondTest = "="      // the field, a whole number (absent is 0), equals the value
	FieldAtLeast CondTest = ">="     // the field, a whole number (absent is 0), is at least the value
	FieldUnset   CondTest = "unset"  // the field is absent or empty
)

// knownTests names the tests, for the refusal of a row that uses another.
const knownTests = "is, is not, =, >=, unset"

// IndexCond is one condition of an index's definition, on one field of the
// card.
type IndexCond struct {
	Field string
	Test  CondTest
	Value string
}

func (w IndexCond) holds(c *IndexCard) (bool, error) {
	v := c.Fields[w.Field]
	switch w.Test {
	case FieldIs:
		return v == w.Value, nil
	case FieldIsNot:
		return v != w.Value, nil
	case FieldUnset:
		return v == "", nil
	case FieldNum, FieldAtLeast:
		n, err := c.whole(w.Field)
		if err != nil {
			return false, err
		}
		want, err := strconv.ParseInt(w.Value, 10, 64)
		if err != nil {
			return false, fmt.Errorf("the definition's condition on %s names %q, not a whole number", w.Field, w.Value)
		}
		if w.Test == FieldNum {
			return n == want, nil
		}
		return n >= want, nil
	}
	return false, fmt.Errorf("the definition's condition on %s has the test %q; the tests are %s", w.Field, w.Test, knownTests)
}

// IndexDef is one row of the derivation table: an index whose members are a
// function of one card. A card is a member, under the key of its row and with
// its own score, when it is placed in Table at Col and every condition of
// Where holds.
type IndexDef struct {
	Index string
	Table string
	Col   string
	Where []IndexCond
}

func (d IndexDef) holds(c *IndexCard) (bool, error) {
	if c.Table != d.Table || c.Col != d.Col {
		return false, nil
	}
	in := true
	for _, w := range d.Where { // every condition is read, so a malformed field never depends on the order
		ok, err := w.holds(c)
		if err != nil {
			return false, err
		}
		in = in && ok
	}
	return in, nil
}

// IndexDefs is the derivation table of section 1.3.1, one row per index the
// step derives from the card alone (its "written by" is "X, derived"). The
// score of every member is the card's score, which the design's ranges read
// (elig:s below the first sentinel's score, fresh:s above it).
var IndexDefs = []IndexDef{
	// sent:<s>: sentinels of s. Kind sentinel, in waiting.
	{IndexSent, Work, Waiting, []IndexCond{{"kind", FieldIs, "sentinel"}}},
	// elig:<s>: primaries of s free to go. In waiting, not a sentinel, open = 0, no refused.
	{IndexElig, Work, Waiting, []IndexCond{{"kind", FieldIsNot, "sentinel"}, {"open", FieldNum, "0"}, {"refused", FieldUnset, ""}}},
	// fresh:<s>: ready primaries never dealt. In ready, attempt = 0, no refused; a sentinel is out by
	// kind (section 1.3.5).
	{IndexFresh, Work, Ready, []IndexCond{{"kind", FieldIsNot, "sentinel"}, {"attempt", FieldNum, "0"}, {"refused", FieldUnset, ""}}},
	// again:<s>: ready primaries dealt before. In ready, attempt >= 1, no bound, no refused.
	{IndexAgain, Work, Ready, []IndexCond{{"attempt", FieldAtLeast, "1"}, {"bound", FieldUnset, ""}, {"refused", FieldUnset, ""}}},
}

// DueKind is one row of the due table of section 1.2: a card of Table placed
// at Col, with its field Field set, has the entry <Kind>:<id> in the due set,
// scored by the field (running ms). The member names the card's row when OfRow
// is set (the stream, for a stream's control card).
type DueKind struct {
	Kind  string
	Table string
	Col   string
	Field string
	OfRow bool
}

// DueKinds is the due table: the five card kinds, each derived like an index.
// A work card is in the fleet table, a read card in the readers table, and a
// stream's control card in the merge table.
var DueKinds = []DueKind{
	{"untaken", Fleet, Ready, "due_untaken", false},           // a work card dealt and not taken
	{"unfinished", Fleet, Working, "due_unfinished", false},   // a work card taken
	{"unbegun", Readers, Asked, "due_unbegun", false},         // a read card asked
	{"unreported", Readers, Reading, "due_unreported", false}, // a read card begun
	{"mergeidle", Merge, Ctl, "due_mergeidle", true},          // a stream's control card
}

// waitNeedsField is the field a card names its needs in; the derivation reads
// it to remove a card that leaves waiting from every wait:<n> it is in.
const waitNeedsField = "needs"

// IndexFields is the fields of a card the derivation reads (the list ask N2
// gives S.before), beside its place and score: every field a definition of
// IndexDefs or DueKinds names, and the needs.
func IndexFields() []string {
	seen := map[string]bool{waitNeedsField: true}
	for _, d := range IndexDefs {
		for _, w := range d.Where {
			seen[w.Field] = true
		}
	}
	for _, k := range DueKinds {
		seen[k.Field] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// indexEntries is the card's members of every derived index and of the due
// set, from its own state alone. A card that does not exist or has no place is
// in none.
func indexEntries(c *IndexCard) ([]IndexEntry, error) {
	if !c.placed() {
		return nil, nil
	}
	var out []IndexEntry
	for _, d := range IndexDefs {
		in, err := d.holds(c)
		if err != nil {
			return nil, err
		}
		if in {
			if c.Row == "" {
				return nil, errNoRow(c)
			}
			out = append(out, IndexEntry{IndexKey{d.Index, c.Row}, c.ID, c.Score})
		}
	}
	for _, k := range DueKinds {
		if c.Table != k.Table || c.Col != k.Col || c.Fields[k.Field] == "" {
			continue
		}
		due, err := c.whole(k.Field)
		if err != nil {
			return nil, err
		}
		of := c.ID
		if k.OfRow {
			if c.Row == "" {
				return nil, errNoRow(c)
			}
			of = c.Row
		}
		out = append(out, IndexEntry{IndexKey{IndexDue, ""}, k.Kind + ":" + of, float64(due)})
	}
	return out, nil
}

// inWaiting says the card is placed in the work table's waiting column.
func inWaiting(c *IndexCard) bool { return c.placed() && c.Table == Work && c.Col == Waiting }

// waitRemovals is what section 1.3.1 gives X to do for wait:<n>: a card that
// leaves waiting, by a move or by leaving the table, is removed from every
// wait:<n> it names. Removing a member that is not there changes nothing; the
// adds are the intents' (waitfor at admission) and are not made here.
func waitRemovals(before, after *IndexCard) []IndexOp {
	if !inWaiting(before) || inWaiting(after) {
		return nil
	}
	var ops []IndexOp
	done := map[string]bool{}
	for _, n := range Split(before.Fields[waitNeedsField]) {
		if !done[n] {
			done[n] = true
			ops = append(ops, IndexOp{Key: IndexKey{IndexWait, n}, Rem: []string{before.ID}})
		}
	}
	return ops
}

// sortOps orders ops by key: index, then argument.
func sortOps(ops []IndexOp) {
	sort.Slice(ops, func(i, j int) bool {
		a, b := ops[i].Key, ops[j].Key
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		return a.Arg < b.Arg
	})
}

// diffEntries is the difference between a card's members before and after: a
// member that was there and is not is removed, a member that is new or whose
// score changed is added.
func diffEntries(before, after []IndexEntry) []IndexOp {
	type slot struct {
		key    IndexKey
		member string
	}
	was, now := map[slot]float64{}, map[slot]float64{}
	for _, e := range before {
		was[slot{e.Key, e.Member}] = e.Score
	}
	for _, e := range after {
		now[slot{e.Key, e.Member}] = e.Score
	}
	byKey := map[IndexKey]*IndexOp{}
	op := func(k IndexKey) *IndexOp {
		if byKey[k] == nil {
			byKey[k] = &IndexOp{Key: k}
		}
		return byKey[k]
	}
	for _, e := range before {
		if _, ok := now[slot{e.Key, e.Member}]; !ok {
			op(e.Key).Rem = append(op(e.Key).Rem, e.Member)
		}
	}
	for _, e := range after {
		if score, ok := was[slot{e.Key, e.Member}]; !ok || score != e.Score {
			op(e.Key).Add = append(op(e.Key).Add, Scored{e.Member, e.Score})
		}
	}
	ops := make([]IndexOp, 0, len(byKey))
	for _, o := range byKey {
		ops = append(ops, *o)
	}
	return ops
}

// IndexOps is what one step does to the indexes for one card, from its state
// before the step and after it: for every index of IndexDefs and every kind
// of DueKinds, the card's membership, key and score before and after, and the
// difference, one op per key; and the removal of a card that leaves waiting
// from every wait:<n> it names. A nil before is a card the step creates, a
// nil after (or one with no place) a card it removes. The ops are ordered by
// key. A change that touches nothing an index reads returns none.
//
// A field the definitions read that holds anything but a whole number where a
// number is meant is refused, naming it, and nothing is returned. Before and
// after must be the same card.
func IndexOps(before, after *IndexCard) ([]IndexOp, error) {
	if before != nil && after != nil && (before.Table != after.Table || before.ID != after.ID) {
		return nil, fmt.Errorf("before is %s card %s and after is %s card %s: one card is changed at a time", before.Table, before.ID, after.Table, after.ID)
	}
	was, err := indexEntries(before)
	if err != nil {
		return nil, err
	}
	now, err := indexEntries(after)
	if err != nil {
		return nil, err
	}
	ops := append(diffEntries(was, now), waitRemovals(before, after)...)
	sortOps(ops)
	return ops, nil
}

// IndexChange is one card's change in a step: its state before and after.
type IndexChange struct {
	Before, After *IndexCard
}

// StepIndexOps is the ops of a whole step: every card's ops folded into one
// op per key, its members in order, and cut into pieces of at most IndexPiece
// members a key (the removals and the adds cut alike). Applying the pieces in
// order is applying every card's ops one after the other. A card named twice
// is refused, as layer 1 refuses it.
func StepIndexOps(changes []IndexChange) ([]IndexOp, error) {
	type named struct{ table, id string }
	seen := map[named]bool{}
	byKey := map[IndexKey]*IndexOp{}
	for _, ch := range changes {
		c := ch.Before
		if c == nil {
			c = ch.After
		}
		if c == nil {
			continue
		}
		id := named{c.Table, c.ID}
		if seen[id] {
			return nil, fmt.Errorf("%s card %s is changed twice in one step", c.Table, c.ID)
		}
		seen[id] = true
		ops, err := IndexOps(ch.Before, ch.After)
		if err != nil {
			return nil, err
		}
		for _, o := range ops {
			if byKey[o.Key] == nil {
				byKey[o.Key] = &IndexOp{Key: o.Key}
			}
			f := byKey[o.Key]
			f.Rem, f.Add = append(f.Rem, o.Rem...), append(f.Add, o.Add...)
		}
	}
	folded := make([]IndexOp, 0, len(byKey))
	for _, o := range byKey {
		sort.Strings(o.Rem)
		sort.Slice(o.Add, func(i, j int) bool { return o.Add[i].Member < o.Add[j].Member })
		folded = append(folded, *o)
	}
	sortOps(folded)
	var out []IndexOp
	for _, o := range folded {
		out = append(out, pieces(o)...)
	}
	return out, nil
}

// pieces cuts one key's op into ops of at most IndexPiece removals and
// IndexPiece adds, in order.
func pieces(o IndexOp) []IndexOp {
	var out []IndexOp
	for i := 0; i < len(o.Rem) || i < len(o.Add); i += IndexPiece {
		p := IndexOp{Key: o.Key}
		if i < len(o.Rem) {
			p.Rem = o.Rem[i:min(i+IndexPiece, len(o.Rem))]
		}
		if i < len(o.Add) {
			p.Add = o.Add[i:min(i+IndexPiece, len(o.Add))]
		}
		out = append(out, p)
	}
	return out
}
