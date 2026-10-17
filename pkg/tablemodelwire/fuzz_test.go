package tablemodelwire

// FuzzTablemodelwire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzTablemodelwire$' -fuzztime=5m ./pkg/tablemodelwire
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
	fuzzBuilder("ConvergeState", func(b *ConvergeStateBuilder, w []byte, r *TableReport) bool { return ConvergeStateLoadBuilder(b, w, r) }, func(b *ConvergeStateBuilder, w []byte, r *TableReport) bool { return ConvergeStateFromJson(b, w, r) }, func(v *ConvergeState) []byte {
		return fuzzImage(ConvergeStateMeasure(v), func(b []byte) int64 { return ConvergeStateSave(v, b) })
	}, func(v *ConvergeState) []byte {
		return fuzzImage(ConvergeStateToJsonMeasure(v), func(b []byte) int64 { return ConvergeStateToJson(v, b) })
	}),
	fuzzBuilder("ConvergeStreamEntry", func(b *ConvergeStreamEntryBuilder, w []byte, r *TableReport) bool {
		return ConvergeStreamEntryLoadBuilder(b, w, r)
	}, func(b *ConvergeStreamEntryBuilder, w []byte, r *TableReport) bool {
		return ConvergeStreamEntryFromJson(b, w, r)
	}, func(v *ConvergeStreamEntry) []byte {
		return fuzzImage(ConvergeStreamEntryMeasure(v), func(b []byte) int64 { return ConvergeStreamEntrySave(v, b) })
	}, func(v *ConvergeStreamEntry) []byte {
		return fuzzImage(ConvergeStreamEntryToJsonMeasure(v), func(b []byte) int64 { return ConvergeStreamEntryToJson(v, b) })
	}),
	fuzzBuilder("ModelAction", func(b *ModelActionBuilder, w []byte, r *TableReport) bool { return ModelActionLoadBuilder(b, w, r) }, func(b *ModelActionBuilder, w []byte, r *TableReport) bool { return ModelActionFromJson(b, w, r) }, func(v *ModelAction) []byte {
		return fuzzImage(ModelActionMeasure(v), func(b []byte) int64 { return ModelActionSave(v, b) })
	}, func(v *ModelAction) []byte {
		return fuzzImage(ModelActionToJsonMeasure(v), func(b []byte) int64 { return ModelActionToJson(v, b) })
	}),
	fuzzBuilder("ModelArg", func(b *ModelArgBuilder, w []byte, r *TableReport) bool { return ModelArgLoadBuilder(b, w, r) }, func(b *ModelArgBuilder, w []byte, r *TableReport) bool { return ModelArgFromJson(b, w, r) }, func(v *ModelArg) []byte {
		return fuzzImage(ModelArgMeasure(v), func(b []byte) int64 { return ModelArgSave(v, b) })
	}, func(v *ModelArg) []byte {
		return fuzzImage(ModelArgToJsonMeasure(v), func(b []byte) int64 { return ModelArgToJson(v, b) })
	}),
	fuzzBuilder("ModelBind", func(b *ModelBindBuilder, w []byte, r *TableReport) bool { return ModelBindLoadBuilder(b, w, r) }, func(b *ModelBindBuilder, w []byte, r *TableReport) bool { return ModelBindFromJson(b, w, r) }, func(v *ModelBind) []byte {
		return fuzzImage(ModelBindMeasure(v), func(b []byte) int64 { return ModelBindSave(v, b) })
	}, func(v *ModelBind) []byte {
		return fuzzImage(ModelBindToJsonMeasure(v), func(b []byte) int64 { return ModelBindToJson(v, b) })
	}),
	fuzzBuilder("ModelCell", func(b *ModelCellBuilder, w []byte, r *TableReport) bool { return ModelCellLoadBuilder(b, w, r) }, func(b *ModelCellBuilder, w []byte, r *TableReport) bool { return ModelCellFromJson(b, w, r) }, func(v *ModelCell) []byte {
		return fuzzImage(ModelCellMeasure(v), func(b []byte) int64 { return ModelCellSave(v, b) })
	}, func(v *ModelCell) []byte {
		return fuzzImage(ModelCellToJsonMeasure(v), func(b []byte) int64 { return ModelCellToJson(v, b) })
	}),
	fuzzBuilder("ModelDatum", func(b *ModelDatumBuilder, w []byte, r *TableReport) bool { return ModelDatumLoadBuilder(b, w, r) }, func(b *ModelDatumBuilder, w []byte, r *TableReport) bool { return ModelDatumFromJson(b, w, r) }, func(v *ModelDatum) []byte {
		return fuzzImage(ModelDatumMeasure(v), func(b []byte) int64 { return ModelDatumSave(v, b) })
	}, func(v *ModelDatum) []byte {
		return fuzzImage(ModelDatumToJsonMeasure(v), func(b []byte) int64 { return ModelDatumToJson(v, b) })
	}),
	fuzzBuilder("ModelDetail", func(b *ModelDetailBuilder, w []byte, r *TableReport) bool { return ModelDetailLoadBuilder(b, w, r) }, func(b *ModelDetailBuilder, w []byte, r *TableReport) bool { return ModelDetailFromJson(b, w, r) }, func(v *ModelDetail) []byte {
		return fuzzImage(ModelDetailMeasure(v), func(b []byte) int64 { return ModelDetailSave(v, b) })
	}, func(v *ModelDetail) []byte {
		return fuzzImage(ModelDetailToJsonMeasure(v), func(b []byte) int64 { return ModelDetailToJson(v, b) })
	}),
	fuzzBuilder("ModelEventField", func(b *ModelEventFieldBuilder, w []byte, r *TableReport) bool {
		return ModelEventFieldLoadBuilder(b, w, r)
	}, func(b *ModelEventFieldBuilder, w []byte, r *TableReport) bool {
		return ModelEventFieldFromJson(b, w, r)
	}, func(v *ModelEventField) []byte {
		return fuzzImage(ModelEventFieldMeasure(v), func(b []byte) int64 { return ModelEventFieldSave(v, b) })
	}, func(v *ModelEventField) []byte {
		return fuzzImage(ModelEventFieldToJsonMeasure(v), func(b []byte) int64 { return ModelEventFieldToJson(v, b) })
	}),
	fuzzBuilder("ModelEvent", func(b *ModelEventBuilder, w []byte, r *TableReport) bool { return ModelEventLoadBuilder(b, w, r) }, func(b *ModelEventBuilder, w []byte, r *TableReport) bool { return ModelEventFromJson(b, w, r) }, func(v *ModelEvent) []byte {
		return fuzzImage(ModelEventMeasure(v), func(b []byte) int64 { return ModelEventSave(v, b) })
	}, func(v *ModelEvent) []byte {
		return fuzzImage(ModelEventToJsonMeasure(v), func(b []byte) int64 { return ModelEventToJson(v, b) })
	}),
	fuzzBuilder("ModelFinding", func(b *ModelFindingBuilder, w []byte, r *TableReport) bool { return ModelFindingLoadBuilder(b, w, r) }, func(b *ModelFindingBuilder, w []byte, r *TableReport) bool { return ModelFindingFromJson(b, w, r) }, func(v *ModelFinding) []byte {
		return fuzzImage(ModelFindingMeasure(v), func(b []byte) int64 { return ModelFindingSave(v, b) })
	}, func(v *ModelFinding) []byte {
		return fuzzImage(ModelFindingToJsonMeasure(v), func(b []byte) int64 { return ModelFindingToJson(v, b) })
	}),
	fuzzBuilder("ModelHash", func(b *ModelHashBuilder, w []byte, r *TableReport) bool { return ModelHashLoadBuilder(b, w, r) }, func(b *ModelHashBuilder, w []byte, r *TableReport) bool { return ModelHashFromJson(b, w, r) }, func(v *ModelHash) []byte {
		return fuzzImage(ModelHashMeasure(v), func(b []byte) int64 { return ModelHashSave(v, b) })
	}, func(v *ModelHash) []byte {
		return fuzzImage(ModelHashToJsonMeasure(v), func(b []byte) int64 { return ModelHashToJson(v, b) })
	}),
	fuzzBuilder("ModelLocation", func(b *ModelLocationBuilder, w []byte, r *TableReport) bool { return ModelLocationLoadBuilder(b, w, r) }, func(b *ModelLocationBuilder, w []byte, r *TableReport) bool { return ModelLocationFromJson(b, w, r) }, func(v *ModelLocation) []byte {
		return fuzzImage(ModelLocationMeasure(v), func(b []byte) int64 { return ModelLocationSave(v, b) })
	}, func(v *ModelLocation) []byte {
		return fuzzImage(ModelLocationToJsonMeasure(v), func(b []byte) int64 { return ModelLocationToJson(v, b) })
	}),
	fuzzBuilder("ModelOutcome", func(b *ModelOutcomeBuilder, w []byte, r *TableReport) bool { return ModelOutcomeLoadBuilder(b, w, r) }, func(b *ModelOutcomeBuilder, w []byte, r *TableReport) bool { return ModelOutcomeFromJson(b, w, r) }, func(v *ModelOutcome) []byte {
		return fuzzImage(ModelOutcomeMeasure(v), func(b []byte) int64 { return ModelOutcomeSave(v, b) })
	}, func(v *ModelOutcome) []byte {
		return fuzzImage(ModelOutcomeToJsonMeasure(v), func(b []byte) int64 { return ModelOutcomeToJson(v, b) })
	}),
	fuzzBuilder("ModelPhys", func(b *ModelPhysBuilder, w []byte, r *TableReport) bool { return ModelPhysLoadBuilder(b, w, r) }, func(b *ModelPhysBuilder, w []byte, r *TableReport) bool { return ModelPhysFromJson(b, w, r) }, func(v *ModelPhys) []byte {
		return fuzzImage(ModelPhysMeasure(v), func(b []byte) int64 { return ModelPhysSave(v, b) })
	}, func(v *ModelPhys) []byte {
		return fuzzImage(ModelPhysToJsonMeasure(v), func(b []byte) int64 { return ModelPhysToJson(v, b) })
	}),
	fuzzBuilder("ModelPlacement", func(b *ModelPlacementBuilder, w []byte, r *TableReport) bool {
		return ModelPlacementLoadBuilder(b, w, r)
	}, func(b *ModelPlacementBuilder, w []byte, r *TableReport) bool { return ModelPlacementFromJson(b, w, r) }, func(v *ModelPlacement) []byte {
		return fuzzImage(ModelPlacementMeasure(v), func(b []byte) int64 { return ModelPlacementSave(v, b) })
	}, func(v *ModelPlacement) []byte {
		return fuzzImage(ModelPlacementToJsonMeasure(v), func(b []byte) int64 { return ModelPlacementToJson(v, b) })
	}),
	fuzzBuilder("ModelReplayEvent", func(b *ModelReplayEventBuilder, w []byte, r *TableReport) bool {
		return ModelReplayEventLoadBuilder(b, w, r)
	}, func(b *ModelReplayEventBuilder, w []byte, r *TableReport) bool {
		return ModelReplayEventFromJson(b, w, r)
	}, func(v *ModelReplayEvent) []byte {
		return fuzzImage(ModelReplayEventMeasure(v), func(b []byte) int64 { return ModelReplayEventSave(v, b) })
	}, func(v *ModelReplayEvent) []byte {
		return fuzzImage(ModelReplayEventToJsonMeasure(v), func(b []byte) int64 { return ModelReplayEventToJson(v, b) })
	}),
	fuzzBuilder("ModelRowAt", func(b *ModelRowAtBuilder, w []byte, r *TableReport) bool { return ModelRowAtLoadBuilder(b, w, r) }, func(b *ModelRowAtBuilder, w []byte, r *TableReport) bool { return ModelRowAtFromJson(b, w, r) }, func(v *ModelRowAt) []byte {
		return fuzzImage(ModelRowAtMeasure(v), func(b []byte) int64 { return ModelRowAtSave(v, b) })
	}, func(v *ModelRowAt) []byte {
		return fuzzImage(ModelRowAtToJsonMeasure(v), func(b []byte) int64 { return ModelRowAtToJson(v, b) })
	}),
	fuzzBuilder("ModelSeen", func(b *ModelSeenBuilder, w []byte, r *TableReport) bool { return ModelSeenLoadBuilder(b, w, r) }, func(b *ModelSeenBuilder, w []byte, r *TableReport) bool { return ModelSeenFromJson(b, w, r) }, func(v *ModelSeen) []byte {
		return fuzzImage(ModelSeenMeasure(v), func(b []byte) int64 { return ModelSeenSave(v, b) })
	}, func(v *ModelSeen) []byte {
		return fuzzImage(ModelSeenToJsonMeasure(v), func(b []byte) int64 { return ModelSeenToJson(v, b) })
	}),
	fuzzBuilder("ModelState", func(b *ModelStateBuilder, w []byte, r *TableReport) bool { return ModelStateLoadBuilder(b, w, r) }, func(b *ModelStateBuilder, w []byte, r *TableReport) bool { return ModelStateFromJson(b, w, r) }, func(v *ModelState) []byte {
		return fuzzImage(ModelStateMeasure(v), func(b []byte) int64 { return ModelStateSave(v, b) })
	}, func(v *ModelState) []byte {
		return fuzzImage(ModelStateToJsonMeasure(v), func(b []byte) int64 { return ModelStateToJson(v, b) })
	}),
	fuzzBuilder("ModelStep", func(b *ModelStepBuilder, w []byte, r *TableReport) bool { return ModelStepLoadBuilder(b, w, r) }, func(b *ModelStepBuilder, w []byte, r *TableReport) bool { return ModelStepFromJson(b, w, r) }, func(v *ModelStep) []byte {
		return fuzzImage(ModelStepMeasure(v), func(b []byte) int64 { return ModelStepSave(v, b) })
	}, func(v *ModelStep) []byte {
		return fuzzImage(ModelStepToJsonMeasure(v), func(b []byte) int64 { return ModelStepToJson(v, b) })
	}),
	fuzzBuilder("ModelStepResult", func(b *ModelStepResultBuilder, w []byte, r *TableReport) bool {
		return ModelStepResultLoadBuilder(b, w, r)
	}, func(b *ModelStepResultBuilder, w []byte, r *TableReport) bool {
		return ModelStepResultFromJson(b, w, r)
	}, func(v *ModelStepResult) []byte {
		return fuzzImage(ModelStepResultMeasure(v), func(b []byte) int64 { return ModelStepResultSave(v, b) })
	}, func(v *ModelStepResult) []byte {
		return fuzzImage(ModelStepResultToJsonMeasure(v), func(b []byte) int64 { return ModelStepResultToJson(v, b) })
	}),
	fuzzBuilder("ModelTrace", func(b *ModelTraceBuilder, w []byte, r *TableReport) bool { return ModelTraceLoadBuilder(b, w, r) }, func(b *ModelTraceBuilder, w []byte, r *TableReport) bool { return ModelTraceFromJson(b, w, r) }, func(v *ModelTrace) []byte {
		return fuzzImage(ModelTraceMeasure(v), func(b []byte) int64 { return ModelTraceSave(v, b) })
	}, func(v *ModelTrace) []byte {
		return fuzzImage(ModelTraceToJsonMeasure(v), func(b []byte) int64 { return ModelTraceToJson(v, b) })
	}),
	fuzzBuilder("ModelTraceStep", func(b *ModelTraceStepBuilder, w []byte, r *TableReport) bool {
		return ModelTraceStepLoadBuilder(b, w, r)
	}, func(b *ModelTraceStepBuilder, w []byte, r *TableReport) bool { return ModelTraceStepFromJson(b, w, r) }, func(v *ModelTraceStep) []byte {
		return fuzzImage(ModelTraceStepMeasure(v), func(b []byte) int64 { return ModelTraceStepSave(v, b) })
	}, func(v *ModelTraceStep) []byte {
		return fuzzImage(ModelTraceStepToJsonMeasure(v), func(b []byte) int64 { return ModelTraceStepToJson(v, b) })
	}),
	fuzzBuilder("ModelViolation", func(b *ModelViolationBuilder, w []byte, r *TableReport) bool {
		return ModelViolationLoadBuilder(b, w, r)
	}, func(b *ModelViolationBuilder, w []byte, r *TableReport) bool { return ModelViolationFromJson(b, w, r) }, func(v *ModelViolation) []byte {
		return fuzzImage(ModelViolationMeasure(v), func(b []byte) int64 { return ModelViolationSave(v, b) })
	}, func(v *ModelViolation) []byte {
		return fuzzImage(ModelViolationToJsonMeasure(v), func(b []byte) int64 { return ModelViolationToJson(v, b) })
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
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "tablemodel", "fuzz", "*")), fuzzImages(t)
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

func FuzzTablemodelwire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "tablemodelwire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
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
		}
	})
}

// TestWriteFuzzTablemodelwireSeeds writes testdata/fuzz/FuzzTablemodelwire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzTablemodelwireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzTablemodelwire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzTablemodelwire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}
