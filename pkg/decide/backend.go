package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
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

// ParseFixed reads a Fixed table in any form (the owner's rules S2 and S4,
// readers first): the binary image of decidewire FixedTable, the padded
// standard base64 of that image, the table's schema JSON text (a list of named
// rows), or the legacy file people write today,
// {"<question>": {"choice": ..., "p": {...}} | {"noul": p}}. Each form reads to
// one Fixed value; damage is refused by name (FIXED_ANSWERS REFUSED: <reason>;
// run: <help>) through the error, never read as an absent or empty table, and
// every read is counted per form (UNION.md C13) on ParseFixedReader.
func ParseFixed(raw []byte) (Fixed, error) {
	f, _, err := ParseFixedReader.Read(raw)
	if err != nil {
		return Fixed{}, err
	}
	return f, nil
}

// ParseFixedReader is the answers file's package reader, whose counters are
// the per-form read counts of every ParseFixed.
var ParseFixedReader = NewFixedReader()

// NewFixedReader is the answers file's reader with empty counters (S2: a
// reader accepts every form, refuses damage by name and counts reads per form).
func NewFixedReader() *schemaread.Reader[Fixed] {
	return schemaread.NewReader(&schemaread.Codec[Fixed]{
		Name:  "FIXED_ANSWERS",
		Help:  "nova-decide ask -h",
		Forms: []byte{variableForm},
		Reset: func(v *Fixed) { *v = Fixed{} },
		Measure: func(wire []byte) error {
			_, err := decidewire.FixedTableLoadMeasureReason(wire)
			return err
		},
		LoadWire:    fixedTyped,
		LoadJSON:    fixedJSON,
		Legacy:      fixedIsLegacy,
		LoadLegacy:  fixedLegacy,
		Constraints: fixedCheck,
	})
}

// fixedIsLegacy says whether in is the legacy answers file rather than the
// table's JSON text. Both are JSON objects, so the shape decides (S2): the
// table's text opens with its one field, "answers", holding a list; the legacy
// file is a map whose values are objects, so a question named answers still
// reads as legacy. A text too short or too broken to show the shape is the
// legacy text, refused as the legacy read refuses it.
func fixedIsLegacy(in []byte) bool {
	if !isLegacy(in) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(in))
	if _, err := dec.Token(); err != nil {
		return true
	}
	if k, err := dec.Token(); err != nil || k != "answers" {
		return true
	}
	d, err := dec.Token()
	return err != nil || d != json.Delim('[')
}

// fixedLegacy reads the legacy answers file as the base read did (a map keyed
// by question, unknown keys refused), keeping the decoder's reason in the
// refusal; a trailing byte after the one value is damage, refused as malformed
// rather than ignored (S2: damage is refused by name).
func fixedLegacy(v *Fixed, in []byte) schemaread.Report {
	var t map[string]FixedAnswer
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return schemaread.Report{Reason: fmt.Sprintf("the answers file is not JSON of the shape {<question>: {choice, p} | {noul}}: %v", err)}
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return schemaread.Report{Malformed: true}
	}
	if t == nil {
		// A JSON null is no table at all: refused, never read as empty (S2).
		return schemaread.Report{Malformed: true}
	}
	v.Table = t
	return schemaread.Report{}
}

// fixedFromTable is the table as the Go code holds it: a row per question, a
// noul kept as a pointer (absent is a choice row). ok is false when a question
// or an option is named twice (putOnce).
func fixedFromTable(r *decidewire.FixedTableRow, ar []*decidewire.TableArena) (Fixed, bool) {
	f := Fixed{Table: map[string]FixedAnswer{}}
	ok := true
	decidewire.FixedTableAnswersEach(&r.Answers, func(n *decidewire.NamedFixedAnswerRow) bool {
		a := FixedAnswer{Choice: strAt(n.Answer.Choice, ar)}
		decidewire.FixedAnswerPEach(&n.Answer.P, func(o *decidewire.OptionPRow) bool {
			if a.P == nil {
				a.P = map[string]float64{}
			}
			ok = putOnce(a.P, strAt(o.Option, ar), o.P)
			return ok
		}, ar...)
		if !ok {
			return false
		}
		if n.Answer.NoulPresent {
			p := n.Answer.Noul
			a.Noul = &p
		}
		ok = putOnce(f.Table, strAt(n.Question, ar), a)
		return ok
	}, ar...)
	return f, ok
}

// fixedTyped reads the binary image (the base64 form arrives here decoded),
// held to the one canonical image of the value it reads (canonical, S2).
func fixedTyped(v *Fixed, wire []byte) schemaread.Report {
	var b decidewire.FixedTableBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.FixedTableLoadBuilder(&b, wire, &rep) {
		return reportOf(&rep, false)
	}
	f, ok := fixedFromTable(b.GetRoot(), []*decidewire.TableArena{&b.Arena})
	if !ok {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	*v = f
	return typedDone(&rep, func() bool {
		return canonical[decidewire.FixedTable](&b, wire, decidewire.FixedTableMeasure, decidewire.FixedTableSave)
	})
}

// fixedJSON reads the table's schema JSON text. A key the table does not know
// is refused, as the legacy read refuses one (S4: the reader is never older
// than the writer, so an unknown key is a damaged name, which would otherwise
// read its field as absent), and so is a byte after the one value.
func fixedJSON(v *Fixed, text []byte) schemaread.Report {
	dec := json.NewDecoder(bytes.NewReader(text))
	if err := dec.Decode(new(json.RawMessage)); err != nil {
		return schemaread.Report{Malformed: true}
	}
	if _, err := dec.Token(); err != io.EOF {
		return schemaread.Report{Malformed: true}
	}
	var b decidewire.FixedTableBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.FixedTableFromJson(&b, text, &rep) {
		return reportOf(&rep, false)
	}
	out := reportOf(&rep, true)
	if out.Reason == "" && out.Unknown > 0 {
		out.Reason = schemaread.ReasonMalformed
	}
	f, ok := fixedFromTable(b.GetRoot(), []*decidewire.TableArena{&b.Arena})
	if !ok {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	*v = f
	return out
}

// fixedCheck holds every probability of the table to [0, 1], the bound
// Schema.Check holds an answer to (decide.go Check), alike in every form (S2);
// past it is refused as clamped, never read into the table. The rows are
// visited in question order, so the field a refusal names is stable.
func fixedCheck(f *Fixed, c *schemaread.Checker) {
	for _, q := range slices.Sorted(maps.Keys(f.Table)) {
		a := f.Table[q]
		for _, opt := range slices.Sorted(maps.Keys(a.P)) {
			prob(c, "p", a.P[opt])
		}
		if a.Noul != nil {
			prob(c, "noul", *a.Noul)
		}
	}
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
