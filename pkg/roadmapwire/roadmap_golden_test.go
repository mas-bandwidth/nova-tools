package roadmapwire_test

// TestRoadmapTablesGolden is the golden test of the roadmap definitions
// (schemas/roadmap/roadmap.schema, schemas/roadmap/tables.baseline and the
// generated pkg/roadmapwire): every table of the area round-trips through
// pkg/schemaread as the binary wire, padded standard base64 of that wire and
// the schema JSON text form (S1, S2); a value past each declared bound is
// refused by name on read and on write, never clamped; a damaged image of
// each form is refused by name; the repository's own docs/roadmap.sexp and
// docs/fixes.sexp, as pkg/roadmap decodes them, round-trip in every form to
// the same pkg/roadmap value; the same data in the hand-editable text form
// (docs/SPEC-TABLES.md section 16.9 of the schema repository) reads to that
// value, comments attached, and rewrites byte for byte; the fuzz corpus is
// what `schema pack` and the generated writer make; and generate.go pins the
// first schema commit whose Go emits the hand-editable text form.
//
// The split it pins (S1, by the coordinator's form ruling of 2026-10-17): a
// record is a FIXED table only where every one of its bounds is a bound the
// code enforces. Every record of this area holds text or a list the code
// does not bound, so every table is VARIABLE: text with no bound in the code
// is *string, a list is []T, and the only bounds declared are the ones the
// code enforces (the YYYY-MM-DD date, the cards range and the two closed
// status lists).
//
// The instances are written as JSON text and read by the generated reader;
// the wire is the generated writer's. The test uses no clock, no socket, no
// network and no program; it reads only files of the repository.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	wire "github.com/mas-bandwidth/nova-tools/pkg/roadmapwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// help is the command every refusal points at.
const help = "go doc github.com/mas-bandwidth/nova-tools/pkg/roadmapwire"

// textFormPin is the pseudo-version of the first schema commit whose Go
// emits the hand-editable text form (the textform surface).
const textFormPin = "v2.5.1-0.20261017145427-028fa66cc914"

// ---------------------------------------------------------------------------
// Reading a loaded value.

func str(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

func blank(slot *int64) bool { return strings.TrimSpace(str(slot)) == "" }

func fixedText(b []byte, n int32) string { return string(b[:n]) }

// ---------------------------------------------------------------------------
// The constraints: what pkg/roadmap's decoder requires of each record,
// stated once for the reader and the writer. A variable table reads an
// absent field as zero, so a field the decoder requires is Required here.
// Each function names the decoder's rule.

// datePattern is pkg/roadmap/roadmap.go datePattern.
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// dateOf is decoder.date: a YYYY-MM-DD date, 10 bytes, the bound the table
// declares, checked again here so the writer refuses it by name before a
// byte is written; a release's date may be absent.
func dateOf(c *schemaread.Checker, b []byte, n int32, required bool) {
	if n < 0 || int(n) > len(b) {
		n = int32(len(b))
	}
	s := fixedText(b, n)
	c.Text("date", s, 10)
	if required || n > 0 {
		c.Required("date", datePattern.MatchString(s))
	}
}

// optional is decoder.str on an optional key: absent, or a non-empty string.
func optional(c *schemaread.Checker, field string, slot *int64) {
	if *slot != 0 {
		c.Required(field, !blank(slot))
	}
}

// keptOf is decoder.kept: each pair a keyword key and a string value (which
// may be empty), each key once.
func keptOf(c *schemaread.Checker, l *wire.TableList[wire.Pair]) {
	seen := map[string]bool{}
	ok := true
	for i := int32(0); i < l.Count; i++ {
		p := l.At(i)
		pairOf(c, p)
		k := str(&p.Key)
		ok = ok && !seen[k]
		seen[k] = true
	}
	c.Required("kept.key distinct", ok)
}

func pairOf(c *schemaread.Checker, p *wire.Pair) {
	c.Required("kept.key", !blank(&p.Key) && !strings.ContainsAny(str(&p.Key), " \t\r\n()\";"))
	c.Required("kept.value", p.Value != 0)
}

// noteOf is decoder.doc on a (scheduled ...) or (done ...) record: id,
// title, text and a YYYY-MM-DD date.
func noteOf(c *schemaread.Checker, n *wire.Note) {
	c.Required("id", !blank(&n.Id))
	c.Required("title", !blank(&n.Title))
	c.Required("text", !blank(&n.Text))
	dateOf(c, n.Date[:], n.DateLength, true)
	optional(c, "release", &n.Release)
	keptOf(c, &n.Kept)
}

func groupOf(c *schemaread.Checker, g *wire.Group) {
	c.Required("id", !blank(&g.Id))
	c.Required("title", !blank(&g.Title))
	c.Required("text", !blank(&g.Text))
}

// itemOf is decoder.doc on an (item ...) record; cards is decoder.count,
// whose range the table declares.
func itemOf(c *schemaread.Checker, it *wire.Item) {
	c.Required("id", !blank(&it.Id))
	c.Required("group", !blank(&it.Group))
	c.Required("title", !blank(&it.Title))
	c.Required("text", !blank(&it.Text))
	dateOf(c, it.Date[:], it.DateLength, true)
	for _, f := range []struct {
		name string
		slot *int64
	}{{"why", &it.Why}, {"area", &it.Area}, {"exists", &it.Exists}, {"origin", &it.Origin}, {"release", &it.Release}} {
		optional(c, f.name, f.slot)
	}
	// decoder.count: absent (0) or a positive integer of at most 1<<20,
	// the range the table declares, checked again so the writer refuses it.
	c.Bound("cards", int64(it.Cards), 0, 1<<20)
	keptOf(c, &it.Kept)
}

func constraintsPair(v *wire.Pair, c *schemaread.Checker)   { pairOf(c, v) }
func constraintsNote(v *wire.Note, c *schemaread.Checker)   { noteOf(c, v) }
func constraintsGroup(v *wire.Group, c *schemaread.Checker) { groupOf(c, v) }
func constraintsItem(v *wire.Item, c *schemaread.Checker)   { itemOf(c, v) }

// constraintsDoc is decoder.doc on the whole roadmap: a scheduled note names
// its release and carries no kept fields, a done note names no release, an
// id is used once across every record, an item's group names a group, and
// every group holds an item.
func constraintsDoc(v *wire.Doc, c *schemaread.Checker) {
	c.Required("title", !blank(&v.Title))
	c.Required("text", !blank(&v.Text))
	ids := map[string]bool{}
	unique := true
	id := func(slot *int64) {
		s := str(slot)
		unique = unique && !ids[s]
		ids[s] = true
	}
	for i := int32(0); i < v.Scheduled.Count; i++ {
		n := v.Scheduled.At(i)
		noteOf(c, n)
		c.Required("scheduled.release", !blank(&n.Release))
		c.Required("scheduled.kept", n.Kept.Count == 0)
		id(&n.Id)
	}
	for i := int32(0); i < v.Done.Count; i++ {
		n := v.Done.At(i)
		noteOf(c, n)
		c.Required("done.release", n.Release == 0)
		id(&n.Id)
	}
	groups := map[string]bool{}
	for i := int32(0); i < v.Groups.Count; i++ {
		g := v.Groups.At(i)
		groupOf(c, g)
		groups[str(&g.Id)] = true
		id(&g.Id)
	}
	used := map[string]bool{}
	named := true
	for i := int32(0); i < v.Items.Count; i++ {
		it := v.Items.At(i)
		itemOf(c, it)
		named = named && groups[str(&it.Group)]
		used[str(&it.Group)] = true
		id(&it.Id)
	}
	held := true
	for g := range groups {
		held = held && used[g]
	}
	c.Required("id distinct", unique)
	c.Required("items.group names a group", named)
	c.Required("groups hold an item", held)
}

// releaseOf is decoder.fixes on a (release ...) record: id, a status of the
// closed list, text, and a YYYY-MM-DD date when one stands.
func releaseOf(c *schemaread.Checker, r *wire.Release) {
	c.Required("id", !blank(&r.Id))
	c.Required("status", r.Status != wire.ReleaseStatusNone)
	c.Enum("status", int(r.Status)-1, int(wire.ReleaseStatusCount))
	c.Required("text", !blank(&r.Text))
	dateOf(c, r.Date[:], r.DateLength, false)
}

// fixOf is decoder.fixes on a (fix ...) record.
func fixOf(c *schemaread.Checker, f *wire.Fix) {
	c.Required("id", !blank(&f.Id))
	c.Required("release", !blank(&f.Release))
	c.Required("title", !blank(&f.Title))
	c.Required("origin", !blank(&f.Origin))
	c.Required("status", f.Status != wire.FixStatusNone)
	c.Enum("status", int(f.Status)-1, int(wire.FixStatusCount))
	optional(c, "text", &f.Text)
	keptOf(c, &f.Kept)
}

func constraintsRelease(v *wire.Release, c *schemaread.Checker) { releaseOf(c, v) }
func constraintsFix(v *wire.Fix, c *schemaread.Checker)         { fixOf(c, v) }

// constraintsFixes is decoder.fixes on the whole file: an id once, every fix
// naming a declared release, every release holding a fix.
func constraintsFixes(v *wire.Fixes, c *schemaread.Checker) {
	c.Required("title", !blank(&v.Title))
	c.Required("text", !blank(&v.Text))
	ids := map[string]bool{}
	unique := true
	releases := map[string]bool{}
	for i := int32(0); i < v.Releases.Count; i++ {
		r := v.Releases.At(i)
		releaseOf(c, r)
		s := str(&r.Id)
		unique = unique && !ids[s]
		ids[s], releases[s] = true, true
	}
	used := map[string]bool{}
	named := true
	for i := int32(0); i < v.Items.Count; i++ {
		f := v.Items.At(i)
		fixOf(c, f)
		s := str(&f.Id)
		unique = unique && !ids[s]
		ids[s] = true
		named = named && releases[str(&f.Release)]
		used[str(&f.Release)] = true
	}
	held := true
	for r := range releases {
		held = held && used[r]
	}
	c.Required("id distinct", unique)
	c.Required("items.release names a release", named)
	c.Required("releases hold a fix", held)
}

// ---------------------------------------------------------------------------
// The harness: one table behind pkg/schemaread.

// doc is one value and the region that holds it: text and lists are
// self-relative references into that region, so the value is never copied
// out of it.
type doc[T any] struct {
	v      *T
	region []byte
}

type table[T any] struct {
	name          string
	info          func() *wire.TableTypeInfo
	measureReason func([]byte) (int64, error)
	load          func(region, w []byte, r *wire.TableReport) *T
	fromJSON      func([]byte, *wire.TableReport) (*T, []byte, bool)
	measure       func(*T, ...wire.TableWriteContext) int64
	save          func(*T, []byte, ...wire.TableWriteContext) int64
	jsonMeasure   func(*T, ...wire.TableAllocator) int64
	toJSON        func(*T, []byte, ...wire.TableAllocator) int64
	saveText      func(*T, ...wire.TableWriteContext) (string, error)
	loadText      func(string, *wire.TableReport) (*T, error)
	constraints   func(*T, *schemaread.Checker)
}

type builder[T any] interface {
	Init(...wire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Region() []byte
}

// viaJSON is a generated builder filled from JSON text by the generated
// reader, then locked into its region.
func viaJSON[T any, B any, PB interface {
	*B
	builder[T]
}](from func(*B, []byte, *wire.TableReport) bool) func([]byte, *wire.TableReport) (*T, []byte, bool) {
	return func(text []byte, r *wire.TableReport) (*T, []byte, bool) {
		b := PB(new(B))
		if !b.Init() || !from((*B)(b), text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	}
}

// region is an aligned buffer of n bytes for a region load.
func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func report(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		// a key given twice in the JSON text is ambiguous: refused, never last-wins
		Malformed: r.Malformed || r.Duplicate > 0,
		Reason:    r.Reason,
	}
}

func (tb table[T]) saveWire(v *T) []byte {
	n := tb.measure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if tb.save(v, buf) != n {
		return nil
	}
	return buf
}

func (tb table[T]) codec() *schemaread.Codec[doc[T]] {
	return &schemaread.Codec[doc[T]]{
		Name:  tb.name,
		Help:  help,
		Reset: func(d *doc[T]) { *d = doc[T]{} },
		Measure: func(w []byte) error {
			_, err := tb.measureReason(w)
			return err
		},
		LoadWire: func(d *doc[T], w []byte) schemaread.Report {
			n, err := tb.measureReason(w)
			if err != nil || n < 0 {
				return schemaread.Report{Malformed: true}
			}
			var r wire.TableReport
			d.region = region(n)
			if d.v = tb.load(d.region, w, &r); d.v == nil {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			return report(&r)
		},
		LoadJSON: func(d *doc[T], text []byte) schemaread.Report {
			var r wire.TableReport
			v, reg, ok := tb.fromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, reg
			return report(&r)
		},
		Constraints: func(d *doc[T], c *schemaread.Checker) {
			c.Required("record", d.v != nil)
			if d.v != nil {
				tb.constraints(d.v, c)
			}
		},
		SaveWire: func(d *doc[T]) []byte { return tb.saveWire(d.v) },
		SaveJSON: func(d *doc[T]) []byte { return tb.json(d.v) },
	}
}

func (tb table[T]) json(v *T) []byte {
	n := tb.jsonMeasure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if tb.toJSON(v, buf) != n {
		return nil
	}
	return buf
}

var forms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

func b64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func write[T any](t *testing.T, r *schemaread.Reader[doc[T]], d doc[T], form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(&d, form)
	require.NoError(t, err, form)
	require.NotEmpty(t, out, "%s: the generated writer wrote it", form)
	return out
}

// canon is JSON text as a value, with the absent and the empty dropped, so
// two texts that say the same thing compare equal; a present empty string
// is kept apart from an absent one, and numbers keep their digits.
func canon(t *testing.T, text []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v), "%.200s", text)
	return clean(v)
}

func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			// A scalar at its default (a zero count, an empty date) is
			// written by the generated writer and reads the same as absent.
			if e == json.Number("0") || (k == "date" && e == "") {
				continue
			}
			if c := clean(e); c != nil {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			if out[i] = clean(e); out[i] == nil {
				out[i] = map[string]any{}
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return v
}

// runner is a table behind the harness, whatever its type.
type runner interface {
	label() string
	roundTrip(t *testing.T, sample []byte)
	damage(t *testing.T, sample []byte)
	typeInfo() *wire.TableTypeInfo
	refuses(t *testing.T, in []byte) string
	binary(t *testing.T, in []byte) []byte
}

func (tb table[T]) label() string                 { return tb.name }
func (tb table[T]) typeInfo() *wire.TableTypeInfo { return tb.info() }

// roundTrip writes the sample in every form and reads each back as the
// sample (S2), and pins the wire as canonical and the base64 text as padded
// standard base64 of it.
func (tb table[T]) roundTrip(t *testing.T, sample []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, res, err := r.Read(sample)
	require.NoError(t, err, "%s: the sample is read", tb.name)
	require.Equal(t, schemaread.FormJSON, res.Form)
	want := canon(t, sample)
	require.Equal(t, want, canon(t, write(t, r, d, schemaread.FormJSON)), "%s: the JSON text reads as written", tb.name)
	for _, form := range forms {
		out := write(t, r, d, form)
		got, res, err := r.Read(out)
		require.NoError(t, err, "%s %s", tb.name, form)
		assert.Equal(t, form, res.Form)
		assert.Zero(t, res.Unknown)
		assert.Equal(t, want, canon(t, write(t, r, got, schemaread.FormJSON)), "%s %s", tb.name, form)
	}
	w := write(t, r, d, schemaread.FormBinary)
	assert.Equal(t, byte(1), w[0], "%s: the wire leads with its form byte", tb.name)
	again, _, err := r.Read(w)
	require.NoError(t, err)
	assert.Equal(t, w, write(t, r, again, schemaread.FormBinary), "%s: a saved wire re-saves to the same bytes", tb.name)
	assert.Equal(t, b64(w), write(t, r, d, schemaread.FormBase64), "%s: base64 is padded standard base64 of the wire", tb.name)
	// The generated text transport is the same base64, and loads back to the
	// same wire.
	text, err := tb.saveText(d.v)
	require.NoError(t, err)
	assert.Equal(t, string(b64(w)), text, tb.name)
	back, err := tb.loadText(text, nil)
	require.NoError(t, err)
	assert.Equal(t, w, tb.saveWire(back), tb.name)
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBinary), "%s: the per-form counter moves on the form read", tb.name)
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormJSON))
}

// refusal is the reason a read or a write refused with, by name; "" when it
// did not refuse.
func refusal(t *testing.T, name string, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), name+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+help), err.Error())
	if ref.Field != "" {
		return ref.Reason + " (" + ref.Field + ")"
	}
	return ref.Reason
}

func readRefusal[T any](t *testing.T, r *schemaread.Reader[doc[T]], name string, in []byte) string {
	t.Helper()
	got, _, err := r.Read(in)
	if err != nil {
		assert.Nil(t, got.v, "a refused read returns nothing")
	}
	return refusal(t, name, err)
}

// damage reads the damaged images of the sample's good wire and JSON text
// and asserts each is refused by name.
func (tb table[T]) damage(t *testing.T, sample []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, _, err := r.Read(sample)
	require.NoError(t, err)
	good, text := write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON)
	r = schemaread.NewReader(tb.codec())
	newer := bytes.Clone(good)
	newer[0] = 9
	for _, tc := range []struct {
		name   string
		in     []byte
		reason string
	}{
		{"truncated wire", good[:len(good)-1], schemaread.ReasonWireDamaged},
		{"truncated to the form byte", good[:1], schemaread.ReasonWireDamaged},
		{"trailing junk", append(bytes.Clone(good), 0, 1), schemaread.ReasonWireDamaged},
		{"a form this reader does not know", newer, schemaread.ReasonUnknownForm},
		{"the same as base64", b64(newer), schemaread.ReasonUnknownForm},
		{"truncated base64", b64(good)[:len(b64(good))-3], schemaread.ReasonMalformed},
		{"bad base64", append([]byte("!"), b64(good)[1:]...), schemaread.ReasonUnknownForm},
		{"empty", nil, schemaread.ReasonUnknownForm},
		{"truncated JSON text", bytes.TrimSpace(text)[:len(bytes.TrimSpace(text))-1], schemaread.ReasonMalformed},
		{"JSON text that does not parse", []byte(`{"x": `), schemaread.ReasonMalformed},
	} {
		assert.Equal(t, tc.reason, readRefusal(t, r, tb.name, tc.in), "%s: %s", tb.name, tc.name)
	}
	for i := range len(good) {
		assert.NotEmpty(t, readRefusal(t, r, tb.name, good[:i]), "%s: every truncation is refused, never read as a shorter record: cut at %d", tb.name, i)
	}
	assert.Positive(t, r.Counts().Refused(schemaread.ReasonWireDamaged))
}

func (tb table[T]) refuses(t *testing.T, in []byte) string {
	t.Helper()
	return readRefusal(t, schemaread.NewReader(tb.codec()), tb.name, in)
}

// binary is the generated writer's wire of a JSON instance.
func (tb table[T]) binary(t *testing.T, in []byte) []byte {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, _, err := r.Read(in)
	require.NoError(t, err, "%s", in)
	return write(t, r, d, schemaread.FormBinary)
}

// read is the value a JSON, base64 or binary image reads as.
func read[T any](t *testing.T, tb table[T], in []byte) doc[T] {
	t.Helper()
	d, _, err := schemaread.NewReader(tb.codec()).Read(in)
	require.NoError(t, err, "%s", tb.name)
	return d
}

// pastBound is the wire image good holds with its string of exactly the n
// bytes s grown by one byte, its length prefix rewritten: the image of a
// writer that ignored the bound. The table wire writes a string(N) as kind
// 12, the LEB128 length and the bytes.
func pastBound(t *testing.T, good []byte, s string) []byte {
	t.Helper()
	enc := func(s string) []byte { return append(binary.AppendUvarint([]byte{12}, uint64(len(s))), s...) }
	require.Equal(t, 1, bytes.Count(good, enc(s)), "the at-bound string %q is in the image once", s)
	return bytes.Replace(good, enc(s), enc(s+"9"), 1)
}

// replaceOnce is an image or a text with its one occurrence of from replaced.
func replaceOnce(t *testing.T, in []byte, from, to string) []byte {
	t.Helper()
	require.Equal(t, 1, bytes.Count(in, []byte(from)), "%q is in it once", from)
	return bytes.Replace(in, []byte(from), []byte(to), 1)
}

// firstDiff is the first byte at which two images of one record differ: the
// start of the variant id an enum rides as.
func firstDiff(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return -1
}

func le32(v uint32) string {
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

var (
	pairTable = table[wire.Pair]{
		name: "Pair", info: wire.PairTableType, measureReason: wire.PairLoadMeasureReason,
		load: wire.PairLoad, fromJSON: viaJSON[wire.Pair, wire.PairBuilder](wire.PairFromJson),
		measure: wire.PairMeasure, save: wire.PairSave,
		jsonMeasure: wire.PairToJsonMeasure, toJSON: wire.PairToJson,
		saveText: wire.PairSaveText, loadText: wire.PairLoadText, constraints: constraintsPair,
	}
	noteTable = table[wire.Note]{
		name: "Note", info: wire.NoteTableType, measureReason: wire.NoteLoadMeasureReason,
		load: wire.NoteLoad, fromJSON: viaJSON[wire.Note, wire.NoteBuilder](wire.NoteFromJson),
		measure: wire.NoteMeasure, save: wire.NoteSave,
		jsonMeasure: wire.NoteToJsonMeasure, toJSON: wire.NoteToJson,
		saveText: wire.NoteSaveText, loadText: wire.NoteLoadText, constraints: constraintsNote,
	}
	groupTable = table[wire.Group]{
		name: "Group", info: wire.GroupTableType, measureReason: wire.GroupLoadMeasureReason,
		load: wire.GroupLoad, fromJSON: viaJSON[wire.Group, wire.GroupBuilder](wire.GroupFromJson),
		measure: wire.GroupMeasure, save: wire.GroupSave,
		jsonMeasure: wire.GroupToJsonMeasure, toJSON: wire.GroupToJson,
		saveText: wire.GroupSaveText, loadText: wire.GroupLoadText, constraints: constraintsGroup,
	}
	itemTable = table[wire.Item]{
		name: "Item", info: wire.ItemTableType, measureReason: wire.ItemLoadMeasureReason,
		load: wire.ItemLoad, fromJSON: viaJSON[wire.Item, wire.ItemBuilder](wire.ItemFromJson),
		measure: wire.ItemMeasure, save: wire.ItemSave,
		jsonMeasure: wire.ItemToJsonMeasure, toJSON: wire.ItemToJson,
		saveText: wire.ItemSaveText, loadText: wire.ItemLoadText, constraints: constraintsItem,
	}
	docTable = table[wire.Doc]{
		name: "Doc", info: wire.DocTableType, measureReason: wire.DocLoadMeasureReason,
		load: wire.DocLoad, fromJSON: viaJSON[wire.Doc, wire.DocBuilder](wire.DocFromJson),
		measure: wire.DocMeasure, save: wire.DocSave,
		jsonMeasure: wire.DocToJsonMeasure, toJSON: wire.DocToJson,
		saveText: wire.DocSaveText, loadText: wire.DocLoadText, constraints: constraintsDoc,
	}
	releaseTable = table[wire.Release]{
		name: "Release", info: wire.ReleaseTableType, measureReason: wire.ReleaseLoadMeasureReason,
		load: wire.ReleaseLoad, fromJSON: viaJSON[wire.Release, wire.ReleaseBuilder](wire.ReleaseFromJson),
		measure: wire.ReleaseMeasure, save: wire.ReleaseSave,
		jsonMeasure: wire.ReleaseToJsonMeasure, toJSON: wire.ReleaseToJson,
		saveText: wire.ReleaseSaveText, loadText: wire.ReleaseLoadText, constraints: constraintsRelease,
	}
	fixTable = table[wire.Fix]{
		name: "Fix", info: wire.FixTableType, measureReason: wire.FixLoadMeasureReason,
		load: wire.FixLoad, fromJSON: viaJSON[wire.Fix, wire.FixBuilder](wire.FixFromJson),
		measure: wire.FixMeasure, save: wire.FixSave,
		jsonMeasure: wire.FixToJsonMeasure, toJSON: wire.FixToJson,
		saveText: wire.FixSaveText, loadText: wire.FixLoadText, constraints: constraintsFix,
	}
	fixesTable = table[wire.Fixes]{
		name: "Fixes", info: wire.FixesTableType, measureReason: wire.FixesLoadMeasureReason,
		load: wire.FixesLoad, fromJSON: viaJSON[wire.Fixes, wire.FixesBuilder](wire.FixesFromJson),
		measure: wire.FixesMeasure, save: wire.FixesSave,
		jsonMeasure: wire.FixesToJsonMeasure, toJSON: wire.FixesToJson,
		saveText: wire.FixesSaveText, loadText: wire.FixesLoadText, constraints: constraintsFixes,
	}
)

// allTables is every table of the area.
var allTables = []runner{pairTable, noteTable, groupTable, itemTable, docTable, releaseTable, fixTable, fixesTable}

// ---------------------------------------------------------------------------
// pkg/roadmap's values and the tables: the one mapping, both ways.

type J = map[string]any
type L = []any

func jsonOf(t *testing.T, m any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// put sets an optional key: the decoder reads an absent key as "" or 0, so
// an empty value is written as no key at all.
func put(m J, k string, v string) {
	if v != "" {
		m[k] = v
	}
}

func keptJ(ps []roadmap.Pair) L {
	out := L{}
	for _, p := range ps {
		out = append(out, J{"key": p.Key, "value": p.Value})
	}
	return out
}

func noteJ(n roadmap.Note) J {
	m := J{"id": n.ID, "title": n.Title, "text": n.Text, "date": n.Date}
	put(m, "release", n.Release)
	if len(n.Kept) > 0 {
		m["kept"] = keptJ(n.Kept)
	}
	return m
}

func itemJ(it roadmap.Item) J {
	m := J{"id": it.ID, "group": it.Group, "title": it.Title, "text": it.Text, "date": it.Date}
	for k, v := range map[string]string{"why": it.Why, "area": it.Area, "exists": it.Exists, "origin": it.Origin, "release": it.Release} {
		put(m, k, v)
	}
	if it.Cards > 0 {
		m["cards"] = it.Cards
	}
	if len(it.Kept) > 0 {
		m["kept"] = keptJ(it.Kept)
	}
	return m
}

func docJ(d *roadmap.Doc) J {
	m := J{"title": d.Title, "text": d.Text}
	var s, dn, g, it L
	for _, n := range d.Scheduled {
		s = append(s, noteJ(n))
	}
	for _, n := range d.Done {
		dn = append(dn, noteJ(n))
	}
	for _, x := range d.Groups {
		g = append(g, J{"id": x.ID, "title": x.Title, "text": x.Text})
	}
	for _, x := range d.Items {
		it = append(it, itemJ(x))
	}
	for k, l := range map[string]L{"scheduled": s, "done": dn, "groups": g, "items": it} {
		if len(l) > 0 {
			m[k] = l
		}
	}
	return m
}

// statusName is a status as the file spells it and as the table's enum
// names it: the file's dash is the enum's underscore.
func statusName(s string) string { return strings.ReplaceAll(s, "-", "_") }

func fixesJ(fx *roadmap.Fixes) J {
	m := J{"title": fx.Title, "text": fx.Text}
	var rs, is L
	for _, r := range fx.Releases {
		x := J{"id": r.ID, "status": statusName(r.Status), "text": r.Text}
		put(x, "date", r.Date)
		rs = append(rs, x)
	}
	for _, f := range fx.Items {
		x := J{"id": f.ID, "release": f.Release, "title": f.Title, "origin": f.Origin, "status": statusName(f.Status)}
		put(x, "text", f.Text)
		if len(f.Kept) > 0 {
			x["kept"] = keptJ(f.Kept)
		}
		is = append(is, x)
	}
	if len(rs) > 0 {
		m["releases"] = rs
	}
	if len(is) > 0 {
		m["items"] = is
	}
	return m
}

func keptOfWire(l *wire.TableList[wire.Pair]) []roadmap.Pair {
	var out []roadmap.Pair
	for i := int32(0); i < l.Count; i++ {
		p := l.At(i)
		out = append(out, roadmap.Pair{Key: str(&p.Key), Value: str(&p.Value)})
	}
	return out
}

func noteOfWire(n *wire.Note) roadmap.Note {
	return roadmap.Note{ID: str(&n.Id), Release: str(&n.Release), Title: str(&n.Title), Text: str(&n.Text),
		Date: fixedText(n.Date[:], n.DateLength), Kept: keptOfWire(&n.Kept)}
}

// docOfWire is the pkg/roadmap value a loaded Doc table holds.
func docOfWire(v *wire.Doc) *roadmap.Doc {
	d := &roadmap.Doc{Title: str(&v.Title), Text: str(&v.Text)}
	for i := int32(0); i < v.Scheduled.Count; i++ {
		d.Scheduled = append(d.Scheduled, noteOfWire(v.Scheduled.At(i)))
	}
	for i := int32(0); i < v.Done.Count; i++ {
		d.Done = append(d.Done, noteOfWire(v.Done.At(i)))
	}
	for i := int32(0); i < v.Groups.Count; i++ {
		g := v.Groups.At(i)
		d.Groups = append(d.Groups, roadmap.Group{ID: str(&g.Id), Title: str(&g.Title), Text: str(&g.Text)})
	}
	for i := int32(0); i < v.Items.Count; i++ {
		it := v.Items.At(i)
		d.Items = append(d.Items, roadmap.Item{ID: str(&it.Id), Group: str(&it.Group), Title: str(&it.Title), Text: str(&it.Text),
			Why: str(&it.Why), Date: fixedText(it.Date[:], it.DateLength), Area: str(&it.Area), Exists: str(&it.Exists),
			Cards: int(it.Cards), Origin: str(&it.Origin), Release: str(&it.Release), Kept: keptOfWire(&it.Kept)})
	}
	return d
}

func fileStatus(name string) string { return strings.ReplaceAll(name, "_", "-") }

// fixesOfWire is the pkg/roadmap value a loaded Fixes table holds.
func fixesOfWire(v *wire.Fixes) *roadmap.Fixes {
	fx := &roadmap.Fixes{Title: str(&v.Title), Text: str(&v.Text)}
	for i := int32(0); i < v.Releases.Count; i++ {
		r := v.Releases.At(i)
		fx.Releases = append(fx.Releases, roadmap.Release{ID: str(&r.Id), Status: fileStatus(wire.EnumNameReleaseStatus(r.Status)),
			Text: str(&r.Text), Date: fixedText(r.Date[:], r.DateLength)})
	}
	for i := int32(0); i < v.Items.Count; i++ {
		f := v.Items.At(i)
		fx.Items = append(fx.Items, roadmap.Fix{ID: str(&f.Id), Release: str(&f.Release), Title: str(&f.Title), Text: str(&f.Text),
			Origin: str(&f.Origin), Status: fileStatus(wire.EnumNameFixStatus(f.Status)), Kept: keptOfWire(&f.Kept)})
	}
	return fx
}

// ---------------------------------------------------------------------------
// The hand-editable text form of a pkg/roadmap value: the spelling the
// schema's section 16.9 reads, written here from the decoded file so the
// test holds the whole of the repository's data to it.

// quote is a JSON string, the text form's string spelling.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

type kv struct{ k, v string }

// record is one (:key value ...) record on one line, keys in the order
// given and the empty ones left out.
func record(kvs ...kv) string {
	var parts []string
	for _, p := range kvs {
		if p.v != "" {
			parts = append(parts, ":"+p.k+" "+p.v)
		}
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func q(s string) string {
	if s == "" {
		return ""
	}
	return quote(s)
}

func keptText(ps []roadmap.Pair) string {
	if len(ps) == 0 {
		return ""
	}
	var parts []string
	for _, p := range ps {
		parts = append(parts, record(kv{"key", quote(p.Key)}, kv{"value", quote(p.Value)}))
	}
	return "(" + strings.Join(parts, " ") + ")"
}

// list is a :key and its records, one per line, a comment above the key.
func list(b *strings.Builder, key, comment string, recs []string) {
	if len(recs) == 0 {
		return
	}
	fmt.Fprintf(b, "\n ; %s\n :%s\n (", comment, key)
	for i, r := range recs {
		if i > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString(r)
	}
	b.WriteString(")\n")
}

// header is the leading comment block of a file: its `;` lines before the
// first form.
func header(raw []byte) []string {
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, ";") {
			break
		}
		out = append(out, l)
	}
	return out
}

func docText(head []string, d *roadmap.Doc) []byte {
	var b strings.Builder
	for _, h := range head {
		b.WriteString(h + "\n")
	}
	fmt.Fprintf(&b, "(:title %s ; the page title\n :text %s\n", quote(d.Title), quote(d.Text))
	var s, dn, g, it []string
	for _, n := range d.Scheduled {
		s = append(s, record(kv{"id", q(n.ID)}, kv{"release", q(n.Release)}, kv{"title", q(n.Title)}, kv{"text", q(n.Text)}, kv{"date", q(n.Date)}))
	}
	for _, n := range d.Done {
		dn = append(dn, record(kv{"id", q(n.ID)}, kv{"title", q(n.Title)}, kv{"text", q(n.Text)}, kv{"date", q(n.Date)}, kv{"kept", keptText(n.Kept)}))
	}
	for _, x := range d.Groups {
		g = append(g, record(kv{"id", q(x.ID)}, kv{"title", q(x.Title)}, kv{"text", q(x.Text)}))
	}
	for _, x := range d.Items {
		cards := ""
		if x.Cards > 0 {
			cards = fmt.Sprint(x.Cards)
		}
		it = append(it, record(kv{"id", q(x.ID)}, kv{"group", q(x.Group)}, kv{"title", q(x.Title)}, kv{"text", q(x.Text)},
			kv{"date", q(x.Date)}, kv{"why", q(x.Why)}, kv{"area", q(x.Area)}, kv{"exists", q(x.Exists)}, kv{"cards", cards},
			kv{"origin", q(x.Origin)}, kv{"release", q(x.Release)}, kv{"kept", keptText(x.Kept)}))
	}
	list(&b, "scheduled", "Scheduled into a release.", s)
	list(&b, "done", "Decided and in place.", dn)
	list(&b, "groups", "The headings.", g)
	list(&b, "items", "The work.", it)
	b.WriteString(")\n; end of the roadmap\n")
	return []byte(b.String())
}

func fixesText(head []string, fx *roadmap.Fixes) []byte {
	var b strings.Builder
	for _, h := range head {
		b.WriteString(h + "\n")
	}
	fmt.Fprintf(&b, "(:title %s ; the page title\n :text %s\n", quote(fx.Title), quote(fx.Text))
	var rs, is []string
	for _, r := range fx.Releases {
		rs = append(rs, record(kv{"id", q(r.ID)}, kv{"status", ":" + statusName(r.Status)}, kv{"text", q(r.Text)}, kv{"date", q(r.Date)}))
	}
	for _, f := range fx.Items {
		is = append(is, record(kv{"id", q(f.ID)}, kv{"release", q(f.Release)}, kv{"title", q(f.Title)}, kv{"origin", q(f.Origin)},
			kv{"status", ":" + statusName(f.Status)}, kv{"text", q(f.Text)}, kv{"kept", keptText(f.Kept)}))
	}
	list(&b, "releases", "The point releases.", rs)
	list(&b, "items", "The fixes.", is)
	b.WriteString(")\n; end of the fixes\n")
	return []byte(b.String())
}

// textLimits bounds a text-form read of the repository's files: 16 MiB,
// above roadmap.Limits' 10 MiB file bound.
var textLimits = wire.TableTextFormLimits{Bytes: 16 << 20, Tokens: 1 << 20, Nodes: 1 << 20, Allocation: 256 << 20, Depth: 128}

// textFormError is a text-form read's refusal; nil when it did not refuse.
func textFormError(t *testing.T, err error) *wire.TableTextFormError {
	t.Helper()
	if err == nil {
		return nil
	}
	var e *wire.TableTextFormError
	require.True(t, errors.As(err, &e), "a text-form refusal is a *TableTextFormError: %v", err)
	return e
}

// textFormName is the name a text-form read refused with; "" when it did
// not refuse.
func textFormName(t *testing.T, err error) string {
	t.Helper()
	if e := textFormError(t, err); e != nil {
		return e.Name
	}
	return ""
}

// changedLines is the lines of b that differ from a, which must have as
// many.
func changedLines(t *testing.T, a, b []byte) []string {
	t.Helper()
	la, lb := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	require.Len(t, lb, len(la), "an edit adds and removes no line")
	var out []string
	for i := range la {
		if la[i] != lb[i] {
			out = append(out, lb[i])
		}
	}
	return out
}

func readRepo(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	require.NoError(t, err)
	return b
}

// ---------------------------------------------------------------------------
// The samples. Every name is obviously fake, and no secret value appears.

func long(c string, n int) string { return strings.Repeat(c, n) }

func pairs(n int) L {
	out := L{}
	for i := range n {
		out = append(out, J{"key": fmt.Sprintf("item-why-%03d", i), "value": fmt.Sprintf("value %d", i)})
	}
	return out
}

func smallItem() J {
	return J{"id": "item-one", "group": "group-one", "title": "An item", "text": "What it is.", "date": "2026-10-17"}
}

func fullItem() J {
	return J{"id": "item-two", "group": "group-one", "title": "A full item", "text": "What it is.", "date": "2026-10-09",
		"why": "Why it waits.", "area": "an area", "exists": "What the code has.", "cards": 1048576,
		"origin": "issue #1", "release": "v1.5", "kept": L{J{"key": "fix-status", "value": "planned"}, J{"key": "fix-text", "value": ""}}}
}

func smallDoc() J {
	return J{"title": "A roadmap", "text": "The opening.",
		"scheduled": L{J{"id": "sched-one", "release": "v1.5", "title": "Scheduled", "text": "In a release.", "date": "2026-10-09"}},
		"done":      L{J{"id": "done-one", "title": "Done", "text": "In place.", "date": "2026-10-10", "kept": L{J{"key": "item-why", "value": "w"}}}},
		"groups":    L{J{"id": "group-one", "title": "Group", "text": "A heading."}},
		"items":     L{smallItem(), fullItem()}}
}

func bigDoc() J {
	var groups, items L
	for i := range 200 {
		g := fmt.Sprintf("group-%03d", i)
		groups = append(groups, J{"id": g, "title": long("t", 4096), "text": "x"})
		items = append(items, J{"id": fmt.Sprintf("item-%03d", i), "group": g, "title": "t", "text": long("x", 1<<12), "date": "2026-10-17", "kept": pairs(3)})
	}
	return J{"title": long("T", 4096), "text": long("x", 1<<20), "groups": groups, "items": items}
}

func smallFixes() J {
	return J{"title": "Fixes", "text": "The point releases.",
		"releases": L{J{"id": "v1.2.10", "status": "shipped", "text": "Cut.", "date": "2026-10-11"}, J{"id": "v1.2.11", "status": "in_progress", "text": "Being cut."}},
		"items": L{J{"id": "fix-one", "release": "v1.2.10", "title": "A fix", "origin": "PR #1", "status": "shipped"},
			J{"id": "fix-two", "release": "v1.2.11", "title": "Another", "origin": "a card", "status": "done", "text": "Landed.", "kept": L{J{"key": "item-why", "value": ""}}}}}
}

func bigFixes() J {
	var rs, is L
	for i := range 200 {
		r := fmt.Sprintf("v1.2.%d", i)
		rs = append(rs, J{"id": r, "status": "planned", "text": long("r", 1<<10)})
		is = append(is, J{"id": fmt.Sprintf("fix-%03d", i), "release": r, "title": "t", "origin": long("o", 4096), "status": "planned", "kept": pairs(2)})
	}
	return J{"title": "F", "text": long("x", 1<<20), "releases": rs, "items": is}
}

func samples() map[string][]J {
	return map[string][]J{
		"Pair":    {{"key": "item-why", "value": "why text"}, {"key": "fix-status", "value": ""}, {"key": long("k", 4096), "value": long("v", 1<<20)}},
		"Note":    {{"id": "sched-one", "release": "v1.5", "title": "T", "text": "x", "date": "2026-10-09"}, {"id": "done-one", "title": "T", "text": "x", "date": "2026-10-10", "kept": pairs(200)}, {"id": long("i", 4096), "title": "T", "text": long("x", 1<<20), "date": "9999-12-31"}},
		"Group":   {{"id": "group-one", "title": "Group", "text": "A heading."}, {"id": long("g", 4096), "title": long("t", 4096), "text": long("x", 1<<20)}},
		"Item":    {smallItem(), fullItem(), {"id": "i", "group": "g", "title": "t", "text": long("x", 1<<20), "date": "2026-10-17", "cards": 1, "kept": pairs(200)}},
		"Doc":     {smallDoc(), {"title": "T", "text": "x"}, bigDoc()},
		"Release": {{"id": "v1.2.10", "status": "shipped", "text": "Cut.", "date": "2026-10-11"}, {"id": "v1.3.0", "status": "in_progress", "text": "Being cut."}, {"id": "v1.4.0", "status": "planned", "text": long("x", 1<<20)}},
		"Fix":     {{"id": "fix-one", "release": "v1.2.10", "title": "A fix", "origin": "PR #1", "status": "shipped"}, {"id": "fix-two", "release": "v1.2.11", "title": "t", "origin": "o", "status": "done", "text": long("x", 1<<20), "kept": pairs(200)}},
		"Fixes":   {smallFixes(), {"title": "T", "text": "x"}, bigFixes()},
	}
}

// small is the first sample of a table, the one a damage test truncates
// byte by byte.
func small(s map[string][]J, label string) J { return s[label][0] }

// ---------------------------------------------------------------------------

func TestRoadmapTablesGolden(t *testing.T) {
	t.Parallel()
	all := samples()

	t.Run("generate.go pins the first schema commit with the hand-editable text form", func(t *testing.T) {
		t.Parallel()
		pin := regexp.MustCompile(`(?m)^//go:generate go run github\.com/mas-bandwidth/schema/v2/cmd/schema@(\S+) generate --lang go --surface (\S+) --out \. \.\./\.\./schemas/(\S+)$`)
		own, err := os.ReadFile("generate.go")
		require.NoError(t, err)
		m := pin.FindSubmatch(own)
		require.NotNil(t, m, "generate.go holds the pin line")
		assert.Equal(t, textFormPin, string(m[1]))
		// the default surface set, and the hand-editable text form beside it
		assert.Equal(t, "wire,fixed,json,cook,block,view,message,retain,textform", string(m[2]))
		assert.Equal(t, "roadmap", string(m[3]))
	})

	t.Run("every table has samples and every sample is a table", func(t *testing.T) {
		t.Parallel()
		assert.Len(t, allTables, 8)
		have := map[string]bool{}
		for _, tb := range allTables {
			have[tb.label()] = true
			assert.NotEmpty(t, all[tb.label()], "%s has samples", tb.label())
		}
		for label := range all {
			assert.True(t, have[label], "%s is a table of the area", label)
		}
	})

	t.Run("the area is variable tables with the code's bounds", func(t *testing.T) {
		t.Parallel()
		// The bounds declared are the ones pkg/roadmap enforces: the
		// YYYY-MM-DD date (datePattern, 10 bytes), the cards range
		// (decoder.count, at most 1<<20) and the two closed status lists.
		// Every other text is *string and every list is open ([]T).
		dates := map[string]bool{"Note.date": true, "Item.date": true, "Release.date": true}
		enums := map[string]string{"Release.status": "ReleaseStatus", "Fix.status": "FixStatus"}
		seenDate, seenEnum, seenRange := map[string]bool{}, map[string]bool{}, false
		for _, tb := range allTables {
			info := tb.typeInfo()
			assert.True(t, info.Variable, "%s is a variable table (S1): its text or lists are not bounded by the code", info.Name)
			for _, f := range info.Fields {
				key := info.Name + "." + f.Name
				switch {
				case f.List:
				case dates[key]:
					assert.Equal(t, int32(10), f.ArrayBound, key)
					seenDate[key] = true
				case enums[key] != "":
					assert.Equal(t, enums[key], f.DeclaredTypeName, key)
					seenEnum[key] = true
				case key == "Item.cards":
					assert.True(t, f.HasRange, key)
					assert.Equal(t, 0.0, f.RangeMin, key)
					assert.Equal(t, float64(1<<20), f.RangeMax, key)
					seenRange = true
				case f.TypeName == "string":
					assert.True(t, f.Pointer, "%s: text the code does not bound is *string", key)
				default:
					t.Errorf("%s declares a bound the code does not enforce: %s", key, f.DeclaredTypeName)
				}
			}
		}
		assert.Len(t, seenDate, len(dates))
		assert.Len(t, seenEnum, len(enums))
		assert.True(t, seenRange)
		_, err := os.Stat(filepath.Join("..", "..", "schemas", "roadmap", "schema.lock"))
		assert.True(t, errors.Is(err, os.ErrNotExist), "no fixed layout is locked: the area has no fixed table")
		baseline, err := os.ReadFile(filepath.Join("..", "..", "schemas", "roadmap", "tables.baseline"))
		require.NoError(t, err)
		for _, tb := range allTables {
			assert.Contains(t, string(baseline), "table "+tb.label()+"\n", "tables.baseline records %s", tb.label())
		}
	})

	t.Run("round trip in every form", func(t *testing.T) {
		t.Parallel()
		for _, tb := range allTables {
			for i, s := range all[tb.label()] {
				t.Run(fmt.Sprintf("%s/%d", tb.label(), i), func(t *testing.T) {
					t.Parallel()
					tb.roundTrip(t, jsonOf(t, s))
				})
			}
		}
	})

	t.Run("every status is accepted in every form", func(t *testing.T) {
		t.Parallel()
		for _, s := range []string{"shipped", "in_progress", "planned"} {
			releaseTable.roundTrip(t, jsonOf(t, J{"id": "v1", "status": s, "text": "x"}))
		}
		for _, s := range []string{"shipped", "in_progress", "planned", "done"} {
			fixTable.roundTrip(t, jsonOf(t, J{"id": "f", "release": "v1", "title": "t", "origin": "o", "status": s}))
		}
	})

	t.Run("the repository's roadmap and fixes round-trip in every form", func(t *testing.T) {
		t.Parallel()
		// Every form docs/roadmap.sexp and docs/fixes.sexp hold today, as
		// pkg/roadmap decodes them, is a value of the tables: written in
		// each form and read back, it is the same pkg/roadmap value.
		rd, err := roadmap.Decode("docs/roadmap.sexp", readRepo(t, "roadmap.sexp"))
		require.NoError(t, err)
		require.NotEmpty(t, rd.Items)
		fx, err := roadmap.DecodeFixes("docs/fixes.sexp", readRepo(t, "fixes.sexp"))
		require.NoError(t, err)
		require.NotEmpty(t, fx.Items)
		docTable.roundTrip(t, jsonOf(t, docJ(rd)))
		fixesTable.roundTrip(t, jsonOf(t, fixesJ(fx)))
		rr, rf := schemaread.NewReader(docTable.codec()), schemaread.NewReader(fixesTable.codec())
		d, _, err := rr.Read(jsonOf(t, docJ(rd)))
		require.NoError(t, err)
		f, _, err := rf.Read(jsonOf(t, fixesJ(fx)))
		require.NoError(t, err)
		for _, form := range forms {
			got, _, err := rr.Read(write(t, rr, d, form))
			require.NoError(t, err, form)
			assert.Equal(t, rd, docOfWire(got.v), "the roadmap as %s", form)
			assert.Equal(t, roadmap.Render(rd, "docs/roadmap.sexp"), roadmap.Render(docOfWire(got.v), "docs/roadmap.sexp"), "the page it renders as %s", form)
			gotf, _, err := rf.Read(write(t, rf, f, form))
			require.NoError(t, err, form)
			assert.Equal(t, fx, fixesOfWire(gotf.v), "the fixes as %s", form)
		}
	})

	t.Run("the hand-editable text form reads the repository's data and rewrites it byte for byte", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name string
			raw  []byte
			text func([]string) []byte
			load func([]byte) (*wire.TableTextFormDocument, any, error)
			save func(*wire.TableTextFormDocument) ([]byte, error)
			edit func(*wire.TableTextFormDocument, wire.TableTextFormEdit) (*wire.TableTextFormDocument, any, error)
			want any
		}{
			func() (c struct {
				name string
				raw  []byte
				text func([]string) []byte
				load func([]byte) (*wire.TableTextFormDocument, any, error)
				save func(*wire.TableTextFormDocument) ([]byte, error)
				edit func(*wire.TableTextFormDocument, wire.TableTextFormEdit) (*wire.TableTextFormDocument, any, error)
				want any
			}) {
				raw := readRepo(t, "roadmap.sexp")
				rd, err := roadmap.Decode("docs/roadmap.sexp", raw)
				require.NoError(t, err)
				c.name, c.raw, c.want = "roadmap", raw, rd
				c.text = func(h []string) []byte { return docText(h, rd) }
				c.load = func(b []byte) (*wire.TableTextFormDocument, any, error) {
					d, v, err := wire.DocLoadTextForm(b, textLimits)
					if err != nil {
						return nil, nil, err
					}
					return d, docOfWire(v), nil
				}
				c.save = wire.DocSaveTextForm
				c.edit = func(d *wire.TableTextFormDocument, e wire.TableTextFormEdit) (*wire.TableTextFormDocument, any, error) {
					nd, v, err := wire.DocEditTextForm(d, e)
					if err != nil {
						return nil, nil, err
					}
					return nd, docOfWire(v), nil
				}
				return c
			}(),
			func() (c struct {
				name string
				raw  []byte
				text func([]string) []byte
				load func([]byte) (*wire.TableTextFormDocument, any, error)
				save func(*wire.TableTextFormDocument) ([]byte, error)
				edit func(*wire.TableTextFormDocument, wire.TableTextFormEdit) (*wire.TableTextFormDocument, any, error)
				want any
			}) {
				raw := readRepo(t, "fixes.sexp")
				fx, err := roadmap.DecodeFixes("docs/fixes.sexp", raw)
				require.NoError(t, err)
				c.name, c.raw, c.want = "fixes", raw, fx
				c.text = func(h []string) []byte { return fixesText(h, fx) }
				c.load = func(b []byte) (*wire.TableTextFormDocument, any, error) {
					d, v, err := wire.FixesLoadTextForm(b, textLimits)
					if err != nil {
						return nil, nil, err
					}
					return d, fixesOfWire(v), nil
				}
				c.save = wire.FixesSaveTextForm
				c.edit = func(d *wire.TableTextFormDocument, e wire.TableTextFormEdit) (*wire.TableTextFormDocument, any, error) {
					nd, v, err := wire.FixesEditTextForm(d, e)
					if err != nil {
						return nil, nil, err
					}
					return nd, fixesOfWire(v), nil
				}
				return c
			}(),
		} {
			head := header(c.raw)
			require.NotEmpty(t, head, "%s: the file opens with its comment block", c.name)
			text := c.text(head)
			d, v, err := c.load(text)
			require.NoError(t, err, c.name)
			assert.Equal(t, c.want, v, "%s: the text form reads to the value pkg/roadmap decodes", c.name)
			out, err := c.save(d)
			require.NoError(t, err)
			assert.Equal(t, string(text), string(out), "%s: read then write with no edit is byte for byte", c.name)
			comments := d.Comments()
			for i, h := range head {
				require.Greater(t, len(comments), i)
				assert.Equal(t, h, comments[i].Text, "%s: the file's comment block is kept", c.name)
				assert.Equal(t, "", comments[i].Path, "%s: a comment before the root belongs to the root", c.name)
			}
			titled := false
			for _, cm := range comments {
				titled = titled || (cm.Path == "title" && cm.Text == "; the page title")
			}
			assert.True(t, titled, "%s: a comment after a value on its line belongs to that field", c.name)
			assert.Equal(t, "; end of the "+c.name, comments[len(comments)-1].Text)
			// One leaf edit changes only that value's line.
			ed, ev, err := c.edit(d, wire.TableTextFormEdit{Path: "items[0].title", Text: quote("An edited title")})
			require.NoError(t, err, c.name)
			changed := changedLines(t, text, ed.Bytes())
			require.Len(t, changed, 1, "%s: one line changes", c.name)
			assert.Contains(t, changed[0], `:title "An edited title"`)
			switch x := ev.(type) {
			case *roadmap.Doc:
				assert.Equal(t, "An edited title", x.Items[0].Title)
			case *roadmap.Fixes:
				assert.Equal(t, "An edited title", x.Items[0].Title)
			}
			// The file's own spelling today, a head symbol, the format and
			// the id as positional atoms, and raw line breaks inside strings,
			// is outside the text form's grammar (section 16.9.1): the read
			// refuses it at the head symbol by name.
			_, _, err = c.load(c.raw)
			e := textFormError(t, err)
			require.NotNil(t, e, "%s: the legacy spelling is refused", c.name)
			assert.Equal(t, "text_syntax", e.Name, c.name)
			assert.Equal(t, bytes.Index(c.raw, []byte("("+c.name+` "v1"`))+1, e.Offset, "%s: refused at the head symbol", c.name)
		}
	})

	t.Run("the hand-editable text form refuses by name", func(t *testing.T) {
		t.Parallel()
		good := `(:title "T" :text "x" :groups ((:id "g" :title "G" :text "T")) :items ((:id "i" :group "g" :title "I" :text "T" :date "2026-10-17" :cards 3)))`
		_, v, err := wire.DocLoadTextForm([]byte(good), wire.TableTextFormDefaultLimits())
		require.NoError(t, err)
		assert.Equal(t, int32(3), v.Items.At(0).Cards)
		for _, c := range []struct {
			name, in, want string
		}{
			{"a date past its 10 bytes", strings.Replace(good, `"2026-10-17"`, `"2026-10-170"`, 1), "text_bound"},
			{"cards past 1<<20", strings.Replace(good, ":cards 3", ":cards 1048577", 1), "text_bound"},
			{"negative cards", strings.Replace(good, ":cards 3", ":cards -1", 1), "text_bound"},
			{"an unknown key", strings.Replace(good, ":cards 3", ":cardz 3", 1), "text_unknown_key"},
			{"a key twice", strings.Replace(good, ":cards 3", ":cards 3 :cards 4", 1), "text_duplicate_key"},
			{"text where a number stands", strings.Replace(good, ":cards 3", `:cards "3"`, 1), "text_kind_mismatch"},
			{"a raw line break in a string", strings.Replace(good, `"x" :groups`, "\"x\nU\" :groups", 1), "text_syntax"},
			{"an unterminated list", good[:len(good)-1], "text_unterminated"},
		} {
			_, _, err := wire.DocLoadTextForm([]byte(c.in), wire.TableTextFormDefaultLimits())
			assert.Equal(t, c.want, textFormName(t, err), c.name)
		}
		fixes := `(:title "T" :text "x" :releases ((:id "v1" :status :shipped :text "t")) :items ((:id "f" :release "v1" :title "t" :origin "o" :status :done)))`
		_, fv, err := wire.FixesLoadTextForm([]byte(fixes), wire.TableTextFormDefaultLimits())
		require.NoError(t, err)
		assert.Equal(t, wire.FixStatusdone, fv.Items.At(0).Status)
		_, _, err = wire.FixesLoadTextForm([]byte(strings.Replace(fixes, ":status :done", ":status :fifth", 1)), wire.TableTextFormDefaultLimits())
		assert.Equal(t, "text_unknown_variant", textFormName(t, err))
	})

	t.Run("the fuzz corpus is what schema pack and the generated writer make", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name string
			tb   runner
			json func(*testing.T, []byte) []byte
		}{
			{"doc-typical", docTable, func(t *testing.T, in []byte) []byte { return docTable.json(read(t, docTable, in).v) }},
			{"fixes-typical", fixesTable, func(t *testing.T, in []byte) []byte { return fixesTable.json(read(t, fixesTable, in).v) }},
		} {
			dir := filepath.Join("..", "..", "schemas", "roadmap", "fuzz")
			bin, err := os.ReadFile(filepath.Join(dir, c.name+".bin"))
			require.NoError(t, err)
			text, err := os.ReadFile(filepath.Join(dir, c.name+".json"))
			require.NoError(t, err)
			assert.Equal(t, bin, c.tb.binary(t, text), "%s: the generated Save writes the packed wire", c.name)
			assert.Equal(t, string(text), string(c.json(t, bin)), "%s: the generated ToJson writes the corpus text", c.name)
		}
		manifest, err := os.ReadFile(filepath.Join("..", "..", "schemas", "roadmap", "fuzz", "MANIFEST.txt"))
		require.NoError(t, err)
		assert.Contains(t, string(manifest), "instance doc-typical roadmap Doc schemas/roadmap/fuzz/doc-typical.bin")
		assert.Contains(t, string(manifest), "instance fixes-typical roadmap Fixes schemas/roadmap/fuzz/fixes-typical.bin")
	})

	t.Run("damage is refused by name in every form", func(t *testing.T) {
		t.Parallel()
		for _, tb := range allTables {
			t.Run(tb.label(), func(t *testing.T) {
				t.Parallel()
				tb.damage(t, jsonOf(t, small(all, tb.label())))
			})
		}
	})

	t.Run("the reader refuses a value past each declared bound", func(t *testing.T) {
		t.Parallel()
		// The date: 10 bytes are accepted (the round trips above), 11 are
		// refused in all three forms, on every table that carries one.
		for _, c := range []struct {
			tb runner
			in J
		}{
			{noteTable, J{"id": "n", "title": "t", "text": "x", "date": "2026-10-17"}},
			{itemTable, smallItem()},
			{releaseTable, J{"id": "v1", "status": "planned", "text": "x", "date": "2026-10-17"}},
		} {
			text := jsonOf(t, c.in)
			assert.Equal(t, "clamped", c.tb.refuses(t, replaceOnce(t, text, "2026-10-17", "2026-10-170")), "%s: a date of 11 bytes as JSON text", c.tb.label())
			past := pastBound(t, c.tb.binary(t, text), "2026-10-17")
			assert.Equal(t, "clamped", c.tb.refuses(t, past), "%s: a date of 11 bytes as binary", c.tb.label())
			assert.Equal(t, "clamped", c.tb.refuses(t, b64(past)), "%s: a date of 11 bytes as base64", c.tb.label())
		}
		// The cards range: 1<<20 is accepted (fullItem round-trips), one more
		// is refused; the wire carries an int32 as four bytes, little-endian.
		full := jsonOf(t, fullItem())
		assert.Equal(t, "clamped", itemTable.refuses(t, replaceOnce(t, full, "1048576", "1048577")), "cards past 1<<20 as JSON text")
		assert.Equal(t, "clamped", itemTable.refuses(t, replaceOnce(t, full, "1048576", "-1")), "negative cards as JSON text")
		w := itemTable.binary(t, full)
		over := replaceOnce(t, w, le32(1<<20), le32(1<<20+1))
		assert.Equal(t, "clamped", itemTable.refuses(t, over), "cards past 1<<20 as binary")
		assert.Equal(t, "clamped", itemTable.refuses(t, b64(over)), "cards past 1<<20 as base64")
		neg := replaceOnce(t, w, le32(1<<20), le32(0xffffffff))
		assert.Equal(t, "clamped", itemTable.refuses(t, neg), "negative cards as binary")

		// The closed status lists. The wire carries an enum as the hash of
		// its variant's name, so a writer that ignored the list is a
		// variant id this reader does not know: it reads as none and counts
		// one unknown field, and the code's rule that a status is one of the
		// list refuses it by name. The same holds for the JSON text.
		rel := J{"id": "v1", "status": "planned", "text": "x"}
		assert.Equal(t, "absent_required (status)", releaseTable.refuses(t, replaceOnce(t, jsonOf(t, rel), `"planned"`, `"fourth"`)), "a fourth release status as JSON text")
		planned := releaseTable.binary(t, jsonOf(t, rel))
		rel["status"] = "shipped"
		fourth := bytes.Clone(planned)
		fourth[firstDiff(planned, releaseTable.binary(t, jsonOf(t, rel)))] ^= 0xff
		assert.Equal(t, "absent_required (status)", releaseTable.refuses(t, fourth), "a fourth release status as binary")
		assert.Equal(t, "absent_required (status)", releaseTable.refuses(t, b64(fourth)), "a fourth release status as base64")
		fix := J{"id": "f", "release": "v1", "title": "t", "origin": "o", "status": "done"}
		assert.Equal(t, "absent_required (status)", fixTable.refuses(t, replaceOnce(t, jsonOf(t, fix), `"done"`, `"fifth"`)), "a fifth fix status as JSON text")
		done := fixTable.binary(t, jsonOf(t, fix))
		fix["status"] = "planned"
		fifth := bytes.Clone(done)
		fifth[firstDiff(done, fixTable.binary(t, jsonOf(t, fix)))] ^= 0xff
		assert.Equal(t, "absent_required (status)", fixTable.refuses(t, fifth), "a fifth fix status as binary")
		assert.Equal(t, "absent_required (status)", fixTable.refuses(t, b64(fifth)), "a fifth fix status as base64")
		// a key given twice is ambiguous
		assert.Equal(t, schemaread.ReasonMalformed, groupTable.refuses(t, []byte(`{"id":"g","id":"h","title":"t","text":"x"}`)))
	})

	t.Run("the reader refuses what the code refuses", func(t *testing.T) {
		t.Parallel()
		with := func(m J, k string, v any) J {
			out := J{}
			for a, b := range m {
				out[a] = b
			}
			if v == nil {
				delete(out, k)
			} else {
				out[k] = v
			}
			return out
		}
		d := smallDoc()
		fx := smallFixes()
		sched := d["scheduled"].(L)[0].(J)
		for _, c := range []struct {
			tb   runner
			in   J
			want string
		}{
			{groupTable, J{"id": " ", "title": "t", "text": "x"}, "absent_required (id)"},
			{groupTable, J{"id": "g", "text": "x"}, "absent_required (title)"},
			{itemTable, with(smallItem(), "date", "17-10-2026"), "absent_required (date)"},
			{itemTable, with(smallItem(), "date", nil), "absent_required (date)"},
			{itemTable, with(smallItem(), "group", nil), "absent_required (group)"},
			{itemTable, with(smallItem(), "why", " "), "absent_required (why)"},
			{itemTable, with(smallItem(), "kept", L{J{"key": "a", "value": "1"}, J{"key": "a", "value": "2"}}), "absent_required (kept.key distinct)"},
			{itemTable, with(smallItem(), "kept", L{J{"key": "a b", "value": "1"}}), "absent_required (kept.key)"},
			{pairTable, J{"key": "k"}, "absent_required (kept.value)"},
			{noteTable, J{"id": "n", "title": "t", "text": "x"}, "absent_required (date)"},
			{docTable, with(d, "title", nil), "absent_required (title)"},
			{docTable, with(d, "scheduled", L{with(sched, "release", nil)}), "absent_required (scheduled.release)"},
			{docTable, with(d, "scheduled", L{with(sched, "kept", L{J{"key": "k", "value": "v"}})}), "absent_required (scheduled.kept)"},
			{docTable, with(d, "done", L{with(sched, "id", "done-x")}), "absent_required (done.release)"},
			{docTable, with(d, "groups", L{J{"id": "item-one", "title": "t", "text": "x"}}), "absent_required (id distinct)"},
			{docTable, with(d, "items", L{with(smallItem(), "group", "nowhere")}), "absent_required (items.group names a group)"},
			{docTable, with(d, "groups", L{J{"id": "group-one", "title": "t", "text": "x"}, J{"id": "group-two", "title": "t", "text": "x"}}), "absent_required (groups hold an item)"},
			{releaseTable, J{"id": "v1", "text": "x"}, "absent_required (status)"},
			{releaseTable, J{"id": "v1", "status": "shipped", "text": "x", "date": "2026/10/17"}, "absent_required (date)"},
			{fixTable, J{"id": "f", "release": "v1", "title": "t", "status": "done"}, "absent_required (origin)"},
			{fixesTable, with(fx, "items", L{J{"id": "f", "release": "v9", "title": "t", "origin": "o", "status": "done"}, J{"id": "g", "release": "v1.2.10", "title": "t", "origin": "o", "status": "done"}, J{"id": "h", "release": "v1.2.11", "title": "t", "origin": "o", "status": "done"}}), "absent_required (items.release names a release)"},
			{fixesTable, with(fx, "items", L{J{"id": "f", "release": "v1.2.10", "title": "t", "origin": "o", "status": "done"}}), "absent_required (releases hold a fix)"},
			{fixesTable, with(fx, "items", L{J{"id": "v1.2.10", "release": "v1.2.10", "title": "t", "origin": "o", "status": "done"}, J{"id": "g", "release": "v1.2.11", "title": "t", "origin": "o", "status": "done"}}), "absent_required (id distinct)"},
			{docTable, d, ""},
			{fixesTable, fx, ""},
		} {
			assert.Equal(t, c.want, c.tb.refuses(t, jsonOf(t, c.in)), "%s: %v", c.tb.label(), c.in)
		}
	})

	t.Run("the writer refuses past each bound and writes nothing", func(t *testing.T) {
		t.Parallel()
		ri := schemaread.NewReader(itemTable.codec())
		di, _, err := ri.Read(jsonOf(t, fullItem()))
		require.NoError(t, err)
		di.v.Cards = 1<<20 + 1
		for _, form := range forms {
			out, err := ri.Write(&di, form)
			assert.Nil(t, out, "cards past 1<<20 %s: nothing is written", form)
			assert.Equal(t, "clamped (cards)", refusal(t, "Item", err), "cards past 1<<20 %s", form)
		}
		di.v.Cards = 3
		di.v.Date[10], di.v.DateLength = '9', 11
		for _, form := range forms {
			out, err := ri.Write(&di, form)
			assert.Nil(t, out, "a date of 11 bytes %s: nothing is written", form)
			assert.Equal(t, "clamped (date)", refusal(t, "Item", err), "a date of 11 bytes %s", form)
		}
		rr := schemaread.NewReader(releaseTable.codec())
		dr, _, err := rr.Read(jsonOf(t, all["Release"][0]))
		require.NoError(t, err)
		dr.v.Status = wire.ReleaseStatusCount + 1
		for _, form := range forms {
			out, err := rr.Write(&dr, form)
			assert.Nil(t, out, "a fourth release status %s: nothing is written", form)
			assert.Equal(t, "clamped (status)", refusal(t, "Release", err), "a fourth release status %s", form)
		}
		rf := schemaread.NewReader(fixTable.codec())
		df, _, err := rf.Read(jsonOf(t, all["Fix"][0]))
		require.NoError(t, err)
		df.v.Status = wire.FixStatusCount + 1
		for _, form := range forms {
			out, err := rf.Write(&df, form)
			assert.Nil(t, out, "a fifth fix status %s: nothing is written", form)
			assert.Equal(t, "clamped (status)", refusal(t, "Fix", err), "a fifth fix status %s", form)
		}
		df.v.Status = wire.FixStatusNone
		_, err = rf.Write(&df, schemaread.FormBinary)
		assert.Equal(t, "absent_required (status)", refusal(t, "Fix", err))
	})
}
