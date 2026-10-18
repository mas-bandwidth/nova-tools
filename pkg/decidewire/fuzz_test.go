package decidewire

// FuzzDecidewire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzDecidewire$' -fuzztime=5m ./pkg/decidewire
// An input is [root selector byte][bytes].

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fuzzForm int

const (
	fuzzWire fuzzForm = iota
	fuzzBase64
	fuzzJSON
	fuzzEmpty // run on a reset value, no input
)

// fuzzRoot is one generated root; run reads in as the form and returns the
// accepted value's wire and JSON images.
type fuzzRoot struct {
	name string
	run  func(f fuzzForm, in []byte, rep *TableReport) (wire, text []byte, ok bool)
}

func (r fuzzRoot) load(f fuzzForm, in []byte) (wire, text []byte, rep TableReport, err error) {
	if f == fuzzBase64 {
		if in, err = base64.StdEncoding.DecodeString(string(in)); err != nil {
			return nil, nil, rep, TableRefuseWireDamaged
		}
	}
	wire, text, ok := r.run(f, in, &rep)
	if err = rep.Err(); err == nil && !ok {
		err = TableRefuseWireDamaged
	}
	return wire, text, rep, err
}

func fuzzB64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func fuzzImage(n int64, save func([]byte) int64) []byte {
	if n < 0 {
		return nil
	}
	b := make([]byte, n)
	if save(b) != n {
		return nil
	}
	return b
}

// fuzzBuilder is a variable table read through its Builder (Lock packs it).
func fuzzBuilder[B any, PB interface {
	*B
	Init(...TableAllocator) bool
	Shutdown()
	Lock() bool
	AsConst() *R
}, R any](name string, loadWire, fromJSON func(PB, []byte, *TableReport) bool, wire, text func(*R) []byte) fuzzRoot {
	return fuzzRoot{name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
		var b B
		pb := PB(&b)
		if !pb.Init() {
			return nil, nil, false
		}
		defer pb.Shutdown()
		ok := f == fuzzEmpty || (f == fuzzJSON && fromJSON(pb, in, rep)) || (f != fuzzJSON && loadWire(pb, in, rep))
		if ok && pb.Lock() {
			return wire(pb.AsConst()), text(pb.AsConst()), true
		}
		return nil, nil, false
	}}
}

// END builder
// fuzzValue is a fixed table, read into a plain value.
func fuzzValue[R any](name string, loadWire, fromJSON func(*R, []byte, *TableReport) bool, wire, text func(*R) []byte) fuzzRoot {
	return fuzzRoot{name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
		var v R
		ok := f == fuzzEmpty || (f == fuzzJSON && fromJSON(&v, in, rep)) || (f != fuzzJSON && loadWire(&v, in, rep))
		if ok {
			return wire(&v), text(&v), true
		}
		return nil, nil, false
	}}
}

// fuzzRoots is every generated root, written by roots.sh.
var fuzzRoots = []fuzzRoot{
	fuzzBuilder("Act", func(b *ActBuilder, w []byte, r *TableReport) bool { return ActLoadBuilder(b, w, r) }, func(b *ActBuilder, w []byte, r *TableReport) bool { return ActFromJson(b, w, r) }, func(v *Act) []byte { return fuzzImage(ActMeasure(v), func(b []byte) int64 { return ActSave(v, b) }) }, func(v *Act) []byte {
		return fuzzImage(ActToJsonMeasure(v), func(b []byte) int64 { return ActToJson(v, b) })
	}),
	fuzzBuilder("Answer", func(b *AnswerBuilder, w []byte, r *TableReport) bool { return AnswerLoadBuilder(b, w, r) }, func(b *AnswerBuilder, w []byte, r *TableReport) bool { return AnswerFromJson(b, w, r) }, func(v *Answer) []byte {
		return fuzzImage(AnswerMeasure(v), func(b []byte) int64 { return AnswerSave(v, b) })
	}, func(v *Answer) []byte {
		return fuzzImage(AnswerToJsonMeasure(v), func(b []byte) int64 { return AnswerToJson(v, b) })
	}),
	fuzzValue("Bars", func(v *Bars, w []byte, r *TableReport) bool { return BarsLoad(v, w, r) }, func(v *Bars, w []byte, r *TableReport) bool { return BarsFromJson(v, w, r) }, func(v *Bars) []byte { return fuzzImage(BarsMeasure(v), func(b []byte) int64 { return BarsSave(v, b) }) }, func(v *Bars) []byte {
		return fuzzImage(BarsToJsonMeasure(v), func(b []byte) int64 { return BarsToJson(v, b) })
	}),
	fuzzBuilder("Chosen", func(b *ChosenBuilder, w []byte, r *TableReport) bool { return ChosenLoadBuilder(b, w, r) }, func(b *ChosenBuilder, w []byte, r *TableReport) bool { return ChosenFromJson(b, w, r) }, func(v *Chosen) []byte {
		return fuzzImage(ChosenMeasure(v), func(b []byte) int64 { return ChosenSave(v, b) })
	}, func(v *Chosen) []byte {
		return fuzzImage(ChosenToJsonMeasure(v), func(b []byte) int64 { return ChosenToJson(v, b) })
	}),
	fuzzBuilder("ClusterCard", func(b *ClusterCardBuilder, w []byte, r *TableReport) bool { return ClusterCardLoadBuilder(b, w, r) }, func(b *ClusterCardBuilder, w []byte, r *TableReport) bool { return ClusterCardFromJson(b, w, r) }, func(v *ClusterCard) []byte {
		return fuzzImage(ClusterCardMeasure(v), func(b []byte) int64 { return ClusterCardSave(v, b) })
	}, func(v *ClusterCard) []byte {
		return fuzzImage(ClusterCardToJsonMeasure(v), func(b []byte) int64 { return ClusterCardToJson(v, b) })
	}),
	fuzzBuilder("Cluster", func(b *ClusterBuilder, w []byte, r *TableReport) bool { return ClusterLoadBuilder(b, w, r) }, func(b *ClusterBuilder, w []byte, r *TableReport) bool { return ClusterFromJson(b, w, r) }, func(v *Cluster) []byte {
		return fuzzImage(ClusterMeasure(v), func(b []byte) int64 { return ClusterSave(v, b) })
	}, func(v *Cluster) []byte {
		return fuzzImage(ClusterToJsonMeasure(v), func(b []byte) int64 { return ClusterToJson(v, b) })
	}),
	fuzzBuilder("Criterion", func(b *CriterionBuilder, w []byte, r *TableReport) bool { return CriterionLoadBuilder(b, w, r) }, func(b *CriterionBuilder, w []byte, r *TableReport) bool { return CriterionFromJson(b, w, r) }, func(v *Criterion) []byte {
		return fuzzImage(CriterionMeasure(v), func(b []byte) int64 { return CriterionSave(v, b) })
	}, func(v *Criterion) []byte {
		return fuzzImage(CriterionToJsonMeasure(v), func(b []byte) int64 { return CriterionToJson(v, b) })
	}),
	fuzzBuilder("Decided", func(b *DecidedBuilder, w []byte, r *TableReport) bool { return DecidedLoadBuilder(b, w, r) }, func(b *DecidedBuilder, w []byte, r *TableReport) bool { return DecidedFromJson(b, w, r) }, func(v *Decided) []byte {
		return fuzzImage(DecidedMeasure(v), func(b []byte) int64 { return DecidedSave(v, b) })
	}, func(v *Decided) []byte {
		return fuzzImage(DecidedToJsonMeasure(v), func(b []byte) int64 { return DecidedToJson(v, b) })
	}),
	fuzzBuilder("DecideSchema", func(b *DecideSchemaBuilder, w []byte, r *TableReport) bool { return DecideSchemaLoadBuilder(b, w, r) }, func(b *DecideSchemaBuilder, w []byte, r *TableReport) bool { return DecideSchemaFromJson(b, w, r) }, func(v *DecideSchema) []byte {
		return fuzzImage(DecideSchemaMeasure(v), func(b []byte) int64 { return DecideSchemaSave(v, b) })
	}, func(v *DecideSchema) []byte {
		return fuzzImage(DecideSchemaToJsonMeasure(v), func(b []byte) int64 { return DecideSchemaToJson(v, b) })
	}),
	fuzzBuilder("Decision", func(b *DecisionBuilder, w []byte, r *TableReport) bool { return DecisionLoadBuilder(b, w, r) }, func(b *DecisionBuilder, w []byte, r *TableReport) bool { return DecisionFromJson(b, w, r) }, func(v *Decision) []byte {
		return fuzzImage(DecisionMeasure(v), func(b []byte) int64 { return DecisionSave(v, b) })
	}, func(v *Decision) []byte {
		return fuzzImage(DecisionToJsonMeasure(v), func(b []byte) int64 { return DecisionToJson(v, b) })
	}),
	fuzzBuilder("Example", func(b *ExampleBuilder, w []byte, r *TableReport) bool { return ExampleLoadBuilder(b, w, r) }, func(b *ExampleBuilder, w []byte, r *TableReport) bool { return ExampleFromJson(b, w, r) }, func(v *Example) []byte {
		return fuzzImage(ExampleMeasure(v), func(b []byte) int64 { return ExampleSave(v, b) })
	}, func(v *Example) []byte {
		return fuzzImage(ExampleToJsonMeasure(v), func(b []byte) int64 { return ExampleToJson(v, b) })
	}),
	fuzzBuilder("Failure", func(b *FailureBuilder, w []byte, r *TableReport) bool { return FailureLoadBuilder(b, w, r) }, func(b *FailureBuilder, w []byte, r *TableReport) bool { return FailureFromJson(b, w, r) }, func(v *Failure) []byte {
		return fuzzImage(FailureMeasure(v), func(b []byte) int64 { return FailureSave(v, b) })
	}, func(v *Failure) []byte {
		return fuzzImage(FailureToJsonMeasure(v), func(b []byte) int64 { return FailureToJson(v, b) })
	}),
	fuzzBuilder("FixedAnswer", func(b *FixedAnswerBuilder, w []byte, r *TableReport) bool { return FixedAnswerLoadBuilder(b, w, r) }, func(b *FixedAnswerBuilder, w []byte, r *TableReport) bool { return FixedAnswerFromJson(b, w, r) }, func(v *FixedAnswer) []byte {
		return fuzzImage(FixedAnswerMeasure(v), func(b []byte) int64 { return FixedAnswerSave(v, b) })
	}, func(v *FixedAnswer) []byte {
		return fuzzImage(FixedAnswerToJsonMeasure(v), func(b []byte) int64 { return FixedAnswerToJson(v, b) })
	}),
	fuzzBuilder("FixedTable", func(b *FixedTableBuilder, w []byte, r *TableReport) bool { return FixedTableLoadBuilder(b, w, r) }, func(b *FixedTableBuilder, w []byte, r *TableReport) bool { return FixedTableFromJson(b, w, r) }, func(v *FixedTable) []byte {
		return fuzzImage(FixedTableMeasure(v), func(b []byte) int64 { return FixedTableSave(v, b) })
	}, func(v *FixedTable) []byte {
		return fuzzImage(FixedTableToJsonMeasure(v), func(b []byte) int64 { return FixedTableToJson(v, b) })
	}),
	fuzzBuilder("Frame", func(b *FrameBuilder, w []byte, r *TableReport) bool { return FrameLoadBuilder(b, w, r) }, func(b *FrameBuilder, w []byte, r *TableReport) bool { return FrameFromJson(b, w, r) }, func(v *Frame) []byte {
		return fuzzImage(FrameMeasure(v), func(b []byte) int64 { return FrameSave(v, b) })
	}, func(v *Frame) []byte {
		return fuzzImage(FrameToJsonMeasure(v), func(b []byte) int64 { return FrameToJson(v, b) })
	}),
	fuzzValue("GateBars", func(v *GateBars, w []byte, r *TableReport) bool { return GateBarsLoad(v, w, r) }, func(v *GateBars, w []byte, r *TableReport) bool { return GateBarsFromJson(v, w, r) }, func(v *GateBars) []byte {
		return fuzzImage(GateBarsMeasure(v), func(b []byte) int64 { return GateBarsSave(v, b) })
	}, func(v *GateBars) []byte {
		return fuzzImage(GateBarsToJsonMeasure(v), func(b []byte) int64 { return GateBarsToJson(v, b) })
	}),
	fuzzBuilder("GateLine", func(b *GateLineBuilder, w []byte, r *TableReport) bool { return GateLineLoadBuilder(b, w, r) }, func(b *GateLineBuilder, w []byte, r *TableReport) bool { return GateLineFromJson(b, w, r) }, func(v *GateLine) []byte {
		return fuzzImage(GateLineMeasure(v), func(b []byte) int64 { return GateLineSave(v, b) })
	}, func(v *GateLine) []byte {
		return fuzzImage(GateLineToJsonMeasure(v), func(b []byte) int64 { return GateLineToJson(v, b) })
	}),
	fuzzBuilder("ImportExport", func(b *ImportExportBuilder, w []byte, r *TableReport) bool { return ImportExportLoadBuilder(b, w, r) }, func(b *ImportExportBuilder, w []byte, r *TableReport) bool { return ImportExportFromJson(b, w, r) }, func(v *ImportExport) []byte {
		return fuzzImage(ImportExportMeasure(v), func(b []byte) int64 { return ImportExportSave(v, b) })
	}, func(v *ImportExport) []byte {
		return fuzzImage(ImportExportToJsonMeasure(v), func(b []byte) int64 { return ImportExportToJson(v, b) })
	}),
	fuzzBuilder("ImportLogLine", func(b *ImportLogLineBuilder, w []byte, r *TableReport) bool { return ImportLogLineLoadBuilder(b, w, r) }, func(b *ImportLogLineBuilder, w []byte, r *TableReport) bool { return ImportLogLineFromJson(b, w, r) }, func(v *ImportLogLine) []byte {
		return fuzzImage(ImportLogLineMeasure(v), func(b []byte) int64 { return ImportLogLineSave(v, b) })
	}, func(v *ImportLogLine) []byte {
		return fuzzImage(ImportLogLineToJsonMeasure(v), func(b []byte) int64 { return ImportLogLineToJson(v, b) })
	}),
	fuzzBuilder("Input", func(b *InputBuilder, w []byte, r *TableReport) bool { return InputLoadBuilder(b, w, r) }, func(b *InputBuilder, w []byte, r *TableReport) bool { return InputFromJson(b, w, r) }, func(v *Input) []byte {
		return fuzzImage(InputMeasure(v), func(b []byte) int64 { return InputSave(v, b) })
	}, func(v *Input) []byte {
		return fuzzImage(InputToJsonMeasure(v), func(b []byte) int64 { return InputToJson(v, b) })
	}),
	fuzzBuilder("JevAnswer", func(b *JevAnswerBuilder, w []byte, r *TableReport) bool { return JevAnswerLoadBuilder(b, w, r) }, func(b *JevAnswerBuilder, w []byte, r *TableReport) bool { return JevAnswerFromJson(b, w, r) }, func(v *JevAnswer) []byte {
		return fuzzImage(JevAnswerMeasure(v), func(b []byte) int64 { return JevAnswerSave(v, b) })
	}, func(v *JevAnswer) []byte {
		return fuzzImage(JevAnswerToJsonMeasure(v), func(b []byte) int64 { return JevAnswerToJson(v, b) })
	}),
	fuzzBuilder("JevRequest", func(b *JevRequestBuilder, w []byte, r *TableReport) bool { return JevRequestLoadBuilder(b, w, r) }, func(b *JevRequestBuilder, w []byte, r *TableReport) bool { return JevRequestFromJson(b, w, r) }, func(v *JevRequest) []byte {
		return fuzzImage(JevRequestMeasure(v), func(b []byte) int64 { return JevRequestSave(v, b) })
	}, func(v *JevRequest) []byte {
		return fuzzImage(JevRequestToJsonMeasure(v), func(b []byte) int64 { return JevRequestToJson(v, b) })
	}),
	fuzzBuilder("JevResponse", func(b *JevResponseBuilder, w []byte, r *TableReport) bool { return JevResponseLoadBuilder(b, w, r) }, func(b *JevResponseBuilder, w []byte, r *TableReport) bool { return JevResponseFromJson(b, w, r) }, func(v *JevResponse) []byte {
		return fuzzImage(JevResponseMeasure(v), func(b []byte) int64 { return JevResponseSave(v, b) })
	}, func(v *JevResponse) []byte {
		return fuzzImage(JevResponseToJsonMeasure(v), func(b []byte) int64 { return JevResponseToJson(v, b) })
	}),
	fuzzBuilder("JudgmentId", func(b *JudgmentIdBuilder, w []byte, r *TableReport) bool { return JudgmentIdLoadBuilder(b, w, r) }, func(b *JudgmentIdBuilder, w []byte, r *TableReport) bool { return JudgmentIdFromJson(b, w, r) }, func(v *JudgmentId) []byte {
		return fuzzImage(JudgmentIdMeasure(v), func(b []byte) int64 { return JudgmentIdSave(v, b) })
	}, func(v *JudgmentId) []byte {
		return fuzzImage(JudgmentIdToJsonMeasure(v), func(b []byte) int64 { return JudgmentIdToJson(v, b) })
	}),
	fuzzBuilder("LogExport", func(b *LogExportBuilder, w []byte, r *TableReport) bool { return LogExportLoadBuilder(b, w, r) }, func(b *LogExportBuilder, w []byte, r *TableReport) bool { return LogExportFromJson(b, w, r) }, func(v *LogExport) []byte {
		return fuzzImage(LogExportMeasure(v), func(b []byte) int64 { return LogExportSave(v, b) })
	}, func(v *LogExport) []byte {
		return fuzzImage(LogExportToJsonMeasure(v), func(b []byte) int64 { return LogExportToJson(v, b) })
	}),
	fuzzBuilder("LogLine", func(b *LogLineBuilder, w []byte, r *TableReport) bool { return LogLineLoadBuilder(b, w, r) }, func(b *LogLineBuilder, w []byte, r *TableReport) bool { return LogLineFromJson(b, w, r) }, func(v *LogLine) []byte {
		return fuzzImage(LogLineMeasure(v), func(b []byte) int64 { return LogLineSave(v, b) })
	}, func(v *LogLine) []byte {
		return fuzzImage(LogLineToJsonMeasure(v), func(b []byte) int64 { return LogLineToJson(v, b) })
	}),
	fuzzBuilder("NamedAnswer", func(b *NamedAnswerBuilder, w []byte, r *TableReport) bool { return NamedAnswerLoadBuilder(b, w, r) }, func(b *NamedAnswerBuilder, w []byte, r *TableReport) bool { return NamedAnswerFromJson(b, w, r) }, func(v *NamedAnswer) []byte {
		return fuzzImage(NamedAnswerMeasure(v), func(b []byte) int64 { return NamedAnswerSave(v, b) })
	}, func(v *NamedAnswer) []byte {
		return fuzzImage(NamedAnswerToJsonMeasure(v), func(b []byte) int64 { return NamedAnswerToJson(v, b) })
	}),
	fuzzBuilder("NamedFixedAnswer", func(b *NamedFixedAnswerBuilder, w []byte, r *TableReport) bool {
		return NamedFixedAnswerLoadBuilder(b, w, r)
	}, func(b *NamedFixedAnswerBuilder, w []byte, r *TableReport) bool {
		return NamedFixedAnswerFromJson(b, w, r)
	}, func(v *NamedFixedAnswer) []byte {
		return fuzzImage(NamedFixedAnswerMeasure(v), func(b []byte) int64 { return NamedFixedAnswerSave(v, b) })
	}, func(v *NamedFixedAnswer) []byte {
		return fuzzImage(NamedFixedAnswerToJsonMeasure(v), func(b []byte) int64 { return NamedFixedAnswerToJson(v, b) })
	}),
	fuzzBuilder("NamedJevAnswer", func(b *NamedJevAnswerBuilder, w []byte, r *TableReport) bool {
		return NamedJevAnswerLoadBuilder(b, w, r)
	}, func(b *NamedJevAnswerBuilder, w []byte, r *TableReport) bool { return NamedJevAnswerFromJson(b, w, r) }, func(v *NamedJevAnswer) []byte {
		return fuzzImage(NamedJevAnswerMeasure(v), func(b []byte) int64 { return NamedJevAnswerSave(v, b) })
	}, func(v *NamedJevAnswer) []byte {
		return fuzzImage(NamedJevAnswerToJsonMeasure(v), func(b []byte) int64 { return NamedJevAnswerToJson(v, b) })
	}),
	fuzzBuilder("NamedQuestion", func(b *NamedQuestionBuilder, w []byte, r *TableReport) bool { return NamedQuestionLoadBuilder(b, w, r) }, func(b *NamedQuestionBuilder, w []byte, r *TableReport) bool { return NamedQuestionFromJson(b, w, r) }, func(v *NamedQuestion) []byte {
		return fuzzImage(NamedQuestionMeasure(v), func(b []byte) int64 { return NamedQuestionSave(v, b) })
	}, func(v *NamedQuestion) []byte {
		return fuzzImage(NamedQuestionToJsonMeasure(v), func(b []byte) int64 { return NamedQuestionToJson(v, b) })
	}),
	fuzzBuilder("OptionP", func(b *OptionPBuilder, w []byte, r *TableReport) bool { return OptionPLoadBuilder(b, w, r) }, func(b *OptionPBuilder, w []byte, r *TableReport) bool { return OptionPFromJson(b, w, r) }, func(v *OptionP) []byte {
		return fuzzImage(OptionPMeasure(v), func(b []byte) int64 { return OptionPSave(v, b) })
	}, func(v *OptionP) []byte {
		return fuzzImage(OptionPToJsonMeasure(v), func(b []byte) int64 { return OptionPToJson(v, b) })
	}),
	fuzzBuilder("Outcome", func(b *OutcomeBuilder, w []byte, r *TableReport) bool { return OutcomeLoadBuilder(b, w, r) }, func(b *OutcomeBuilder, w []byte, r *TableReport) bool { return OutcomeFromJson(b, w, r) }, func(v *Outcome) []byte {
		return fuzzImage(OutcomeMeasure(v), func(b []byte) int64 { return OutcomeSave(v, b) })
	}, func(v *Outcome) []byte {
		return fuzzImage(OutcomeToJsonMeasure(v), func(b []byte) int64 { return OutcomeToJson(v, b) })
	}),
	fuzzBuilder("Question", func(b *QuestionBuilder, w []byte, r *TableReport) bool { return QuestionLoadBuilder(b, w, r) }, func(b *QuestionBuilder, w []byte, r *TableReport) bool { return QuestionFromJson(b, w, r) }, func(v *Question) []byte {
		return fuzzImage(QuestionMeasure(v), func(b []byte) int64 { return QuestionSave(v, b) })
	}, func(v *Question) []byte {
		return fuzzImage(QuestionToJsonMeasure(v), func(b []byte) int64 { return QuestionToJson(v, b) })
	}),
	fuzzBuilder("RawRecord", func(b *RawRecordBuilder, w []byte, r *TableReport) bool { return RawRecordLoadBuilder(b, w, r) }, func(b *RawRecordBuilder, w []byte, r *TableReport) bool { return RawRecordFromJson(b, w, r) }, func(v *RawRecord) []byte {
		return fuzzImage(RawRecordMeasure(v), func(b []byte) int64 { return RawRecordSave(v, b) })
	}, func(v *RawRecord) []byte {
		return fuzzImage(RawRecordToJsonMeasure(v), func(b []byte) int64 { return RawRecordToJson(v, b) })
	}),
	fuzzValue("RawTokens", func(v *RawTokens, w []byte, r *TableReport) bool { return RawTokensLoad(v, w, r) }, func(v *RawTokens, w []byte, r *TableReport) bool { return RawTokensFromJson(v, w, r) }, func(v *RawTokens) []byte {
		return fuzzImage(RawTokensMeasure(v), func(b []byte) int64 { return RawTokensSave(v, b) })
	}, func(v *RawTokens) []byte {
		return fuzzImage(RawTokensToJsonMeasure(v), func(b []byte) int64 { return RawTokensToJson(v, b) })
	}),
	fuzzBuilder("RawUsage", func(b *RawUsageBuilder, w []byte, r *TableReport) bool { return RawUsageLoadBuilder(b, w, r) }, func(b *RawUsageBuilder, w []byte, r *TableReport) bool { return RawUsageFromJson(b, w, r) }, func(v *RawUsage) []byte {
		return fuzzImage(RawUsageMeasure(v), func(b []byte) int64 { return RawUsageSave(v, b) })
	}, func(v *RawUsage) []byte {
		return fuzzImage(RawUsageToJsonMeasure(v), func(b []byte) int64 { return RawUsageToJson(v, b) })
	}),
	fuzzBuilder("RecordLine", func(b *RecordLineBuilder, w []byte, r *TableReport) bool { return RecordLineLoadBuilder(b, w, r) }, func(b *RecordLineBuilder, w []byte, r *TableReport) bool { return RecordLineFromJson(b, w, r) }, func(v *RecordLine) []byte {
		return fuzzImage(RecordLineMeasure(v), func(b []byte) int64 { return RecordLineSave(v, b) })
	}, func(v *RecordLine) []byte {
		return fuzzImage(RecordLineToJsonMeasure(v), func(b []byte) int64 { return RecordLineToJson(v, b) })
	}),
	fuzzBuilder("SetEntry", func(b *SetEntryBuilder, w []byte, r *TableReport) bool { return SetEntryLoadBuilder(b, w, r) }, func(b *SetEntryBuilder, w []byte, r *TableReport) bool { return SetEntryFromJson(b, w, r) }, func(v *SetEntry) []byte {
		return fuzzImage(SetEntryMeasure(v), func(b []byte) int64 { return SetEntrySave(v, b) })
	}, func(v *SetEntry) []byte {
		return fuzzImage(SetEntryToJsonMeasure(v), func(b []byte) int64 { return SetEntryToJson(v, b) })
	}),
	fuzzBuilder("StagePath", func(b *StagePathBuilder, w []byte, r *TableReport) bool { return StagePathLoadBuilder(b, w, r) }, func(b *StagePathBuilder, w []byte, r *TableReport) bool { return StagePathFromJson(b, w, r) }, func(v *StagePath) []byte {
		return fuzzImage(StagePathMeasure(v), func(b []byte) int64 { return StagePathSave(v, b) })
	}, func(v *StagePath) []byte {
		return fuzzImage(StagePathToJsonMeasure(v), func(b []byte) int64 { return StagePathToJson(v, b) })
	}),
	fuzzValue("Usage", func(v *Usage, w []byte, r *TableReport) bool { return UsageLoad(v, w, r) }, func(v *Usage, w []byte, r *TableReport) bool { return UsageFromJson(v, w, r) }, func(v *Usage) []byte {
		return fuzzImage(UsageMeasure(v), func(b []byte) int64 { return UsageSave(v, b) })
	}, func(v *Usage) []byte {
		return fuzzImage(UsageToJsonMeasure(v), func(b []byte) int64 { return UsageToJson(v, b) })
	}),
}

var fuzzSeeds = map[string]bool{}

func fuzzImages(t testing.TB, globs ...string) (out [][]byte) {
	for _, glob := range globs {
		files, _ := filepath.Glob(glob)
		for _, file := range files {
			b, err := os.ReadFile(file)
			require.NoError(t, err)
			if ext := filepath.Ext(file); ext == ".bin" || ext == ".b64" || ext == ".json" {
				out = append(out, b)
			}
		}
	}
	return
}

// fuzzSeedList is every seed as (selector, bytes): per root the reset value in
// three forms, and every image against every root as wire and base64. The
// schema corpus is this build's own; the others only read or are refused.
func fuzzSeedList(t testing.TB) (sels []uint8, ins [][]byte) {
	add := func(i int, in []byte) { sels, ins = append(sels, uint8(i)), append(ins, in) }
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "decide", "fuzz", "*")), fuzzImages(t)
	for i, root := range fuzzRoots {
		var rep TableReport
		if wire, text, _ := root.run(fuzzEmpty, nil, &rep); wire != nil && text != nil {
			fuzzSeeds[string(wire)] = true
			add(i, wire)
			add(i, fuzzB64(wire))
			add(i, text)
		}
		for n, img := range append(append([][]byte{}, corpus...), others...) {
			fuzzSeeds[string(img)] = fuzzSeeds[string(img)] || n < len(corpus)
			add(i, img)
			add(i, fuzzB64(img))
		}
	}
	return
}

func FuzzDecidewire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "decidewire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
				continue
			}
			require.NotNil(t, wire, "%s as %d %q: an accepted value does not re-encode", root.name, form, in)
			require.NotNil(t, text, "%s as %d %q: an accepted value does not re-encode", root.name, form, in)
			for _, again := range []struct {
				form fuzzForm
				in   []byte
			}{{fuzzWire, wire}, {fuzzBase64, fuzzB64(wire)}, {fuzzJSON, text}} {
				w, tx, _, err := root.load(again.form, again.in)
				require.NoError(t, err, "%s as %d %q: the %d image", root.name, form, in, again.form)
				require.Equal(t, wire, w, "%s as %d %q: the %d image does not read back to itself", root.name, form, in, again.form)
				require.Equal(t, text, tx, "%s as %d %q: the %d image does not read back to itself", root.name, form, in, again.form)
			}
			clean := rep.Unknown == 0 && rep.Widened == 0 && rep.Retained == 0 && rep.Clamped == 0 && rep.KindMismatch == 0 && rep.Duplicate == 0
			if form == fuzzWire && fuzzSeeds[string(in)] && clean {
				require.Equal(t, in, wire, "%s: an accepted seed wire does not reproduce", root.name)
			}
			require.Empty(t, fuzzNeighbours(root, wire), "%s as %d %q: an accepted value fails the neighbour oracle", root.name, form, in)
		}
	})
}

// TestWriteFuzzDecidewireSeeds writes testdata/fuzz/FuzzDecidewire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzDecidewireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzDecidewire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzDecidewire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}

// fuzzNeighbours is the input-to-output oracle for every accepted value, seed
// or not: each strict prefix of its wire is refused with a named Table* error,
// and its wire with one byte appended (0x00, then 0xff) does not read clean.
// It returns "" or what failed.
func fuzzNeighbours(root fuzzRoot, wire []byte) string {
	for n := 0; n < len(wire); n++ {
		_, _, _, err := root.load(fuzzWire, wire[:n])
		if err == nil {
			return fmt.Sprintf("%s: a strict prefix (%d of %d bytes) of an accepted wire is accepted", root.name, n, len(wire))
		}
		if !strings.HasPrefix(fmt.Sprintf("%T", err), "decidewire.Table") {
			return fmt.Sprintf("%s: a prefix refusal with no name: %T %v", root.name, err, err)
		}
	}
	for _, x := range []byte{0x00, 0xff} {
		_, _, rep, err := root.load(fuzzWire, append(append([]byte{}, wire...), x))
		if err == nil && rep.Unknown == 0 && rep.Widened == 0 && rep.Retained == 0 && rep.Clamped == 0 && rep.KindMismatch == 0 && rep.Duplicate == 0 {
			return fmt.Sprintf("%s: an accepted wire plus one byte %02x reads clean", root.name, x)
		}
	}
	return ""
}

// TestFuzzDecidewireNeighbours proves fuzzNeighbours on every root: the real reader
// passes at its reset value, and two planted readers fail, one that accepts
// the reset wire with a byte appended and one that accepts a strict prefix.
func TestFuzzDecidewireNeighbours(t *testing.T) {
	t.Parallel()
	for _, root := range fuzzRoots {
		var rep TableReport
		wire, _, ok := root.run(fuzzEmpty, nil, &rep)
		require.True(t, ok, "%s: the reset value does not encode", root.name)
		require.Empty(t, fuzzNeighbours(root, wire))
		trailing := fuzzRoot{root.name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
			if f == fuzzWire && len(in) > len(wire) && string(in[:len(wire)]) == string(wire) {
				in = wire
			}
			return root.run(f, in, rep)
		}}
		require.Contains(t, fuzzNeighbours(trailing, wire), "plus one byte", "%s: the planted trailing-byte reader passes", root.name)
		prefix := fuzzRoot{root.name, func(f fuzzForm, in []byte, rep *TableReport) ([]byte, []byte, bool) {
			if f == fuzzWire && len(in) < len(wire) && string(wire[:len(in)]) == string(in) {
				in = wire
			}
			return root.run(f, in, rep)
		}}
		require.Contains(t, fuzzNeighbours(prefix, wire), "strict prefix", "%s: the planted prefix reader passes", root.name)
	}
}
