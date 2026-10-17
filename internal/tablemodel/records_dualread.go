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
// ReplayEvent (replay.go). The strict read lives in pkg/schemaread; the
// generated tables supply the codec. Writers stay legacy here; the writer
// switch is a later card.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/mas-bandwidth/nova-tools/pkg/readregular"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	tw "github.com/mas-bandwidth/nova-tools/pkg/tablemodelwire"
)

// modelHelp is what a refusal points at: the verbs that run the table model.
const modelHelp = "tlacheck replay -h"

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

// tableReport is the generated report reduced to the strict read's.
func tableReport(rep tw.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == tw.TableOpenRefused || rep.Verdict == tw.TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

// wireFns are the generated functions of one table, B its builder and M its
// row.
type wireFns[B, M any] struct {
	init        func(*B, ...tw.TableAllocator) bool
	shutdown    func(*B)
	lock        func(*B) bool
	asConst     func(*B) *M
	load        func(*B, []byte, *tw.TableReport) bool
	fromJSON    func(*B, []byte, *tw.TableReport) bool
	jsonMeasure func(*M, ...tw.TableAllocator) int64
	toJSON      func(*M, []byte, ...tw.TableAllocator) int64
	measure     func(*M, ...tw.TableWriteContext) int64
	save        func(*M, []byte, ...tw.TableWriteContext) int64
	loadMeasure func([]byte) (int64, error)
}

// recordSpec is one record: its refusal name, its table, its canonical schema
// JSON both ways, the key that tells its legacy JSON apart from the schema JSON
// (both open with `{`), and the fields its writer always writes, which a read
// of any form refuses as absent_required when they are missing.
type recordSpec[V, B, M any] struct {
	name        string
	fns         wireFns[B, M]
	canon       func(V) obj
	fromCanon   func(*fields, obj) V
	legacy      func(map[string]json.RawMessage) bool
	constraints func(V, *schemaread.Checker)
}

// empty is the record a table image with no known field reads as.
func (s recordSpec[V, B, M]) empty() V { return s.fromCanon(&fields{what: s.name}, obj{}) }

// record is the value a record's codec loads.
type record[V any] struct{ v V }

// build fills a fresh builder and reads the locked image back as the value,
// through the canonical JSON the table writes.
func (s recordSpec[V, B, M]) build(fill func(*B, *tw.TableReport) bool) (V, schemaread.Report) {
	var zero V
	var rep tw.TableReport
	b := new(B)
	if !s.fns.init(b) {
		return zero, malformedReport
	}
	defer s.fns.shutdown(b)
	if !fill(b, &rep) || !s.fns.lock(b) {
		return zero, tableReport(rep, false)
	}
	v, err := s.valueOf(s.fns.asConst(b))
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
	return v, tableReport(rep, true)
}

func (s recordSpec[V, B, M]) valueOf(m *M) (V, error) {
	var zero V
	n := s.fns.jsonMeasure(m)
	if n < 0 {
		return zero, fmt.Errorf("%s: the image has no JSON", s.name)
	}
	buf := make([]byte, n)
	if s.fns.toJSON(m, buf) != n {
		return zero, fmt.Errorf("%s: the image's JSON is not readable", s.name)
	}
	o, err := decodeCanon(buf)
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

// wire is v's binary image. A value the table cannot hold as it is (a clamp,
// a kind mismatch, an unknown field) builds no image.
func (s recordSpec[V, B, M]) wire(v V) ([]byte, error) {
	canon, err := json.Marshal(s.canon(v))
	if err != nil {
		return nil, err
	}
	b := new(B)
	if !s.fns.init(b) {
		return nil, fmt.Errorf("%s: cannot start the builder", s.name)
	}
	defer s.fns.shutdown(b)
	var rep tw.TableReport
	if !s.fns.fromJSON(b, canon, &rep) || !s.fns.lock(b) || rep.Clamped > 0 || rep.KindMismatch > 0 || rep.Unknown > 0 {
		return nil, fmt.Errorf("%s: a value builds no image", s.name)
	}
	root := s.fns.asConst(b)
	n := s.fns.measure(root)
	if n < 0 {
		return nil, fmt.Errorf("%s: the image has no wire", s.name)
	}
	buf := make([]byte, n)
	if s.fns.save(root, buf) != n {
		return nil, fmt.Errorf("%s: the wire is not writable", s.name)
	}
	return buf, nil
}

// isLegacy is true for the JSON object the record's writer writes today.
func (s recordSpec[V, B, M]) isLegacy(in []byte) bool {
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
func (s recordSpec[V, B, M]) codec() *schemaread.Codec[record[V]] {
	return &schemaread.Codec[record[V]]{
		Name: s.name,
		Help: modelHelp,
		Reset: func(r *record[V]) {
			var zero V
			r.v = zero
		},
		Measure: func(wire []byte) error {
			_, err := s.fns.loadMeasure(wire)
			return err
		},
		LoadWire: func(r *record[V], wire []byte) schemaread.Report {
			v, rep := s.build(func(b *B, tr *tw.TableReport) bool { return s.fns.load(b, wire, tr) })
			r.v = v
			return rep
		},
		LoadJSON: func(r *record[V], text []byte) schemaread.Report {
			v, rep := s.build(func(b *B, tr *tw.TableReport) bool { return s.fns.fromJSON(b, text, tr) })
			r.v = v
			return rep
		},
		Legacy: s.isLegacy,
		Constraints: func(r *record[V], c *schemaread.Checker) {
			if s.constraints != nil {
				s.constraints(r.v, c)
			}
		},
		LoadLegacy: func(r *record[V], in []byte) schemaread.Report {
			if err := json.Unmarshal(in, &r.v); err != nil {
				return malformedReport
			}
			return schemaread.Report{}
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

// texts is a list of single-text tables (ModelArg, ModelDetail).
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
var stateSpec = recordSpec[State, tw.ModelStateBuilder, tw.ModelState]{
	name: "STATE",
	fns: wireFns[tw.ModelStateBuilder, tw.ModelState]{
		(*tw.ModelStateBuilder).Init, (*tw.ModelStateBuilder).Shutdown, (*tw.ModelStateBuilder).Lock, (*tw.ModelStateBuilder).AsConst,
		tw.ModelStateLoadBuilder, tw.ModelStateFromJson, tw.ModelStateToJsonMeasure, tw.ModelStateToJson,
		tw.ModelStateMeasure, tw.ModelStateSave, tw.ModelStateLoadMeasureReason,
	},
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
// source_sha256, the schema JSON sourceSha256.
var traceSpec = recordSpec[Trace, tw.ModelTraceBuilder, tw.ModelTrace]{
	name: "TRACE",
	fns: wireFns[tw.ModelTraceBuilder, tw.ModelTrace]{
		(*tw.ModelTraceBuilder).Init, (*tw.ModelTraceBuilder).Shutdown, (*tw.ModelTraceBuilder).Lock, (*tw.ModelTraceBuilder).AsConst,
		tw.ModelTraceLoadBuilder, tw.ModelTraceFromJson, tw.ModelTraceToJsonMeasure, tw.ModelTraceToJson,
		tw.ModelTraceMeasure, tw.ModelTraceSave, tw.ModelTraceLoadMeasureReason,
	},
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
var actionSpec = recordSpec[Action, tw.ModelActionBuilder, tw.ModelAction]{
	name: "ACTION",
	fns: wireFns[tw.ModelActionBuilder, tw.ModelAction]{
		(*tw.ModelActionBuilder).Init, (*tw.ModelActionBuilder).Shutdown, (*tw.ModelActionBuilder).Lock, (*tw.ModelActionBuilder).AsConst,
		tw.ModelActionLoadBuilder, tw.ModelActionFromJson, tw.ModelActionToJsonMeasure, tw.ModelActionToJson,
		tw.ModelActionMeasure, tw.ModelActionSave, tw.ModelActionLoadMeasureReason,
	},
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
// every value text; the schema JSON is a list of key and value fields.
var eventSpec = recordSpec[Event, tw.ModelEventBuilder, tw.ModelEvent]{
	name: "EVENT",
	fns: wireFns[tw.ModelEventBuilder, tw.ModelEvent]{
		(*tw.ModelEventBuilder).Init, (*tw.ModelEventBuilder).Shutdown, (*tw.ModelEventBuilder).Lock, (*tw.ModelEventBuilder).AsConst,
		tw.ModelEventLoadBuilder, tw.ModelEventFromJson, tw.ModelEventToJsonMeasure, tw.ModelEventToJson,
		tw.ModelEventMeasure, tw.ModelEventSave, tw.ModelEventLoadMeasureReason,
	},
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
var findingSpec = recordSpec[Finding, tw.ModelFindingBuilder, tw.ModelFinding]{
	name: "FINDING",
	fns: wireFns[tw.ModelFindingBuilder, tw.ModelFinding]{
		(*tw.ModelFindingBuilder).Init, (*tw.ModelFindingBuilder).Shutdown, (*tw.ModelFindingBuilder).Lock, (*tw.ModelFindingBuilder).AsConst,
		tw.ModelFindingLoadBuilder, tw.ModelFindingFromJson, tw.ModelFindingToJsonMeasure, tw.ModelFindingToJson,
		tw.ModelFindingMeasure, tw.ModelFindingSave, tw.ModelFindingLoadMeasureReason,
	},
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
var replayEventSpec = recordSpec[ReplayEvent, tw.ModelReplayEventBuilder, tw.ModelReplayEvent]{
	name: "REPLAY_EVENT",
	fns: wireFns[tw.ModelReplayEventBuilder, tw.ModelReplayEvent]{
		(*tw.ModelReplayEventBuilder).Init, (*tw.ModelReplayEventBuilder).Shutdown, (*tw.ModelReplayEventBuilder).Lock, (*tw.ModelReplayEventBuilder).AsConst,
		tw.ModelReplayEventLoadBuilder, tw.ModelReplayEventFromJson, tw.ModelReplayEventToJsonMeasure, tw.ModelReplayEventToJson,
		tw.ModelReplayEventMeasure, tw.ModelReplayEventSave, tw.ModelReplayEventLoadMeasureReason,
	},
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

// The codecs, one per record.
var (
	stateCodec       = stateSpec.codec()
	traceCodec       = traceSpec.codec()
	actionCodec      = actionSpec.codec()
	eventCodec       = eventSpec.codec()
	findingCodec     = findingSpec.codec()
	replayEventCodec = replayEventSpec.codec()
)

// traceReader reads trace.json; its counters are the Trace record's reads per
// form (S4: the per-form counter is the cutover's evidence).
var traceReader = schemaread.NewReader(traceCodec)

// ReadTraceFile is trace.json read strictly: its binary image, the padded
// standard base64 of that image, its schema JSON and the legacy JSON the
// capture writes today all read alike, and a damaged file is refused by name
// (`TRACE REFUSED: <reason>; run: <help>`), never read as an empty trace.
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
