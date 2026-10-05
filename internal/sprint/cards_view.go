package sprint

import (
	"cmp"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// CardsViewFilter specifies filters for primary cards.
type CardsViewFilter struct {
	Col    string
	Stream string
	Holder string
}

// CardSummary is one primary card's summary for view cards.
type CardSummary struct {
	ID     string  `json:"id"`
	Stream string  `json:"stream"`
	Col    string  `json:"col"`
	Tier   string  `json:"tier"`
	Holder string  `json:"holder,omitempty"`
	Score  float64 `json:"score,omitempty"`
}

// CardsViewResult is the gathered cards and counts for view cards.
type CardsViewResult struct {
	Total  int
	Counts map[string]int
	Cards  []CardSummary
}

// CardHolder returns the fleet member or friend that holds or held the primary card c.
func CardHolder(s *Snapshot, c *Card) string {
	if s.Fleet == nil {
		return ""
	}
	att := c.Int("attempt")
	var wc *Card
	if att > 0 {
		wc = s.Fleet.Card(WorkCardID(c.ID, att))
	}
	if wc == nil && c.F("work") != "" {
		wc = s.Fleet.Card(c.F("work"))
	}
	if wc == nil {
		for _, candidate := range s.Fleet.Of(c.ID) {
			if wc == nil || candidate.Int("attempt") > wc.Int("attempt") {
				wc = candidate
			}
		}
	}
	if wc == nil {
		return ""
	}
	if m := wc.F("member"); m != "" {
		return m
	}
	return wc.Row
}

// MatchHolder reports whether cardHolder matches the wanted member or friend name.
func MatchHolder(cardHolder, want string) bool {
	if cardHolder == "" {
		return want == "-" || want == ""
	}
	if cardHolder == want {
		return true
	}
	if friend, ok := FriendOfRow(cardHolder); ok && friend == want {
		return true
	}
	if wantFriend, ok := FriendOfRow(want); ok && wantFriend == cardHolder {
		return true
	}
	return false
}

// PrimaryTier returns the card's tier as the dealer reads it (CardTiers), defaulting
// to the dealer's default (cardhdr.RouteFlash).
func PrimaryTier(c *Card) string {
	now, _ := CardTiers(c)
	if now == "" {
		return cardhdr.RouteFlash
	}
	return now
}

// CollectCardsView gathers primary cards matching the filter from s.Work and aggregates
// them by 'by' (tier, stream, col, holder) if non-empty, or lists them in work order.
func CollectCardsView(s *Snapshot, f CardsViewFilter, by string) CardsViewResult {
	res := CardsViewResult{
		Counts: make(map[string]int),
	}
	if s.Work == nil {
		return res
	}

	var matched []*Card
	for _, c := range s.Work.Cards() {
		if !c.Placed() || IsSentinel(c) {
			continue
		}
		if f.Col != "" && c.Col != f.Col {
			continue
		}
		if f.Stream != "" && c.Row != f.Stream {
			continue
		}
		holder := CardHolder(s, c)
		if f.Holder != "" && !MatchHolder(holder, f.Holder) {
			continue
		}
		matched = append(matched, c)
	}

	res.Total = len(matched)

	if by != "" {
		for _, c := range matched {
			var key string
			switch by {
			case "tier":
				key = PrimaryTier(c)
			case "stream":
				key = c.Row
			case "col":
				key = c.Col
			case "holder":
				key = CardHolder(s, c)
				if key == "" {
					key = "-"
				}
			}
			res.Counts[key]++
		}
		return res
	}

	slices.SortStableFunc(matched, func(a, b *Card) int {
		return cmp.Or(cmp.Compare(a.Row, b.Row), cmp.Compare(a.Score, b.Score), cmp.Compare(a.ID, b.ID))
	})

	for _, c := range matched {
		res.Cards = append(res.Cards, CardSummary{
			ID:     c.ID,
			Stream: c.Row,
			Col:    c.Col,
			Tier:   PrimaryTier(c),
			Holder: CardHolder(s, c),
			Score:  c.Score,
		})
	}
	return res
}
