// Package recordsread is the typed half of the table model's record readers
// (internal/tablemodel records_dualread.go): it loads each record from the
// generated tables of schemas/tablemodel (pkg/tablemodelwire), from the table
// wire or from the schema JSON text, and hands internal/tablemodel the
// record's canonical JSON. internal/tablemodel imports no generated table
// (pkg/tablemodelwire's golden test imports it), so a program that reads the
// typed forms imports this package, whose init installs the tables
// (tablemodel.InstallRecordWire), as pkg/config/rowsread does for pkg/config.
//
// Every table here is a variable table (the owner's rule S1, tablemodel.schema
// says why); the base64 of an image is decoded by pkg/schemaread before a load
// is called (S2). A load the generated table refuses, or one it clamps, is
// handed back with its report, and the strict read refuses it by name.
package recordsread

import (
	"errors"

	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	tw "github.com/mas-bandwidth/nova-tools/pkg/tablemodelwire"
)

func init() {
	tablemodel.InstallRecordWire(tablemodel.RecordWire{
		State: table[tw.ModelStateBuilder, tw.ModelState]{
			(*tw.ModelStateBuilder).Init, (*tw.ModelStateBuilder).Shutdown, (*tw.ModelStateBuilder).Lock, (*tw.ModelStateBuilder).AsConst,
			tw.ModelStateLoadBuilder, tw.ModelStateFromJson, tw.ModelStateToJsonMeasure, tw.ModelStateToJson,
			tw.ModelStateMeasure, tw.ModelStateSave, tw.ModelStateLoadMeasureReason,
		}.record(),
		Trace: table[tw.ModelTraceBuilder, tw.ModelTrace]{
			(*tw.ModelTraceBuilder).Init, (*tw.ModelTraceBuilder).Shutdown, (*tw.ModelTraceBuilder).Lock, (*tw.ModelTraceBuilder).AsConst,
			tw.ModelTraceLoadBuilder, tw.ModelTraceFromJson, tw.ModelTraceToJsonMeasure, tw.ModelTraceToJson,
			tw.ModelTraceMeasure, tw.ModelTraceSave, tw.ModelTraceLoadMeasureReason,
		}.record(),
		Action: table[tw.ModelActionBuilder, tw.ModelAction]{
			(*tw.ModelActionBuilder).Init, (*tw.ModelActionBuilder).Shutdown, (*tw.ModelActionBuilder).Lock, (*tw.ModelActionBuilder).AsConst,
			tw.ModelActionLoadBuilder, tw.ModelActionFromJson, tw.ModelActionToJsonMeasure, tw.ModelActionToJson,
			tw.ModelActionMeasure, tw.ModelActionSave, tw.ModelActionLoadMeasureReason,
		}.record(),
		Event: table[tw.ModelEventBuilder, tw.ModelEvent]{
			(*tw.ModelEventBuilder).Init, (*tw.ModelEventBuilder).Shutdown, (*tw.ModelEventBuilder).Lock, (*tw.ModelEventBuilder).AsConst,
			tw.ModelEventLoadBuilder, tw.ModelEventFromJson, tw.ModelEventToJsonMeasure, tw.ModelEventToJson,
			tw.ModelEventMeasure, tw.ModelEventSave, tw.ModelEventLoadMeasureReason,
		}.record(),
		Finding: table[tw.ModelFindingBuilder, tw.ModelFinding]{
			(*tw.ModelFindingBuilder).Init, (*tw.ModelFindingBuilder).Shutdown, (*tw.ModelFindingBuilder).Lock, (*tw.ModelFindingBuilder).AsConst,
			tw.ModelFindingLoadBuilder, tw.ModelFindingFromJson, tw.ModelFindingToJsonMeasure, tw.ModelFindingToJson,
			tw.ModelFindingMeasure, tw.ModelFindingSave, tw.ModelFindingLoadMeasureReason,
		}.record(),
		ReplayEvent: table[tw.ModelReplayEventBuilder, tw.ModelReplayEvent]{
			(*tw.ModelReplayEventBuilder).Init, (*tw.ModelReplayEventBuilder).Shutdown, (*tw.ModelReplayEventBuilder).Lock, (*tw.ModelReplayEventBuilder).AsConst,
			tw.ModelReplayEventLoadBuilder, tw.ModelReplayEventFromJson, tw.ModelReplayEventToJsonMeasure, tw.ModelReplayEventToJson,
			tw.ModelReplayEventMeasure, tw.ModelReplayEventSave, tw.ModelReplayEventLoadMeasureReason,
		}.record(),
	})
}

// table is one generated table's functions, B its builder and M its row.
type table[B, M any] struct {
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

func (t table[B, M]) record() tablemodel.RecordTable {
	return tablemodel.RecordTable{
		Measure: func(wire []byte) error {
			_, err := t.loadMeasure(wire)
			return err
		},
		Load: func(wire []byte) ([]byte, schemaread.Report) {
			return t.canon(func(b *B, rep *tw.TableReport) bool { return t.load(b, wire, rep) })
		},
		LoadJSON: func(text []byte) ([]byte, schemaread.Report) {
			return t.canon(func(b *B, rep *tw.TableReport) bool { return t.fromJSON(b, text, rep) })
		},
		Save: t.image,
	}
}

// report is the generated report reduced to the strict read's.
func report(rep tw.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == tw.TableOpenRefused || rep.Verdict == tw.TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

// canon fills a fresh builder and writes the locked value as the table's
// canonical JSON; nil JSON when the fill or the lock is refused.
func (t table[B, M]) canon(fill func(*B, *tw.TableReport) bool) ([]byte, schemaread.Report) {
	var rep tw.TableReport
	b := new(B)
	if !t.init(b) {
		return nil, schemaread.Report{Malformed: true}
	}
	defer t.shutdown(b)
	if !fill(b, &rep) || !t.lock(b) {
		return nil, report(rep, false)
	}
	root := t.asConst(b)
	n := t.jsonMeasure(root)
	if n < 0 {
		return nil, schemaread.Report{Malformed: true}
	}
	buf := make([]byte, n)
	if t.toJSON(root, buf) != n {
		return nil, schemaread.Report{Malformed: true}
	}
	return buf, report(rep, true)
}

// image is the binary image of a canonical JSON. A value the table cannot
// hold as it is (a clamp, a kind mismatch, an unknown field) builds none.
func (t table[B, M]) image(canon []byte) ([]byte, error) {
	b := new(B)
	if !t.init(b) {
		return nil, errors.New("cannot start the builder")
	}
	defer t.shutdown(b)
	var rep tw.TableReport
	if !t.fromJSON(b, canon, &rep) || !t.lock(b) || rep.Clamped > 0 || rep.KindMismatch > 0 || rep.Unknown > 0 {
		return nil, errors.New("the value builds no image")
	}
	root := t.asConst(b)
	n := t.measure(root)
	if n < 0 {
		return nil, errors.New("the image has no wire")
	}
	buf := make([]byte, n)
	if t.save(root, buf) != n {
		return nil, errors.New("the wire is not writable")
	}
	return buf, nil
}
