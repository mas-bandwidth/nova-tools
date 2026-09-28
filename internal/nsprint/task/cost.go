package task

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// Cost is what one task cost the friend who did it, carried in the task's
// done evidence (nova-tools #3105, #2756 spec 4.1.1, control 55). A metered
// cost names dollars, tokens in and out, the model and the route. A cost that
// was not measured is `cost: unmetered <reason>`: an absence, never $0, so a
// fold that sums reads prints the unmetered count beside the dollars.
type Cost struct {
	Metered   bool
	USDMicro  int64
	TokensIn  int64
	TokensOut int64
	Model     string
	Route     string
	// Reason says why an unmetered cost was not measured (for example
	// "plan-billed": a main-session read on a plan has no per-read $).
	Reason string
}

// Evidence field names of a metered cost.
const (
	CostUSDField       = "cost_usd"
	CostTokensInField  = "tokens_in"
	CostTokensOutField = "tokens_out"
	CostModelField     = "model"
	CostRouteField     = "route"
	// CostUnmetered opens the clause of a cost that was not measured.
	CostUnmetered = "cost: unmetered"
)

// Clause is the cost as the evidence text a done carries. Empty model, route
// or reason print as the dash, so every field is always present.
func (c Cost) Clause() string {
	if !c.Metered {
		return CostUnmetered + " " + orDash(oneWord(c.Reason, true))
	}
	return fmt.Sprintf("%s=%s %s=%d %s=%d %s=%s %s=%s",
		CostUSDField, tokens.Usd(c.USDMicro),
		CostTokensInField, c.TokensIn,
		CostTokensOutField, c.TokensOut,
		CostModelField, orDash(oneWord(c.Model, false)),
		CostRouteField, orDash(oneWord(c.Route, false)))
}

// WithCost appends the cost clause to evidence, once: evidence that already
// carries a cost clause is returned unchanged so a repeated done stays
// identical (exit 0 CLOSED, never CONFLICT). Empty evidence stays empty: a
// cost is not evidence, and the done is refused NOEVIDENCE.
func WithCost(evidence string, c Cost) string {
	if strings.TrimSpace(evidence) == "" {
		return evidence
	}
	if _, found, _ := ParseCost(evidence); found {
		return evidence
	}
	return evidence + " | " + c.Clause()
}

// ParseCost reads the cost clause out of done evidence. found is false when
// the evidence names no cost at all: that is unmetered, and a caller that
// sums must never count it as $0. A cost_usd that is not a dollar amount, or
// a token count that is not a whole number, is an error, not a zero.
// `cost_usd=-` is an explicit absence and parses as unmetered.
func ParseCost(evidence string) (Cost, bool, error) {
	if i := strings.Index(evidence, CostUnmetered); i >= 0 {
		rest := evidence[i+len(CostUnmetered):]
		if j := strings.Index(rest, " | "); j >= 0 {
			rest = rest[:j]
		}
		reason := strings.TrimSpace(rest)
		if reason == "" || reason == tokens.Dash {
			reason = ""
		}
		return Cost{Reason: reason}, true, nil
	}
	fields := map[string]string{}
	for _, word := range strings.Fields(evidence) {
		k, v, ok := strings.Cut(word, "=")
		if !ok {
			continue
		}
		switch k {
		case CostUSDField, CostTokensInField, CostTokensOutField, CostModelField, CostRouteField:
			if _, seen := fields[k]; !seen {
				fields[k] = strings.TrimRight(v, ",;")
			}
		}
	}
	raw, ok := fields[CostUSDField]
	if !ok {
		return Cost{}, false, nil
	}
	if strings.TrimSpace(raw) == "" || raw == tokens.Dash {
		return Cost{Reason: CostUSDField + "=-"}, true, nil
	}
	micro, ok := tokens.ParseMicro(raw)
	if !ok {
		return Cost{}, true, fmt.Errorf("%s=%q is not a dollar amount", CostUSDField, raw)
	}
	c := Cost{Metered: true, USDMicro: micro, Model: undash(fields[CostModelField]), Route: undash(fields[CostRouteField])}
	for _, f := range []struct {
		name string
		dst  *int64
	}{{CostTokensInField, &c.TokensIn}, {CostTokensOutField, &c.TokensOut}} {
		v, present := fields[f.name]
		if !present || v == tokens.Dash {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return Cost{}, true, fmt.Errorf("%s=%q is not a token count", f.name, v)
		}
		*f.dst = n
	}
	return c, true, nil
}

func orDash(s string) string {
	if s == "" {
		return tokens.Dash
	}
	return s
}

func undash(s string) string {
	if s == tokens.Dash {
		return ""
	}
	return s
}

// oneWord keeps a field on one evidence word: whitespace and the clause
// separator become '-'. A reason may keep inner spaces.
func oneWord(s string, spaces bool) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "|", "-"))
	if spaces {
		return strings.Join(strings.Fields(s), " ")
	}
	return strings.Join(strings.Fields(s), "-")
}
