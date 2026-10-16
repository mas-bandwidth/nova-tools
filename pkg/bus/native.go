package bus

// The native binary form (SPEC-BUS.md, the native form): the typed bus
// envelope (pkg/buswire, generated from schemas/bus/envelope.schema) rides in
// one stream field of its own, EnvelopeField, as the raw binary wire led by
// its form byte. A Redis stream field is a byte string, so the wire goes on
// the bus as it is, with no base64 and no text decode; the text forms (padded
// standard base64 of the wire, and the schema JSON text) are for text
// transports, never for the bus itself. The read is pkg/schemaread's strict
// one: damage is refused by name, never read as a shorter envelope, and each
// read is counted under the form it arrived in.

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/buswire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// EnvelopeField is the stream field that holds a message's typed envelope as
// the binary wire. An entry without it is a legacy entry, read from its text
// fields (Parse).
const EnvelopeField = "envelope"

// EnvelopeForm is the form byte the envelope's wire leads with: the table
// wire's first form. A reader refuses a wire led by any other byte as
// unknown_form.
const EnvelopeForm byte = 1

// EnvelopeHelp is the command an envelope refusal points at.
const EnvelopeHelp = "nova-bus send -h"

// envelopeName is the prefix of every envelope refusal.
const envelopeName = "ENVELOPE"

// envelope is one typed envelope and the region that holds it: the body is a
// pointer field, a self-relative reference into that region, so the value is
// never copied out of it.
type envelope struct {
	e      *buswire.Envelope
	region []byte
}

// envelopeBound is a string or array field's declared bound, read from the
// generated descriptors, never typed twice.
func envelopeBound(name string) int {
	for _, f := range buswire.EnvelopeTableType().Fields {
		if f.Name == name {
			return int(f.ArrayBound)
		}
	}
	panic("the bus envelope has no field " + name)
}

func envelopeReport(r *buswire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		// a key given twice in the JSON text is ambiguous: refused, never last-wins
		Malformed: r.Malformed || r.Duplicate > 0,
		Reason:    r.Reason,
	}
}

// alignedRegion is a buffer of n bytes aligned to 64, for a region load.
func alignedRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func envelopeLoadWire(d *envelope, wire []byte) schemaread.Report {
	n := buswire.EnvelopeLoadMeasure(wire)
	if n < 0 {
		return schemaread.Report{Malformed: true}
	}
	var r buswire.TableReport
	d.region = alignedRegion(n)
	d.e = buswire.EnvelopeLoad(d.region, wire, &r)
	if d.e == nil {
		return schemaread.Report{Malformed: true}
	}
	return envelopeReport(&r)
}

func envelopeLoadJSON(d *envelope, text []byte) schemaread.Report {
	var b buswire.EnvelopeBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	var r buswire.TableReport
	ok := buswire.EnvelopeFromJson(&b, text, &r)
	if !ok || !b.Lock() {
		return schemaread.Report{Malformed: true, Reason: r.Reason}
	}
	d.e, d.region = b.AsConst(), b.Region()
	return envelopeReport(&r)
}

func envelopeText(b []byte, n int32) string { return string(b[:n]) }

func envelopeBody(e *buswire.Envelope) string {
	v := buswire.TableStringAt(&e.Body)
	return string(v.Data[:v.Length])
}

// envelopeConstraints are the envelope's bounds for the reader and the
// writer: every field a sent message always has is required, because a
// variable table reads an absent field as zero, and a missing stamp or kind
// must never read as the epoch or as None.
func envelopeConstraints(d *envelope, c *schemaread.Checker) {
	e := d.e
	c.Required("envelope", e != nil)
	if e == nil {
		return
	}
	c.Required("id", e.IdLength > 0)
	c.Required("from", e.FromLength > 0)
	c.Required("to", e.ToCount > 0)
	c.Required("subject", e.SubjectLength > 0)
	c.Required("kind", e.Kind != buswire.KindNone)
	c.Required("at_ms", e.AtMsPresent)
	c.Required("body", e.Body != 0)
	if e.Kind == buswire.Kindask {
		c.Required("due_ms", e.DueMsPresent)
	}
	c.Enum("kind", int(e.Kind), int(buswire.KindCount)+1)
	for _, s := range []struct {
		name string
		v    string
	}{
		{"id", envelopeText(e.Id[:], e.IdLength)}, {"from", envelopeText(e.From[:], e.FromLength)},
		{"re", envelopeText(e.Re[:], e.ReLength)}, {"subject", envelopeText(e.Subject[:], e.SubjectLength)},
		{"card", envelopeText(e.Card[:], e.CardLength)}, {"head", envelopeText(e.Head[:], e.HeadLength)},
		{"token", envelopeText(e.Token[:], e.TokenLength)},
	} {
		c.Text(s.name, s.v, envelopeBound(s.name))
	}
	for i := range e.ToCount {
		c.Text("to", envelopeText(e.To[i].S[:], e.To[i].SLength), MaxName)
	}
	for i := range e.CcCount {
		c.Text("cc", envelopeText(e.Cc[i].S[:], e.Cc[i].SLength), MaxName)
	}
	if e.Body != 0 {
		c.Text("body", envelopeBody(e), MaxBody)
	}
}

func envelopeSaveWire(d *envelope) []byte {
	n := buswire.EnvelopeMeasure(d.e)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if buswire.EnvelopeSave(d.e, buf) != n {
		return nil
	}
	return buf
}

func envelopeSaveJSON(d *envelope) []byte {
	n := buswire.EnvelopeToJsonMeasure(d.e)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if buswire.EnvelopeToJson(d.e, buf) != n {
		return nil
	}
	return buf
}

// envelopeCodec is the generated table's functions as pkg/schemaread takes
// them.
func envelopeCodec() *schemaread.Codec[envelope] {
	return &schemaread.Codec[envelope]{
		Name:  envelopeName,
		Help:  EnvelopeHelp,
		Reset: func(d *envelope) { *d = envelope{} },
		Forms: []byte{EnvelopeForm},
		Measure: func(wire []byte) error {
			_, err := buswire.EnvelopeLoadMeasureReason(wire)
			return err
		},
		LoadWire:    envelopeLoadWire,
		LoadJSON:    envelopeLoadJSON,
		Constraints: envelopeConstraints,
		SaveWire:    envelopeSaveWire,
		SaveJSON:    envelopeSaveJSON,
	}
}

// envelopeRefusal is a writer's refusal of a value past a declared bound,
// spelled as the reader spells its own.
func envelopeRefusal(format string, args ...any) error {
	return &Refusal{[]string{fmt.Sprintf("%s REFUSED: %s; run: %s", envelopeName, fmt.Sprintf(format, args...), EnvelopeHelp)}}
}

// envelopeKind is the envelope kind a bus kind names; "" (a message from
// before kinds) is status, as KindName reads it.
func envelopeKind(name string) (buswire.Kind, error) {
	if name == "" {
		name = KindStatus
	}
	var names []string
	for k := buswire.Kind(1); k <= buswire.KindCount; k++ {
		if buswire.EnumNameKind(k) == name {
			return k, nil
		}
		names = append(names, buswire.EnumNameKind(k))
	}
	return 0, envelopeRefusal("kind %q is not one of %s", name, strings.Join(names, ", "))
}

// envelopeOf is the typed envelope of m: the writer side. A value past a
// declared bound is refused by name (a *Refusal), never cut.
func envelopeOf(m Message) (envelope, error) {
	var b buswire.EnvelopeBuilder
	if !b.Init() {
		return envelope{}, errors.New("the envelope builder did not start")
	}
	e := b.GetRoot()
	set := func(name string, dst []byte, n *int32, v string) error {
		if len(v) > envelopeBound(name) {
			return envelopeRefusal("%s over %d bytes", name, envelopeBound(name))
		}
		*n = int32(copy(dst, v))
		return nil
	}
	names := func(name string, dst []buswire.NameRow, n *int32, vs []string) error {
		if len(vs) > envelopeBound(name) {
			return envelopeRefusal("%s over %d names", name, envelopeBound(name))
		}
		for i, v := range vs {
			if len(v) > MaxName {
				return envelopeRefusal("a name in %s over %d bytes", name, MaxName)
			}
			dst[i].SLength = int32(copy(dst[i].S[:], v))
		}
		*n = int32(len(vs))
		return nil
	}
	k, err := envelopeKind(m.Kind)
	if err != nil {
		return envelope{}, err
	}
	e.Kind = k
	for _, err := range []error{
		set("id", e.Id[:], &e.IdLength, m.ID),
		set("from", e.From[:], &e.FromLength, m.From),
		names("to", e.To[:], &e.ToCount, m.To),
		names("cc", e.Cc[:], &e.CcCount, m.CC),
		set("re", e.Re[:], &e.ReLength, m.Re),
		set("subject", e.Subject[:], &e.SubjectLength, m.Subject),
		set("token", e.Token[:], &e.TokenLength, m.Token),
	} {
		if err != nil {
			return envelope{}, err
		}
	}
	if !m.At.IsZero() {
		e.AtMs, e.AtMsPresent = m.At.UnixMilli(), true
	}
	if !m.Due.IsZero() {
		e.DueMs, e.DueMsPresent = m.Due.UnixMilli(), true
	}
	if m.Body != "" {
		if len(m.Body) > MaxBody {
			return envelope{}, envelopeRefusal("body over %d bytes", MaxBody)
		}
		buswire.TableStringEmplace(&b.Main, &e.Body, []byte(m.Body))
	}
	if !b.Lock() {
		return envelope{}, errors.New("the envelope builder did not lock")
	}
	return envelope{e: b.AsConst(), region: b.Region()}, nil
}

// message is the bus message an envelope holds: the reader side.
func (d envelope) message() Message {
	e := d.e
	list := func(rows []buswire.NameRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, envelopeText(r.S[:], r.SLength))
		}
		return out
	}
	m := Message{
		ID: envelopeText(e.Id[:], e.IdLength), From: envelopeText(e.From[:], e.FromLength),
		To: list(e.To[:e.ToCount]), CC: list(e.Cc[:e.CcCount]),
		Re: envelopeText(e.Re[:], e.ReLength), Subject: envelopeText(e.Subject[:], e.SubjectLength),
		Token: envelopeText(e.Token[:], e.TokenLength),
	}
	if e.Kind != buswire.KindNone {
		m.Kind = buswire.EnumNameKind(e.Kind)
	}
	if e.AtMsPresent {
		m.At = time.UnixMilli(e.AtMs).UTC()
	}
	if e.DueMsPresent {
		m.Due = time.UnixMilli(e.DueMs).UTC()
	}
	if e.Body != 0 {
		m.Body = envelopeBody(e)
	}
	return m
}

// writeEnvelope is m in form, checked by the envelope's bounds before a byte is
// written: a value past one is refused by name and nothing is written.
func writeEnvelope(m Message, form schemaread.Form) ([]byte, error) {
	d, err := envelopeOf(m)
	if err != nil {
		return nil, err
	}
	out, err := schemaread.NewReader(envelopeCodec()).Write(&d, form)
	var ref *schemaread.Refusal
	if errors.As(err, &ref) {
		return nil, &Refusal{[]string{ref.Error()}}
	}
	return out, err
}

// EnvelopeWire is m as the envelope's binary wire, led by EnvelopeForm: the
// bytes the native field holds. Every field m sets is carried, the token
// included; NativeFields leaves the sender's token off an entry. A value past
// one of the envelope's bounds, or a field a sent message always has that m
// lacks, is a *Refusal naming it, and no wire is made.
func EnvelopeWire(m Message) ([]byte, error) {
	return writeEnvelope(m, schemaread.FormBinary)
}

// NativeFields is m's entry with the native field: the legacy text fields
// (Message.Fields), unchanged so a reader that has not learned the field
// reads the entry as before, and EnvelopeField holding the binary wire of the
// message as the entry holds it (no token: the token is the sender's, never on
// an entry).
func NativeFields(m Message) (map[string]string, error) {
	wire, err := EnvelopeWire(Message{ID: m.ID, From: m.From, To: m.To, CC: m.CC, Subject: m.Subject, Re: m.Re,
		Kind: m.Kind, At: m.At, Due: m.Due, Body: m.Body})
	if err != nil {
		return nil, err
	}
	f := m.Fields()
	f[EnvelopeField] = string(wire)
	return f, nil
}

// EnvelopeReader reads bus entries and text-transport envelopes strictly, and
// counts each read under the form it arrived in: binary (the native field),
// base64 and json (the text forms) and legacy (an entry with no native field).
// The counts are the cutover's evidence (readers first, then writers): when
// legacy stops moving, every writer has switched. It is safe for concurrent
// use.
type EnvelopeReader struct {
	typed  *schemaread.Reader[envelope]
	legacy atomic.Uint64
	// notWire is the native fields refused for holding no binary wire.
	notWire atomic.Uint64
}

// NewEnvelopeReader is a reader with every count at zero.
func NewEnvelopeReader() *EnvelopeReader {
	return &EnvelopeReader{typed: schemaread.NewReader(envelopeCodec())}
}

// ReadEntry is the message an entry's fields hold, and the form it was read
// in. With EnvelopeField there, the field is the message: it must be the
// binary wire (a text form in it is refused as unknown_form, since the bus
// carries no text form of the envelope), and damage is refused by name,
// never read as a shorter envelope and never patched from the legacy fields
// beside it. Without the field the entry is legacy and reads as Parse reads
// it. A refusal is a *schemaread.Refusal and answers no message.
func (r *EnvelopeReader) ReadEntry(fields map[string]string) (Message, schemaread.Form, error) {
	raw, native := fields[EnvelopeField]
	if !native {
		r.legacy.Add(1)
		return Parse(fields), schemaread.FormLegacy, nil
	}
	wire := []byte(raw)
	if form := r.typed.Detect(wire); form != schemaread.FormBinary {
		r.notWire.Add(1)
		return Message{}, form, &schemaread.Refusal{Name: envelopeName, Reason: schemaread.ReasonUnknownForm, Field: EnvelopeField, Help: EnvelopeHelp}
	}
	d, res, err := r.typed.Read(wire)
	if err != nil {
		return Message{}, res.Form, err
	}
	return d.message(), res.Form, nil
}

// ReadText is the message a text transport carried, in any of the
// envelope's forms: padded standard base64 of the wire, the JSON text, or the
// wire itself. Damage, and a text that is no form, are refused by name.
func (r *EnvelopeReader) ReadText(in []byte) (Message, schemaread.Form, error) {
	d, res, err := r.typed.Read(in)
	if err != nil {
		return Message{}, res.Form, err
	}
	return d.message(), res.Form, nil
}

// WriteText is m in form for a text transport (base64 or json; binary is the
// wire), refused by name before a byte is written when a value is past a
// bound. It counts nothing: counts are reads.
func (r *EnvelopeReader) WriteText(m Message, form schemaread.Form) ([]byte, error) {
	return writeEnvelope(m, form)
}

// Reads is the number of reads that arrived in form f.
func (r *EnvelopeReader) Reads(f schemaread.Form) uint64 {
	if f == schemaread.FormLegacy {
		return r.legacy.Load()
	}
	return r.typed.Counts().Reads(f)
}

// Snapshot is the counts as a flat map to export: "binary", "base64",
// "json", "legacy", "unknown_fields" and "refused_<reason>".
func (r *EnvelopeReader) Snapshot() map[string]uint64 {
	m := r.typed.Counts().Snapshot()
	m[schemaread.FormLegacy.String()] = r.legacy.Load()
	if n := r.notWire.Load(); n > 0 {
		m["refused_"+schemaread.ReasonUnknownForm] += n
	}
	return m
}
