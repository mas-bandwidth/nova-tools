// Package tablejson is the seam between a record package and its generated
// table when the generated package's golden test imports the record package,
// so the record package can import no generated table. The record holds a Go
// value in the shape of the table's schema JSON text; an installer package
// that imports the generated table fills a Slot with a Wire that loads and
// saves the table through that text, as a database/sql driver registers. A
// program reads the table forms once it imports the installer; until then a
// table image is refused as unknown_form and only the legacy form reads.
//
// The rules: the owner's S2 (a text transport carries the padded standard
// base64 of the wire and the schema JSON text; a reader accepts either,
// refuses damage by name and counts reads per form) and S4 (readers first),
// held by pkg/schemaread.
package tablejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// Wire is one generated table's loads and saves through its schema JSON text.
type Wire struct {
	// Measure is the generated LoadMeasureReason: an error's text is the
	// refusal reason.
	Measure func(wire []byte) error
	// WireJSON loads the table wire and writes the value as the schema JSON
	// text; the report names what the load refused or counted.
	WireJSON func(wire []byte) ([]byte, schemaread.Report)
	// JSON loads the schema JSON text strictly and writes it back as the
	// generated writer writes it.
	JSON func(text []byte) ([]byte, schemaread.Report)
	// JSONWire loads the schema JSON text and writes the table wire.
	JSONWire func(text []byte) ([]byte, error)
}

// Slot is where an installer puts a table's Wire. The zero Slot holds none.
type Slot struct{ p atomic.Pointer[Wire] }

// Install puts w in the slot; a later Install replaces it.
func (s *Slot) Install(w Wire) { s.p.Store(&w) }

// Installed reports whether the slot holds a Wire.
func (s *Slot) Installed() bool { return s.p.Load() != nil }

// errNotInstalled is the measure refusal of a table no installer wired in.
var errNotInstalled = errors.New(schemaread.ReasonUnknownForm)

// decode reads the schema JSON text into v, refusing a field v does not hold:
// the value and the table must name the same fields.
func decode[T any](v *T, text []byte, rep schemaread.Report) schemaread.Report {
	d := json.NewDecoder(bytes.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		rep.Malformed = true
	}
	return rep
}

// refused reports whether rep refuses the read.
func refused(rep schemaread.Report) bool {
	return rep.Reason != "" || rep.Malformed || rep.KindMismatch > 0 || rep.Clamped > 0
}

// Codec is the schemaread codec of T, a Go value in the shape of the table's
// schema JSON text: the binary wire and its base64 through the slot's Wire,
// the schema JSON text through the slot's Wire, and the legacy form through
// legacy and loadLegacy. constraints states the record's bounds once for the
// reader and the writer.
func Codec[T any](name, help string, slot *Slot, legacy func([]byte) bool, loadLegacy func(*T, []byte) schemaread.Report, constraints func(*T, *schemaread.Checker)) *schemaread.Codec[T] {
	bounds := schemaread.NewReader(&schemaread.Codec[T]{Name: name, Help: help, Constraints: constraints})
	return &schemaread.Codec[T]{
		Name:  name,
		Help:  help,
		Reset: func(v *T) { var zero T; *v = zero },
		Measure: func(wire []byte) error {
			w := slot.p.Load()
			if w == nil {
				return errNotInstalled
			}
			return w.Measure(wire)
		},
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			w := slot.p.Load()
			if w == nil {
				return schemaread.Report{Reason: schemaread.ReasonUnknownForm}
			}
			text, rep := w.WireJSON(wire)
			if refused(rep) {
				return rep
			}
			rep = decode(v, text, rep)
			if refused(rep) {
				return rep
			}
			// A table image is the unique image of the value it reads: the
			// value written back is the same bytes, or the image is damage the
			// load let through (an element count cut short of its body, a
			// stray byte past the last field), refused as malformed and never
			// read short. A value past a bound (an enum id no enum names) is
			// left to the read's constraints, which name it clamped. These
			// records are tool-owned and their readers switch first (the
			// owner's S4), so no newer writer's image meets this reader.
			if again, err := w.JSONWire(text); (err != nil || !bytes.Equal(again, wire)) && bounds.Check(v) == nil {
				rep.Malformed = true
			}
			return rep
		},
		LoadJSON: func(v *T, in []byte) schemaread.Report {
			w := slot.p.Load()
			if w == nil {
				return schemaread.Report{Reason: schemaread.ReasonUnknownForm}
			}
			text, rep := w.JSON(in)
			if refused(rep) {
				return rep
			}
			return decode(v, text, rep)
		},
		Legacy:      legacy,
		LoadLegacy:  loadLegacy,
		Constraints: constraints,
		SaveWire: func(v *T) []byte {
			w := slot.p.Load()
			text, err := json.Marshal(v)
			if w == nil || err != nil {
				return nil
			}
			wire, err := w.JSONWire(text)
			if err != nil {
				return nil
			}
			return wire
		},
		SaveJSON: func(v *T) []byte {
			w := slot.p.Load()
			text, err := json.Marshal(v)
			if w == nil || err != nil {
				return nil
			}
			out, rep := w.JSON(text)
			if refused(rep) {
				return nil
			}
			// One line: the generated writer indents, and a text transport
			// (a hash field, a log line) carries the record on one line.
			var line bytes.Buffer
			if json.Compact(&line, out) != nil {
				return nil
			}
			return line.Bytes()
		},
	}
}
