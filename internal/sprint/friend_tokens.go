package sprint

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// The friends table's tokens column (friends.tokens): each friend card's usage is
// summed per friend, and the cell shows the sum. A subscription friend shows the
// compact token count (1.2M); a friend her nova-config row bills per call (the
// billing field the cost card is adding) shows the dollars charged, to the cent,
// rounded up. Until that field is set, every friend is a subscription.

// FriendUsage reads a friend card's usage out of the report her session wrote: the
// first `usage:` line, whose in, out and cache words are cardcost's input, output
// and cache read, or a `tokens:` line, or the tokens segment of the friend
// machinery's `Cost:` line (intern/friend, lane_parity.go, PublishCost). ok is
// false when the text names none of the classes.
func FriendUsage(text string) (cardcost.Usage, bool) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(line, "#*-_ \t"))
		key, rest, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "usage", "tokens":
			return parseFriendUsageWords(rest), true
		case "cost":
			if i := strings.Index(rest, " tokens "); i >= 0 {
				return parseFriendUsageWords(rest[i+len(" tokens "):]), true
			}
		}
	}
	return cardcost.NoUsage(), false
}

// parseFriendUsageWords reads a usage line's words: in, out and cache are the
// card's own compact names for cardcost's input, output and cache read; every
// other word is cardcost's, so the machinery's tokens line reads as itself.
func parseFriendUsageWords(line string) cardcost.Usage {
	words := make([]string, 0, 8)
	for _, w := range strings.Fields(line) {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		switch k {
		case "in":
			words = append(words, "input="+v)
		case "out":
			words = append(words, "output="+v)
		case "cache":
			words = append(words, "cache_read="+v)
		default:
			words = append(words, w)
		}
	}
	return cardcost.ParseUsage(strings.Join(words, " "))
}

// FriendTokensFromCards is the token total of a friend's cards: each card's usage
// record (FieldUsage) summed over the classes the column counts, input, output,
// cache read and cache write, never reasoning. A card with no usage record adds
// nothing.
func FriendTokensFromCards(cards []*Card) int64 {
	var total int64
	for _, c := range cards {
		total += friendUsageTokens(cardcost.ParseUsage(c.F(FieldUsage)))
	}
	return total
}

// FriendChargedFromCards is the dollars a friend's cards charged, exact: each
// card's usage record's actual cost where it reported one, else its predicted
// one, summed by cardcost.Total. "" when no card named a cost.
func FriendChargedFromCards(cards []*Card) string {
	usages := make([]cardcost.Usage, 0, len(cards))
	for _, c := range cards {
		usages = append(usages, cardcost.ParseUsage(c.F(FieldUsage)))
	}
	return cardcost.SumUsage(usages).Charged
}

// friendUsageTokens is a usage record's tokens over the classes the friends table
// sums: input, output, cache read and cache write, never reasoning or requests.
func friendUsageTokens(u cardcost.Usage) int64 {
	var total int64
	for _, n := range []int64{u.Tokens.Input, u.Tokens.CacheRead, u.Tokens.CacheWrite, u.Tokens.Output} {
		if n > 0 {
			total += n
		}
	}
	return total
}

// FriendTokensCell is the friends table's tokens cell. A subscription friend (an
// empty or subscription billing word) shows the compact token count; a friend the
// row bills per call (api or metered) shows the charged dollars to the cent,
// rounded up. Until the billing field is set, every friend is a subscription.
func FriendTokensCell(tokens int64, charged, billing string) string {
	switch strings.ToLower(strings.TrimSpace(billing)) {
	case "api", "metered":
		return friendDollarsCell(charged)
	default:
		return CompactTokens(tokens)
	}
}

// friendDollarsCell is an amount as the friends table shows it: dollars and cents,
// rounded up (cardcost.Cents). An amount that does not read, or none, is $0.00.
func friendDollarsCell(charged string) string {
	r := new(big.Rat)
	if charged != "" {
		if parsed, err := cardcost.Decimal(charged); err == nil {
			r = parsed
		}
	}
	return cardcost.Cents(r)
}

// CompactTokens is a token count as the friends table shows it: under a thousand as
// itself, else one decimal place, half up, with a 1000-based suffix (1.2M, 2M).
func CompactTokens(n int64) string {
	if n < 0 {
		n = 0
	}
	for _, u := range []struct {
		div int64
		suf string
	}{
		{1_000_000_000_000, "T"},
		{1_000_000_000, "B"},
		{1_000_000, "M"},
		{1_000, "K"},
	} {
		if n < u.div {
			continue
		}
		scaled := (n*10 + u.div/2) / u.div
		whole, frac := scaled/10, scaled%10
		if frac == 0 {
			return strconv.FormatInt(whole, 10) + u.suf
		}
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(frac, 10) + u.suf
	}
	return strconv.FormatInt(n, 10)
}
