package tablemodel

// The table model's records read in both forms (the owner's rules S2 and S4,
// readers first): each record reads alike from its binary image (the table
// wire of pkg/tablemodelwire), the padded standard base64 of that image, its
// schema JSON text, and the legacy JSON its writer writes today. A damaged
// value of each form is refused by name (`<RECORD> REFUSED: <reason>; run:
// <help>`) and never read as absent, empty or zero; a clamped value is
// refused; each read is counted under its record (one reader per record) and
// its form.
//
// The records: the State (state.go) and the Trace with its steps (replay.go,
// trace.json), the Action and the committed receipt Event of the capture
// (capture.go; the Action is what the harness turns into a model step,
// harness.go ActionTLA), the witness Finding (witnesses.go) and the
// ReplayEvent (replay.go). The strict read is pkg/schemaread's. Writers stay
// legacy here; the writer switch is a later card.
//
// This package imports no generated table: pkg/tablemodelwire's golden test
// imports this package, so a tablemodel that imported pkg/tablemodelwire would
// close an import cycle. The typed half (the table image and the schema JSON,
// both as the canonical JSON this file converts) is handed in through
// InstallRecordWire by internal/tablemodel/recordsread, as pkg/config/rowsread
// hands pkg/config its loads; until it is installed a binary, base64 or schema
// JSON value is refused as unknown_form, by name, and never read as absent.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync/atomic"

	"github.com/mas-bandwidth/nova-tools/pkg/readregular"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// modelHelp is what a refusal points at: the verbs that run the table model.
const modelHelp = "tlacheck replay -h"

// RecordTable is the typed half of one record's reader: the generated table's
// functions over the record's canonical schema JSON (the JSON the table's
// writer writes, which this package reads into and writes from the Go value).
type RecordTable struct {
	Measure  func(wire []byte) error                       // the generated LoadMeasureReason: an error's text is the refusal reason
	Load     func(wire []byte) ([]byte, schemaread.Report) // the image as canonical JSON; nil JSON with the report on a refusal
	LoadJSON func(text []byte) ([]byte, schemaread.Report) // a schema JSON text as canonical JSON, the same way
	Save     func(canon []byte) ([]byte, error)            // the image of a canonical JSON; an error when the table cannot hold it as it is
}

// RecordWire is the typed half of every record reader, one table per record.
// A table with no Load refuses its typed forms as unknown_form.
type RecordWire struct {
	State, Trace, Action, Event, Finding, ReplayEvent RecordTable
}

var recordWire atomic.Pointer[RecordWire]

// InstallRecordWire hands the record readers their typed half; every reader,
// existing or new, reads the typed forms from then on.
func InstallRecordWire(w RecordWire) { recordWire.Store(&w) }

type obj = map[string]any

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var malformedReport = schemaread.Report{Malformed: true}

// recordSpec is one record: its refusal name, its table in the typed half, its
// canonical schema JSON both ways, the test that tells its legacy JSON apart
// from the schema JSON (both open with `{`), and the fields its writer always
// writes, which a read of any form refuses as absent_required when missing.
type recordSpec[V any] struct {
	name        string
	table       func(*RecordWire) *RecordTable
	canon       func(V) obj
	fromCanon   func(*fields, obj) V
	legacy      func(map[string]json.RawMessage) bool
	constraints func(V, *schemaread.Checker)
}

// record is the value a record's codec loads.
type record[V any] struct{ v V }

// typed is the record's table in the installed typed half, nil when none is.
func (s recordSpec[V]) typed() *RecordTable {
	w := recordWire.Load()
	if w == nil {
		return nil
	}
	t := s.table(w)
	if t.Load == nil || t.LoadJSON == nil || t.Save == nil {
		return nil
	}
	return t
}

// empty is the record a table image with no known field reads as.
func (s recordSpec[V]) empty() V { return s.fromCanon(&fields{what: s.name}, obj{}) }

// value is the Go value of a canonical JSON.
func (s recordSpec[V]) value(canon []byte) (V, error) {
	var zero V
	o, err := decodeCanon(canon)
	if err != nil {
		return zero, err
	}
	f := &fields{what: s.name}
	v := s.fromCanon(f, o)
	if f.err != nil {
		return zero, f.err
	}
	return v, nil
}

// read loads one typed form through load and reads it as the Go value.
func (s recordSpec[V]) read(load func(*RecordTable) ([]byte, schemaread.Report)) (V, schemaread.Report) {
	var zero V
	t := s.typed()
	if t == nil {
		return zero, schemaread.Report{Reason: schemaread.ReasonUnknownForm}
	}
	canon, rep := load(t)
	if canon == nil {
		if !rep.Malformed && rep.Reason == "" {
			rep.Malformed = true
		}
		return zero, rep
	}
	v, err := s.value(canon)
	if err != nil {
		return zero, malformedReport
	}
	// An image whose every field is unknown is not this record (a damaged
	// field id, or another record's text): it is refused, never read as the
	// empty record. Unknown fields beside known ones stay tolerated (S4: a
	// newer writer's fields).
	if rep.Unknown > 0 && reflect.DeepEqual(v, s.empty()) {
		return zero, malformedReport
	}
	return v, rep
}

// wire is v's binary image.
func (s recordSpec[V]) wire(v V) ([]byte, error) {
	t := s.typed()
	if t == nil {
		return nil, fmt.Errorf("%s: no typed table is installed", s.name)
	}
	canon, err := json.Marshal(s.canon(v))
	if err != nil {
		return nil, err
	}
	return t.Save(canon)
}

// isLegacy is true for the JSON object the record's writer writes today.
func (s recordSpec[V]) isLegacy(in []byte) bool {
	t := bytes.TrimSpace(in)
	if len(t) == 0 || t[0] != '{' {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(t, &m) != nil {
		return false
	}
	return s.legacy(m)
}

// codec is the record's strict read (pkg/schemaread).
func (s recordSpec[V]) codec() *schemaread.Codec[record[V]] {
	return &schemaread.Codec[record[V]]{
		Name: s.name,
		Help: modelHelp,
		Reset: func(r *record[V]) {
			var zero V
			r.v = zero
		},
		Measure: func(wire []byte) error {
			if t := s.typed(); t != nil && t.Measure != nil {
				return t.Measure(wire)
			}
			return nil
		},
		LoadWire: func(r *record[V], wire []byte) schemaread.Report {
			v, rep := s.read(func(t *RecordTable) ([]byte, schemaread.Report) { return t.Load(wire) })
			r.v = v
			return rep
		},
		LoadJSON: func(r *record[V], text []byte) schemaread.Report {
			v, rep := s.read(func(t *RecordTable) ([]byte, schemaread.Report) { return t.LoadJSON(text) })
			r.v = v
			return rep
		},
		Legacy: s.isLegacy,
		LoadLegacy: func(r *record[V], in []byte) schemaread.Report {
			if err := json.Unmarshal(in, &r.v); err != nil {
				return malformedReport
			}
			return schemaread.Report{}
		},
		Constraints: func(r *record[V], c *schemaread.Checker) {
			if s.constraints != nil {
				s.constraints(r.v, c)
			}
		},
		SaveWire: func(r *record[V]) []byte {
			wire, err := s.wire(r.v)
			if err != nil {
				return nil
			}
			return wire
		},
		SaveJSON: func(r *record[V]) []byte {
			raw, err := json.Marshal(s.canon(r.v))
			if err != nil {
				return nil
			}
			return raw
		},
	}
}

// decodeCanon decodes a canonical JSON object, its integers kept exact.
func decodeCanon(b []byte) (obj, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var o obj
	if err := dec.Decode(&o); err != nil {
		return nil, err
	}
	if o == nil {
		return nil, errors.New("the JSON is not an object")
	}
	return o, nil
}

// fields reads the canonical JSON a table writes. An absent field reads as
// zero (a variable table's rule, tablemodel.schema); a field of the wrong
// shape, or a map key given twice, is an error, and the first one wins.
type fields struct {
	what string
	err  error
}

func (f *fields) fail(format string, a ...any) {
	if f.err == nil {
		f.err = fmt.Errorf("%s: %s", f.what, fmt.Sprintf(format, a...))
	}
}

func (f *fields) text(o obj, k string) string {
	v, ok := o[k]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		f.fail("%s is not text", k)
	}
	return s
}

func (f *fields) integer(o obj, k string) int {
	v, ok := o[k]
	if !ok || v == nil {
		return 0
	}
	n, ok := v.(json.Number)
	if !ok {
		f.fail("%s is not an integer", k)
		return 0
	}
	i, err := n.Int64()
	if err != nil {
		f.fail("%s is not an integer", k)
		return 0
	}
	return int(i)
}

// boolean is the field's value and whether it is present.
func (f *fields) boolean(o obj, k string) (bool, bool) {
	v, ok := o[k]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	if !ok {
		f.fail("%s is not a boolean", k)
	}
	return b, true
}

func (f *fields) object(o obj, k string) obj {
	v, ok := o[k]
	if !ok || v == nil {
		return obj{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		f.fail("%s is not an object", k)
		return obj{}
	}
	return m
}

func (f *fields) list(o obj, k string) []obj {
	v, ok := o[k]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		f.fail("%s is not a list", k)
		return nil
	}
	out := make([]obj, 0, len(arr))
	for _, x := range arr {
		m, ok := x.(map[string]any)
		if !ok {
			f.fail("an element of %s is not an object", k)
			return nil
		}
		out = append(out, m)
	}
	return out
}

// texts is a list of single-text tables (ModelArg, ModelDetail), never nil:
// the writers write an empty list, not null.
func (f *fields) texts(o obj, k string) []string {
	out := []string{}
	for _, e := range f.list(o, k) {
		out = append(out, f.text(e, "text"))
	}
	return out
}

func textsCanon(items []string) []any {
	out := []any{}
	for _, a := range items {
		out = append(out, obj{"text": a})
	}
	return out
}

// The State record (state.go, table ModelState).

func physCanon(p Phys) obj { return obj{"table_name": p.Table, "epoch": p.Epoch} }
func cellCanon(c Cell) obj { return obj{"p": physCanon(c.P), "row": c.Row, "col": c.Col} }

func (f *fields) phys(o obj) Phys { return Phys{f.text(o, "table_name"), f.integer(o, "epoch")} }
func (f *fields) cell(o obj) Cell {
	return Cell{f.phys(f.object(o, "p")), f.text(o, "row"), f.text(o, "col")}
}

// stateCanon is a State's canonical schema JSON. Seen is a map: its entries
// go in the writer name's sorted order (tablemodel.schema ModelSeen).
func stateCanon(s State) obj {
	live, rows, binds, data, place, seen := []any{}, []any{}, []any{}, []any{}, []any{}, []any{}
	for _, p := range s.Live {
		live = append(live, physCanon(p))
	}
	for _, r := range s.Rows {
		rows = append(rows, obj{"p": physCanon(r.P), "row": r.Row})
	}
	for _, b := range s.Binds {
		binds = append(binds, obj{"cell": cellCanon(b.Cell), "target": cellCanon(b.Target)})
	}
	for _, d := range s.Data {
		data = append(data, obj{"cell": cellCanon(d.Cell), "member": d.Member, "score": d.Score})
	}
	for _, p := range s.Place {
		locs := []any{}
		for _, l := range p.Locations {
			locs = append(locs, obj{"p": physCanon(l.P), "cell": cellCanon(l.Cell)})
		}
		place = append(place, obj{"member": p.Member, "locations": locs})
	}
	for _, k := range sortedKeys(s.Seen) {
		seen = append(seen, obj{"writer": k, "epoch": s.Seen[k]})
	}
	return obj{"live": live, "rows": rows, "binds": binds, "data": data, "place": place, "active": s.Active, "seen": seen}
}

// state reads a State as the capture writes it (capture.go snapshot): every
// list and the seen map present, empty rather than absent, since the table
// form has no null list to tell the two apart.
func (f *fields) state(o obj) State {
	s := State{Live: []Phys{}, Rows: []RowAt{}, Binds: []Bind{}, Data: []Datum{}, Place: []Placement{}, Seen: map[string]int{}}
	for _, p := range f.list(o, "live") {
		s.Live = append(s.Live, f.phys(p))
	}
	for _, r := range f.list(o, "rows") {
		s.Rows = append(s.Rows, RowAt{f.phys(f.object(r, "p")), f.text(r, "row")})
	}
	for _, b := range f.list(o, "binds") {
		s.Binds = append(s.Binds, Bind{f.cell(f.object(b, "cell")), f.cell(f.object(b, "target"))})
	}
	for _, d := range f.list(o, "data") {
		s.Data = append(s.Data, Datum{f.cell(f.object(d, "cell")), f.text(d, "member"), f.integer(d, "score")})
	}
	for _, p := range f.list(o, "place") {
		pl := Placement{Member: f.text(p, "member"), Locations: []Location{}}
		for _, l := range f.list(p, "locations") {
			pl.Locations = append(pl.Locations, Location{f.phys(f.object(l, "p")), f.cell(f.object(l, "cell"))})
		}
		s.Place = append(s.Place, pl)
	}
	s.Active = f.integer(o, "active")
	for _, e := range f.list(o, "seen") {
		w := f.text(e, "writer")
		if _, dup := s.Seen[w]; dup {
			f.fail("seen names the writer %q twice", w)
		}
		s.Seen[w] = f.integer(e, "epoch")
	}
	return s
}

// stateSpec is the State record. Its legacy JSON writes seen as an object (a
// Go map), the schema JSON as a list.
var stateSpec = recordSpec[State]{
	name:      "STATE",
	table:     func(w *RecordWire) *RecordTable { return &w.State },
	canon:     stateCanon,
	fromCanon: func(f *fields, o obj) State { return f.state(o) },
	legacy: func(m map[string]json.RawMessage) bool {
		seen, ok := m["seen"]
		return ok && !bytes.HasPrefix(bytes.TrimSpace(seen), []byte("["))
	},
}

// The Trace record and its steps (replay.go, tables ModelTrace and
// ModelTraceStep).

func eventCanon(e Event) obj {
	fs := []any{}
	for _, k := range sortedKeys(e) {
		fs = append(fs, obj{"key": k, "value": e[k]})
	}
	return obj{"fields": fs}
}

func (f *fields) event(o obj) Event {
	e := Event{}
	for _, x := range f.list(o, "fields") {
		k := f.text(x, "key")
		if _, dup := e[k]; dup {
			f.fail("the receipt names the field %q twice", k)
		}
		e[k] = f.text(x, "value")
	}
	return e
}

// traceStepCanon is a step's canonical JSON. refused is absent for the two
// model-only steps (a nil pointer); hasReceipt says whether the step carries a
// receipt (tablemodel.schema ModelTraceStep).
func traceStepCanon(s TraceStep) obj {
	o := obj{"verb": s.Verb, "args": textsCanon(s.Args), "actor": s.Actor, "hasReceipt": s.Receipt != nil, "receipt": eventCanon(s.Receipt), "model": s.Model, "state": stateCanon(s.State)}
	if s.Refused != nil {
		o["refused"] = *s.Refused
	}
	return o
}

func (f *fields) traceStep(o obj) TraceStep {
	s := TraceStep{Verb: f.text(o, "verb"), Args: f.texts(o, "args"), Actor: f.text(o, "actor"), Model: f.text(o, "model"), State: f.state(f.object(o, "state"))}
	if b, ok := f.boolean(o, "refused"); ok {
		s.Refused = &b
	}
	if has, _ := f.boolean(o, "hasReceipt"); has {
		s.Receipt = f.event(f.object(o, "receipt"))
	}
	return s
}

func traceCanon(tr Trace) obj {
	hashes, steps := []any{}, []any{}
	for _, k := range sortedKeys(tr.ModelSHA256) {
		hashes = append(hashes, obj{"module": k, "sha256": tr.ModelSHA256[k]})
	}
	for _, s := range tr.Steps {
		steps = append(steps, traceStepCanon(s))
	}
	return obj{"sourceSha256": tr.SourceSHA256, "modelSha256": hashes, "initial": stateCanon(tr.Initial), "steps": steps}
}

func (f *fields) trace(o obj) Trace {
	tr := Trace{SourceSHA256: f.text(o, "sourceSha256"), ModelSHA256: map[string]string{}, Steps: []TraceStep{}}
	for _, h := range f.list(o, "modelSha256") {
		k := f.text(h, "module")
		if _, dup := tr.ModelSHA256[k]; dup {
			f.fail("the model hashes name %q twice", k)
		}
		tr.ModelSHA256[k] = f.text(h, "sha256")
	}
	tr.Initial = f.state(f.object(o, "initial"))
	for _, s := range f.list(o, "steps") {
		tr.Steps = append(tr.Steps, f.traceStep(s))
	}
	return tr
}

// traceSpec is the Trace record, trace.json. Its legacy JSON spells
// source_sha256, the schema JSON sourceSha256. RunReplay always writes the
// source hash, and every step's verb, actor and model.
var traceSpec = recordSpec[Trace]{
	name:      "TRACE",
	table:     func(w *RecordWire) *RecordTable { return &w.Trace },
	canon:     traceCanon,
	fromCanon: func(f *fields, o obj) Trace { return f.trace(o) },
	legacy: func(m map[string]json.RawMessage) bool {
		_, ok := m["source_sha256"]
		return ok
	},
	constraints: func(tr Trace, c *schemaread.Checker) {
		c.Required("sourceSha256", tr.SourceSHA256 != "")
		for _, s := range tr.Steps {
			c.Required("steps.verb", s.Verb != "")
			c.Required("steps.actor", s.Actor != "")
			c.Required("steps.model", s.Model != "")
			if s.Receipt != nil {
				c.Required("steps.receipt.id", s.Receipt["id"] != "")
			}
		}
	},
}

// The capture's Action and its committed receipt Event (capture.go, tables
// ModelAction and ModelEvent). The Action is also the harness's input
// (harness.go ActionTLA).

// actionSpec is the Action record. Its legacy JSON is the Go struct's, whose
// fields carry no tags (Verb, Args, Actor); the schema JSON spells verb.
var actionSpec = recordSpec[Action]{
	name:  "ACTION",
	table: func(w *RecordWire) *RecordTable { return &w.Action },
	canon: func(a Action) obj { return obj{"verb": a.Verb, "args": textsCanon(a.Args), "actor": a.Actor} },
	fromCanon: func(f *fields, o obj) Action {
		return Action{Verb: f.text(o, "verb"), Args: f.texts(o, "args"), Actor: f.text(o, "actor")}
	},
	legacy: func(m map[string]json.RawMessage) bool {
		_, ok := m["Verb"]
		return ok
	},
	constraints: func(a Action, c *schemaread.Checker) {
		c.Required("verb", a.Verb != "")
		c.Required("actor", a.Actor != "")
	},
}

// eventSpec is the receipt Event record. Its legacy JSON is the map itself,
// every value text; the schema JSON is a list of key and value fields. A
// receipt always carries its stream id (capture.go receipt).
var eventSpec = recordSpec[Event]{
	name:      "EVENT",
	table:     func(w *RecordWire) *RecordTable { return &w.Event },
	canon:     eventCanon,
	fromCanon: func(f *fields, o obj) Event { return f.event(o) },
	legacy: func(m map[string]json.RawMessage) bool {
		for _, v := range m {
			if !bytes.HasPrefix(bytes.TrimSpace(v), []byte(`"`)) {
				return false
			}
		}
		return true
	},
	constraints: func(e Event, c *schemaread.Checker) { c.Required("id", e["id"] != "") },
}

// The witness Finding (witnesses.go, table ModelFinding) and the ReplayEvent
// (replay.go, table ModelReplayEvent).

// findingSpec is the Finding record. Its legacy JSON is the Go struct's (Name,
// Result, Text); the schema JSON spells name.
var findingSpec = recordSpec[Finding]{
	name:  "FINDING",
	table: func(w *RecordWire) *RecordTable { return &w.Finding },
	canon: func(x Finding) obj { return obj{"name": x.Name, "result": x.Result, "text": x.Text} },
	fromCanon: func(f *fields, o obj) Finding {
		return Finding{Name: f.text(o, "name"), Result: f.text(o, "result"), Text: f.text(o, "text")}
	},
	legacy: func(m map[string]json.RawMessage) bool {
		_, ok := m["Name"]
		return ok
	},
	constraints: func(x Finding, c *schemaread.Checker) {
		c.Required("name", x.Name != "")
		c.Required("result", x.Result != "")
	},
}

// replayEventSpec is the ReplayEvent record. Its legacy JSON is the Go
// struct's (Step, Verdict, Fields); the schema JSON spells step.
var replayEventSpec = recordSpec[ReplayEvent]{
	name:  "REPLAY_EVENT",
	table: func(w *RecordWire) *RecordTable { return &w.ReplayEvent },
	canon: func(e ReplayEvent) obj {
		return obj{"step": e.Step, "verdict": e.Verdict, "fields": textsCanon(e.Fields)}
	},
	fromCanon: func(f *fields, o obj) ReplayEvent {
		return ReplayEvent{Step: f.text(o, "step"), Verdict: f.text(o, "verdict"), Fields: f.texts(o, "fields")}
	},
	legacy: func(m map[string]json.RawMessage) bool {
		_, ok := m["Step"]
		return ok
	},
	constraints: func(e ReplayEvent, c *schemaread.Checker) {
		c.Required("step", e.Step != "")
		c.Required("verdict", e.Verdict != "")
	},
}

// traceReader reads trace.json; its counters are the Trace record's reads per
// form (S4: the per-form counter is the cutover's evidence).
var traceReader = schemaread.NewReader(traceSpec.codec())

// ReadTraceFile is trace.json read strictly: its binary image, the padded
// standard base64 of that image and its schema JSON (once the typed half is
// installed) and the legacy JSON the capture writes today all read alike, and
// a damaged file is refused by name (`TRACE REFUSED: <reason>; run: <help>`),
// never read as an empty trace.
func ReadTraceFile(path string) (Trace, error) {
	raw, err := readregular.Read(path, readregular.DefaultMax)
	if err != nil {
		return Trace{}, err
	}
	got, _, err := traceReader.Read(raw)
	if err != nil {
		return Trace{}, err
	}
	return got.v, nil
}
