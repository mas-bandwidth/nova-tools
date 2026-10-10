package sprint

import (
	"cmp"
	"maps"
	"slices"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// The cards view (docs/SPEC-SPRINT.md section 11, "view cards"): the work table's primaries
// listed or counted by column, tier, stream or holder, so a coordinator reads what is in review
// by tier from one verb and scans no keys by hand.

// CardsFilter keeps the primaries of a column, a stream and a holder; "" keeps all.
type CardsFilter struct {
	Col, Stream, Holder string
}

// CardRow is one primary of the cards view.
type CardRow struct {
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Col    string `json:"col"`
	Tier   string `json:"tier"`
	Holder string `json:"holder,omitempty"` // the member or friend running it; "" when no one is
}

// CardTier is the tier the dealer reads on the primary c: the tier its last deal drew
// (FieldTierNow), a pinned tier or model, else its brief's line 1 (flash when it names none).
func CardTier(c *Card) string {
	m, _ := cardhdr.ReadModel(c.F("brief"))
	return cmp.Or(cardTier(c, m), cardhdr.RouteFlash)
}

// CardHolder is the member, or the friend, whose fleet row holds the primary c's working card;
// "" when none does.
func CardHolder(s *Snapshot, c *Card) string {
	for _, w := range s.Fleet.Of(c.ID) {
		if w.Col != Working {
			continue
		}
		if f, ok := FriendOfRow(w.Row); ok {
			return f
		}
		return w.Row
	}
	return ""
}

// CardRows lists the work table's primaries (sentinels left out) that the filter keeps, in
// stream, column and work order.
func CardRows(s *Snapshot, f CardsFilter) []CardRow {
	out := []CardRow{}
	for _, stream := range s.Streams() {
		if f.Stream != "" && stream != f.Stream {
			continue
		}
		for _, col := range States {
			if f.Col != "" && string(col) != f.Col {
				continue
			}
			for _, c := range s.Work.Cell(stream, string(col)) {
				if IsSentinel(c) {
					continue
				}
				r := CardRow{ID: c.ID, Stream: stream, Col: string(col), Tier: CardTier(c), Holder: CardHolder(s, c)}
				if f.Holder == "" || r.Holder == f.Holder {
					out = append(out, r)
				}
			}
		}
	}
	return out
}

// CardsBy is the rows counted by the field named: tier, stream, col or holder ("-" for no
// holder); ok is false for any other name.
func CardsBy(rows []CardRow, by string) (counts map[string]int, ok bool) {
	var key func(CardRow) string
	switch by {
	case "tier":
		key = func(r CardRow) string { return r.Tier }
	case "stream":
		key = func(r CardRow) string { return r.Stream }
	case "col":
		key = func(r CardRow) string { return r.Col }
	case "holder":
		key = func(r CardRow) string { return cmp.Or(r.Holder, "-") }
	default:
		return nil, false
	}
	counts = map[string]int{}
	for _, r := range rows {
		counts[key(r)]++
	}
	return counts, true
}

// SortedKeys is the counts' keys in order.
func SortedKeys(m map[string]int) []string { return slices.Sorted(maps.Keys(m)) }
