package tokens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
)

// The dual readers of the tokens dayfile records: a day file (dayfile.go
// ParseDayFile) and the bus roster (bus.go laneNames) are each read through
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
		Help:  "nova-tokens fold --day <d> -h",
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
	name: "DAYFILE", measureReason: wire.DayFileLoadMeasureReason,
	load: wire.DayFileLoad, fromJSON: srViaJSON[wire.DayFile, wire.DayFileBuilder](wire.DayFileFromJson),
	measure: wire.DayFileMeasure, save: wire.DayFileSave,
	jsonMeasure: wire.DayFileToJsonMeasure, toJSON: wire.DayFileToJson,
	constraints: dayConstraints,
}

var rosterTable = srTable[wire.BusRoster]{
	name: "ROSTER", measureReason: wire.BusRosterLoadMeasureReason,
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

func labels(l *wire.TableList[wire.SourceLabelRow]) []string {
	var out []string
	for i := int32(0); i < l.Count; i++ {
		out = append(out, str(&l.At(i).Text))
	}
	return out
}

func dayConstraints(v *wire.DayFile, c *schemaread.Checker) {
	c.Required("day", v.DayLength > 0)
	c.Text("day", fixedText(v.Day[:], min(v.DayLength, int32(len(v.Day)))), 10)
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

func slugOf(lane string) string {
	slug, ok := strings.CutPrefix(lane, "from-")
	if !ok {
		return ""
	}
	return slug
}

func rosterConstraints(v *wire.BusRoster, c *schemaread.Checker) {
	lanes := 0
	for i := int32(0); i < v.Participants.Count; i++ {
		if slugOf(str(&v.Participants.At(i).Lane)) != "" {
			lanes++
		}
	}
	c.Required("participants.lane", lanes > 0)
}

// dayfileLegacy is true when the input is the legacy TSV day file: it opens
// with the version line the writers write today.
func dayfileLegacy(in []byte) bool {
	return bytes.HasPrefix(in, []byte(Version+" "))
}

// rosterLegacy is true when the input is the roster's legacy JSON, which is
// the schema JSON text itself: participants.json has the same shape, so the
// legacy form reads as the JSON form and is never a second shape.
func rosterLegacy(in []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(in, " \t\r\n"), []byte(`{"participants":`))
}

// loadDayfileLegacy reads the TSV day file the writers write today into the
// table value, by parsing it to the schema JSON text and loading that text.
func loadDayfileLegacy(v *srDoc[wire.DayFile], in []byte) schemaread.Report {
	obj, rep := tsvToJSON(string(in))
	if rep != (schemaread.Report{}) {
		return rep
	}
	text, err := json.Marshal(obj)
	if err != nil {
		return schemaread.Report{Malformed: true}
	}
	return dayTable.codec().LoadJSON(v, text)
}

// tsvToJSON parses one legacy TSV day file into the schema JSON text's shape,
// refusing every damage the legacy reader refuses.
func tsvToJSON(text string) (any, schemaread.Report) {
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil, schemaread.Report{Malformed: true}
	}
	head := lines[0]
	if !strings.HasPrefix(head, Version+" ") {
		return nil, schemaread.Report{Malformed: true}
	}
	fields := map[string]string{}
	for _, tok := range strings.Fields(strings.TrimPrefix(head, Version+" ")) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			fields[k] = v
		}
	}
	for _, want := range []string{"day", "at", "build", "turns"} {
		if _, ok := fields[want]; !ok {
			return nil, schemaread.Report{Malformed: true}
		}
	}
	out := map[string]any{
		"day": fields["day"], "at": fields["at"], "build": fields["build"],
	}
	if fields["turns"] != Dash {
		n, err := strconv.ParseInt(fields["turns"], 10, 64)
		if err != nil || n < 0 {
			return nil, schemaread.Report{Malformed: true}
		}
		out["turns"] = n
	}
	if s := fields["sources"]; s != "" {
		labelsSrc := []map[string]string{}
		for _, l := range strings.Split(s, ",") {
			labelsSrc = append(labelsSrc, map[string]string{"text": l})
		}
		out["sources"] = labelsSrc
	}
	if len(lines) < 2 || len(lines) == 2 {
		return nil, schemaread.Report{Malformed: true}
	}
	width := len(Columns)
	if lines[1] == LegacyHeaderLine {
		width = len(LegacyColumns)
	} else if lines[1] != HeaderLine {
		return nil, schemaread.Report{Malformed: true}
	}
	rev := map[string]Type{}
	for t := Type(0); t < NTypes; t++ {
		rev[TypeNames[t]] = t
	}
	var rows []any
	for i := 2; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			return nil, schemaread.Report{Malformed: true}
		}
		cells := strings.Split(line, "\t")
		if len(cells) != width {
			return nil, schemaread.Report{Malformed: true}
		}
		row := map[string]any{"date": cells[0], "model": cells[1], "repo": cells[2]}
		for t := Type(0); t < NTypes; t++ {
			cell := cells[3+int(t)]
			if cell == "" || cell == Dash {
				continue
			}
			v, err := strconv.ParseInt(cell, 10, 64)
			if err != nil || v < 0 {
				return nil, schemaread.Report{Malformed: true}
			}
			row[TypeNames[t]] = v
		}
		rough, err := strconv.Atoi(cells[8])
		if err != nil || rough < 0 {
			return nil, schemaread.Report{Malformed: true}
		}
		row["rough"] = rough
		if cells[10] == "" || cells[9] == "" {
			return nil, schemaread.Report{Malformed: true}
		}
		row["basis"] = cells[9]
		src := []map[string]string{}
		for _, l := range strings.Split(cells[10], ",") {
			src = append(src, map[string]string{"text": l})
		}
		row["sources"] = src
		rows = append(rows, row)
	}
	out["rows"] = rows
	return out, schemaread.Report{}
}

// DayFileReader reads a day file in every form and counts the reads per form.
type DayFileReader struct {
	r *schemaread.Reader[srDoc[wire.DayFile]]
}

// NewDayFileReader is a day-file reader with empty counters.
func NewDayFileReader() *DayFileReader {
	c := dayTable.codec()
	c.Legacy = dayfileLegacy
	c.LoadLegacy = loadDayfileLegacy
	return &DayFileReader{r: schemaread.NewReader(c)}
}

// Counts are the day-file reader's per-form counters and refusals.
func (r *DayFileReader) Counts() *schemaread.Counts { return r.r.Counts() }

// ReadDayFile reads one day file in any form into a DayFile, or returns the
// named refusal.
func (r *DayFileReader) ReadDayFile(in []byte) (DayFile, error) {
	d, _, err := r.r.Read(in)
	if err != nil {
		return DayFile{}, err
	}
	v := d.v
	var out DayFile
	out.Day = fixedText(v.Day[:], v.DayLength)
	if p := strp(&v.At); p != nil {
		out.At = *p
	}
	if p := strp(&v.Build); p != nil {
		out.Build = *p
	}
	if v.TurnsPresent {
		out.Turns = strconv.FormatInt(v.Turns, 10)
	} else {
		out.Turns = Dash
	}
	out.Sources = labels(&v.Sources)
	for i := int32(0); i < v.Rows.Count; i++ {
		row := v.Rows.At(i)
		r := DayRow{Date: fixedText(row.Date[:], row.DateLength), Model: str(&row.Model),
			Repo: str(&row.Repo), Rough: int(row.Rough), Basis: str(&row.Basis), Sources: labels(&row.Sources)}
		typs := []struct {
			t Type
			v int64
			p bool
		}{
			{Input, row.Input, row.InputPresent}, {Output, row.Output, row.OutputPresent},
			{CacheWrite, row.CacheWrite, row.CacheWritePresent}, {CacheRead, row.CacheRead, row.CacheReadPresent},
			{Reasoning, row.Reasoning, row.ReasoningPresent},
		}
		for _, tm := range typs {
			if tm.p {
				r.Counts.Set(tm.t, tm.v)
			}
		}
		out.Rows = append(out.Rows, r)
	}
	return out, nil
}

// BusRosterReader reads the bus roster in every form and counts the reads per
// form.
type BusRosterReader struct {
	r *schemaread.Reader[srDoc[wire.BusRoster]]
}

// NewBusRosterReader is a bus-roster reader with empty counters.
func NewBusRosterReader() *BusRosterReader {
	c := rosterTable.codec()
	c.Legacy = rosterLegacy
	c.LoadLegacy = func(v *srDoc[wire.BusRoster], in []byte) schemaread.Report {
		return c.LoadJSON(v, in)
	}
	return &BusRosterReader{r: schemaread.NewReader(c)}
}

// Counts are the bus-roster reader's per-form counters and refusals.
func (r *BusRosterReader) Counts() *schemaread.Counts { return r.r.Counts() }

// ReadRoster reads the roster in any form, or returns the named refusal.
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

// fmt keeps the import with the build.
var _ = fmt.Sprintf
