package main

// counts.go holds the JSON type of one count field (docs/STANDARD.md section 2: one value,
// two renderings). The TEXT line prints the number, or the dash where the source never
// measured the field (rule 15: a count a source did not give is a dash, never 0); the SAME
// value in --json is a number, and an absent count is null. A count is a decimal integer
// here because every value wrapped comes from a renderer that prints the dash or the
// number: StatField, Agg.Cell and the fold's turn count.

import (
	"strconv"

	"github.com/nova-tools/internal/tokens"
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
