package tablemodelwire

// TestModelsRecordsGolden is the golden test of the tablemodel definitions
// (schemas/tablemodel). Every table of the area is a VARIABLE table (schema
// owner rule S1: a record is a fixed table only where every bound it declares
// is a bound the code enforces, and none of these is), so the test pins:
//
//   - every table round-trips as the binary wire, as padded standard base64 of
//     that wire and as the JSON text form, each read strictly through
//     pkg/schemaread (the owner's S2), and the base64 text is the binary wire;
//   - each sample is filled from the Go value the table replaces
//     (internal/tablemodel, pkg/tlc, internal/converge), field by field, so a
//     field the struct serializes and the table lacks shows as a difference;
//   - the retained trace as capture writes it (internal/tablemodel/testdata/
//     replay/trace.json: 34 model hashes, 32 steps) round-trips, and so does a
//     trace with 200 model modules and module names of 4096 bytes: the list
//     and the text have no bound because replay.go hashModels (170-184) has
//     none;
//   - ModelStep.workers is a plain int64, since RunSteps and tlc.Run pass any
//     value through: 0, -1, the smallest and the largest int64 round-trip in
//     every form, and no field of these tables declares a range;
//   - a damaged image of each form is refused by name, for every table;
//   - the SHA-256 of each sample's wire is pinned, so a change of layout or
//     encoding shows as a changed digest.
//
// The test uses no clock, no socket and no network.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/converge"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modelsHelp = "nova-sprint help tablemodel"

// builderOf is what every generated builder gives a caller.
type builderOf[T any] interface {
	Init(...TableAllocator) bool
	Lock() bool
	AsConst() *T
	Shutdown()
}

// ops is one table's generated functions reduced to bytes: build a wire and its
// canonical JSON from JSON text, and read a wire back to canonical JSON. A
// builder root is only meaningful once locked, so every read and write goes
// through Lock and AsConst.
type ops struct {
	name     string
	fields   []TableFieldInfo
	fromJSON func(text []byte, rep *TableReport) (canon, wire []byte, ok bool)
	loadWire func(wire []byte, rep *TableReport) (canon []byte, ok bool)
	measure  func(wire []byte) error
}

func newOps[T, B any, PB interface {
	*B
	builderOf[T]
}](name string, fields []TableFieldInfo,
	fromJSON func(PB, []byte, *TableReport) bool,
	loadBuilder func(PB, []byte, *TableReport) bool,
	measure func([]byte) (int64, error),
	jsonMeasure func(*T, ...TableAllocator) int64,
	jsonWrite func(*T, []byte, ...TableAllocator) int64,
	wireMeasure func(*T, ...TableWriteContext) int64,
	wireSave func(*T, []byte, ...TableWriteContext) int64) ops {
	render := func(root *T) (canon, wire []byte) {
		if n := jsonMeasure(root); n >= 0 {
			buf := make([]byte, n)
			if jsonWrite(root, buf) == n {
				canon = buf
			}
		}
		if n := wireMeasure(root); n >= 0 {
			buf := make([]byte, n)
			if wireSave(root, buf) == n {
				wire = buf
			}
		}
		return canon, wire
	}
	return ops{
		name:   name,
		fields: fields,
		fromJSON: func(text []byte, rep *TableReport) ([]byte, []byte, bool) {
			b := PB(new(B))
			if !b.Init() {
				return nil, nil, false
			}
			defer b.Shutdown()
			if !fromJSON(b, text, rep) || !b.Lock() {
				return nil, nil, false
			}
			canon, wire := render(b.AsConst())
			return canon, wire, canon != nil && wire != nil
		},
		loadWire: func(wire []byte, rep *TableReport) ([]byte, bool) {
			b := PB(new(B))
			if !b.Init() {
				return nil, false
			}
			defer b.Shutdown()
			if !loadBuilder(b, wire, rep) || !b.Lock() {
				return nil, false
			}
			canon, _ := render(b.AsConst())
			return canon, canon != nil
		},
		measure: func(wire []byte) error {
			_, err := measure(wire)
			return err
		},
	}
}

// The twenty-five tables of the area, one line each (the mechanical part of
// this file: a table added to the schema adds its line here).
var modelOps = []ops{
	newOps("ModelArg", ModelArgTableFields, ModelArgFromJson, ModelArgLoadBuilder, ModelArgLoadMeasureReason, ModelArgToJsonMeasure, ModelArgToJson, ModelArgMeasure, ModelArgSave),
	newOps("ModelDetail", ModelDetailTableFields, ModelDetailFromJson, ModelDetailLoadBuilder, ModelDetailLoadMeasureReason, ModelDetailToJsonMeasure, ModelDetailToJson, ModelDetailMeasure, ModelDetailSave),
	newOps("ModelPhys", ModelPhysTableFields, ModelPhysFromJson, ModelPhysLoadBuilder, ModelPhysLoadMeasureReason, ModelPhysToJsonMeasure, ModelPhysToJson, ModelPhysMeasure, ModelPhysSave),
	newOps("ModelCell", ModelCellTableFields, ModelCellFromJson, ModelCellLoadBuilder, ModelCellLoadMeasureReason, ModelCellToJsonMeasure, ModelCellToJson, ModelCellMeasure, ModelCellSave),
	newOps("ModelRowAt", ModelRowAtTableFields, ModelRowAtFromJson, ModelRowAtLoadBuilder, ModelRowAtLoadMeasureReason, ModelRowAtToJsonMeasure, ModelRowAtToJson, ModelRowAtMeasure, ModelRowAtSave),
	newOps("ModelBind", ModelBindTableFields, ModelBindFromJson, ModelBindLoadBuilder, ModelBindLoadMeasureReason, ModelBindToJsonMeasure, ModelBindToJson, ModelBindMeasure, ModelBindSave),
	newOps("ModelDatum", ModelDatumTableFields, ModelDatumFromJson, ModelDatumLoadBuilder, ModelDatumLoadMeasureReason, ModelDatumToJsonMeasure, ModelDatumToJson, ModelDatumMeasure, ModelDatumSave),
	newOps("ModelLocation", ModelLocationTableFields, ModelLocationFromJson, ModelLocationLoadBuilder, ModelLocationLoadMeasureReason, ModelLocationToJsonMeasure, ModelLocationToJson, ModelLocationMeasure, ModelLocationSave),
	newOps("ModelPlacement", ModelPlacementTableFields, ModelPlacementFromJson, ModelPlacementLoadBuilder, ModelPlacementLoadMeasureReason, ModelPlacementToJsonMeasure, ModelPlacementToJson, ModelPlacementMeasure, ModelPlacementSave),
	newOps("ModelSeen", ModelSeenTableFields, ModelSeenFromJson, ModelSeenLoadBuilder, ModelSeenLoadMeasureReason, ModelSeenToJsonMeasure, ModelSeenToJson, ModelSeenMeasure, ModelSeenSave),
	newOps("ModelState", ModelStateTableFields, ModelStateFromJson, ModelStateLoadBuilder, ModelStateLoadMeasureReason, ModelStateToJsonMeasure, ModelStateToJson, ModelStateMeasure, ModelStateSave),
	newOps("ModelAction", ModelActionTableFields, ModelActionFromJson, ModelActionLoadBuilder, ModelActionLoadMeasureReason, ModelActionToJsonMeasure, ModelActionToJson, ModelActionMeasure, ModelActionSave),
	newOps("ModelEventField", ModelEventFieldTableFields, ModelEventFieldFromJson, ModelEventFieldLoadBuilder, ModelEventFieldLoadMeasureReason, ModelEventFieldToJsonMeasure, ModelEventFieldToJson, ModelEventFieldMeasure, ModelEventFieldSave),
	newOps("ModelEvent", ModelEventTableFields, ModelEventFromJson, ModelEventLoadBuilder, ModelEventLoadMeasureReason, ModelEventToJsonMeasure, ModelEventToJson, ModelEventMeasure, ModelEventSave),
	newOps("ModelTraceStep", ModelTraceStepTableFields, ModelTraceStepFromJson, ModelTraceStepLoadBuilder, ModelTraceStepLoadMeasureReason, ModelTraceStepToJsonMeasure, ModelTraceStepToJson, ModelTraceStepMeasure, ModelTraceStepSave),
	newOps("ModelHash", ModelHashTableFields, ModelHashFromJson, ModelHashLoadBuilder, ModelHashLoadMeasureReason, ModelHashToJsonMeasure, ModelHashToJson, ModelHashMeasure, ModelHashSave),
	newOps("ModelTrace", ModelTraceTableFields, ModelTraceFromJson, ModelTraceLoadBuilder, ModelTraceLoadMeasureReason, ModelTraceToJsonMeasure, ModelTraceToJson, ModelTraceMeasure, ModelTraceSave),
	newOps("ModelFinding", ModelFindingTableFields, ModelFindingFromJson, ModelFindingLoadBuilder, ModelFindingLoadMeasureReason, ModelFindingToJsonMeasure, ModelFindingToJson, ModelFindingMeasure, ModelFindingSave),
	newOps("ModelReplayEvent", ModelReplayEventTableFields, ModelReplayEventFromJson, ModelReplayEventLoadBuilder, ModelReplayEventLoadMeasureReason, ModelReplayEventToJsonMeasure, ModelReplayEventToJson, ModelReplayEventMeasure, ModelReplayEventSave),
	newOps("ModelStep", ModelStepTableFields, ModelStepFromJson, ModelStepLoadBuilder, ModelStepLoadMeasureReason, ModelStepToJsonMeasure, ModelStepToJson, ModelStepMeasure, ModelStepSave),
	newOps("ModelViolation", ModelViolationTableFields, ModelViolationFromJson, ModelViolationLoadBuilder, ModelViolationLoadMeasureReason, ModelViolationToJsonMeasure, ModelViolationToJson, ModelViolationMeasure, ModelViolationSave),
	newOps("ModelOutcome", ModelOutcomeTableFields, ModelOutcomeFromJson, ModelOutcomeLoadBuilder, ModelOutcomeLoadMeasureReason, ModelOutcomeToJsonMeasure, ModelOutcomeToJson, ModelOutcomeMeasure, ModelOutcomeSave),
	newOps("ModelStepResult", ModelStepResultTableFields, ModelStepResultFromJson, ModelStepResultLoadBuilder, ModelStepResultLoadMeasureReason, ModelStepResultToJsonMeasure, ModelStepResultToJson, ModelStepResultMeasure, ModelStepResultSave),
	newOps("ConvergeStreamEntry", ConvergeStreamEntryTableFields, ConvergeStreamEntryFromJson, ConvergeStreamEntryLoadBuilder, ConvergeStreamEntryLoadMeasureReason, ConvergeStreamEntryToJsonMeasure, ConvergeStreamEntryToJson, ConvergeStreamEntryMeasure, ConvergeStreamEntrySave),
	newOps("ConvergeState", ConvergeStateTableFields, ConvergeStateFromJson, ConvergeStateLoadBuilder, ConvergeStateLoadMeasureReason, ConvergeStateToJsonMeasure, ConvergeStateToJson, ConvergeStateMeasure, ConvergeStateSave),
}

// doc is the value a read gives the test: the table's canonical JSON, taken
// while its builder is alive (a table read lives in the builder's region).
type doc struct{ canon []byte }

func readReport(rep TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// openBound is true for a field that declares no bound: a text without a limit
// and a list without a count read their bound as 0 or as the open sentinel.
func openBound(f TableFieldInfo) bool { return f.ArrayBound == 0 || f.ArrayBound == math.MaxInt32 }

// field returns a declared field's descriptor, so the test reads the bound from
// the generated Go and cannot drift from the definition.
func (o ops) field(t *testing.T, name string) TableFieldInfo {
	t.Helper()
	for _, f := range o.fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s declares no field %s", o.name, name)
	return TableFieldInfo{}
}

// codec is the strict read and write of one table over its generated functions
// (schema-tables union research section 4.6). No field of these tables declares
// a bound, so the codec has no constraint.
func (o ops) codec(t *testing.T) *schemaread.Codec[doc] {
	var constraints func(*doc, *schemaread.Checker)
	return &schemaread.Codec[doc]{
		Name:  strings.ToUpper(o.name),
		Help:  modelsHelp,
		Reset: func(d *doc) { d.canon = nil },
		Measure: func(wire []byte) error {
			return o.measure(wire)
		},
		LoadWire: func(d *doc, wire []byte) schemaread.Report {
			var rep TableReport
			canon, ok := o.loadWire(wire, &rep)
			d.canon = canon
			return readReport(rep, ok)
		},
		LoadJSON: func(d *doc, text []byte) schemaread.Report {
			var rep TableReport
			canon, _, ok := o.fromJSON(text, &rep)
			d.canon = canon
			return readReport(rep, ok)
		},
		Constraints: constraints,
		SaveWire: func(d *doc) []byte {
			var rep TableReport
			_, wire, _ := o.fromJSON(d.canon, &rep)
			return wire
		},
		SaveJSON: func(d *doc) []byte { return d.canon },
	}
}

// ---- the Go values the tables replace, as the tables' JSON ----------------

type obj = map[string]any

func physJSON(p tablemodel.Phys) obj { return obj{"table_name": p.Table, "epoch": p.Epoch} }

func cellJSON(c tablemodel.Cell) obj {
	return obj{"p": physJSON(c.P), "row": c.Row, "col": c.Col}
}

func locationJSON(l tablemodel.Location) obj {
	return obj{"p": physJSON(l.P), "cell": cellJSON(l.Cell)}
}

func argsJSON(args []string) []any {
	out := []any{}
	for _, a := range args {
		out = append(out, obj{"text": a})
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func seenJSON(m map[string]int) []any {
	out := []any{}
	for _, k := range sortedKeys(m) {
		out = append(out, obj{"writer": k, "epoch": m[k]})
	}
	return out
}

func stateJSON(s tablemodel.State) obj {
	live, rows, binds, data, place := []any{}, []any{}, []any{}, []any{}, []any{}
	for _, p := range s.Live {
		live = append(live, physJSON(p))
	}
	for _, r := range s.Rows {
		rows = append(rows, obj{"p": physJSON(r.P), "row": r.Row})
	}
	for _, b := range s.Binds {
		binds = append(binds, obj{"cell": cellJSON(b.Cell), "target": cellJSON(b.Target)})
	}
	for _, d := range s.Data {
		data = append(data, obj{"cell": cellJSON(d.Cell), "member": d.Member, "score": d.Score})
	}
	for _, p := range s.Place {
		locs := []any{}
		for _, l := range p.Locations {
			locs = append(locs, locationJSON(l))
		}
		place = append(place, obj{"member": p.Member, "locations": locs})
	}
	return obj{"live": live, "rows": rows, "binds": binds, "data": data, "place": place, "active": s.Active, "seen": seenJSON(s.Seen)}
}

func eventJSON(e tablemodel.Event) obj {
	fields := []any{}
	for _, k := range sortedKeys(e) {
		fields = append(fields, obj{"key": k, "value": e[k]})
	}
	return obj{"fields": fields}
}

func actionJSON(a tablemodel.Action) obj {
	return obj{"verb": a.Verb, "args": argsJSON(a.Args), "actor": a.Actor}
}

func traceStepJSON(s tablemodel.TraceStep) obj {
	o := obj{"verb": s.Verb, "args": argsJSON(s.Args), "actor": s.Actor, "hasReceipt": s.Receipt != nil, "receipt": eventJSON(s.Receipt), "model": s.Model, "state": stateJSON(s.State)}
	if s.Refused != nil {
		o["refused"] = *s.Refused
	}
	return o
}

func hashesJSON(m map[string]string) []any {
	out := []any{}
	for _, k := range sortedKeys(m) {
		out = append(out, obj{"module": k, "sha256": m[k]})
	}
	return out
}

func traceJSON(tr tablemodel.Trace) obj {
	steps := []any{}
	for _, s := range tr.Steps {
		steps = append(steps, traceStepJSON(s))
	}
	return obj{"sourceSha256": tr.SourceSHA256, "modelSha256": hashesJSON(tr.ModelSHA256), "initial": stateJSON(tr.Initial), "steps": steps}
}

func findingJSON(f tablemodel.Finding) obj {
	return obj{"name": f.Name, "result": f.Result, "text": f.Text}
}

func replayEventJSON(e tablemodel.ReplayEvent) obj {
	fields := []any{}
	for _, f := range e.Fields {
		fields = append(fields, obj{"text": f})
	}
	return obj{"step": e.Step, "verdict": e.Verdict, "fields": fields}
}

func stepJSON(s tablemodel.Step) obj {
	return obj{"name": s.Name, "module": s.Module, "workers": s.Workers, "kind": s.Kind, "property": s.Property, "met": s.Met}
}

func violationJSON(v tlc.Violation) obj { return obj{"kind": v.Kind, "name": v.Name} }

func outcomeJSON(o tlc.Outcome) obj {
	vs := []any{}
	for _, v := range o.Violations {
		vs = append(vs, violationJSON(v))
	}
	return obj{"completed": o.Completed, "generated": o.Generated, "distinct": o.Distinct, "violations": vs, "initialState": o.InitialState}
}

func stepResultJSON(r tablemodel.StepResult) obj {
	return obj{"step": stepJSON(r.Step), "verdict": r.Verdict, "ok": r.OK, "code": r.Code, "seconds": int64(r.Seconds), "outcome": outcomeJSON(r.Outcome), "log": r.Log}
}

func streamEntryJSON(name string, s converge.StreamState) obj {
	return obj{"name": name, "now": s.Now, "at": s.At, "widening": s.Widening}
}

func convergeJSON(st converge.State) obj {
	streams := []any{}
	for _, k := range sortedKeys(st.Streams) {
		streams = append(streams, streamEntryJSON(k, st.Streams[k]))
	}
	return obj{"streams": streams}
}

// shippedTrace is the retained trace capture writes, the repository's own
// fixture: 34 model hashes and 32 steps.
func shippedTrace(t *testing.T) tablemodel.Trace {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "tablemodel", "testdata", "replay", "trace.json"))
	require.NoError(t, err)
	var tr tablemodel.Trace
	require.NoError(t, json.Unmarshal(raw, &tr))
	require.Len(t, tr.ModelSHA256, 34, "the shipped capture carries 34 model hashes")
	require.Len(t, tr.Steps, 32, "the shipped capture carries 32 steps")
	return tr
}

func hexOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// bigTrace is the shipped trace with n model modules, each named at nameLen
// bytes: hashModels has no bound on either (replay.go:170-184).
func bigTrace(t *testing.T, n, nameLen int) tablemodel.Trace {
	tr := shippedTrace(t)
	tr.ModelSHA256 = map[string]string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("MCModule%04d_", i)
		name += strings.Repeat("x", nameLen-len(name)-len(".tla")) + ".tla"
		tr.ModelSHA256[name] = hexOf(name)
	}
	require.Len(t, tr.ModelSHA256, n)
	return tr
}

// sample is one table's sample: the Go value's JSON.
type sample struct {
	ops  ops
	want obj
}

func samples(t *testing.T) map[string]sample {
	tr := shippedTrace(t)
	var refusedStep, receiptStep tablemodel.TraceStep
	for _, s := range tr.Steps {
		if s.Refused != nil && *s.Refused && refusedStep.Verb == "" {
			refusedStep = s
		}
		if s.Receipt != nil && receiptStep.Verb == "" {
			receiptStep = s
		}
	}
	require.NotEmpty(t, refusedStep.Verb, "the shipped trace has a refused step")
	require.NotEmpty(t, receiptStep.Verb, "the shipped trace has a step with a receipt")
	last := tr.Steps[len(tr.Steps)-1].State
	cell := tablemodel.Cell{P: tablemodel.Phys{Table: "t1", Epoch: 1}, Row: "r1", Col: "c2"}
	finding := tablemodel.Finding{Name: "OnePlace", Result: "confirmed", Text: "a member placed in two cells of one table is refused by the model's invariant"}
	step := tablemodel.Step{Name: "MCEpochMemberTable", Module: "MCEpochMemberTable.tla", Workers: 4, Kind: "pass", Property: "", Met: tablemodel.VerdictPass}
	outcome := tlc.Outcome{Completed: false, Generated: "184467440737095516160", Distinct: "-", Violations: []tlc.Violation{{Kind: "Invariant", Name: "OnePlacePerTable"}, {Kind: "Temporal property"}}, InitialState: true}
	result := tablemodel.StepResult{Step: step, Verdict: tablemodel.VerdictPass, OK: true, Code: 12, Seconds: 90 * 1e9, Outcome: outcome, Log: "/var/tmp/tablemodel-1/logs/MCEpochMemberTable.log"}
	state := converge.State{Streams: map[string]converge.StreamState{
		"LANDING": {Now: 12, At: "2026-10-17T02:03:04Z", Widening: 2},
		"CLASSES": {Now: 0.5, At: "2026-10-17T02:03:04Z", Widening: 0},
		"LEDGER":  {Now: 1e6, At: "2026-10-17T02:03:05Z", Widening: 1},
	}}
	replay := tablemodel.ReplayEvent{Step: "capture", Verdict: tablemodel.VerdictPass, Fields: []string{"transitions=32", "trace=/var/tmp/tablemodel-1/trace.json"}}
	place := last.Place[0]
	phys := tablemodel.Phys{Table: "t2", Epoch: 2}
	byName := map[string]sample{}
	add := func(name string, want obj) {
		for _, o := range modelOps {
			if o.name == name {
				byName[name] = sample{o, want}
				return
			}
		}
		t.Fatalf("no ops for %s", name)
	}
	add("ModelArg", obj{"text": `{"binds": {"c2": "external"}}`})
	add("ModelDetail", obj{"text": "transitions=32"})
	add("ModelPhys", physJSON(phys))
	add("ModelCell", cellJSON(cell))
	add("ModelRowAt", obj{"p": physJSON(phys), "row": "r2"})
	add("ModelBind", obj{"cell": cellJSON(cell), "target": cellJSON(tablemodel.External)})
	add("ModelDatum", obj{"cell": cellJSON(cell), "member": "m3", "score": -7})
	add("ModelLocation", locationJSON(place.Locations[0]))
	add("ModelPlacement", obj{"member": place.Member, "locations": func() []any {
		out := []any{}
		for _, l := range place.Locations {
			out = append(out, locationJSON(l))
		}
		return out
	}()})
	add("ModelSeen", obj{"writer": "w2", "epoch": 2})
	add("ModelState", stateJSON(last))
	add("ModelAction", actionJSON(tablemodel.Action{Verb: receiptStep.Verb, Args: receiptStep.Args, Actor: receiptStep.Actor}))
	add("ModelEventField", obj{"key": "outcome", "value": "ok"})
	add("ModelEvent", eventJSON(receiptStep.Receipt))
	add("ModelTraceStep", traceStepJSON(receiptStep))
	add("ModelHash", obj{"module": "MCEpochMemberTable_TTrace_1790631425.tla", "sha256": hexOf("m")})
	add("ModelTrace", traceJSON(tr))
	add("ModelFinding", findingJSON(finding))
	add("ModelReplayEvent", replayEventJSON(replay))
	add("ModelStep", stepJSON(step))
	add("ModelViolation", violationJSON(tlc.Violation{Kind: "Action property", Name: "StaleWritesRefuse"}))
	add("ModelOutcome", outcomeJSON(outcome))
	add("ModelStepResult", stepResultJSON(result))
	add("ConvergeStreamEntry", streamEntryJSON("PRS", converge.StreamState{Now: 3.25, At: "2026-10-17T02:03:04Z", Widening: 4}))
	add("ConvergeState", convergeJSON(state))
	// the refused step is its own sample of the optional refused and of an empty
	// receipt; it rides in the ModelTraceStep case of TestModelsRecordsGolden.
	byName["ModelTraceStep/refused"] = sample{byName["ModelTraceStep"].ops, traceStepJSON(refusedStep)}
	return byName
}

// strip drops the null an absent optional or text reads as, so a comparison
// is about the values the Go side holds.
func strip(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if e != nil {
				out[k] = strip(e)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = strip(e)
		}
		return out
	}
	return v
}

// normal is JSON text or a Go value as the comparable tree strip leaves.
func normal(t *testing.T, v any) any {
	t.Helper()
	var raw []byte
	switch x := v.(type) {
	case []byte:
		raw = x
	default:
		var err error
		raw, err = json.Marshal(v)
		require.NoError(t, err)
	}
	var out any
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return strip(out)
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

// refusal is the reason a read was refused by, in the house style.
func refusal(t *testing.T, name string, err error) string {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), strings.ToUpper(name)+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+modelsHelp), err.Error())
	if r.Field != "" {
		return r.Reason + " (" + r.Field + ")"
	}
	return r.Reason
}

var forms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

// roundTrip writes want in each form through the strict reader's writer, reads
// each back through its reader, and returns the binary wire. The canonical JSON
// of every read is the Go value's JSON.
func roundTrip(t *testing.T, s sample) []byte {
	t.Helper()
	o := s.ops
	rd := schemaread.NewReader(o.codec(t))
	var rep TableReport
	canon, wire, ok := o.fromJSON(marshal(t, s.want), &rep)
	require.True(t, ok, "%s: the sample builds", o.name)
	require.Zero(t, rep.Clamped, "%s: the sample clamps nothing", o.name)
	require.Zero(t, rep.Unknown, "%s: the sample has no unknown field", o.name)
	want := normal(t, s.want)
	require.Equal(t, want, normal(t, canon), "%s: the canonical JSON is the Go value's JSON", o.name)
	for _, form := range forms {
		img, err := rd.Write(&doc{canon: canon}, form)
		require.NoError(t, err, "%s %s", o.name, form)
		got, res, err := rd.Read(img)
		require.NoError(t, err, "%s %s", o.name, form)
		assert.Equal(t, form, res.Form, "%s: the form read", o.name)
		assert.Equal(t, 0, res.Unknown)
		assert.Equal(t, want, normal(t, got.canon), "%s %s: the read value is the written value", o.name, form)
		switch form {
		case schemaread.FormBinary:
			assert.Equal(t, wire, img, "%s: the writer's binary is the wire", o.name)
		case schemaread.FormBase64:
			assert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(img), "%s: base64 is the padded standard encoding of the wire", o.name)
		}
	}
	for _, form := range forms {
		assert.Equal(t, uint64(1), rd.Counts().Reads(form), "%s: one read counted per form", o.name)
	}
	assert.Zero(t, rd.Counts().Unknown())
	return wire
}

// damaged reads a damaged image of each form of s and returns the reason each
// was refused by.
func damaged(t *testing.T, s sample, wire []byte) {
	t.Helper()
	o := s.ops
	rd := schemaread.NewReader(o.codec(t))
	name := o.name
	reason := func(img []byte) string {
		t.Helper()
		_, _, err := rd.Read(img)
		return refusal(t, name, err)
	}
	// binary: a form byte no build carries, a cut wire, a flipped byte inside
	// the body (the framing check)
	badForm := append([]byte{}, wire...)
	badForm[0] = 9
	assert.Equal(t, "unknown_form", reason(badForm), "%s: an unknown form byte", name)
	assert.Equal(t, "wire_damaged", reason(wire[:len(wire)-1]), "%s: a wire cut by one byte", name)
	assert.Equal(t, "wire_damaged", reason(wire[:len(wire)/2]), "%s: a wire cut in half", name)
	flipped := append([]byte{}, wire...)
	flipped[len(flipped)-1] ^= 0xff
	assert.Equal(t, "wire_damaged", reason(flipped), "%s: a flipped byte of the framing check", name)
	// base64: text that does not decode, and a cut text
	text := base64.StdEncoding.EncodeToString(wire)
	assert.Equal(t, "malformed", reason([]byte("A"+text[1:len(text)-2]+"!=")), "%s: base64 that does not decode", name)
	assert.Equal(t, "malformed", reason([]byte(text[:len(text)-1])), "%s: base64 without its padding", name)
	cut := text[:len(text)/2/4*4]
	assert.Equal(t, "wire_damaged", reason([]byte(cut)), "%s: base64 cut at a group", name)
	// json: an unclosed object, and a document that is not an object of this table
	assert.Equal(t, "malformed", reason([]byte(`{"x":`)), "%s: JSON cut short", name)
	assert.Equal(t, "malformed", reason([]byte(`{"x":1}}`)), "%s: JSON with trailing text", name)
}

func wireDigest(wire []byte) string {
	sum := sha256.Sum256(wire)
	return hex.EncodeToString(sum[:])
}

// pinned is the SHA-256 of each sample's binary wire.
var pinned = map[string]string{
	"ConvergeState":       "4263dbac0dafe43ecc781aec9f2d2f4bebc08be03118c1424142d3c9957de329",
	"ConvergeStreamEntry": "ed0e4497b0512d4375b646da4b89b8997618c02897d63944849b6e46564600b4",
	"ModelAction":         "ec2357c741577e9a6b713138d8553eb9914661c2295b266710eac8a40c737f25",
	"ModelArg":            "83ffefb0459415ac9d4e009ab39af489aea767a4bb8ab5a0ebfcdaa4514a42af",
	"ModelBind":           "65577c4a5da06e8baf06d877abcd8441626c492598b434a7fa43605649bc56b1",
	"ModelCell":           "c2ef71f81038859c4ed511bb3062d24a0d4493534106f8473773cbe078209497",
	"ModelDatum":          "72e407712c0ac3c06ce382159711fe08adb9b39289c9159fff02611eb1691ba4",
	"ModelDetail":         "394e02d471c0747da61d4966ecbfb41e347a64ce7634279008cfe54f2cd97d78",
	"ModelEvent":          "d2b1036802368d1aa3a40da0803acdd0d0198232e135d0f675ba9572cf57a12b",
	"ModelEventField":     "2c05efa6aceb541031f9ce7f423ae395072fda040fd83d161710b10f4c80b9fa",
	"ModelFinding":        "6fb0861d22be4fb43bfd821e0c3d482c0eba19160d7895be276346c7b1fc0cfc",
	"ModelHash":           "e75320c0900ac345da0645531df8a0eb91c782babd0b69af3d65493788f1f26f",
	"ModelLocation":       "bed552c944b056a95aad8fabc0854feddf8f4d6dc650a88f3198acc027386455",
	"ModelOutcome":        "1050602bd7a9d1613b2b658c2d6f0b75035b4c7c972b6e4259e16c5875fad77a",
	"ModelPhys":           "93c4ad127b9ff6c1eefd0fc1bea25fe5a1c73cd93d0c962e52c78dfedc9ea4b4",
	"ModelPlacement":      "96757a4f6c5c4eade7f99ed87408181cc8b57b77f91f4d6268e8628243e43ab7",
	"ModelReplayEvent":    "172aa282ddf19fd83e9d33dcd30507eaaee1c3baae1b538285a9fba0bb8da3d2",
	"ModelRowAt":          "ae0d8dfcfa84ce23344e7fe0b8e0796438a89a94602abaf513c6f960ca6a4d15",
	"ModelSeen":           "5abf7c53037f9668100d99eab5f14624f413053fcdc635e868cbbf74d16b7fd0",
	"ModelState":          "d3e61b98e1f0c94f070e8500f06e1661c6c9d1984b3979e60ce4b190fbf5b832",
	"ModelStep":           "30eca37fa922e407313c61b800281aaa5c2719cc3b4bcb05c83aef0162ef86fe",
	"ModelStepResult":     "80a286a61c9f525af3cbbd324b5440a173329d36b7cfbc089194707999adfc74",
	"ModelTrace":          "1bcd77648c45728339046aed5244698913848eefcb6a042b6419b223f5bd4c56",
	"ModelTraceStep":      "0b58afe1c44515d9cf7e5ebc5c2afcc8d40a1daffe14d331c5d9205c36bbf4c3",
	"ModelViolation":      "c2561c5cbfeb2520c5bad01d6939b72440cae2503fb7e9824cf8922369677c81",
}

func TestModelsRecordsGolden(t *testing.T) {
	t.Parallel()

	byName := samples(t)
	t.Run("every table is in the sample set", func(t *testing.T) {
		t.Parallel()
		seen := map[string]bool{}
		for _, o := range modelOps {
			_, ok := byName[o.name]
			assert.True(t, ok, "%s has a sample", o.name)
			seen[o.name] = true
		}
		assert.Len(t, modelOps, 25)
		assert.Len(t, seen, 25)
		for _, o := range modelOps {
			assert.NotEmpty(t, o.fields, o.name)
			for _, f := range o.fields {
				// no fixed table: every list is open and every text unbounded
				assert.True(t, openBound(f), "%s.%s declares no bound the code does not enforce", o.name, f.Name)
			}
		}
	})

	names := sortedKeys(byName)
	for _, name := range names {
		s := byName[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wire := roundTrip(t, s)
			damaged(t, s, wire)
			if !strings.Contains(name, "/") {
				assert.Equal(t, pinned[name], wireDigest(wire), "%s: the wire of the sample is pinned", name)
			}
		})
	}

	t.Run("a trace has no bound", testTraceHasNoBound)
	t.Run("the one bound the code enforces", testRecordBounds)
}

func testTraceHasNoBound(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		n, nameLen   int
		wantModuleOK bool
	}{
		{"the shipped capture, 34 modules", 34, 40, true},
		{"77 modules, as tla/ holds today", 77, 40, true},
		{"200 modules", 200, 64, true},
		{"200 modules with names of 4096 bytes", 200, 4096, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := bigTrace(t, c.n, c.nameLen)
			var o ops
			for _, x := range modelOps {
				if x.name == "ModelTrace" {
					o = x
				}
			}
			s := sample{o, traceJSON(tr)}
			wire := roundTrip(t, s)
			// the trace read back holds every module, in the sorted order, whole
			var rep TableReport
			canon, ok := o.loadWire(wire, &rep)
			require.True(t, ok)
			var got struct {
				ModelSha256 []struct{ Module, Sha256 string } `json:"modelSha256"`
				Steps       []json.RawMessage                 `json:"steps"`
			}
			require.NoError(t, json.Unmarshal(canon, &got))
			require.Len(t, got.ModelSha256, c.n)
			require.Len(t, got.Steps, 32)
			want := sortedKeys(tr.ModelSHA256)
			for i, h := range got.ModelSha256 {
				require.Equal(t, want[i], h.Module)
				require.Equal(t, tr.ModelSHA256[want[i]], h.Sha256)
				require.Len(t, h.Module, c.nameLen)
			}
		})
	}
}

func testRecordBounds(t *testing.T) {
	t.Parallel()
	var step ops
	for _, x := range modelOps {
		if x.name == "ModelStep" {
			step = x
		}
	}
	workers := step.field(t, "workers")
	require.False(t, workers.HasRange, "workers declares no range: RunSteps and tlc.Run take any value")

	base := func(w int64) obj {
		return obj{"name": "MCTableMachine", "module": "MCTableMachine.tla", "workers": w, "kind": "pass", "property": "", "met": "pass"}
	}
	rd := schemaread.NewReader(step.codec(t))
	write := func(w int64, form schemaread.Form) ([]byte, error) {
		return rd.Write(&doc{canon: marshal(t, base(w))}, form)
	}

	// any int64 is accepted in every form
	for _, w := range []int64{0, -1, 1, 2, 1024, math.MinInt64, math.MaxInt64} {
		for _, form := range forms {
			img, err := write(w, form)
			require.NoError(t, err, "workers %d %s", w, form)
			got, _, err := rd.Read(img)
			require.NoError(t, err, "workers %d %s", w, form)
			assert.Equal(t, normal(t, base(w)), normal(t, got.canon))
		}
	}

	// the counts of a form are per form
	assert.Equal(t, uint64(0), rd.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(7), rd.Counts().Reads(schemaread.FormBinary))

	// past int64 is refused by name in the JSON form
	_, _, err := rd.Read([]byte(`{"workers": 9223372036854775808, "name": "x"}`))
	reason := refusal(t, step.name, err)
	assert.Contains(t, []string{"malformed", "clamped", "kind_mismatch"}, reason, "a number past int64")

	// the schema declares no range: no text and no list carries a bound, and
	// no integer carries a range
	for _, o := range modelOps {
		for _, f := range o.fields {
			assert.False(t, f.HasRange, "%s.%s declares a range", o.name, f.Name)
			assert.True(t, openBound(f), "%s.%s declares a bound", o.name, f.Name)
		}
	}
}
