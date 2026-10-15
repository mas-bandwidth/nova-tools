// Package schemaread is the one strict read every typed message uses. It takes
// the functions a generated table gives a caller (load, measure, the JSON
// reader) as a Codec, detects the form of the incoming bytes or text (binary
// wire by its form byte, padded standard base64 of that wire, JSON text, or a
// legacy text the caller recognises), measures, loads, and refuses by name
// what the permissive load would only count: malformed, clamped,
// kind_mismatch, unknown_form, wire_damaged and an absent required field. A
// refusal reads `<NAME> REFUSED: <reason>; run: <help>`. Unknown fields are
// tolerated and counted. The reader counts each read once, on the form read,
// in counters a caller exports. The write side runs the same bounds before a
// byte is written, because the generated writer checks none of them.
//
// The package imports no generated package: the generated table functions are
// passed in. The rules it holds are section 4.6 of the schema-tables union
// research (the facade rules) and the owner's S2: text transports carry both
// base64 and JSON, a reader accepts either, refuses damage by name and counts
// reads per form.
package schemaread

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Form is the form an incoming read arrived in.
type Form int

// The forms a read is counted under.
const (
	FormNone   Form = iota // nothing recognisable: refused, counted under no form
	FormBinary             // the wire, led by its form byte
	FormBase64             // padded standard base64 of the wire
	FormJSON               // the schema JSON text form
	FormLegacy             // a text the caller recognises
	formCount
)

func (f Form) String() string {
	switch f {
	case FormBinary:
		return "binary"
	case FormBase64:
		return "base64"
	case FormJSON:
		return "json"
	case FormLegacy:
		return "legacy"
	}
	return "none"
}

// The refusal reasons, spelled as the generated tables spell them (UNION.md
// section 4.6).
const (
	ReasonMalformed      = "malformed"
	ReasonClamped        = "clamped"
	ReasonKindMismatch   = "kind_mismatch"
	ReasonUnknownForm    = "unknown_form"
	ReasonWireDamaged    = "wire_damaged"
	ReasonAbsentRequired = "absent_required"
)

// Report is what a load function tells the helper, the generated TableReport
// reduced to the fields the strict read decides on.
type Report struct {
	Unknown      int    // unknown field ids skipped (newer data): tolerated, counted
	KindMismatch int    // a known id with a changed kind, skipped
	Clamped      int    // values clamped to the declared bounds
	Malformed    bool   // framing damage, decode stopped
	Reason       string // a refusal the load names itself, such as unknown_form; empty otherwise
}

// Codec is one table's functions plus what the helper needs to refuse by name.
type Codec[T any] struct {
	Name string // the refusal prefix, such as the message's type name
	Help string // the command a refusal points at: `run: <Help>`

	Reset func(*T) // restores the declared defaults before a load

	Forms    []byte                  // the form bytes this reader knows; empty means {1}
	Measure  func(wire []byte) error // the generated LoadMeasureReason shape; an error's text is the refusal reason
	LoadWire func(*T, []byte) Report // the generated Load over the whole wire, form byte included
	LoadJSON func(*T, []byte) Report // the generated FromJson

	Legacy     func([]byte) bool // true when the input is the caller's legacy text
	LoadLegacy func(*T, []byte) Report

	// Constraints states the bounds of the table once, for the reader and the
	// writer: it calls the Checker for each field. A violation after a load is
	// a refusal; a violation before a write refuses the write.
	Constraints func(*T, *Checker)

	SaveWire func(*T) []byte // the generated Measure and Save, returning the whole wire
	SaveJSON func(*T) []byte // the generated ToJson
}

// Refusal is the typed error of every refusal.
type Refusal struct {
	Name   string
	Reason string
	Field  string // the field a constraint named, when one did
	Help   string
}

func (r *Refusal) Error() string {
	reason := r.Reason
	if r.Field != "" {
		reason += " (" + r.Field + ")"
	}
	return fmt.Sprintf("%s REFUSED: %s; run: %s", r.Name, reason, r.Help)
}

// Checker collects the first violation of a table's constraints.
type Checker struct {
	reason, field string
}

func (c *Checker) fail(reason, field string) {
	if c.reason == "" {
		c.reason, c.field = reason, field
	}
}

// Required refuses an absent field. A variable table reads an absent field as
// zero, so the caller says whether the read value is present (non-zero).
func (c *Checker) Required(field string, present bool) {
	if !present {
		c.fail(ReasonAbsentRequired, field)
	}
}

// Bound refuses a value outside [lo, hi], the reader's clamp.
func (c *Checker) Bound(field string, v, lo, hi int64) {
	if v < lo || v > hi {
		c.fail(ReasonClamped, field)
	}
}

// Enum refuses an ordinal outside [0, count), the reader's clamp.
func (c *Checker) Enum(field string, ordinal, count int) {
	if ordinal < 0 || ordinal >= count {
		c.fail(ReasonClamped, field)
	}
}

// Text refuses a string over maxBytes (clamped), and one that is not UTF-8 or
// holds a NUL (malformed).
func (c *Checker) Text(field, s string, maxBytes int) {
	switch {
	case len(s) > maxBytes:
		c.fail(ReasonClamped, field)
	case !utf8.ValidString(s) || bytes.IndexByte([]byte(s), 0) >= 0:
		c.fail(ReasonMalformed, field)
	}
}

// Counts are the per-form read counters and the refusal and unknown-field
// tallies a caller exports.
type Counts struct {
	forms   [formCount]atomic.Uint64
	unknown atomic.Uint64
	mu      sync.Mutex
	refused map[string]uint64
}

// Reads is the number of reads that arrived in form f.
func (c *Counts) Reads(f Form) uint64 { return c.forms[f].Load() }

// Unknown is the number of unknown fields tolerated across all reads.
func (c *Counts) Unknown() uint64 { return c.unknown.Load() }

// Refused is the number of reads refused with reason.
func (c *Counts) Refused(reason string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refused[reason]
}

// Snapshot is the counters as a flat map to export: "binary", "base64",
// "json", "legacy", "unknown_fields" and "refused_<reason>".
func (c *Counts) Snapshot() map[string]uint64 {
	m := map[string]uint64{"unknown_fields": c.Unknown()}
	for f := FormBinary; f < formCount; f++ {
		m[f.String()] = c.Reads(f)
	}
	c.mu.Lock()
	for k, v := range c.refused {
		m["refused_"+k] = v
	}
	c.mu.Unlock()
	return m
}

// Names lists the snapshot keys in order, for a stable export.
func (c *Counts) Names() []string {
	m := c.Snapshot()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *Counts) refuse(reason string) {
	c.mu.Lock()
	if c.refused == nil {
		c.refused = map[string]uint64{}
	}
	c.refused[reason]++
	c.mu.Unlock()
}

// Result is what a successful read adds to the value.
type Result struct {
	Form    Form
	Unknown int // unknown fields skipped in this read
}

// Reader reads one table strictly and counts its reads.
type Reader[T any] struct {
	c      *Codec[T]
	counts Counts
}

// NewReader returns a reader over c.
func NewReader[T any](c *Codec[T]) *Reader[T] { return &Reader[T]{c: c} }

// Counts is the reader's counters.
func (r *Reader[T]) Counts() *Counts { return &r.counts }

func (r *Reader[T]) refusal(reason, field string) error {
	r.counts.refuse(reason)
	return &Refusal{Name: r.c.Name, Reason: reason, Field: field, Help: r.c.Help}
}

// Detect names the form of in. Legacy text is the caller's to recognise; JSON
// text opens with `{`; base64 text opens with a base64 character; anything
// else that is not printable text is the binary wire, whose form byte the
// load then checks.
func (r *Reader[T]) Detect(in []byte) Form {
	if len(in) == 0 {
		return FormNone
	}
	if r.c.Legacy != nil && r.c.Legacy(in) {
		return FormLegacy
	}
	t := bytes.TrimLeft(in, " \t\r\n")
	if len(t) == 0 {
		return FormNone
	}
	switch b := t[0]; {
	case b == '{':
		return FormJSON
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9', b == '+', b == '/':
		return FormBase64
	case b < 0x20, b >= 0x80:
		return FormBinary
	}
	return FormNone
}

// Read detects the form of in, measures, loads and refuses by name. It counts
// the read once under the form read, whether it is refused or not. A refused
// read returns the zero value.
func (r *Reader[T]) Read(in []byte) (T, Result, error) {
	var zero T
	form := r.Detect(in)
	if form == FormNone {
		return zero, Result{}, r.refusal(ReasonUnknownForm, "")
	}
	r.counts.forms[form].Add(1)

	var v T
	if r.c.Reset != nil {
		r.c.Reset(&v)
	}
	var rep Report
	switch form {
	case FormJSON:
		if r.c.LoadJSON == nil {
			return zero, Result{Form: form}, r.refusal(ReasonUnknownForm, "")
		}
		rep = r.c.LoadJSON(&v, bytes.TrimSpace(in))
	case FormLegacy:
		if r.c.LoadLegacy == nil {
			return zero, Result{Form: form}, r.refusal(ReasonUnknownForm, "")
		}
		rep = r.c.LoadLegacy(&v, in)
	default:
		wire := in
		if form == FormBase64 {
			// Padded standard base64 keeps the form byte and the framing check.
			dec, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(in)))
			if err != nil || len(dec) == 0 {
				return zero, Result{Form: form}, r.refusal(ReasonMalformed, "")
			}
			wire = dec
		}
		forms := r.c.Forms
		if len(forms) == 0 {
			forms = []byte{1}
		}
		if !bytes.Contains(forms, wire[:1]) {
			return zero, Result{Form: form}, r.refusal(ReasonUnknownForm, "")
		}
		if r.c.Measure != nil {
			if err := r.c.Measure(wire); err != nil {
				reason := err.Error()
				if reason == "" {
					reason = ReasonWireDamaged
				}
				return zero, Result{Form: form}, r.refusal(reason, "")
			}
		}
		rep = r.c.LoadWire(&v, wire)
	}

	res := Result{Form: form, Unknown: rep.Unknown}
	switch {
	case rep.Reason != "":
		return zero, res, r.refusal(rep.Reason, "")
	case rep.Malformed:
		return zero, res, r.refusal(ReasonMalformed, "")
	case rep.KindMismatch > 0:
		return zero, res, r.refusal(ReasonKindMismatch, "")
	case rep.Clamped > 0:
		return zero, res, r.refusal(ReasonClamped, "")
	}
	if err := r.Check(&v); err != nil {
		return zero, res, err
	}
	r.counts.unknown.Add(uint64(rep.Unknown))
	return v, res, nil
}

// Check runs the table's constraints over v: what the reader refuses after a
// load, the writer refuses before it writes.
func (r *Reader[T]) Check(v *T) error {
	if r.c.Constraints == nil {
		return nil
	}
	var c Checker
	r.c.Constraints(v, &c)
	if c.reason != "" {
		return r.refusal(c.reason, c.field)
	}
	return nil
}

// Write checks v, then writes it as form: the binary wire, padded standard
// base64 of the wire, or the JSON text. Nothing is written when the check
// refuses; legacy is read only.
func (r *Reader[T]) Write(v *T, form Form) ([]byte, error) {
	if err := r.Check(v); err != nil {
		return nil, err
	}
	switch form {
	case FormBinary, FormBase64:
		if r.c.SaveWire == nil {
			return nil, errors.New(r.c.Name + ": no wire writer")
		}
		wire := r.c.SaveWire(v)
		if form == FormBinary {
			return wire, nil
		}
		return []byte(base64.StdEncoding.EncodeToString(wire)), nil
	case FormJSON:
		if r.c.SaveJSON == nil {
			return nil, errors.New(r.c.Name + ": no JSON writer")
		}
		return r.c.SaveJSON(v), nil
	}
	return nil, fmt.Errorf("%s: cannot write form %s", r.c.Name, form)
}
