package sprint

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// ResultUsage is one friend card's token line, the first `usage:` line of her
// RESULT.md: in, out and cache counts, and usd when the line carries a decimal.
type ResultUsage struct {
	In    int64  `json:"in,omitempty"`
	Out   int64  `json:"out,omitempty"`
	Cache int64  `json:"cache,omitempty"`
	USD   string `json:"usd,omitempty"`
}

// Tokens is in + out + cache. A sum that would overflow stays at the maximum.
func (u ResultUsage) Tokens() int64 {
	return addNonNeg(addNonNeg(u.In, u.Out), u.Cache)
}

// ParseResultUsage reads the first usage line of text. The counts are in=,
// out= and cache=, in any order; a missing count is zero; a negative, a
// fraction or a word is skipped. usd= is kept when it is a non-negative
// decimal. ok is false when the text has no usage line, or the line names
// none of the four.
func ParseResultUsage(text string) (ResultUsage, bool) {
	rest, ok := firstUsageLine(text)
	if !ok {
		return ResultUsage{}, false
	}
	var u ResultUsage
	saw := false
	for _, w := range strings.Fields(rest) {
		k, v, cut := strings.Cut(w, "=")
		if !cut {
			continue
		}
		switch k {
		case "in", "out", "cache":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				continue
			}
			saw = true
			switch k {
			case "in":
				u.In = n
			case "out":
				u.Out = n
			case "cache":
				u.Cache = n
			}
		case "usd":
			if _, err := cardcost.Decimal(v); err != nil {
				continue
			}
			u.USD = v
			saw = true
		}
	}
	if !saw {
		return ResultUsage{}, false
	}
	return u, true
}

// firstUsageLine is the text after the first `usage:` that is not the tail of
// a longer word (misusage: is not one).
func firstUsageLine(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, "usage:")
		if i < 0 {
			continue
		}
		if i > 0 && isWordByte(line[i-1]) {
			continue
		}
		return line[i+len("usage:"):], true
	}
	return "", false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// SumResultUsage adds counts and usd. A usd that is not a decimal is skipped.
// The usd sum is an exact decimal (cardcost.Text).
func SumResultUsage(parts []ResultUsage) ResultUsage {
	var u ResultUsage
	var usd *big.Rat
	for _, p := range parts {
		u.In = addNonNeg(u.In, p.In)
		u.Out = addNonNeg(u.Out, p.Out)
		u.Cache = addNonNeg(u.Cache, p.Cache)
		if p.USD == "" {
			continue
		}
		r, err := cardcost.Decimal(p.USD)
		if err != nil {
			continue
		}
		if usd == nil {
			usd = new(big.Rat)
		}
		usd.Add(usd, r)
	}
	if usd != nil {
		u.USD = cardcost.Text(usd)
	}
	return u
}

func addNonNeg(a, b int64) int64 {
	if b <= 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// FriendTokensCell is the friends table's tokens cell. api or metered shows
// dollars to the cent, rounded up (cardcost.Cents), from usd; no usd is $0.00.
// Every other billing word, including empty, is subscription and shows the
// compact token count. usd is not a price and is not used for a subscription.
func FriendTokensCell(u ResultUsage, billing string) string {
	switch strings.ToLower(strings.TrimSpace(billing)) {
	case "api", "metered":
		r := new(big.Rat)
		if u.USD != "" {
			if parsed, err := cardcost.Decimal(u.USD); err == nil {
				r = parsed
			}
		}
		return cardcost.Cents(r)
	default:
		return CompactTokens(u.Tokens())
	}
}

// CompactTokens is a non-negative count: under 1000 as an integer, otherwise
// one decimal place, half up, with a 1000-based suffix (1.2M, 2M).
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
		whole, frac := compactOneDecimal(n, u.div)
		if frac == 0 {
			return strconv.FormatInt(whole, 10) + u.suf
		}
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(frac, 10) + u.suf
	}
	return strconv.FormatInt(n, 10)
}

// compactOneDecimal is n/div to one decimal place, half up, as whole and frac.
func compactOneDecimal(n, div int64) (whole, frac int64) {
	q := n / div
	r := n % div
	extra := (r*10 + div/2) / div
	scaled := q*10 + extra
	return scaled / 10, scaled % 10
}
