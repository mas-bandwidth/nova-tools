package config

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// The record readers: every record nova-config reads back from a store (a
// history row's before and after, the file twin, a loop's Redis view, a
// machine's member beat and its bench hash, a canonical argv) is read through
// pkg/schemaread in each form it may arrive in: the generated table's binary
// image (the table wire, or a fixed batch), the padded standard base64 of that
// image, and the legacy form its writer writes today (the owner's rule S2: a
// reader accepts each form, refuses damage by name and counts reads per
// form). A damaged or out-of-bound value is refused as `<RECORD> REFUSED:
// <reason>; run: <help>`, and every read is counted under its record and its
// form: the cutover's evidence (S4, readers first, UNION.md C13). Every writer
// still writes the legacy form.
//
// This package imports no generated table: pkg/configwire's golden test
// imports this package, so a pkg/config that imported pkg/configwire would
// close an import cycle. The typed half is handed in through
// InstallRecordWire by pkg/config/rowsread, as hook/seatstamp hands the hook
// its stamp reader; until it is installed a binary or base64 image is refused
// as unknown_form, by name, and never read as absent.

// WireField is the one field of a Redis hash record (a loop's view, a bench
// hash) that carries its typed image: a hash that holds only this field holds
// the binary image or its base64, any other hash is the legacy form.
const WireField = "wire"

// The bounds of schemas/config/config.schema that pkg/config names nowhere
// else: the reader refuses past each one (clamped), as the generated load does.
const (
	maxRowFields   = 24              // RowFields.pairs: the widest kind has 23 fields
	maxFieldName   = 32              // FieldPair.name
	maxStampBytes  = 32              // the RFC 3339 stamps of a row and a change
	maxUnixMilli   = 253402300799999 // MemberBeat.at_ms and BenchBeat.at_ms: the end of year 9999
	maxBeatCores   = 100000          // MemberBeat.cores
	reasonTrailing = "trailing_data" // a legacy value followed by more than its one JSON value
)

// tableForms are the form bytes of the typed images: 1, the table wire, and 3,
// the fixed form (pkg/configwire TableFixedForm; pkg/config/rowsread pins
// both).
var tableForms = []byte{1, 3}

// FileState is the file twin's one record (file.go), read in every form.
type FileState = fileState

// MemberBeat is the part of the nova-sprint member's beat record
// (MemberBeatKey) the inventory reads: the beat time as Unix milliseconds (0
// when the record has none) and the logical cores. Every other field is the
// sprint's.
type MemberBeat struct {
	AtMS  int64
	Cores int
}

// RecordWire is the typed half of the record readers: one load per record from
// its binary image, the base64 already decoded. A nil load refuses its typed
// forms as unknown_form.
type RecordWire struct {
	Side       func(wire []byte) (map[string]string, schemaread.Report)
	File       func(wire []byte) (FileState, schemaread.Report)
	Loop       func(wire []byte) (View, schemaread.Report)
	MemberBeat func(wire []byte) (MemberBeat, schemaread.Report)
	BenchBeat  func(wire []byte) (View, schemaread.Report)
}

var recordWire atomic.Pointer[RecordWire]

// InstallRecordWire hands the record readers their typed loads; every reader,
// made before or after, reads with them.
func InstallRecordWire(w RecordWire) { recordWire.Store(&w) }

// Records is one set of record readers, each counting its own reads per form
// and its refusals by reason. The store reads go through the process's one
// set; a test makes its own to count.
type Records struct {
	Side   *schemaread.Reader[map[string]string] // CHANGE: a history row's before or after
	File   *schemaread.Reader[FileState]         // FILE: the file twin
	Loop   *schemaread.Reader[View]              // LOOP: a loop's Redis view
	Member *schemaread.Reader[MemberBeat]        // MEMBER_BEAT: sprint:beat:<machine>
	Bench  *schemaread.Reader[View]              // BENCH_BEAT: bench:<machine>:beat
	Argv   *schemaread.Reader[[]string]          // ARGV: a canonical argv value
}

// records is the process's one set. It is made in init, not in its
// declaration: Kinds (kind.go) reads argv through it, and fileConstraints
// reads the kind words from Kinds.
var records *Records

func init() { records = NewRecords() }

// NewRecords is a set of record readers with empty counters.
func NewRecords() *Records {
	return &Records{
		Side:   schemaread.NewReader(recordCodec("CHANGE", "nova-config machine history -h", func(w *RecordWire) func([]byte) (map[string]string, schemaread.Report) { return w.Side }, loadObject[map[string]string], sideConstraints)),
		File:   schemaread.NewReader(recordCodec("FILE", "nova-config migrate -h", func(w *RecordWire) func([]byte) (FileState, schemaread.Report) { return w.File }, loadFileLegacy, fileConstraints)),
		Loop:   schemaread.NewReader(recordCodec("LOOP", "nova-config loop show -h", func(w *RecordWire) func([]byte) (View, schemaread.Report) { return w.Loop }, loadObject[View], loopConstraints)),
		Member: schemaread.NewReader(recordCodec("MEMBER_BEAT", "nova-config inventory -h", func(w *RecordWire) func([]byte) (MemberBeat, schemaread.Report) { return w.MemberBeat }, loadMemberLegacy, memberConstraints)),
		Bench:  schemaread.NewReader(recordCodec("BENCH_BEAT", "nova-config inventory -h", func(w *RecordWire) func([]byte) (View, schemaread.Report) { return w.BenchBeat }, loadBenchLegacy, benchConstraints)),
		Argv:   schemaread.NewReader(argvCodec()),
	}
}

// recordCodec is one record's codec: the typed load handed in, the legacy
// load (a JSON object, the form every writer writes today), and the bounds of
// the record's table, run on every form.
func recordCodec[T any](name, help string, typed func(*RecordWire) func([]byte) (T, schemaread.Report), legacy func(*T, []byte) schemaread.Report, constraints func(*T, *schemaread.Checker)) *schemaread.Codec[T] {
	return &schemaread.Codec[T]{
		Name:  name,
		Help:  help,
		Forms: tableForms,
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			w := recordWire.Load()
			if w == nil || typed(w) == nil {
				return schemaread.Report{Reason: schemaread.ReasonUnknownForm}
			}
			out, rep := typed(w)(wire)
			*v = out
			return rep
		},
		Legacy:      func(in []byte) bool { return in[0] == '{' },
		LoadLegacy:  legacy,
		Constraints: constraints,
	}
}

// argvCodec reads a canonical argv (kind.go canonicalArgv): its legacy form is
// the compact JSON list, and no table carries it alone (RowFields and
// InventoryLoop carry it as this text), so a typed image is unknown_form.
func argvCodec() *schemaread.Codec[[]string] {
	return &schemaread.Codec[[]string]{
		Name:  "ARGV",
		Help:  "nova-config loop show -h",
		Forms: tableForms,
		LoadWire: func(*[]string, []byte) schemaread.Report {
			return schemaread.Report{Reason: schemaread.ReasonUnknownForm}
		},
		Legacy: func(in []byte) bool { return in[0] == '[' },
		LoadLegacy: func(v *[]string, in []byte) schemaread.Report {
			return decodeOne(v, in, false)
		},
		Constraints: func(v *[]string, c *schemaread.Checker) {
			c.Bound("argv", int64(len(*v)), 0, MaxArgs)
			for _, w := range *v {
				c.Text("argv", w, MaxArgvBytes)
			}
		},
	}
}

// decodeOne reads exactly one JSON value into v: a value followed by more is
// trailing_data, an unreadable one malformed.
func decodeOne[T any](v *T, in []byte, strict bool) schemaread.Report {
	d := json.NewDecoder(bytes.NewReader(in))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(v); err != nil {
		return schemaread.Report{Malformed: true}
	}
	if _, err := d.Token(); err != io.EOF {
		return schemaread.Report{Reason: reasonTrailing}
	}
	return schemaread.Report{}
}

// loadObject reads the legacy JSON object of text values: a history side's
// JSON (pg.go record) or a Redis hash's fields.
func loadObject[T ~map[string]string](v *T, in []byte) schemaread.Report {
	return decodeOne(v, in, false)
}

// loadFileLegacy reads the file twin's JSON as file.go always has: one
// object, no key it does not know.
func loadFileLegacy(v *FileState, in []byte) schemaread.Report {
	return decodeOne(v, in, true)
}

// loadMemberLegacy reads the member's JSON: every key but at and cores is the
// sprint's and is passed over; at is an RFC 3339 time and cores a whole
// number, or the record is malformed.
func loadMemberLegacy(v *MemberBeat, in []byte) schemaread.Report {
	var m map[string]json.RawMessage
	if rep := decodeOne(&m, in, false); rep != (schemaread.Report{}) {
		return rep
	}
	if raw, ok := m["at"]; ok {
		var at time.Time
		if json.Unmarshal(raw, &at) != nil {
			return schemaread.Report{Malformed: true}
		}
		if !at.IsZero() {
			v.AtMS = at.UnixMilli()
		}
	}
	if raw, ok := m["cores"]; ok {
		if json.Unmarshal(raw, &v.Cores) != nil {
			return schemaread.Report{Malformed: true}
		}
	}
	return schemaread.Report{}
}

// loadBenchLegacy reads the bench hash's fields; at, when the hash has it, is
// Unix milliseconds as decimal text, or the record is malformed.
func loadBenchLegacy(v *View, in []byte) schemaread.Report {
	if rep := loadObject(v, in); rep != (schemaread.Report{}) {
		return rep
	}
	if at, ok := (*v)["at"]; ok {
		if _, err := strconv.ParseInt(at, 10, 64); err != nil {
			return schemaread.Report{Malformed: true}
		}
	}
	return schemaread.Report{}
}

// checkFields holds a row's fields to RowFields and FieldPair (the schema's
// MaxRowFields, MaxFieldName, MaxFieldValue and MaxArgvBytes). A history
// side carries the write's reason under historyReasonKey (pg.go), bounded by
// MaxReason and not counted as a field.
func checkFields(fields map[string]string, c *schemaread.Checker, side bool) {
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	n := len(names)
	for _, k := range names {
		switch {
		case side && k == historyReasonKey:
			n--
			c.Text(k, fields[k], MaxReason)
		case k == "argv":
			c.Text(k, fields[k], MaxArgvBytes)
		default:
			c.Text("field name", k, maxFieldName)
			c.Text(k, fields[k], MaxFieldValue)
		}
	}
	c.Bound("fields", int64(n), 0, maxRowFields)
}

func sideConstraints(v *map[string]string, c *schemaread.Checker) { checkFields(*v, c, true) }

// fileConstraints holds the file twin to ConfigRow and ConfigChange: each
// row's name, fields and stamps, and each change's canonical kind and op words
// (the ConfigKind and ChangeOp enums), sides, actor, reason and time.
func fileConstraints(v *FileState, c *schemaread.Checker) {
	ops := []string{OpAdd, OpSet, OpRemove}
	kinds := KindNames()
	for _, rows := range v.Rows {
		for name, r := range rows {
			c.Text("rows", name, MaxName)
			c.Text("rows.name", r.Name, MaxName)
			c.Text("rows.created_at", r.CreatedAt, maxStampBytes)
			c.Text("rows.updated_at", r.UpdatedAt, maxStampBytes)
			checkFields(r.Fields, c, false)
		}
	}
	for _, h := range v.History {
		c.Enum("history.kind", indexOf(kinds, h.Kind), len(kinds))
		c.Enum("history.op", indexOf(ops, h.Op), len(ops))
		c.Text("history.name", h.Name, MaxName)
		c.Text("history.actor", h.Actor, MaxActor)
		c.Text("history.reason", h.Reason, MaxReason)
		c.Text("history.at", h.At, maxStampBytes)
		checkFields(h.Before, c, false)
		checkFields(h.After, c, false)
	}
}

func indexOf(words []string, w string) int {
	for i, x := range words {
		if x == w {
			return i
		}
	}
	return -1
}

// loopConstraints holds a loop's view to InventoryLoop's text bounds; its
// keys, every and the flags are parseLoop's (inventory.go), which refuses each
// by name.
func loopConstraints(v *View, c *schemaread.Checker) {
	for _, f := range []struct {
		name string
		max  int
	}{{"name", MaxName}, {"machine", MaxName}, {"argv", MaxArgvBytes}, {"seat", MaxFieldValue}, {"log", MaxFieldValue}} {
		c.Text(f.name, (*v)[f.name], f.max)
	}
}

func memberConstraints(v *MemberBeat, c *schemaread.Checker) {
	c.Bound("at", v.AtMS, 0, maxUnixMilli)
	c.Bound("cores", int64(v.Cores), 0, maxBeatCores)
}

// benchConstraints holds the bench hash to BenchBeat's bounds.
func benchConstraints(v *View, c *schemaread.Checker) {
	for _, f := range []struct {
		name string
		max  int
	}{{"host", MaxName}, {"load1", 16}, {"ncpu", 8}, {"cpu", 16}, {"os", 32}, {"arch", 32}, {"memory_gb", 16}} {
		c.Text(f.name, (*v)[f.name], f.max)
	}
	if at, ok := (*v)["at"]; ok {
		ms, err := strconv.ParseInt(at, 10, 64)
		if err == nil {
			c.Bound("at", ms, 0, maxUnixMilli)
		}
	}
}

// hashInput is what a Redis hash record is read from: the typed image its one
// WireField holds, or the legacy fields as one JSON object.
func hashInput(h map[string]string) []byte {
	if w, ok := h[WireField]; ok && len(h) == 1 {
		return []byte(w)
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return nil
	}
	return raw
}

// ReadSide reads a history row's before or after (pg.go History): the fields
// and, on the side record put it on, the write's reason.
func (r *Records) ReadSide(raw []byte) (map[string]string, error) {
	v, _, err := r.Side.Read(raw)
	return v, err
}

// ReadFile reads the file twin (file.go OpenFile).
func (r *Records) ReadFile(raw []byte) (FileState, error) {
	v, _, err := r.File.Read(raw)
	return v, err
}

// ReadLoop reads a loop's Redis view (inventory.go hostLoops) into the view
// parseLoop reads.
func (r *Records) ReadLoop(v View) (View, error) {
	out, _, err := r.Loop.Read(hashInput(v))
	return out, err
}

// ReadMemberBeat reads a machine's member beat record (redis.go mergeBeat).
func (r *Records) ReadMemberBeat(raw string) (MemberBeat, error) {
	v, _, err := r.Member.Read([]byte(raw))
	return v, err
}

// ReadBenchBeat reads a machine's bench hash (redis.go mergeBeat) into its
// fields.
func (r *Records) ReadBenchBeat(h map[string]string) (View, error) {
	v, _, err := r.Bench.Read(hashInput(h))
	return v, err
}

// ReadArgv reads a canonical argv value (kind.go Argv).
func (r *Records) ReadArgv(canonical string) ([]string, error) {
	v, _, err := r.Argv.Read([]byte(canonical))
	return v, err
}
