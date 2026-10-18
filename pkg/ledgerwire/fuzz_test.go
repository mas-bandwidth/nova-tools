package ledgerwire

// FuzzLedgerwire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzLedgerwire$' -fuzztime=5m ./pkg/ledgerwire
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

// fuzzRoots is every generated root, written by roots.sh.
var fuzzRoots = []fuzzRoot{
	fuzzBuilder("CairnLogRecord", func(b *CairnLogRecordBuilder, w []byte, r *TableReport) bool {
		return CairnLogRecordLoadBuilder(b, w, r)
	}, func(b *CairnLogRecordBuilder, w []byte, r *TableReport) bool { return CairnLogRecordFromJson(b, w, r) }, func(v *CairnLogRecord) []byte {
		return fuzzImage(CairnLogRecordMeasure(v), func(b []byte) int64 { return CairnLogRecordSave(v, b) })
	}, func(v *CairnLogRecord) []byte {
		return fuzzImage(CairnLogRecordToJsonMeasure(v), func(b []byte) int64 { return CairnLogRecordToJson(v, b) })
	}),
	fuzzBuilder("DogfoodReceipt", func(b *DogfoodReceiptBuilder, w []byte, r *TableReport) bool {
		return DogfoodReceiptLoadBuilder(b, w, r)
	}, func(b *DogfoodReceiptBuilder, w []byte, r *TableReport) bool { return DogfoodReceiptFromJson(b, w, r) }, func(v *DogfoodReceipt) []byte {
		return fuzzImage(DogfoodReceiptMeasure(v), func(b []byte) int64 { return DogfoodReceiptSave(v, b) })
	}, func(v *DogfoodReceipt) []byte {
		return fuzzImage(DogfoodReceiptToJsonMeasure(v), func(b []byte) int64 { return DogfoodReceiptToJson(v, b) })
	}),
	fuzzBuilder("FuseBox", func(b *FuseBoxBuilder, w []byte, r *TableReport) bool { return FuseBoxLoadBuilder(b, w, r) }, func(b *FuseBoxBuilder, w []byte, r *TableReport) bool { return FuseBoxFromJson(b, w, r) }, func(v *FuseBox) []byte {
		return fuzzImage(FuseBoxMeasure(v), func(b []byte) int64 { return FuseBoxSave(v, b) })
	}, func(v *FuseBox) []byte {
		return fuzzImage(FuseBoxToJsonMeasure(v), func(b []byte) int64 { return FuseBoxToJson(v, b) })
	}),
	fuzzBuilder("Fuse", func(b *FuseBuilder, w []byte, r *TableReport) bool { return FuseLoadBuilder(b, w, r) }, func(b *FuseBuilder, w []byte, r *TableReport) bool { return FuseFromJson(b, w, r) }, func(v *Fuse) []byte { return fuzzImage(FuseMeasure(v), func(b []byte) int64 { return FuseSave(v, b) }) }, func(v *Fuse) []byte {
		return fuzzImage(FuseToJsonMeasure(v), func(b []byte) int64 { return FuseToJson(v, b) })
	}),
	fuzzBuilder("LedgerKey", func(b *LedgerKeyBuilder, w []byte, r *TableReport) bool { return LedgerKeyLoadBuilder(b, w, r) }, func(b *LedgerKeyBuilder, w []byte, r *TableReport) bool { return LedgerKeyFromJson(b, w, r) }, func(v *LedgerKey) []byte {
		return fuzzImage(LedgerKeyMeasure(v), func(b []byte) int64 { return LedgerKeySave(v, b) })
	}, func(v *LedgerKey) []byte {
		return fuzzImage(LedgerKeyToJsonMeasure(v), func(b []byte) int64 { return LedgerKeyToJson(v, b) })
	}),
	fuzzBuilder("LedgerValue", func(b *LedgerValueBuilder, w []byte, r *TableReport) bool { return LedgerValueLoadBuilder(b, w, r) }, func(b *LedgerValueBuilder, w []byte, r *TableReport) bool { return LedgerValueFromJson(b, w, r) }, func(v *LedgerValue) []byte {
		return fuzzImage(LedgerValueMeasure(v), func(b []byte) int64 { return LedgerValueSave(v, b) })
	}, func(v *LedgerValue) []byte {
		return fuzzImage(LedgerValueToJsonMeasure(v), func(b []byte) int64 { return LedgerValueToJson(v, b) })
	}),
	fuzzBuilder("QuarantineEntry", func(b *QuarantineEntryBuilder, w []byte, r *TableReport) bool {
		return QuarantineEntryLoadBuilder(b, w, r)
	}, func(b *QuarantineEntryBuilder, w []byte, r *TableReport) bool {
		return QuarantineEntryFromJson(b, w, r)
	}, func(v *QuarantineEntry) []byte {
		return fuzzImage(QuarantineEntryMeasure(v), func(b []byte) int64 { return QuarantineEntrySave(v, b) })
	}, func(v *QuarantineEntry) []byte {
		return fuzzImage(QuarantineEntryToJsonMeasure(v), func(b []byte) int64 { return QuarantineEntryToJson(v, b) })
	}),
	fuzzBuilder("TokenValue", func(b *TokenValueBuilder, w []byte, r *TableReport) bool { return TokenValueLoadBuilder(b, w, r) }, func(b *TokenValueBuilder, w []byte, r *TableReport) bool { return TokenValueFromJson(b, w, r) }, func(v *TokenValue) []byte {
		return fuzzImage(TokenValueMeasure(v), func(b []byte) int64 { return TokenValueSave(v, b) })
	}, func(v *TokenValue) []byte {
		return fuzzImage(TokenValueToJsonMeasure(v), func(b []byte) int64 { return TokenValueToJson(v, b) })
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
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "ledger", "fuzz", "*")), fuzzImages(t)
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

func FuzzLedgerwire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "ledgerwire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
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

// TestWriteFuzzLedgerwireSeeds writes testdata/fuzz/FuzzLedgerwire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzLedgerwireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzLedgerwire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzLedgerwire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}
