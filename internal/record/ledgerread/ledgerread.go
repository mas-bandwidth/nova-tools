// Package ledgerread installs the generated tables of schemas/ledger
// (pkg/ledgerwire) into the record ledger (internal/record) and the cairn log
// (internal/cairn). Neither imports a generated table, because pkg/ledgerwire's
// golden test imports both; a program that reads the table forms of the
// ledger keys and values and the cairn log lines imports this package for its
// init, as a database/sql driver registers (internal/record/tablejson).
//
// Every table here is a VARIABLE table (schemas/ledger/ledger.schema: each
// record holds unbounded text); the base64 of an image is decoded by
// pkg/schemaread before a load is called (the owner's S2).
package ledgerread

import (
	"errors"

	"github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/record/tablejson"
	"github.com/mas-bandwidth/nova-tools/pkg/ledgerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

func init() {
	record.LedgerKeyWire.Install(wireOf(ledgerwire.LedgerKeyLoadMeasureReason, ledgerwire.LedgerKeyLoadBuilder, ledgerwire.LedgerKeyFromJson,
		ledgerwire.LedgerKeyMeasure, ledgerwire.LedgerKeySave, ledgerwire.LedgerKeyToJsonMeasure, ledgerwire.LedgerKeyToJson))
	record.LedgerValueWire.Install(wireOf(ledgerwire.LedgerValueLoadMeasureReason, ledgerwire.LedgerValueLoadBuilder, ledgerwire.LedgerValueFromJson,
		ledgerwire.LedgerValueMeasure, ledgerwire.LedgerValueSave, ledgerwire.LedgerValueToJsonMeasure, ledgerwire.LedgerValueToJson))
	cairn.LogRecordWire.Install(wireOf(ledgerwire.CairnLogRecordLoadMeasureReason, ledgerwire.CairnLogRecordLoadBuilder, ledgerwire.CairnLogRecordFromJson,
		ledgerwire.CairnLogRecordMeasure, ledgerwire.CairnLogRecordSave, ledgerwire.CairnLogRecordToJsonMeasure, ledgerwire.CairnLogRecordToJson))
}

// builder is the generated Builder's pointer interface.
type builder[T, B any] interface {
	*B
	Init(...ledgerwire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Shutdown()
}

// errWrite is a value the generated writer would not write.
var errWrite = errors.New(schemaread.ReasonMalformed)

// reportOf is the generated TableReport as the strict read decides on it: a
// refused or damaged open, and a missing required field, name their reason.
func reportOf(rep *ledgerwire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped),
		Malformed: rep.Malformed || rep.Duplicate > 0 || !ok}
	if rep.Verdict == ledgerwire.TableOpenRefused || rep.Verdict == ledgerwire.TableOpenDamaged || rep.MissingRequired > 0 {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}

// wireOf is one table's Wire: each load into a fresh builder, written out
// through the generated writers, the builder shut down before it returns.
func wireOf[T, B any, PB builder[T, B]](
	measure func([]byte) (int64, error),
	loadWire, fromJSON func(PB, []byte, *ledgerwire.TableReport) bool,
	wireMeasure func(*T, ...ledgerwire.TableWriteContext) int64, wireSave func(*T, []byte, ...ledgerwire.TableWriteContext) int64,
	jsonMeasure func(*T, ...ledgerwire.TableAllocator) int64, jsonSave func(*T, []byte, ...ledgerwire.TableAllocator) int64,
) tablejson.Wire {
	load := func(in []byte, load func(PB, []byte, *ledgerwire.TableReport) bool, out func(*T) []byte) ([]byte, schemaread.Report) {
		b := PB(new(B))
		if !b.Init() {
			return nil, schemaread.Report{Malformed: true}
		}
		defer b.Shutdown()
		var rep ledgerwire.TableReport
		ok := load(b, in, &rep)
		r := reportOf(&rep, ok)
		if !ok || r.Reason != "" || r.Malformed || !b.Lock() {
			r.Malformed = r.Malformed || r.Reason == ""
			return nil, r
		}
		v := b.AsConst()
		if v == nil {
			return nil, schemaread.Report{Malformed: true}
		}
		text := out(v)
		if text == nil {
			r.Malformed = true
		}
		return text, r
	}
	asJSON := func(v *T) []byte {
		n := jsonMeasure(v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if jsonSave(v, buf) != n {
			return nil
		}
		return buf
	}
	asWire := func(v *T) []byte {
		n := wireMeasure(v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if wireSave(v, buf) != n {
			return nil
		}
		return buf
	}
	return tablejson.Wire{
		Measure:  func(wire []byte) error { _, err := measure(wire); return err },
		WireJSON: func(wire []byte) ([]byte, schemaread.Report) { return load(wire, loadWire, asJSON) },
		JSON:     func(text []byte) ([]byte, schemaread.Report) { return load(text, fromJSON, asJSON) },
		JSONWire: func(text []byte) ([]byte, error) {
			wire, rep := load(text, fromJSON, asWire)
			if wire == nil || rep.Reason != "" || rep.Malformed || rep.Clamped > 0 || rep.KindMismatch > 0 {
				return nil, errWrite
			}
			return wire, nil
		},
	}
}
