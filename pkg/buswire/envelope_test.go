package buswire_test

// The golden test of the bus envelope's table wire (schemas/bus/envelope.schema,
// generated into this package). The envelope is read strictly through
// pkg/schemaread: the codec below is the three functions the generated table
// gives a caller, wrapped so a loaded value keeps the region its body points
// into. The converters between today's pkg/bus Message and the Envelope live
// here too: they are what the dual-read test reads both forms with, and the
// writer-side refusals they spell are the definitions' bounds, read from the
// generated descriptors, never typed twice.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/buswire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doc is one envelope and the region that holds it: the body is a pointer
// field, a self-relative reference into that region, so the value is never
// copied out of it.
type doc struct {
	e      *buswire.Envelope
	region []byte
}

const help = "nova-bus send -h"

// field is the generated descriptor of one Envelope field.
func field(t *testing.T, name string) buswire.TableFieldInfo {
	t.Helper()
	for _, f := range buswire.EnvelopeTableType().Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("Envelope has no field %q", name)
	return buswire.TableFieldInfo{}
}

// bound is a string or array field's declared bound, from the descriptors.
func bound(name string) int {
	for _, f := range buswire.EnvelopeTableType().Fields {
		if f.Name == name {
			return int(f.ArrayBound)
		}
	}
	panic("Envelope has no field " + name)
}

func report(r *buswire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		// a key given twice in the JSON text is ambiguous: refused, never last-wins
		Malformed: r.Malformed || r.Duplicate > 0,
		Reason:    r.Reason,
	}
}

// region is an aligned buffer of n bytes for a region load.
func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func loadWire(d *doc, wire []byte) schemaread.Report {
	n := buswire.EnvelopeLoadMeasure(wire)
	if n < 0 {
		return schemaread.Report{Malformed: true}
	}
	var r buswire.TableReport
	d.region = region(n)
	d.e = buswire.EnvelopeLoad(d.region, wire, &r)
	if d.e == nil {
		return schemaread.Report{Malformed: true}
	}
	return report(&r)
}

func loadJSON(d *doc, text []byte) schemaread.Report {
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
	return report(&r)
}

func str(b []byte, n int32) string { return string(b[:n]) }

func body(e *buswire.Envelope) string {
	v := buswire.TableStringAt(&e.Body)
	return string(v.Data[:v.Length])
}

// constraints are the envelope's bounds for the reader and the writer: every
// field a sent message always has is required, because a variable table reads
// an absent field as zero, and a liveness count must never read a missing
// stamp or kind as the epoch or as None.
func constraints(d *doc, c *schemaread.Checker) {
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
		{"id", str(e.Id[:], e.IdLength)}, {"from", str(e.From[:], e.FromLength)},
		{"re", str(e.Re[:], e.ReLength)}, {"subject", str(e.Subject[:], e.SubjectLength)},
		{"card", str(e.Card[:], e.CardLength)}, {"head", str(e.Head[:], e.HeadLength)},
		{"token", str(e.Token[:], e.TokenLength)},
	} {
		c.Text(s.name, s.v, bound(s.name))
	}
	for i := range e.ToCount {
		c.Text("to", str(e.To[i].S[:], e.To[i].SLength), bus.MaxName)
	}
	for i := range e.CcCount {
		c.Text("cc", str(e.Cc[i].S[:], e.Cc[i].SLength), bus.MaxName)
	}
	if e.Body != 0 {
		c.Text("body", body(e), bus.MaxBody)
	}
}

func saveWire(d *doc) []byte {
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

func saveJSON(d *doc) []byte {
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

func codec() *schemaread.Codec[doc] {
	return &schemaread.Codec[doc]{
		Name:  "ENVELOPE",
		Help:  help,
		Reset: func(d *doc) { *d = doc{} },
		Measure: func(wire []byte) error {
			_, err := buswire.EnvelopeLoadMeasureReason(wire)
			return err
		},
		LoadWire:    loadWire,
		LoadJSON:    loadJSON,
		Constraints: constraints,
		SaveWire:    saveWire,
		SaveJSON:    saveJSON,
	}
}

// kindOf is the Kind a pkg/bus kind names; "" (a message from before kinds)
// is status, as pkg/bus reads it.
func kindOf(name string) (buswire.Kind, error) {
	if name == "" {
		name = bus.KindStatus
	}
	for k := buswire.Kind(1); k <= buswire.KindCount; k++ {
		if buswire.EnumNameKind(k) == name {
			return k, nil
		}
	}
	var names []string
	for k := buswire.Kind(1); k <= buswire.KindCount; k++ {
		names = append(names, buswire.EnumNameKind(k))
	}
	return 0, fmt.Errorf("ENVELOPE REFUSED: kind %q is not one of %s; run: %s", name, strings.Join(names, ", "), help)
}

// fromMessage is the typed envelope of a pkg/bus message: the writer side. A
// value past a declared bound is refused by name, never cut.
func fromMessage(m bus.Message) (doc, error) {
	var b buswire.EnvelopeBuilder
	if !b.Init() {
		return doc{}, errors.New("no builder")
	}
	e := b.GetRoot()
	set := func(name string, dst []byte, n *int32, v string) error {
		if len(v) > bound(name) {
			return fmt.Errorf("ENVELOPE REFUSED: %s over %d bytes; run: %s", name, bound(name), help)
		}
		*n = int32(copy(dst, v))
		return nil
	}
	names := func(name string, dst []buswire.NameRow, n *int32, vs []string) error {
		if len(vs) > bound(name) {
			return fmt.Errorf("ENVELOPE REFUSED: %s over %d names; run: %s", name, bound(name), help)
		}
		for i, v := range vs {
			if len(v) > bus.MaxName {
				return fmt.Errorf("ENVELOPE REFUSED: a name in %s over %d bytes; run: %s", name, bus.MaxName, help)
			}
			dst[i].SLength = int32(copy(dst[i].S[:], v))
		}
		*n = int32(len(vs))
		return nil
	}
	k, err := kindOf(m.Kind)
	if err != nil {
		return doc{}, err
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
			return doc{}, err
		}
	}
	if !m.At.IsZero() {
		e.AtMs, e.AtMsPresent = m.At.UnixMilli(), true
	}
	if !m.Due.IsZero() {
		e.DueMs, e.DueMsPresent = m.Due.UnixMilli(), true
	}
	if m.Body != "" {
		if len(m.Body) > bus.MaxBody {
			return doc{}, fmt.Errorf("ENVELOPE REFUSED: body over %d bytes; run: %s", bus.MaxBody, help)
		}
		buswire.TableStringEmplace(&b.Main, &e.Body, []byte(m.Body))
	}
	if !b.Lock() {
		return doc{}, errors.New("the builder did not lock")
	}
	return doc{e: b.AsConst(), region: b.Region()}, nil
}

// toMessage is the pkg/bus message an envelope holds: the reader side.
func toMessage(d doc) bus.Message {
	e := d.e
	list := func(rows []buswire.NameRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, str(r.S[:], r.SLength))
		}
		return out
	}
	m := bus.Message{
		ID: str(e.Id[:], e.IdLength), From: str(e.From[:], e.FromLength),
		To: list(e.To[:e.ToCount]), CC: list(e.Cc[:e.CcCount]),
		Re: str(e.Re[:], e.ReLength), Subject: str(e.Subject[:], e.SubjectLength),
		Token: str(e.Token[:], e.TokenLength),
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
		m.Body = body(e)
	}
	return m
}

// at is a fixed store stamp: tests take no real time.
var at = time.Date(2026, 10, 15, 21, 4, 5, 0, time.UTC)

// sent is a message as pkg/bus Send stores it: recipients sorted, the stamp
// to the second, a kind always named.
func sent(kind string) bus.Message {
	m := bus.Message{
		ID: "01JA2B3C4D5E6F7G8H9J0KMNPQ", From: "sender", To: []string{"reader-a", "reader-b"},
		CC: []string{"reader-c"}, Subject: "#202 HOLD at 3f9c", Re: "01JA2B3C4D5E6F7G8H9J0KMNPA",
		Kind: kind, At: at, Body: "the reader found it broken:\n- a line\n",
	}
	if kind == bus.KindAsk {
		m.Due = at.Add(10 * time.Minute)
	}
	return m
}

func readOf(t *testing.T, r *schemaread.Reader[doc], in []byte) (bus.Message, schemaread.Result) {
	t.Helper()
	d, res, err := r.Read(in)
	require.NoError(t, err)
	return toMessage(d), res
}

// refusalOf is the refusal a read gives in, "" when it reads.
func refusalOf(t *testing.T, r *schemaread.Reader[doc], in []byte) string {
	t.Helper()
	_, _, err := r.Read(in)
	if err == nil {
		return ""
	}
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), "ENVELOPE REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+help), err.Error())
	return err.Error()
}

func refused(reason, field string) string {
	if field != "" {
		reason += " (" + field + ")"
	}
	return "ENVELOPE REFUSED: " + reason + "; run: " + help
}

// fixture is a wire written by a generated writer of another definition
// (testdata/writers, testdata/writers/write), never by hand.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".b64"))
	require.NoError(t, err)
	return bytes.TrimSpace(b)
}

func wireOf(t *testing.T, r *schemaread.Reader[doc], m bus.Message) []byte {
	t.Helper()
	d, err := fromMessage(m)
	require.NoError(t, err)
	wire, err := r.Write(&d, schemaread.FormBinary)
	require.NoError(t, err)
	return wire
}

// TestLayer1Envelope: the Envelope, its Kind enum and its Name element
// round-trip as the binary wire, as padded base64 of the wire and as the JSON
// text, through pkg/schemaread; the definitions hold the bounds pkg/bus
// holds; and the value renders to the name-keyed text form.
func TestLayer1Envelope(t *testing.T) {
	t.Parallel()
	t.Run("definitions", func(t *testing.T) {
		t.Parallel()
		for name, want := range map[string]int{
			"id": bus.MaxName, "from": bus.MaxName, "re": bus.MaxName, "card": bus.MaxName,
			"subject": 200, "head": 40, "token": bus.MaxToken, "to": 64, "cc": 64,
		} {
			assert.Equal(t, want, bound(name), name)
		}
		for _, name := range []string{"to", "cc"} {
			assert.Equal(t, "Name", field(t, name).TypeName, name)
			assert.True(t, field(t, name).Counted, name)
		}
		assert.True(t, field(t, "body").Pointer, "body is *string: prose at its own size, null when absent")
		for _, name := range []string{"at_ms", "due_ms"} {
			assert.True(t, field(t, name).Optional, "%s is ?int64: an absent stamp is absent, never the epoch", name)
		}
		// the kinds are pkg/bus's, in its help order or not, every one named
		var kinds []string
		for k := buswire.Kind(1); k <= buswire.KindCount; k++ {
			kinds = append(kinds, buswire.EnumNameKind(k))
		}
		assert.ElementsMatch(t, bus.Kinds, kinds)
		assert.Equal(t, "None", buswire.EnumNameKind(buswire.KindNone))
		var name buswire.Name
		buswire.NameReset(&name)
		assert.Len(t, name.S, bus.MaxName+1, "Name holds MaxName bytes and a terminator")
	})
	t.Run("round trip in every form", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		full := sent(bus.KindAsk)
		full.Token = "retry.ab:1-x"
		cases := []bus.Message{full, fuzzMin()}
		for _, k := range bus.Kinds {
			cases = append(cases, sent(k))
		}
		every := sent(bus.KindReport)
		every.ID, every.From, every.Re = strings.Repeat("a", 64), strings.Repeat("f", 64), strings.Repeat("r", 64)
		every.Subject, every.Token = strings.Repeat("s", 200), strings.Repeat("t", bus.MaxToken)
		every.To, every.CC = nil, nil
		for i := range 64 {
			every.To = append(every.To, fmt.Sprintf("%064d", i))
			every.CC = append(every.CC, fmt.Sprintf("c%063d", i))
		}
		every.Body = strings.Repeat("b", bus.MaxBody)
		cases = append(cases, every)
		for i, m := range cases {
			d, err := fromMessage(m)
			require.NoError(t, err, i)
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				out, err := r.Write(&d, form)
				require.NoError(t, err, "%d %s", i, form)
				got, res := readOf(t, r, out)
				assert.Equal(t, form, res.Form, "%d", i)
				assert.Zero(t, res.Unknown, "%d %s", i, form)
				assert.Equal(t, m, got, "%d %s", i, form)
			}
			// the wire is the canonical form: a saved wire re-saves to the same bytes
			wire, err := r.Write(&d, schemaread.FormBinary)
			require.NoError(t, err)
			d2, _, err := r.Read(wire)
			require.NoError(t, err)
			again, err := r.Write(&d2, schemaread.FormBinary)
			require.NoError(t, err)
			assert.Equal(t, wire, again, "%d", i)
			b64, err := r.Write(&d, schemaread.FormBase64)
			require.NoError(t, err)
			assert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(b64), "base64 is padded standard base64 of the wire, form byte first")
			assert.Equal(t, byte(1), wire[0], "the wire leads with its form byte")
		}
		n := uint64(len(cases))
		assert.Equal(t, 2*n, r.Counts().Reads(schemaread.FormBinary), "each case reads as the wire twice: once in the form loop, once for the canonical check")
		assert.Equal(t, n, r.Counts().Reads(schemaread.FormBase64))
		assert.Equal(t, n, r.Counts().Reads(schemaread.FormJSON))
		assert.Zero(t, r.Counts().Reads(schemaread.FormLegacy))
	})
	t.Run("the typed card and head references round-trip", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		d := fuzzFullDoc(t)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&d, form)
			require.NoError(t, err, form)
			got, _, err := r.Read(out)
			require.NoError(t, err, form)
			assert.Equal(t, "l1-defs-bus-envelope-2", str(got.e.Card[:], got.e.CardLength), form)
			assert.Equal(t, strings.Repeat("3f9c", 10), str(got.e.Head[:], got.e.HeadLength), form)
			assert.Equal(t, toMessage(d), toMessage(got), form)
		}
	})
	t.Run("renders to the name-keyed text form", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		m := sent(bus.KindAsk)
		m.Token = "retry-1"
		d, err := fromMessage(m)
		require.NoError(t, err)
		text, err := r.Write(&d, schemaread.FormJSON)
		require.NoError(t, err)
		golden, err := os.ReadFile(filepath.Join("testdata", "envelope.json"))
		require.NoError(t, err)
		assert.Equal(t, string(golden), string(text), "the text form is testdata/envelope.json")
		for _, key := range []string{`"id"`, `"from"`, `"to"`, `"cc"`, `"re"`, `"kind": "ask"`, `"subject"`, `"token"`, `"at_ms"`, `"due_ms"`, `"body"`, `"s": "reader-a"`} {
			assert.Contains(t, string(text), key, "keyed by the field's name; the kind and each name by their own")
		}
		var name buswire.Name
		name.SLength = int32(copy(name.S[:], "sender"))
		buf := make([]byte, buswire.NameToJsonMeasure(&name))
		require.Positive(t, buswire.NameToJson(&name, buf))
		assert.Contains(t, string(buf), `"s": "sender"`)
	})
	t.Run("the committed fuzz corpus is what the writer writes", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		minDoc, err := fromMessage(fuzzMin())
		require.NoError(t, err)
		for name, d := range map[string]doc{"envelope_full": fuzzFullDoc(t), "envelope_min": minDoc} {
			for ext, form := range map[string]schemaread.Form{".bin": schemaread.FormBinary, ".json": schemaread.FormJSON} {
				want, err := r.Write(&d, form)
				require.NoError(t, err, name)
				got, err := os.ReadFile(filepath.Join("..", "..", "schemas", "bus", "fuzz", name+ext))
				require.NoError(t, err, name+ext)
				assert.Equal(t, string(want), string(got), "schemas/bus/fuzz/%s%s", name, ext)
			}
		}
	})
}

// fuzzFullDoc and fuzzMin are the wire-fuzz corpus's two seeds
// (schemas/bus/fuzz/MANIFEST.txt): every field set, and the fewest a sent
// message has.
func fuzzFullDoc(t *testing.T) doc {
	t.Helper()
	m := sent(bus.KindAsk)
	m.Token = "retry-1"
	d, err := fromMessage(m)
	require.NoError(t, err)
	d.e.CardLength = int32(copy(d.e.Card[:], "l1-defs-bus-envelope-2"))
	d.e.HeadLength = int32(copy(d.e.Head[:], strings.Repeat("3f9c", 10)))
	return d
}

func fuzzMin() bus.Message {
	return bus.Message{ID: "01JA2B3C4D5E6F7G8H9J0KMNPQ", From: "sender", To: []string{"reader-a"}, Subject: "s", Kind: bus.KindStatus, At: at, Body: "b"}
}

// TestLayer1EnvelopeRefusals: each named refusal of the schema-table design
// (UNION.md section 4.6) that applies to the envelope fires on its bound, on
// the writer before a byte is written and on the reader after a load.
func TestLayer1EnvelopeRefusals(t *testing.T) {
	t.Parallel()
	t.Run("the writer refuses past each bound and writes nothing", func(t *testing.T) {
		t.Parallel()
		at := func(f func(*bus.Message)) error {
			m := sent(bus.KindStatus)
			f(&m)
			_, err := fromMessage(m)
			return err
		}
		ok := func(f func(*bus.Message)) { assert.NoError(t, at(f)) }
		ok(func(m *bus.Message) { m.Subject = strings.Repeat("s", 200) })
		assert.EqualError(t, at(func(m *bus.Message) { m.Subject = strings.Repeat("s", 201) }),
			"ENVELOPE REFUSED: subject over 200 bytes; run: nova-bus send -h")
		assert.EqualError(t, at(func(m *bus.Message) { m.Kind = "x" }),
			`ENVELOPE REFUSED: kind "x" is not one of status, report, ack, request, blocker, ask, timeout, judgment; run: nova-bus send -h`)
		for _, k := range bus.Kinds {
			ok(func(m *bus.Message) { m.Kind = k })
		}
		ok(func(m *bus.Message) { m.ID = strings.Repeat("i", bus.MaxName) })
		assert.EqualError(t, at(func(m *bus.Message) { m.ID = strings.Repeat("i", bus.MaxName+1) }),
			"ENVELOPE REFUSED: id over 64 bytes; run: nova-bus send -h")
		ok(func(m *bus.Message) { m.Token = strings.Repeat("t", bus.MaxToken) })
		assert.EqualError(t, at(func(m *bus.Message) { m.Token = strings.Repeat("t", bus.MaxToken+1) }),
			"ENVELOPE REFUSED: token over 128 bytes; run: nova-bus send -h")
		ok(func(m *bus.Message) { m.To = slices.Repeat([]string{"reader-a"}, 64) })
		assert.EqualError(t, at(func(m *bus.Message) { m.To = slices.Repeat([]string{"reader-a"}, 65) }),
			"ENVELOPE REFUSED: to over 64 names; run: nova-bus send -h")
		ok(func(m *bus.Message) { m.CC = []string{strings.Repeat("n", bus.MaxName)} })
		assert.EqualError(t, at(func(m *bus.Message) { m.CC = []string{strings.Repeat("n", bus.MaxName+1)} }),
			"ENVELOPE REFUSED: a name in cc over 64 bytes; run: nova-bus send -h")
		ok(func(m *bus.Message) { m.Body = strings.Repeat("b", bus.MaxBody) })
		assert.EqualError(t, at(func(m *bus.Message) { m.Body = strings.Repeat("b", bus.MaxBody+1) }),
			"ENVELOPE REFUSED: body over 1048576 bytes; run: nova-bus send -h")
	})
	t.Run("the writer refuses what its own reader refuses", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		for _, tc := range []struct {
			name, reason string
			f            func(*bus.Message)
		}{
			{"no subject", refused("absent_required", "subject"), func(m *bus.Message) { m.Subject = "" }},
			{"no recipient", refused("absent_required", "to"), func(m *bus.Message) { m.To = nil }},
			{"no stamp", refused("absent_required", "at_ms"), func(m *bus.Message) { m.At = time.Time{} }},
			{"no body", refused("absent_required", "body"), func(m *bus.Message) { m.Body = "" }},
			{"an ask with no deadline", refused("absent_required", "due_ms"), func(m *bus.Message) { m.Kind, m.Due = bus.KindAsk, time.Time{} }},
			{"a NUL in the subject", refused("malformed", "subject"), func(m *bus.Message) { m.Subject = "a\x00b" }},
			{"a subject that is not UTF-8", refused("malformed", "subject"), func(m *bus.Message) { m.Subject = "a\xffb" }},
		} {
			m := sent(bus.KindStatus)
			tc.f(&m)
			d, err := fromMessage(m)
			require.NoError(t, err, tc.name)
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				out, err := r.Write(&d, form)
				assert.Nil(t, out, "%s: nothing is written", tc.name)
				assert.EqualError(t, err, tc.reason, "%s %s", tc.name, form)
			}
		}
	})
	t.Run("the reader refuses damage and newer input by name", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		good := wireOf(t, r, sent(bus.KindStatus))
		newerForm := slices.Clone(good)
		newerForm[0] = 9
		b64 := func(w []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(w)) }
		for _, tc := range []struct {
			name   string
			in     []byte
			reason string
		}{
			{"truncated wire", good[:len(good)-1], refused("wire_damaged", "")},
			{"truncated to the form byte", good[:1], refused("wire_damaged", "")},
			{"trailing junk", append(slices.Clone(good), 0, 1), refused("wire_damaged", "")},
			{"a form this reader does not know", newerForm, refused("unknown_form", "")},
			{"the same as base64", b64(newerForm), refused("unknown_form", "")},
			{"truncated base64", b64(good)[:len(b64(good))-3], refused("malformed", "")},
			{"not a message at all", []byte("#202 HOLD at 3f9c"), refused("unknown_form", "")},
			{"empty", nil, refused("unknown_form", "")},
			{"a JSON text that does not parse", []byte(`{"id": `), refused("malformed", "")},
			{"a JSON key given twice", []byte(`{"subject": "a", "subject": "b"}`), refused("malformed", "")},
			{"a subject over 200 bytes as JSON", jsonWith(t, "subject", strings.Repeat("s", 201)), refused("clamped", "")},
			{"a kind no build names as JSON", jsonWith(t, "kind", "notice"), refused("absent_required", "kind")},
			{"a subject over 200 bytes from a wider writer", fixture(t, "oversize-subject"), refused("clamped", "")},
			{"65 recipients from a wider writer", fixture(t, "too-many-recipients"), refused("clamped", "")},
			{"a 65-byte name from a wider writer", fixture(t, "oversize-name"), refused("clamped", "")},
			{"a kind this build does not name", fixture(t, "newer-kind"), refused("absent_required", "kind")},
			{"a subject that changed kind", fixture(t, "subject-changed-kind"), refused("kind_mismatch", "")},
		} {
			assert.Equal(t, tc.reason, refusalOf(t, r, tc.in), tc.name)
		}
		for i := range len(good) {
			assert.NotEmpty(t, refusalOf(t, r, good[:i]), "every truncation is refused, never read as a shorter envelope: cut at %d", i)
		}
		assert.Positive(t, r.Counts().Refused("wire_damaged"))
		assert.Positive(t, r.Counts().Refused("clamped"))
	})
	t.Run("a newer writer's field is skipped and counted", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		m, res := readOf(t, r, fixture(t, "newer-field"))
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, uint64(1), r.Counts().Unknown())
		assert.Equal(t, "sender", m.From)
		assert.Equal(t, []string{"reader-a"}, m.To)
	})
}

// jsonWith is a good envelope's JSON text with one key's value replaced.
func jsonWith(t *testing.T, key string, value any) []byte {
	t.Helper()
	r := schemaread.NewReader(codec())
	d, err := fromMessage(fuzzMin())
	require.NoError(t, err)
	text, err := r.Write(&d, schemaread.FormJSON)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(text, &m))
	require.Contains(t, m, key)
	m[key] = value
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return out
}

// TestLayer1EnvelopeDualRead is this aspect's interop test. The old form
// (today's stream entry, written by Message.Fields and read by bus.Parse) and
// the new form (the typed envelope in each of its three forms) read
// identically; and a writer switched to the typed envelope still hands
// today's readers the entry they read today, field for field, so old readers
// keep working while writers switch. Nothing in pkg/bus changes in this step.
func TestLayer1EnvelopeDualRead(t *testing.T) {
	t.Parallel()
	r := schemaread.NewReader(codec())
	var corpus []bus.Message
	for _, k := range bus.Kinds {
		corpus = append(corpus, sent(k))
	}
	one := sent(bus.KindReport)
	one.CC, one.Re = nil, ""
	corpus = append(corpus, one, fuzzMin())
	for i, m := range corpus {
		entry := m.Fields()
		old := bus.Parse(entry) // the old writer, then the old reader
		d, err := fromMessage(old)
		require.NoError(t, err, i)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&d, form)
			require.NoError(t, err, "%d %s", i, form)
			got, _ := readOf(t, r, out)
			assert.Equal(t, old, got, "%d %s: the new form reads as the old form reads", i, form)
			// the writer switch: the typed value, projected to today's entry,
			// is the entry today's writer writes, and today's reader reads it alike
			assert.Equal(t, entry, got.Fields(), "%d %s: the entry an old reader meets is unchanged", i, form)
			assert.Equal(t, old, bus.Parse(got.Fields()), "%d %s", i, form)
		}
	}
	assert.Equal(t, uint64(len(corpus)), r.Counts().Reads(schemaread.FormBinary), "the per-form counter moves on the form read")
	assert.Equal(t, uint64(len(corpus)), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(len(corpus)), r.Counts().Reads(schemaread.FormJSON))

	t.Run("a message from before kinds reads as status in both forms", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		m := sent(bus.KindStatus)
		m.Kind = ""
		old := bus.Parse(m.Fields())
		d, err := fromMessage(old)
		require.NoError(t, err)
		out, err := r.Write(&d, schemaread.FormBinary)
		require.NoError(t, err)
		got, _ := readOf(t, r, out)
		assert.Equal(t, old.KindName(), got.KindName())
		assert.Equal(t, bus.KindStatus, got.Kind, "the typed form always names its kind")
	})
	t.Run("an old entry missing a field the typed form requires is refused by name, never zeroed", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec())
		entry := sent(bus.KindStatus).Fields()
		delete(entry, "at")
		old := bus.Parse(entry)
		assert.True(t, old.At.IsZero(), "the old reader reads a missing stamp as the zero time, as it does today")
		d, err := fromMessage(old)
		require.NoError(t, err)
		_, err = r.Write(&d, schemaread.FormBinary)
		assert.EqualError(t, err, refused("absent_required", "at_ms"))
	})
}

// TestLayer1TextTransport: the generated EnvelopeSaveText and
// EnvelopeLoadText are the base64 text transport: the text equals the padded
// standard base64 of the envelope's wire, and LoadText returns an envelope
// that writes the same wire back.
func TestLayer1TextTransport(t *testing.T) {
	t.Parallel()
	d := fuzzFullDoc(t)
	wire := saveWire(&d)
	require.NotNil(t, wire)
	text, err := buswire.EnvelopeSaveText(d.e)
	require.NoError(t, err)
	assert.Equal(t, base64.StdEncoding.EncodeToString(wire), text)
	got, err := buswire.EnvelopeLoadText(text, nil)
	require.NoError(t, err)
	assert.Equal(t, wire, saveWire(&doc{e: got}))
	_, err = buswire.EnvelopeLoadText("not base64!", nil)
	require.Error(t, err)
}
