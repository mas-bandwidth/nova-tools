package seatrecwire

// FuzzSeatrecwire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzSeatrecwire$' -fuzztime=5m ./pkg/seatrecwire
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
	fuzzBuilder("BusRoster", func(b *BusRosterBuilder, w []byte, r *TableReport) bool { return BusRosterLoadBuilder(b, w, r) }, func(b *BusRosterBuilder, w []byte, r *TableReport) bool { return BusRosterFromJson(b, w, r) }, func(v *BusRoster) []byte {
		return fuzzImage(BusRosterMeasure(v), func(b []byte) int64 { return BusRosterSave(v, b) })
	}, func(v *BusRoster) []byte {
		return fuzzImage(BusRosterToJsonMeasure(v), func(b []byte) int64 { return BusRosterToJson(v, b) })
	}),
	fuzzBuilder("DayFile", func(b *DayFileBuilder, w []byte, r *TableReport) bool { return DayFileLoadBuilder(b, w, r) }, func(b *DayFileBuilder, w []byte, r *TableReport) bool { return DayFileFromJson(b, w, r) }, func(v *DayFile) []byte {
		return fuzzImage(DayFileMeasure(v), func(b []byte) int64 { return DayFileSave(v, b) })
	}, func(v *DayFile) []byte {
		return fuzzImage(DayFileToJsonMeasure(v), func(b []byte) int64 { return DayFileToJson(v, b) })
	}),
	fuzzBuilder("DayLine", func(b *DayLineBuilder, w []byte, r *TableReport) bool { return DayLineLoadBuilder(b, w, r) }, func(b *DayLineBuilder, w []byte, r *TableReport) bool { return DayLineFromJson(b, w, r) }, func(v *DayLine) []byte {
		return fuzzImage(DayLineMeasure(v), func(b []byte) int64 { return DayLineSave(v, b) })
	}, func(v *DayLine) []byte {
		return fuzzImage(DayLineToJsonMeasure(v), func(b []byte) int64 { return DayLineToJson(v, b) })
	}),
	fuzzBuilder("Participant", func(b *ParticipantBuilder, w []byte, r *TableReport) bool { return ParticipantLoadBuilder(b, w, r) }, func(b *ParticipantBuilder, w []byte, r *TableReport) bool { return ParticipantFromJson(b, w, r) }, func(v *Participant) []byte {
		return fuzzImage(ParticipantMeasure(v), func(b []byte) int64 { return ParticipantSave(v, b) })
	}, func(v *Participant) []byte {
		return fuzzImage(ParticipantToJsonMeasure(v), func(b []byte) int64 { return ParticipantToJson(v, b) })
	}),
	fuzzBuilder("PlacedReceipt", func(b *PlacedReceiptBuilder, w []byte, r *TableReport) bool { return PlacedReceiptLoadBuilder(b, w, r) }, func(b *PlacedReceiptBuilder, w []byte, r *TableReport) bool { return PlacedReceiptFromJson(b, w, r) }, func(v *PlacedReceipt) []byte {
		return fuzzImage(PlacedReceiptMeasure(v), func(b []byte) int64 { return PlacedReceiptSave(v, b) })
	}, func(v *PlacedReceipt) []byte {
		return fuzzImage(PlacedReceiptToJsonMeasure(v), func(b []byte) int64 { return PlacedReceiptToJson(v, b) })
	}),
	fuzzBuilder("SeatEnvironment", func(b *SeatEnvironmentBuilder, w []byte, r *TableReport) bool {
		return SeatEnvironmentLoadBuilder(b, w, r)
	}, func(b *SeatEnvironmentBuilder, w []byte, r *TableReport) bool {
		return SeatEnvironmentFromJson(b, w, r)
	}, func(v *SeatEnvironment) []byte {
		return fuzzImage(SeatEnvironmentMeasure(v), func(b []byte) int64 { return SeatEnvironmentSave(v, b) })
	}, func(v *SeatEnvironment) []byte {
		return fuzzImage(SeatEnvironmentToJsonMeasure(v), func(b []byte) int64 { return SeatEnvironmentToJson(v, b) })
	}),
	fuzzBuilder("SeatLogin", func(b *SeatLoginBuilder, w []byte, r *TableReport) bool { return SeatLoginLoadBuilder(b, w, r) }, func(b *SeatLoginBuilder, w []byte, r *TableReport) bool { return SeatLoginFromJson(b, w, r) }, func(v *SeatLogin) []byte {
		return fuzzImage(SeatLoginMeasure(v), func(b []byte) int64 { return SeatLoginSave(v, b) })
	}, func(v *SeatLogin) []byte {
		return fuzzImage(SeatLoginToJsonMeasure(v), func(b []byte) int64 { return SeatLoginToJson(v, b) })
	}),
	fuzzBuilder("SourceLabel", func(b *SourceLabelBuilder, w []byte, r *TableReport) bool { return SourceLabelLoadBuilder(b, w, r) }, func(b *SourceLabelBuilder, w []byte, r *TableReport) bool { return SourceLabelFromJson(b, w, r) }, func(v *SourceLabel) []byte {
		return fuzzImage(SourceLabelMeasure(v), func(b []byte) int64 { return SourceLabelSave(v, b) })
	}, func(v *SourceLabel) []byte {
		return fuzzImage(SourceLabelToJsonMeasure(v), func(b []byte) int64 { return SourceLabelToJson(v, b) })
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
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "seatrec", "fuzz", "*")), fuzzImages(t)
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

func FuzzSeatrecwire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "seatrecwire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
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

// TestWriteFuzzSeatrecwireSeeds writes testdata/fuzz/FuzzSeatrecwire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzSeatrecwireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzSeatrecwire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzSeatrecwire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}
