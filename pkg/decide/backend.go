package decide

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Fixed is the backend that answers from a table: question name -> the answer
// it gives, whatever the state. It needs no network and no key, so a first run,
// a test, or a dry comparison against a recorded answer set runs anywhere
// (SPEC-NOVA-DECIDE section 3).
type Fixed struct {
	Table map[string]FixedAnswer
}

// FixedAnswer is one row of a Fixed table: a choice's option and its
// probabilities, or a noul's probability of yes.
type FixedAnswer struct {
	Choice string             `json:"choice,omitempty"`
	P      map[string]float64 `json:"p,omitempty"`
	Noul   *float64           `json:"noul,omitempty"`
}

// ParseFixed reads a Fixed table: {"<question>": {"choice": ..., "p": {...}} | {"noul": p}}.
func ParseFixed(raw []byte) (Fixed, error) {
	var t map[string]FixedAnswer
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return Fixed{}, fmt.Errorf("the answers file is not JSON of the shape {<question>: {choice, p} | {noul}}: %w", err)
	}
	return Fixed{Table: t}, nil
}

// Name is the backend as the record names it.
func (Fixed) Name() string { return "fixed" }

// Ask answers every question of s from the table; a question the table does
// not answer is an error naming it, never a guess.
func (f Fixed) Ask(_ context.Context, s Schema, _ string) (map[string]Answer, Usage, error) {
	out := map[string]Answer{}
	var missing []string
	for _, name := range slices.Sorted(maps.Keys(s.Questions)) {
		row, ok := f.Table[name]
		switch {
		case !ok:
			missing = append(missing, name)
		case s.Questions[name].Type == Noul && row.Noul != nil:
			out[name] = noulAnswer(*row.Noul)
		default:
			out[name] = Answer{Type: Choice, Value: row.Choice, P: row.P}
		}
	}
	if len(missing) > 0 {
		return nil, Usage{}, fmt.Errorf("the answers file has no answer to %s", strings.Join(missing, ", "))
	}
	return out, Usage{}, nil
}
