package tablemodel

// The table model's records read in both forms (the owner's rules S2 and S4,
// readers first): each record that a reader decodes reads alike from its
// binary image, the padded standard base64 of that image, and the legacy form
// its writer writes today. A damaged value of each form is refused by name
// and never read as absent, empty or zero, and each read is counted under its
// record and its form.
//
// The strict read lives in pkg/schemaread; the tables of pkg/tablemodelwire
// supply the codec. Writers stay legacy here; the writer switch is a later
// card. This file holds the read of the State record, the central serialized
// value (trace.json holds its states); the other records read the same shape
// and land in later cards.

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/tablemodelwire"
)

// modelHelp is what a refusal points at.
const modelHelp = "nova-sprint help tablemodel"

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
func tableReport(rep tablemodelwire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == tablemodelwire.TableOpenRefused || rep.Verdict == tablemodelwire.TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

func physObj(p Phys) obj { return obj{"table_name": p.Table, "epoch": p.Epoch} }
func cellObj(c Cell) obj { return obj{"p": physObj(c.P), "row": c.Row, "col": c.Col} }

// stateCanon is a State's canonical schema JSON, the form the table's JSON
// reader and writer speak.
func stateCanon(s State) obj {
	live, rows, binds, data, place := []any{}, []any{}, []any{}, []any{}, []any{}
	for _, p := range s.Live {
		live = append(live, physObj(p))
	}
	for _, r := range s.Rows {
		rows = append(rows, obj{"p": physObj(r.P), "row": r.Row})
	}
	for _, b := range s.Binds {
		binds = append(binds, obj{"cell": cellObj(b.Cell), "target": cellObj(b.Target)})
	}
	for _, d := range s.Data {
		data = append(data, obj{"cell": cellObj(d.Cell), "member": d.Member, "score": d.Score})
	}
	for _, p := range s.Place {
		locs := []any{}
		for _, l := range p.Locations {
			locs = append(locs, obj{"p": physObj(l.P), "cell": cellObj(l.Cell)})
		}
		place = append(place, obj{"member": p.Member, "locations": locs})
	}
	seen := []any{}
	for _, k := range sortedKeys(s.Seen) {
		seen = append(seen, obj{"writer": k, "epoch": s.Seen[k]})
	}
	return obj{"live": live, "rows": rows, "binds": binds, "data": data, "place": place, "active": s.Active, "seen": seen}
}

func physFromAny(x any) (Phys, error) {
	m, ok := x.(map[string]any)
	if !ok {
		return Phys{}, errors.New("state: a physical table is not an object")
	}
	ep, okE := m["epoch"].(float64)
	tab, okT := m["table_name"].(string)
	if !okE || !okT {
		return Phys{}, errors.New("state: a physical table has no name or epoch")
	}
	return Phys{tab, int(ep)}, nil
}

func cellFromAny(x any) (Cell, error) {
	m, ok := x.(map[string]any)
	if !ok {
		return Cell{}, errors.New("state: a cell is not an object")
	}
	p, err := physFromAny(m["p"])
	if err != nil {
		return Cell{}, err
	}
	row, okR := m["row"].(string)
	col, okC := m["col"].(string)
	if !okR || !okC {
		return Cell{}, errors.New("state: a cell has no row or column")
	}
	return Cell{p, row, col}, nil
}

// stateFromCanon is the canonical schema JSON's State value.
func stateFromCanon(b []byte) (State, error) {
	var o obj
	if err := json.Unmarshal(b, &o); err != nil {
		return State{}, err
	}
	var s State
	if v, ok := o["active"].(float64); ok {
		s.Active = int(v)
	}
	s.Seen = map[string]int{}
	ok := true
	for _, x := range o["live"].([]any) {
		p, err := physFromAny(x)
		ok = ok && err == nil
		s.Live = append(s.Live, p)
	}
	for _, x := range o["rows"].([]any) {
		m, okm := x.(map[string]any)
		p, err := physFromAny(m["p"])
		row, okr := m["row"].(string)
		ok = ok && okm && err == nil && okr
		s.Rows = append(s.Rows, RowAt{p, row})
	}
	for _, x := range o["binds"].([]any) {
		m, okm := x.(map[string]any)
		c, e1 := cellFromAny(m["cell"])
		tg, e2 := cellFromAny(m["target"])
		ok = ok && okm && e1 == nil && e2 == nil
		s.Binds = append(s.Binds, Bind{c, tg})
	}
	for _, x := range o["data"].([]any) {
		m, okm := x.(map[string]any)
		c, e1 := cellFromAny(m["cell"])
		member, okr := m["member"].(string)
		score, oks := m["score"].(float64)
		ok = ok && okm && e1 == nil && okr && oks
		s.Data = append(s.Data, Datum{c, member, int(score)})
	}
	for _, x := range o["place"].([]any) {
		m := x.(map[string]any)
		pl := Placement{}
		if member, okr := m["member"].(string); okr {
			pl.Member = member
		} else {
			ok = false
		}
		for _, lx := range m["locations"].([]any) {
			lm, okl := lx.(map[string]any)
			p, e1 := physFromAny(lm["p"])
			c, e2 := cellFromAny(lm["cell"])
			ok = ok && okl && e1 == nil && e2 == nil
			pl.Locations = append(pl.Locations, Location{p, c})
		}
		s.Place = append(s.Place, pl)
	}
	for _, x := range o["seen"].([]any) {
		m, okm := x.(map[string]any)
		writer, okr := m["writer"].(string)
		epoch, oks := m["epoch"].(float64)
		ok = ok && okm && okr && oks
		s.Seen[writer] = int(epoch)
	}
	if !ok {
		return State{}, errors.New("state: a value is not the shape the table declares")
	}
	return s, nil
}

// stateValue is the value a State codec loads.
type stateValue struct{ s State }

// stateCodec is the strict read of a State record.
var stateCodec = &schemaread.Codec[stateValue]{
	Name:  "STATE",
	Help:  modelHelp,
	Reset: func(v *stateValue) { v.s = State{} },
	Measure: func(wire []byte) error {
		_, err := tablemodelwire.ModelStateLoadMeasureReason(wire)
		return err
	},
	LoadWire: func(v *stateValue, wire []byte) schemaread.Report {
		var rep tablemodelwire.TableReport
		b := new(tablemodelwire.ModelStateBuilder)
		if !b.Init() {
			return malformedReport
		}
		defer b.Shutdown()
		if !tablemodelwire.ModelStateLoadBuilder(b, wire, &rep) || !b.Lock() {
			return tableReport(rep, false)
		}
		s, err := stateFromModel(b.AsConst())
		if err != nil {
			return malformedReport
		}
		v.s = s
		return tableReport(rep, true)
	},
	LoadJSON: func(v *stateValue, text []byte) schemaread.Report {
		var rep tablemodelwire.TableReport
		b := new(tablemodelwire.ModelStateBuilder)
		if !b.Init() {
			return malformedReport
		}
		defer b.Shutdown()
		if !tablemodelwire.ModelStateFromJson(b, text, &rep) || !b.Lock() {
			return tableReport(rep, false)
		}
		s, err := stateFromModel(b.AsConst())
		if err != nil {
			return malformedReport
		}
		v.s = s
		return tableReport(rep, true)
	},
	Legacy: func(in []byte) bool {
		var probe struct {
			Live []json.RawMessage `json:"live"`
		}
		if json.Unmarshal(bytes.TrimSpace(in), &probe) != nil {
			return false
		}
		for _, l := range probe.Live {
			var p Phys
			if json.Unmarshal(l, &p) == nil {
				return true
			}
		}
		return false
	},
	LoadLegacy: func(v *stateValue, in []byte) schemaread.Report {
		if err := json.Unmarshal(in, &v.s); err != nil {
			return malformedReport
		}
		return schemaread.Report{}
	},
	SaveWire: func(v *stateValue) []byte {
		wire, err := stateWire(v.s)
		if err != nil {
			return nil
		}
		return wire
	},
	SaveJSON: func(v *stateValue) []byte {
		raw, _ := json.Marshal(stateCanon(v.s))
		return raw
	},
}

// newStateReader is a fresh strict reader of a State record, with its own
// per-form counters.
func newStateReader() *schemaread.Reader[stateValue] { return schemaread.NewReader(stateCodec) }

// stateFromModel is a loaded ModelState as the State value, via its canonical
// JSON.
func stateFromModel(m *tablemodelwire.ModelState) (State, error) {
	n := tablemodelwire.ModelStateToJsonMeasure(m)
	if n < 0 {
		return State{}, errors.New("state: the image has no JSON")
	}
	buf := make([]byte, n)
	if tablemodelwire.ModelStateToJson(m, buf) != n {
		return State{}, errors.New("state: the image's JSON is not readable")
	}
	return stateFromCanon(buf)
}

func argsCanon(args []string) []any {
	out := []any{}
	for _, a := range args {
		out = append(out, obj{"text": a})
	}
	return out
}

func eventCanon(e Event) obj {
	fields := []any{}
	for _, k := range sortedKeys(e) {
		fields = append(fields, obj{"key": k, "value": e[k]})
	}
	return obj{"fields": fields}
}

func traceStepCanon(s TraceStep) obj {
	o := obj{"verb": s.Verb, "args": argsCanon(s.Args), "actor": s.Actor, "hasReceipt": s.Receipt != nil, "receipt": eventCanon(s.Receipt), "model": s.Model, "state": stateCanon(s.State)}
	if s.Refused != nil {
		o["refused"] = *s.Refused
	}
	return o
}

func traceCanon(tr Trace) obj {
	hashes := []any{}
	for _, k := range sortedKeys(tr.ModelSHA256) {
		hashes = append(hashes, obj{"module": k, "sha256": tr.ModelSHA256[k]})
	}
	steps := []any{}
	for _, s := range tr.Steps {
		steps = append(steps, traceStepCanon(s))
	}
	return obj{"sourceSha256": tr.SourceSHA256, "modelSha256": hashes, "initial": stateCanon(tr.Initial), "steps": steps}
}

func argsFromAny(x any) ([]string, error) {
	arr, ok := x.([]any)
	if !ok {
		return nil, errors.New("trace: an argument list is not an array")
	}
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		m, okm := a.(map[string]any)
		text, okt := m["text"].(string)
		if !okm || !okt {
			return nil, errors.New("trace: an argument is not a text field")
		}
		out = append(out, text)
	}
	return out, nil
}

func eventFromAny(x any) (Event, error) {
	m, ok := x.(map[string]any)
	if !ok {
		return nil, errors.New("trace: an event is not an object")
	}
	fields, okf := m["fields"].([]any)
	if !okf {
		return nil, errors.New("trace: an event has no fields")
	}
	out := Event{}
	for _, f := range fields {
		fm, okm := f.(map[string]any)
		k, okk := fm["key"].(string)
		v, okv := fm["value"].(string)
		if !okm || !okk || !okv {
			return nil, errors.New("trace: an event field has no key or value")
		}
		out[k] = v
	}
	return out, nil
}

func traceStepFromAny(x any) (TraceStep, error) {
	m, ok := x.(map[string]any)
	if !ok {
		return TraceStep{}, errors.New("trace: a step is not an object")
	}
	var s TraceStep
	verb, okv := m["verb"].(string)
	actor, oka := m["actor"].(string)
	model, okm := m["model"].(string)
	if !okv || !oka || !okm {
		return TraceStep{}, errors.New("trace: a step has no verb, actor or model")
	}
	s.Verb = verb
	s.Actor = actor
	s.Model = model
	args, err := argsFromAny(m["args"])
	if err != nil {
		return TraceStep{}, err
	}
	s.Args = args
	if has, okh := m["hasReceipt"].(bool); okh && has {
		e, err := eventFromAny(m["receipt"])
		if err != nil {
			return TraceStep{}, err
		}
		s.Receipt = e
	}
	if v, okr := m["refused"]; okr {
		b, okb := v.(bool)
		if !okb {
			return TraceStep{}, errors.New("trace: refused is not boolean")
		}
		s.Refused = &b
	}
	sf, err := json.Marshal(m["state"])
	if err != nil {
		return TraceStep{}, err
	}
	st, err := stateFromCanon(sf)
	if err != nil {
		return TraceStep{}, err
	}
	s.State = st
	return s, nil
}

// traceFromCanon is the canonical schema JSON's Trace value.
func traceFromCanon(b []byte) (Trace, error) {
	var o obj
	if err := json.Unmarshal(b, &o); err != nil {
		return Trace{}, err
	}
	tr := Trace{ModelSHA256: map[string]string{}}
	src, ok := o["sourceSha256"].(string)
	if !ok {
		return Trace{}, errors.New("trace: no source hash")
	}
	tr.SourceSHA256 = src
	for _, h := range o["modelSha256"].([]any) {
		m := h.(map[string]any)
		mod, okm := m["module"].(string)
		sha, oks := m["sha256"].(string)
		if !okm || !oks {
			return Trace{}, errors.New("trace: a model hash has no module or sha256")
		}
		tr.ModelSHA256[mod] = sha
	}
	ini, err := json.Marshal(o["initial"])
	if err != nil {
		return Trace{}, err
	}
	if tr.Initial, err = stateFromCanon(ini); err != nil {
		return Trace{}, err
	}
	for _, x := range o["steps"].([]any) {
		s, err := traceStepFromAny(x)
		if err != nil {
			return Trace{}, err
		}
		tr.Steps = append(tr.Steps, s)
	}
	return tr, nil
}

// traceValue is the value a Trace codec loads.
type traceValue struct{ tr Trace }

// traceCodec is the strict read of a Trace record.
var traceCodec = &schemaread.Codec[traceValue]{
	Name:  "TRACE",
	Help:  modelHelp,
	Reset: func(v *traceValue) { v.tr = Trace{} },
	Measure: func(wire []byte) error {
		_, err := tablemodelwire.ModelTraceLoadMeasureReason(wire)
		return err
	},
	LoadWire: func(v *traceValue, wire []byte) schemaread.Report {
		var rep tablemodelwire.TableReport
		b := new(tablemodelwire.ModelTraceBuilder)
		if !b.Init() {
			return malformedReport
		}
		defer b.Shutdown()
		if !tablemodelwire.ModelTraceLoadBuilder(b, wire, &rep) || !b.Lock() {
			return tableReport(rep, false)
		}
		tr, err := traceFromModel(b.AsConst())
		if err != nil {
			return malformedReport
		}
		v.tr = tr
		return tableReport(rep, true)
	},
	LoadJSON: func(v *traceValue, text []byte) schemaread.Report {
		var rep tablemodelwire.TableReport
		b := new(tablemodelwire.ModelTraceBuilder)
		if !b.Init() {
			return malformedReport
		}
		defer b.Shutdown()
		if !tablemodelwire.ModelTraceFromJson(b, text, &rep) || !b.Lock() {
			return tableReport(rep, false)
		}
		tr, err := traceFromModel(b.AsConst())
		if err != nil {
			return malformedReport
		}
		v.tr = tr
		return tableReport(rep, true)
	},
	Legacy: func(in []byte) bool {
		var probe struct {
			Steps []json.RawMessage `json:"steps"`
		}
		return json.Unmarshal(bytes.TrimSpace(in), &probe) == nil
	},
	LoadLegacy: func(v *traceValue, in []byte) schemaread.Report {
		if err := json.Unmarshal(in, &v.tr); err != nil {
			return malformedReport
		}
		return schemaread.Report{}
	},
	SaveWire: func(v *traceValue) []byte {
		wire, err := traceWire(v.tr)
		if err != nil {
			return nil
		}
		return wire
	},
	SaveJSON: func(v *traceValue) []byte {
		raw, _ := json.Marshal(traceCanon(v.tr))
		return raw
	},
}

// newTraceReader is a fresh strict reader of a Trace record, with its own
// per-form counters.
func newTraceReader() *schemaread.Reader[traceValue] { return schemaread.NewReader(traceCodec) }

// traceFromModel is a loaded ModelTrace as the Trace value, via its canonical
// JSON.
func traceFromModel(m *tablemodelwire.ModelTrace) (Trace, error) {
	n := tablemodelwire.ModelTraceToJsonMeasure(m)
	if n < 0 {
		return Trace{}, errors.New("trace: the image has no JSON")
	}
	buf := make([]byte, n)
	if tablemodelwire.ModelTraceToJson(m, buf) != n {
		return Trace{}, errors.New("trace: the image's JSON is not readable")
	}
	return traceFromCanon(buf)
}

// traceWire is a Trace's binary image (the table wire).
func traceWire(tr Trace) ([]byte, error) {
	canon, err := json.Marshal(traceCanon(tr))
	if err != nil {
		return nil, err
	}
	b := new(tablemodelwire.ModelTraceBuilder)
	if !b.Init() {
		return nil, errors.New("trace: cannot start the builder")
	}
	defer b.Shutdown()
	var rep tablemodelwire.TableReport
	if !tablemodelwire.ModelTraceFromJson(b, canon, &rep) || !b.Lock() {
		return nil, errors.New("trace: a value builds no image")
	}
	root := b.AsConst()
	n := tablemodelwire.ModelTraceMeasure(root)
	if n < 0 {
		return nil, errors.New("trace: the image has no wire")
	}
	buf := make([]byte, n)
	if tablemodelwire.ModelTraceSave(root, buf) != n {
		return nil, errors.New("trace: the wire is not writable")
	}
	return buf, nil
}

// stateWire is a State's binary image (the table wire).
func stateWire(s State) ([]byte, error) {
	canon, err := json.Marshal(stateCanon(s))
	if err != nil {
		return nil, err
	}
	b := new(tablemodelwire.ModelStateBuilder)
	if !b.Init() {
		return nil, errors.New("state: cannot start the builder")
	}
	defer b.Shutdown()
	var rep tablemodelwire.TableReport
	if !tablemodelwire.ModelStateFromJson(b, canon, &rep) || !b.Lock() {
		return nil, errors.New("state: a value builds no image")
	}
	root := b.AsConst()
	n := tablemodelwire.ModelStateMeasure(root)
	if n < 0 {
		return nil, errors.New("state: the image has no wire")
	}
	buf := make([]byte, n)
	if tablemodelwire.ModelStateSave(root, buf) != n {
		return nil, errors.New("state: the wire is not writable")
	}
	return buf, nil
}
