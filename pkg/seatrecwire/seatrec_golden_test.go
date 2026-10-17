package seatrecwire_test

// TestSeatAndTokensRecordsGolden is the golden test of the seatrec
// definitions (schemas/seatrec/seatrec.schema, schemas/seatrec/schema.lock,
// schemas/seatrec/tables.baseline and the generated pkg/seatrecwire): the
// seat environment, the tokens day file, the tokens bus roster and the
// secrets place receipt round-trip through pkg/schemaread as the binary
// wire, padded standard base64 of that wire and the schema JSON text form
// (S1, S2); a value past each declared bound is refused by name on write and
// on read, never clamped; a damaged image of each form is refused by name;
// and generate.go pins the repository's schema pin.
//
// The split it pins (S1, as the coordinator read it for this card): every
// record of the area is a VARIABLE table, because each holds text, and the
// day file and the roster hold lists, that the Go code does not bound. Text
// with no bound in the code is *string, a list is []T, and a string(N) or a
// range is declared only where the code enforces it (the day and the row
// date, 10 bytes; the counts, never negative). So a 100-row day file, a
// 200-participant roster and text of a megabyte round-trip, where the
// attempt-1 fixed tables refused them.
//
// The instances are written as JSON text and read by the generated reader,
// the wire is the generated writer's, and the lock and the baseline are
// files of the repository. The test uses no clock, no socket, no network and
// no program, and no field or fixture holds a secret value: a secret is
// named, and the names here are obviously fake.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// help is the command every refusal points at.
const help = "go doc github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"

// ---------------------------------------------------------------------------
// The models: today's records, field for field, as the test states them.
// Text the Go code treats as absent when empty is a string here, and a null
// *string on the table; the day file's at and build, whose keys the Go code
// requires while it accepts an empty value, are *string so present-and-empty
// is kept apart from absent.

type label struct {
	Text string `json:"text,omitempty"`
}

type login struct {
	Redis  string `json:"redis,omitempty"`
	User   string `json:"user,omitempty"`
	Store  string `json:"store,omitempty"`
	As     string `json:"as,omitempty"`
	Key    string `json:"key,omitempty"`
	Sops   string `json:"sops,omitempty"`
	Secret string `json:"secret,omitempty"`
}

type seatEnv struct {
	Version          *int32 `json:"version,omitempty"`
	ForgeIdentity    string `json:"forge_identity,omitempty"`
	NovaSprintRedis  string `json:"nova_sprint_redis,omitempty"`
	NovaSprintServer string `json:"nova_sprint_server,omitempty"`
	NovaBusRedis     string `json:"nova_bus_redis,omitempty"`
	Login            *login `json:"login,omitempty"`
}

type dayLine struct {
	Date       string  `json:"date,omitempty"`
	Model      string  `json:"model,omitempty"`
	Repo       string  `json:"repo,omitempty"`
	Input      *int64  `json:"input,omitempty"`
	Output     *int64  `json:"output,omitempty"`
	CacheWrite *int64  `json:"cache_write,omitempty"`
	CacheRead  *int64  `json:"cache_read,omitempty"`
	Reasoning  *int64  `json:"reasoning,omitempty"`
	Rough      int64   `json:"rough,omitempty"`
	Basis      string  `json:"basis,omitempty"`
	Sources    []label `json:"sources,omitempty"`
}

type dayFile struct {
	Day     string    `json:"day,omitempty"`
	At      *string   `json:"at,omitempty"`
	Build   *string   `json:"build,omitempty"`
	Turns   *int64    `json:"turns,omitempty"`
	Sources []label   `json:"sources,omitempty"`
	Rows    []dayLine `json:"rows,omitempty"`
}

type participant struct {
	Name string `json:"name,omitempty"`
	Lane string `json:"lane,omitempty"`
}

type roster struct {
	Participants []participant `json:"participants,omitempty"`
}

type receipt struct {
	Secret string `json:"secret,omitempty"`
	Path   string `json:"path,omitempty"`
	File   string `json:"file,omitempty"`
	Head   string `json:"head,omitempty"`
	Blob   string `json:"blob,omitempty"`
	Stamp  string `json:"stamp,omitempty"`
}

func i64(v int64) *int64  { return &v }
func i32(v int32) *int32  { return &v }
func sp(v string) *string { return &v }

// ---------------------------------------------------------------------------
// Reading a loaded value back into its model.

func str(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

func strp(slot *int64) *string {
	if *slot == 0 {
		return nil
	}
	s := str(slot)
	return &s
}

func opt(v int64, present bool) *int64 {
	if !present {
		return nil
	}
	return &v
}

func fixedText(b []byte, n int32) string { return string(b[:n]) }

func labels(l *wire.TableList[wire.SourceLabelRow]) []label {
	var out []label
	for i := int32(0); i < l.Count; i++ {
		out = append(out, label{Text: str(&l.At(i).Text)})
	}
	return out
}

func seatOf(v *wire.SeatEnvironment) seatEnv {
	m := seatEnv{
		ForgeIdentity: str(&v.ForgeIdentity), NovaSprintRedis: str(&v.NovaSprintRedis),
		NovaSprintServer: str(&v.NovaSprintServer), NovaBusRedis: str(&v.NovaBusRedis),
	}
	if v.VersionPresent {
		m.Version = i32(v.Version)
	}
	if l := wire.SeatLoginAt(&v.Login); l != nil {
		m.Login = &login{Redis: str(&l.Redis), User: str(&l.User), Store: str(&l.Store), As: str(&l.AsName),
			Key: str(&l.Key), Sops: str(&l.Sops), Secret: str(&l.Secret)}
	}
	return m
}

func dayOf(v *wire.DayFile) dayFile {
	m := dayFile{
		Day: fixedText(v.Day[:], v.DayLength), At: strp(&v.At), Build: strp(&v.Build),
		Turns: opt(v.Turns, v.TurnsPresent), Sources: labels(&v.Sources),
	}
	for i := int32(0); i < v.Rows.Count; i++ {
		r := v.Rows.At(i)
		m.Rows = append(m.Rows, dayLine{
			Date: fixedText(r.Date[:], r.DateLength), Model: str(&r.Model), Repo: str(&r.Repo),
			Input: opt(r.Input, r.InputPresent), Output: opt(r.Output, r.OutputPresent),
			CacheWrite: opt(r.CacheWrite, r.CacheWritePresent), CacheRead: opt(r.CacheRead, r.CacheReadPresent),
			Reasoning: opt(r.Reasoning, r.ReasoningPresent), Rough: r.Rough, Basis: str(&r.Basis),
			Sources: labels(&r.Sources),
		})
	}
	return m
}

func rosterOf(v *wire.BusRoster) roster {
	var m roster
	for i := int32(0); i < v.Participants.Count; i++ {
		p := v.Participants.At(i)
		m.Participants = append(m.Participants, participant{Name: str(&p.Name), Lane: str(&p.Lane)})
	}
	return m
}

func receiptOf(v *wire.PlacedReceipt) receipt {
	return receipt{Secret: str(&v.Secret), Path: str(&v.Path), File: str(&v.File),
		Head: str(&v.Head), Blob: str(&v.Blob), Stamp: str(&v.Stamp)}
}

// ---------------------------------------------------------------------------
// The constraints: what the Go code requires of each record, stated once for
// the reader and the writer. A variable table reads an absent field as zero,
// so a field the code requires is Required here; a declared bound is checked
// again here so the writer refuses it by name before a byte is written.

// noCap is the bound of text the code does not bound: Checker.Text then
// refuses only a NUL and text that is not UTF-8, which the table writer
// cannot carry.
const noCap = math.MaxInt

func seatConstraints(v *wire.SeatEnvironment, c *schemaread.Checker) {
	// LoadSeatEnvironment accepts version 1 only (pkg/seatcred/environment.go:101).
	c.Required("version", v.VersionPresent)
	c.Bound("version", int64(v.Version), 1, 1)
	l := wire.SeatLoginAt(&v.Login)
	if l == nil {
		return
	}
	// validate and StoreLogin.missing require every reference of a present
	// login (environment.go:248-258, 275-277).
	for _, f := range []struct {
		name string
		slot *int64
	}{{"redis", &l.Redis}, {"user", &l.User}, {"store", &l.Store}, {"as", &l.AsName}, {"key", &l.Key}, {"sops", &l.Sops}, {"secret", &l.Secret}} {
		c.Required("login."+f.name, str(f.slot) != "")
	}
}

func dayConstraints(v *wire.DayFile, c *schemaread.Checker) {
	// The version line requires day, at and build (dayfile.go:353-358); the
	// day is a ValidDay name of 10 bytes (check.go:99, dayfile.go:372-374).
	c.Required("day", v.DayLength > 0)
	c.Text("day", fixedText(v.Day[:], min(v.DayLength, int32(len(v.Day)))), 10)
	if v.DayLength > 10 {
		c.Bound("day", int64(v.DayLength), 0, 10)
	}
	c.Required("at", v.At != 0)
	c.Required("build", v.Build != 0)
	if v.TurnsPresent {
		c.Bound("turns", v.Turns, 0, math.MaxInt64) // dayfile.go:361-371
	}
	// A file with no rows is refused (dayfile.go:463-465).
	c.Required("rows", v.Rows.Count > 0)
	for i := int32(0); i < v.Rows.Count; i++ {
		r := v.Rows.At(i)
		c.Required("rows.date", r.DateLength > 0)
		if r.DateLength > 10 {
			c.Bound("rows.date", int64(r.DateLength), 0, 10)
		}
		for _, n := range []struct {
			name    string
			v       int64
			present bool
		}{
			{"rows.input", r.Input, r.InputPresent}, {"rows.output", r.Output, r.OutputPresent},
			{"rows.cache_write", r.CacheWrite, r.CacheWritePresent}, {"rows.cache_read", r.CacheRead, r.CacheReadPresent},
			{"rows.reasoning", r.Reasoning, r.ReasoningPresent}, {"rows.rough", r.Rough, true},
		} {
			if n.present {
				c.Bound(n.name, n.v, 0, math.MaxInt64) // dayfile.go:408-418, 421-426
			}
		}
		// An empty sources cell is refused (dayfile.go:432-436).
		c.Required("rows.sources", r.Sources.Count > 0)
	}
}

// slugOf is laneNames's rule (internal/tokens/bus.go:280-283): a lane is
// "from-<slug>" with a non-empty slug.
func slugOf(lane string) string {
	slug, ok := strings.CutPrefix(lane, "from-")
	if !ok {
		return ""
	}
	return slug
}

func rosterConstraints(v *wire.BusRoster, c *schemaread.Checker) {
	// A roster that yields no lane is refused (bus.go:285-287).
	lanes := 0
	for i := int32(0); i < v.Participants.Count; i++ {
		if slugOf(str(&v.Participants.At(i).Lane)) != "" {
			lanes++
		}
	}
	c.Required("participants.lane", lanes > 0)
}

// receiptConstraints is empty on purpose: readReceipts
// (pkg/secrets/place.go:448-482) checks no field, so the table refuses no
// receipt line today's reader accepts.
func receiptConstraints(*wire.PlacedReceipt, *schemaread.Checker) {}

// ---------------------------------------------------------------------------
// The harness: one generated table behind pkg/schemaread.

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
		SaveJSON: func(d *doc[T]) []byte {
			n := tb.jsonMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.toJSON(d.v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

var (
	seatTable = table[wire.SeatEnvironment]{
		name: "SEAT", info: wire.SeatEnvironmentTableType, measureReason: wire.SeatEnvironmentLoadMeasureReason,
		load: wire.SeatEnvironmentLoad, fromJSON: viaJSON[wire.SeatEnvironment, wire.SeatEnvironmentBuilder](wire.SeatEnvironmentFromJson),
		measure: wire.SeatEnvironmentMeasure, save: wire.SeatEnvironmentSave,
		jsonMeasure: wire.SeatEnvironmentToJsonMeasure, toJSON: wire.SeatEnvironmentToJson,
		saveText: wire.SeatEnvironmentSaveText, loadText: wire.SeatEnvironmentLoadText, constraints: seatConstraints,
	}
	dayTable = table[wire.DayFile]{
		name: "DAYFILE", info: wire.DayFileTableType, measureReason: wire.DayFileLoadMeasureReason,
		load: wire.DayFileLoad, fromJSON: viaJSON[wire.DayFile, wire.DayFileBuilder](wire.DayFileFromJson),
		measure: wire.DayFileMeasure, save: wire.DayFileSave,
		jsonMeasure: wire.DayFileToJsonMeasure, toJSON: wire.DayFileToJson,
		saveText: wire.DayFileSaveText, loadText: wire.DayFileLoadText, constraints: dayConstraints,
	}
	rosterTable = table[wire.BusRoster]{
		name: "ROSTER", info: wire.BusRosterTableType, measureReason: wire.BusRosterLoadMeasureReason,
		load: wire.BusRosterLoad, fromJSON: viaJSON[wire.BusRoster, wire.BusRosterBuilder](wire.BusRosterFromJson),
		measure: wire.BusRosterMeasure, save: wire.BusRosterSave,
		jsonMeasure: wire.BusRosterToJsonMeasure, toJSON: wire.BusRosterToJson,
		saveText: wire.BusRosterSaveText, loadText: wire.BusRosterLoadText, constraints: rosterConstraints,
	}
	receiptTable = table[wire.PlacedReceipt]{
		name: "RECEIPT", info: wire.PlacedReceiptTableType, measureReason: wire.PlacedReceiptLoadMeasureReason,
		load: wire.PlacedReceiptLoad, fromJSON: viaJSON[wire.PlacedReceipt, wire.PlacedReceiptBuilder](wire.PlacedReceiptFromJson),
		measure: wire.PlacedReceiptMeasure, save: wire.PlacedReceiptSave,
		jsonMeasure: wire.PlacedReceiptToJsonMeasure, toJSON: wire.PlacedReceiptToJson,
		saveText: wire.PlacedReceiptSaveText, loadText: wire.PlacedReceiptLoadText, constraints: receiptConstraints,
	}
)

var forms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

func b64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func jsonOf(t *testing.T, m any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// read is a model written as JSON text and read by the generated reader.
func read[T any](t *testing.T, r *schemaread.Reader[doc[T]], m any) doc[T] {
	t.Helper()
	d, res, err := r.Read(jsonOf(t, m))
	require.NoError(t, err)
	require.Equal(t, schemaread.FormJSON, res.Form)
	return d
}

func write[T any](t *testing.T, r *schemaread.Reader[doc[T]], d doc[T], form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(&d, form)
	require.NoError(t, err, form)
	require.NotEmpty(t, out, "%s: the generated writer wrote it", form)
	return out
}

// roundTrip writes m in every form and reads each back as m (S2), and pins
// the wire as canonical and the base64 text as padded standard base64 of it.
func roundTrip[T any, M any](t *testing.T, tb table[T], of func(*T) M, m M) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d := read(t, r, m)
	require.Equal(t, m, of(d.v), "%s: the JSON text reads as written", tb.name)
	for _, form := range forms {
		out := write(t, r, d, form)
		got, res, err := r.Read(out)
		require.NoError(t, err, "%s %s", tb.name, form)
		assert.Equal(t, form, res.Form)
		assert.Zero(t, res.Unknown)
		assert.Equal(t, m, of(got.v), "%s %s", tb.name, form)
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

// refusal is the reason a read or a write refused with, by name; "" when
// it did not refuse.
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

// refusesDamage reads the damaged images of a good wire and its JSON text
// and asserts each is refused by name.
func refusesDamage[T any](t *testing.T, tb table[T], good, text []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
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

// duplicated is JSON text with its first key given twice.
func duplicated(text []byte) []byte {
	i := bytes.IndexByte(text, ',')
	return append(append(bytes.Clone(text[:i]), ','), text[1:]...)
}

// pastBound is the wire image good holds with its string of exactly the n
// bytes s grown by one byte, its length prefix rewritten: the image of a
// writer that ignored the bound. The table wire writes a string(N) as kind
// 12, the LEB128 length and the bytes, so the image differs from good only
// there.
func pastBound(t *testing.T, good []byte, s string) []byte {
	t.Helper()
	enc := func(s string) []byte { return append(binary.AppendUvarint([]byte{12}, uint64(len(s))), s...) }
	require.Equal(t, 1, bytes.Count(good, enc(s)), "the at-bound string %q is in the image once", s)
	return bytes.Replace(good, enc(s), enc(s+"9"), 1)
}

// hostile is good with its one occurrence of the eight little-endian bytes
// of from replaced by those of to.
func hostile(t *testing.T, good []byte, from, to int64) []byte {
	t.Helper()
	le := func(v int64) []byte { return binary.LittleEndian.AppendUint64(nil, uint64(v)) }
	require.Equal(t, 1, bytes.Count(good, le(from)), "the marker %d is in the image once", from)
	return bytes.Replace(good, le(from), le(to), 1)
}

// edited is JSON text with one value replaced.
func edited(t *testing.T, text []byte, from, to string) []byte {
	t.Helper()
	require.Equal(t, 1, bytes.Count(text, []byte(from)), "%q is in the text once", from)
	return bytes.Replace(text, []byte(from), []byte(to), 1)
}

// ---------------------------------------------------------------------------
// The samples. Every name is obviously fake, and no secret value appears.

func sampleSeat() seatEnv {
	return seatEnv{
		Version:       i32(1),
		ForgeIdentity: "/home/example-seat/.forge",
		NovaBusRedis:  "127.0.0.1:6381", NovaSprintServer: "sprint.example.test:7070",
		Login: &login{Redis: "store.example.test:6390", User: "example-seat", Store: "/srv/example-store",
			As: "example-seat", Key: "/srv/example-store/keys/example-seat.txt", Sops: "/srv/example-store/.sops.yaml",
			Secret: "EXAMPLE_FAKE_SECRET_NAME"},
	}
}

func sampleRow(day string, i int) dayLine {
	return dayLine{
		Date: day, Model: fmt.Sprintf("example-model-%03d", i), Repo: "example/repo",
		Input: i64(1000 + int64(i)), Output: i64(0), CacheRead: i64(math.MaxInt64), Reasoning: i64(7),
		Rough: int64(i), Basis: "America/New_York", Sources: []label{{"claude"}, {"opencode"}},
	}
}

func sampleDay(rows int) dayFile {
	d := dayFile{Day: "2026-10-16", At: sp("2026-10-16T23:00:00Z"), Build: sp("v1.5.0-example"), Turns: i64(42),
		Sources: []label{{"claude"}, {"codex"}, {"opencode"}}}
	for i := range rows {
		d.Rows = append(d.Rows, sampleRow(d.Day, i))
	}
	return d
}

func sampleRoster(n int) roster {
	var r roster
	for i := range n {
		r.Participants = append(r.Participants, participant{Name: fmt.Sprintf("Example %03d", i), Lane: fmt.Sprintf("from-example-%03d", i)})
	}
	return r
}

func sampleReceipt() receipt {
	return receipt{Secret: "EXAMPLE_FAKE_SECRET_NAME", Path: "/etc/example/fake.env", File: "example-seat.yaml",
		Head: strings.Repeat("0123456789abcdef", 4), Blob: strings.Repeat("fedcba9876", 4), Stamp: "1970-01-01T00:16:40Z"}
}

// ---------------------------------------------------------------------------

func TestSeatAndTokensRecordsGolden(t *testing.T) {
	t.Parallel()

	t.Run("generate.go pins the repository's schema pin", func(t *testing.T) {
		t.Parallel()
		pin := regexp.MustCompile(`(?m)^//go:generate go run github\.com/mas-bandwidth/schema/v2/cmd/schema@(\S+) generate --lang go --out \. \.\./\.\./schemas/(\S+)$`)
		own, err := os.ReadFile("generate.go")
		require.NoError(t, err)
		m := pin.FindSubmatch(own)
		require.NotNil(t, m, "generate.go holds the pin line")
		assert.Equal(t, "seatrec", string(m[2]))
		pong, err := os.ReadFile(filepath.Join("..", "pongwire", "generate.go"))
		require.NoError(t, err)
		pm := pin.FindSubmatch(pong)
		require.NotNil(t, pm)
		assert.Equal(t, string(pm[1]), string(m[1]), "one compiler writes the whole tree (schemas/README.md, the pin)")
	})

	t.Run("the definitions are variable tables with the code's bounds", func(t *testing.T) {
		t.Parallel()
		for _, info := range []*wire.TableTypeInfo{
			wire.SeatEnvironmentTableType(), wire.SeatLoginTableType(), wire.SourceLabelTableType(), wire.DayLineTableType(),
			wire.DayFileTableType(), wire.ParticipantTableType(), wire.BusRosterTableType(), wire.PlacedReceiptTableType(),
		} {
			assert.True(t, info.Variable, "%s is a variable table (S1): it holds text or lists the code does not bound", info.Name)
			for _, f := range info.Fields {
				switch {
				case f.Name == "day" || f.Name == "date":
					assert.Equal(t, int32(10), f.ArrayBound, "%s.%s: ValidDay's length", info.Name, f.Name)
				case f.Name == "sources" || f.Name == "rows" || f.Name == "participants":
					assert.True(t, f.List, "%s.%s: a list the code does not count is open ([]T)", info.Name, f.Name)
				case f.Kind == 12 || f.Kind == 17:
					assert.True(t, f.Pointer, "%s.%s: text the code does not bound is *string", info.Name, f.Name)
				}
			}
		}
		lock, err := os.ReadFile(filepath.Join("..", "..", "schemas", "seatrec", "schema.lock"))
		require.NoError(t, err)
		assert.NotContains(t, string(lock), "fixed ", "no fixed layout is locked: the area has no fixed table")
		baseline, err := os.ReadFile(filepath.Join("..", "..", "schemas", "seatrec", "tables.baseline"))
		require.NoError(t, err)
		for _, name := range []string{"SeatEnvironment", "SeatLogin", "SourceLabel", "DayLine", "DayFile", "Participant", "BusRoster", "PlacedReceipt"} {
			assert.Contains(t, string(baseline), "table "+name+"\n", "tables.baseline records %s", name)
		}
	})

	t.Run("round trip in every form", func(t *testing.T) {
		t.Parallel()
		full := sampleSeat()
		bare := seatEnv{Version: i32(1)}
		roundTrip(t, seatTable, seatOf, full)
		roundTrip(t, seatTable, seatOf, bare)
		roundTrip(t, dayTable, dayOf, sampleDay(2))
		dashes := sampleDay(1)
		dashes.Turns, dashes.Sources, dashes.At, dashes.Build = nil, nil, sp(""), sp("")
		dashes.Rows[0] = dayLine{Date: dashes.Day, Sources: []label{{"manual"}}}
		roundTrip(t, dayTable, dayOf, dashes)
		roundTrip(t, rosterTable, rosterOf, sampleRoster(3))
		roundTrip(t, receiptTable, receiptOf, sampleReceipt())
		roundTrip(t, receiptTable, receiptOf, receipt{Secret: "EXAMPLE_LEGACY", Path: "/etc/example/old.env", Stamp: "1970-01-01T00:16:40Z"})
	})

	t.Run("records sized by content round-trip", func(t *testing.T) {
		t.Parallel()
		// A month of rows in one day file, where attempt 1 capped it at 24.
		big := sampleDay(100)
		big.Sources = nil
		for i := range 40 {
			big.Sources = append(big.Sources, label{fmt.Sprintf("source-%02d", i)})
		}
		big.Rows[99].Sources = big.Sources
		roundTrip(t, dayTable, dayOf, big)
		// A roster past the bus envelope's 64 recipients, which laneNames never counted.
		roundTrip(t, rosterTable, rosterOf, sampleRoster(200))
		// Long text in every field the code does not bound.
		long := func(c string, n int) string { return strings.Repeat(c, n) }
		seat := sampleSeat()
		seat.ForgeIdentity = "/" + long("f", 4095)
		seat.NovaSprintRedis = long("h", 1000) + ":6379"
		seat.Login.Store, seat.Login.Key, seat.Login.Sops = "/"+long("s", 8191), "/"+long("k", 2048), "/"+long("o", 2048)
		seat.Login.User, seat.Login.As, seat.Login.Secret = long("u", 300), long("a", 300), "EXAMPLE_"+long("X", 300)
		roundTrip(t, seatTable, seatOf, seat)
		day := sampleDay(1)
		day.At, day.Build = sp(long("t", 500)), sp(long("b", 500))
		day.Rows[0].Model, day.Rows[0].Repo, day.Rows[0].Basis = long("m", 4096), long("r", 4096), long("z", 300)
		day.Rows[0].Sources = []label{{long("l", 1000)}}
		roundTrip(t, dayTable, dayOf, day)
		ros := roster{Participants: []participant{{Name: long("n", 1000), Lane: "from-" + long("e", 1000)}}}
		roundTrip(t, rosterTable, rosterOf, ros)
		rec := receipt{Secret: "EXAMPLE_" + long("Q", 500), Path: "/" + long("p", 1<<20), File: long("y", 1000) + ".yaml",
			Head: long("e", 200), Blob: long("d", 200), Stamp: long("1", 200)}
		roundTrip(t, receiptTable, receiptOf, rec)
	})

	t.Run("the JSON text is keyed by the schema's names", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(seatTable.codec())
		text := write(t, r, read(t, r, sampleSeat()), schemaread.FormJSON)
		for _, key := range []string{`"version"`, `"forge_identity"`, `"nova_bus_redis"`, `"login"`, `"as"`, `"secret"`} {
			assert.Contains(t, string(text), key)
		}
		assert.NotContains(t, string(text), `"as_name"`, "the seat record's key is \"as\"")
		rd := schemaread.NewReader(dayTable.codec())
		text = write(t, rd, read(t, rd, sampleDay(1)), schemaread.FormJSON)
		for _, key := range []string{`"day"`, `"at"`, `"build"`, `"turns"`, `"rows"`, `"date"`, `"cache_read"`, `"rough"`, `"basis"`, `"sources"`} {
			assert.Contains(t, string(text), key)
		}
	})

	t.Run("damage is refused by name in every form", func(t *testing.T) {
		t.Parallel()
		{
			r := schemaread.NewReader(seatTable.codec())
			d := read(t, r, sampleSeat())
			refusesDamage(t, seatTable, write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON))
		}
		{
			r := schemaread.NewReader(dayTable.codec())
			d := read(t, r, sampleDay(2))
			refusesDamage(t, dayTable, write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON))
		}
		{
			r := schemaread.NewReader(rosterTable.codec())
			d := read(t, r, sampleRoster(2))
			refusesDamage(t, rosterTable, write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON))
		}
		{
			r := schemaread.NewReader(receiptTable.codec())
			d := read(t, r, sampleReceipt())
			refusesDamage(t, receiptTable, write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON))
		}
	})

	t.Run("the reader refuses a value past each declared bound", func(t *testing.T) {
		t.Parallel()
		// The day file: the day and the row date are 10 bytes, every count is
		// non-negative. A row date that differs from the day keeps each
		// at-bound string unique in the image.
		r := schemaread.NewReader(dayTable.codec())
		day := sampleDay(1)
		day.Rows[0].Date = "2026-10-15"
		day.Rows[0].Input = i64(0x0102030405060708)
		day.Turns = i64(0x0a0b0c0d0e0f1011)
		day.Rows[0].Rough = 0x1213141516171819
		d := read(t, r, day)
		good, text := write(t, r, d, schemaread.FormBinary), write(t, r, d, schemaread.FormJSON)
		for _, tc := range []struct {
			name   string
			in     []byte
			reason string
		}{
			{"a day of 11 bytes", pastBound(t, good, "2026-10-16"), "clamped"},
			{"a day of 11 bytes as base64", b64(pastBound(t, good, "2026-10-16")), "clamped"},
			{"a day of 11 bytes as JSON text", edited(t, text, `"2026-10-16"`, `"2026-10-160"`), "clamped"},
			{"a row date of 11 bytes as JSON text", edited(t, text, `"2026-10-15"`, `"2026-10-150"`), "clamped"},
			{"a negative input count", hostile(t, good, 0x0102030405060708, -1), "clamped"},
			{"a negative input count as base64", b64(hostile(t, good, 0x0102030405060708, -1)), "clamped"},
			{"a negative input count as JSON text", edited(t, text, fmt.Sprint(0x0102030405060708), "-1"), "clamped"},
			{"a negative turns", hostile(t, good, 0x0a0b0c0d0e0f1011, -5), "clamped"},
			{"a negative turns as JSON text", edited(t, text, fmt.Sprint(0x0a0b0c0d0e0f1011), "-5"), "clamped"},
			{"a negative rough", hostile(t, good, 0x1213141516171819, math.MinInt64), "clamped"},
			{"a negative rough as JSON text", edited(t, text, fmt.Sprint(0x1213141516171819), "-1"), "clamped"},
			{"a key given twice", duplicated(text), schemaread.ReasonMalformed},
		} {
			assert.Equal(t, tc.reason, readRefusal(t, r, "DAYFILE", tc.in), tc.name)
		}
		assert.Positive(t, r.Counts().Refused(schemaread.ReasonClamped))

		// The seat environment: version 1 only. The range is the reader's
		// constraint (seatrec.schema, version), so an image written by a
		// writer that ignored it is refused after the load, by name.
		rs := schemaread.NewReader(seatTable.codec())
		seat := sampleSeat()
		seat.Version = i32(2)
		_, _, err := rs.Read(jsonOf(t, seat))
		assert.Equal(t, "clamped (version)", refusal(t, "SEAT", err), "a version 2 record as JSON text")
		v2, _, ok := seatTable.fromJSON(jsonOf(t, seat), &wire.TableReport{})
		require.True(t, ok)
		v2wire := seatTable.saveWire(v2)
		require.NotNil(t, v2wire, "the generated writer, which knows no range, writes version 2")
		assert.Equal(t, "clamped (version)", readRefusal(t, rs, "SEAT", v2wire), "a version 2 record from a writer that ignored the bound")
		assert.Equal(t, "clamped (version)", readRefusal(t, rs, "SEAT", b64(v2wire)))
		seat.Version = nil
		_, _, err = rs.Read(jsonOf(t, seat))
		assert.Equal(t, "absent_required (version)", refusal(t, "SEAT", err), "a record with no version")
		seat = sampleSeat()
		seat.Login.Secret = ""
		_, _, err = rs.Read(jsonOf(t, seat))
		assert.Equal(t, "absent_required (login.secret)", refusal(t, "SEAT", err), "a login with no secret name")

		// The day file's required keys and lists.
		noRows := sampleDay(0)
		_, _, err = r.Read(jsonOf(t, noRows))
		assert.Equal(t, "absent_required (rows)", refusal(t, "DAYFILE", err), "a file with no rows")
		noAt := sampleDay(1)
		noAt.At = nil
		_, _, err = r.Read(jsonOf(t, noAt))
		assert.Equal(t, "absent_required (at)", refusal(t, "DAYFILE", err), "a version line with no at")
		noSources := sampleDay(1)
		noSources.Rows[0].Sources = nil
		_, _, err = r.Read(jsonOf(t, noSources))
		assert.Equal(t, "absent_required (rows.sources)", refusal(t, "DAYFILE", err), "a row with an empty sources cell")

		// The roster: one that yields no lane.
		rr := schemaread.NewReader(rosterTable.codec())
		_, _, err = rr.Read(jsonOf(t, roster{Participants: []participant{{Name: "Example", Lane: "example"}, {Name: "Other", Lane: "from-"}}}))
		assert.Equal(t, "absent_required (participants.lane)", refusal(t, "ROSTER", err), "a roster with no from-<slug> lane")
	})

	t.Run("the writer refuses past each bound and writes nothing", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(dayTable.codec())
		for _, tc := range []struct {
			name   string
			edit   func(*wire.DayFile)
			reason string
		}{
			{"a day of 11 bytes", func(v *wire.DayFile) { v.Day[10], v.DayLength = '9', 11 }, "clamped (day)"},
			{"a row date of 11 bytes", func(v *wire.DayFile) { r := v.Rows.At(0); r.Date[10], r.DateLength = '9', 11 }, "clamped (rows.date)"},
			{"a negative input", func(v *wire.DayFile) { v.Rows.At(0).Input = -1 }, "clamped (rows.input)"},
			{"a negative rough", func(v *wire.DayFile) { v.Rows.At(0).Rough = -1 }, "clamped (rows.rough)"},
			{"a negative turns", func(v *wire.DayFile) { v.Turns = -1 }, "clamped (turns)"},
			{"no day", func(v *wire.DayFile) { v.DayLength = 0 }, "absent_required (day)"},
		} {
			d := read(t, r, sampleDay(1))
			tc.edit(d.v)
			for _, form := range forms {
				out, err := r.Write(&d, form)
				assert.Nil(t, out, "%s %s: nothing is written", tc.name, form)
				assert.Equal(t, tc.reason, refusal(t, "DAYFILE", err), "%s %s", tc.name, form)
			}
		}
		rs := schemaread.NewReader(seatTable.codec())
		d := read(t, rs, sampleSeat())
		d.v.Version = 2
		for _, form := range forms {
			out, err := rs.Write(&d, form)
			assert.Nil(t, out)
			assert.Equal(t, "clamped (version)", refusal(t, "SEAT", err), form)
		}
		d.v.VersionPresent = false
		_, err := rs.Write(&d, schemaread.FormBinary)
		assert.Equal(t, "absent_required (version)", refusal(t, "SEAT", err))
	})
}
