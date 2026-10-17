package tokens

import (
	"bytes"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
)

// The dual readers of the tokens dayfile records: a day file (dayfile.go
// ReadDayFile) and the bus roster (bus.go laneNames) are each read through
// pkg/schemaread from the fixed binary image of their generated table, the
// padded standard base64 of that image, and the legacy form the writers write
// today, refused by name when damaged, counted per record and per form (the
// owner's S2 and the reader-first order C13).

// srDoc is one value and the region that holds it: text and lists are
// self-relative references into that region, so the value is never copied out
// of it.
type srDoc[T any] struct {
	v      *T
	region []byte
}

func srRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

type srBuilder[T any] interface {
	Init(...wire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Region() []byte
}

func srViaJSON[T any, B any, PB interface {
	*B
	srBuilder[T]
}](from func(*B, []byte, *wire.TableReport) bool) func([]byte, *wire.TableReport) (*T, []byte, bool) {
	return func(text []byte, r *wire.TableReport) (*T, []byte, bool) {
		b := PB(new(B))
		if !b.Init() || !from((*B)(b), text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	}
}

func srReport(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
}

type srTable[T any] struct {
	name          string
	help          string
	measureReason func([]byte) (int64, error)
	load          func(region, w []byte, r *wire.TableReport) *T
	fromJSON      func([]byte, *wire.TableReport) (*T, []byte, bool)
	measure       func(*T, ...wire.TableWriteContext) int64
	save          func(*T, []byte, ...wire.TableWriteContext) int64
	jsonMeasure   func(*T, ...wire.TableAllocator) int64
	toJSON        func(*T, []byte, ...wire.TableAllocator) int64
	constraints   func(*T, *schemaread.Checker)
}

func (tb srTable[T]) saveWire(v *T) []byte {
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

func (tb srTable[T]) codec() *schemaread.Codec[srDoc[T]] {
	return &schemaread.Codec[srDoc[T]]{
		Name:  tb.name,
		Help:  tb.help,
		Reset: func(d *srDoc[T]) { *d = srDoc[T]{} },
		Measure: func(w []byte) error {
			_, err := tb.measureReason(w)
			return err
		},
		LoadWire: func(d *srDoc[T], w []byte) schemaread.Report {
			n, err := tb.measureReason(w)
			if err != nil || n < 0 {
				return schemaread.Report{Malformed: true}
			}
			var r wire.TableReport
			d.region = srRegion(n)
			if d.v = tb.load(d.region, w, &r); d.v == nil {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			return srReport(&r)
		},
		LoadJSON: func(d *srDoc[T], text []byte) schemaread.Report {
			var r wire.TableReport
			v, reg, ok := tb.fromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, reg
			return srReport(&r)
		},
		Constraints: func(d *srDoc[T], c *schemaread.Checker) {
			if d.v != nil {
				tb.constraints(d.v, c)
			}
		},
		SaveWire: func(d *srDoc[T]) []byte { return tb.saveWire(d.v) },
		SaveJSON: func(d *srDoc[T]) []byte {
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

var dayTable = srTable[wire.DayFile]{
	name: "DAYFILE", help: "nova-tokens fold --day <d>", measureReason: wire.DayFileLoadMeasureReason,
	load: wire.DayFileLoad, fromJSON: srViaJSON[wire.DayFile, wire.DayFileBuilder](wire.DayFileFromJson),
	measure: wire.DayFileMeasure, save: wire.DayFileSave,
	jsonMeasure: wire.DayFileToJsonMeasure, toJSON: wire.DayFileToJson,
	constraints: dayConstraints,
}

var rosterTable = srTable[wire.BusRoster]{
	name: "ROSTER", help: "nova-tokens fold -h", measureReason: wire.BusRosterLoadMeasureReason,
	load: wire.BusRosterLoad, fromJSON: srViaJSON[wire.BusRoster, wire.BusRosterBuilder](wire.BusRosterFromJson),
	measure: wire.BusRosterMeasure, save: wire.BusRosterSave,
	jsonMeasure: wire.BusRosterToJsonMeasure, toJSON: wire.BusRosterToJson,
	constraints: rosterConstraints,
}

func str(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// fixedText is the text of a fixed string field, its length kept inside the
// field so a damaged length never reads past it.
func fixedText(b []byte, n int32) string { return string(b[:max(0, min(int(n), len(b)))]) }

func labels(l *wire.TableList[wire.SourceLabelRow]) []string {
	var out []string
	for i := int32(0); i < l.Count; i++ {
		out = append(out, str(&l.At(i).Text))
	}
	return out
}

// dayConstraints are the table's own bounds (the seatrec schema's DayFile and
// DayLine): the shape of the day file the version line and the rows require.
// What a day file must also be against its name and its neighbours (the day is
// the file's name, rows sorted and unique, a zone basis) is ParseDayFile's,
// and dayStructure applies it to every table form.
func dayConstraints(v *wire.DayFile, c *schemaread.Checker) {
	c.Required("day", v.DayLength > 0)
	c.Text("day", fixedText(v.Day[:], v.DayLength), 10)
	if v.DayLength > 10 {
		c.Bound("day", int64(v.DayLength), 0, 10)
	}
	c.Required("at", v.At != 0)
	c.Required("build", v.Build != 0)
	if v.TurnsPresent {
		c.Bound("turns", v.Turns, 0, math.MaxInt64)
	}
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
				c.Bound(n.name, n.v, 0, math.MaxInt64)
			}
		}
		c.Required("rows.sources", r.Sources.Count > 0)
	}
}

// rosterConstraints: the BusRoster table declares no bound (its list is open
// and its text uncapped, the seatrec schema's BusRoster). The roster's one
// rule, that it names a lane, is laneNames's and holds for every form.
func rosterConstraints(*wire.BusRoster, *schemaread.Checker) {}

// dayOf is the table value as the DayFile the package reads.
func dayOf(v *wire.DayFile) DayFile {
	out := DayFile{Day: fixedText(v.Day[:], v.DayLength), At: str(&v.At), Build: str(&v.Build), Turns: Dash}
	if v.TurnsPresent {
		out.Turns = strconv.FormatInt(v.Turns, 10)
	}
	out.Sources = labels(&v.Sources)
	for i := int32(0); i < v.Rows.Count; i++ {
		row := v.Rows.At(i)
		r := DayRow{Date: fixedText(row.Date[:], row.DateLength), Model: str(&row.Model),
			Repo: str(&row.Repo), Rough: int(row.Rough), Basis: str(&row.Basis), Sources: labels(&row.Sources)}
		for _, tm := range []struct {
			t Type
			v int64
			p bool
		}{
			{Input, row.Input, row.InputPresent}, {Output, row.Output, row.OutputPresent},
			{CacheWrite, row.CacheWrite, row.CacheWritePresent}, {CacheRead, row.CacheRead, row.CacheReadPresent},
			{Reasoning, row.Reasoning, row.ReasoningPresent},
		} {
			if tm.p {
				r.Counts.Set(tm.t, tm.v)
			}
		}
		out.Rows = append(out.Rows, r)
	}
	return out
}

// dayfileTable is true when the input is one of the table forms: the binary
// wire (led by a control byte, the form byte), the schema JSON text, or one
// unbroken run of padded standard base64. Everything else is the legacy TSV
// day file the writers write today, damaged or not, so a damaged legacy file
// is still named by ParseDayFile's own findings.
func dayfileTable(in []byte) bool {
	t := bytes.TrimSpace(in)
	if len(t) == 0 {
		return false
	}
	if b := t[0]; b < 0x20 || b >= 0x80 || b == '{' {
		return true
	}
	for _, b := range t {
		ok := b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '+' || b == '/' || b == '='
		if !ok {
			return false
		}
	}
	return true
}

// DayFileReader reads a day file in every form and counts the reads per form
// (S2, C13). One read at a time: the file's name is the read's own state.
type DayFileReader struct {
	r *schemaread.Reader[srDoc[wire.DayFile]]

	mu       sync.Mutex
	name     string
	parsed   DayFile
	findings []Finding
}

// NewDayFileReader is a day-file reader with empty counters.
func NewDayFileReader() *DayFileReader {
	d := &DayFileReader{}
	c := dayTable.codec()
	loadWire, loadJSON := c.LoadWire, c.LoadJSON
	c.LoadWire = func(v *srDoc[wire.DayFile], in []byte) schemaread.Report { return d.structure(v, loadWire(v, in)) }
	c.LoadJSON = func(v *srDoc[wire.DayFile], in []byte) schemaread.Report { return d.structure(v, loadJSON(v, in)) }
	c.Legacy = func(in []byte) bool { return !dayfileTable(in) }
	c.LoadLegacy = func(_ *srDoc[wire.DayFile], in []byte) schemaread.Report {
		d.parsed, d.findings = ParseDayFile(d.name, string(in))
		if len(d.findings) > 0 {
			return schemaread.Report{Reason: schemaread.ReasonMalformed}
		}
		return schemaread.Report{}
	}
	d.r = schemaread.NewReader(c)
	return d
}

// structure holds a cleanly loaded table value to everything ParseDayFile
// holds the legacy form to, by reading the value's own rendering: the day is
// the file's name, each row's date is too, rows are sorted and unique by
// (model, repo), the basis is a zone, the counts and turns are whole numbers,
// and every cell is one the legacy file holds as written, so each form reads
// as one equal value. A table form is never read more loosely than the
// legacy one (C13).
func (d *DayFileReader) structure(v *srDoc[wire.DayFile], rep schemaread.Report) schemaread.Report {
	if rep.Malformed || rep.Reason != "" || rep.KindMismatch > 0 || rep.Clamped > 0 || v.v == nil {
		return rep
	}
	day := dayOf(v.v)
	back, f := ParseDayFile(d.name, day.Render())
	if len(f) == 0 && !reflect.DeepEqual(back, day) {
		f = []Finding{{Line: 0, Reason: "a cell holds a value the day file cannot hold as written " +
			"(whitespace, a control character, `=`, or a `,` inside a source label); every cell is written through oneline.Field"}}
	}
	if len(f) > 0 {
		d.findings = f
		rep.Reason = schemaread.ReasonMalformed
	}
	d.parsed = day
	return rep
}

// Read reads the day file named name (its day, YYYY-MM-DD) in any form. A
// legacy file reads as ParseDayFile reads it, its findings unchanged. A table
// form reads as the same DayFile; a refused read returns the named refusal,
// and the findings that caused it.
func (d *DayFileReader) Read(name string, in []byte) (DayFile, []Finding, schemaread.Form, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.name, d.parsed, d.findings = name, DayFile{}, nil
	_, res, err := d.r.Read(in)
	form := res.Form
	if form == schemaread.FormNone {
		form = d.r.Detect(in)
	}
	if err != nil && form != schemaread.FormLegacy {
		return DayFile{}, d.findings, form, err
	}
	return d.parsed, d.findings, form, err
}

// dayReader is the one day-file reader of the package; its counters are the
// per-form read counts (S2, C13); nothing exports them until a main reads them
// (TestDeadCode refuses an accessor no main reaches).
var dayReader = NewDayFileReader()

// readDayFile reads one day file's bytes in any form. A legacy file returns
// ParseDayFile's findings as it always has. A refused table form is one
// finding per cause on line 1, the version line's place, each carrying the
// named refusal, so every caller that refuses a bad version line refuses it,
// and none reads it as a day with no rows.
func readDayFile(name string, raw []byte) (DayFile, []Finding) {
	d, f, form, err := dayReader.Read(name, raw)
	if err == nil || form == schemaread.FormLegacy {
		return d, f
	}
	if len(f) == 0 {
		return DayFile{}, []Finding{{Line: 1, Reason: err.Error()}}
	}
	out := make([]Finding, 0, len(f))
	for _, x := range f {
		out = append(out, Finding{Line: 1, Reason: err.Error() + ": " + x.Reason})
	}
	return DayFile{}, out
}

// BusRosterReader reads the bus roster in every form and counts the reads per
// form.
type BusRosterReader struct {
	r *schemaread.Reader[srDoc[wire.BusRoster]]
}

// rosterLegacy is true when the input is the roster's legacy JSON. That file
// is the schema JSON text itself (participants.json has the table's shape),
// so until the writer switch a JSON roster is counted as the legacy form.
func rosterLegacy(in []byte) bool {
	t := bytes.TrimLeft(in, " \t\r\n")
	return len(t) > 0 && t[0] == '{'
}

// NewBusRosterReader is a bus-roster reader with empty counters.
func NewBusRosterReader() *BusRosterReader {
	c := rosterTable.codec()
	c.Legacy = rosterLegacy
	c.LoadLegacy = c.LoadJSON
	return &BusRosterReader{r: schemaread.NewReader(c)}
}

// ReadRoster reads the roster in any form into its lane owners' slugs, in
// roster order: a lane "from-<slug>" with a slug is named, any other is
// skipped, as laneNames always has. A damaged roster is the named refusal.
func (r *BusRosterReader) ReadRoster(in []byte) ([]string, error) {
	d, _, err := r.r.Read(in)
	if err != nil {
		return nil, err
	}
	var out []string
	for i := int32(0); i < d.v.Participants.Count; i++ {
		p := d.v.Participants.At(i)
		if slug, ok := strings.CutPrefix(str(&p.Lane), "from-"); ok && slug != "" {
			out = append(out, slug)
		}
	}
	return out, nil
}

// rosterReader is the one roster reader of the package (S2, C13).
var rosterReader = NewBusRosterReader()
