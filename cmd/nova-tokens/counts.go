package main

// counts.go holds the JSON type of one count field and one cost field (docs/STANDARD.md
// section 2: one value, two renderings). The TEXT line prints the number, or the dash where
// the source never measured the field (rule 15: a count a source did not give is a dash,
// never 0); the SAME value in --json is a number, and an absent count is null. A count is a
// decimal integer here because every value wrapped comes from a renderer that prints the
// dash or the number: StatField, Agg.Cell and the fold's turn count. A cost is the decimal
// the cost renderers (tokens.Usd, tokens.UsdPerMtok) write, and its absent form is the same
// dash and null.

import (
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// count is a count field's value as its text rendering. String keeps that rendering; the
// JSON rendering of the same value is MarshalJSON's, so the object a program reads carries
// a number and never the dash string.
type count string

func (c count) String() string { return string(c) }

// MarshalJSON writes the number, or null for the dash. Any other value is a count this type
// was not meant to carry, and the error names it rather than printing invalid JSON.
func (c count) MarshalJSON() ([]byte, error) {
	if string(c) == tokens.Dash {
		return []byte("null"), nil
	}
	n, err := strconv.ParseInt(string(c), 10, 64)
	if err != nil {
		return nil, err
	}
	return strconv.AppendInt(nil, n, 10), nil
}

// cost is a cost field's value as its text rendering, count's sibling for a dollar amount.
// The TEXT line prints the decimal tokens.Usd and tokens.UsdPerMtok wrote, or the dash where
// no source reported a cost (rule 15: an absence is not a zero); the SAME value in --json is
// null for the dash, and the reported cost's string otherwise.
type cost string

func (c cost) String() string { return string(c) }

// MarshalJSON writes null for the dash, and the reported cost's string otherwise. The value
// a source reported keeps the rendering it had before this type carried it; only the absent
// cost, which read as the string "-", becomes the null an absent measurement is.
func (c cost) MarshalJSON() ([]byte, error) {
	if string(c) == tokens.Dash {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(string(c))), nil
}
