package configwire

// FuzzConfigwire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzConfigwire$' -fuzztime=5m ./pkg/configwire
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
	fuzzValue("BenchBeat", func(v *BenchBeat, w []byte, r *TableReport) bool { return BenchBeatLoad(v, w, r) }, func(v *BenchBeat, w []byte, r *TableReport) bool { return BenchBeatFromJson(v, w, r) }, func(v *BenchBeat) []byte {
		return fuzzImage(BenchBeatMeasure(v), func(b []byte) int64 { return BenchBeatSave(v, b) })
	}, func(v *BenchBeat) []byte {
		return fuzzImage(BenchBeatToJsonMeasure(v), func(b []byte) int64 { return BenchBeatToJson(v, b) })
	}),
	fuzzValue("CanonicalWord", func(v *CanonicalWord, w []byte, r *TableReport) bool { return CanonicalWordLoad(v, w, r) }, func(v *CanonicalWord, w []byte, r *TableReport) bool { return CanonicalWordFromJson(v, w, r) }, func(v *CanonicalWord) []byte {
		return fuzzImage(CanonicalWordMeasure(v), func(b []byte) int64 { return CanonicalWordSave(v, b) })
	}, func(v *CanonicalWord) []byte {
		return fuzzImage(CanonicalWordToJsonMeasure(v), func(b []byte) int64 { return CanonicalWordToJson(v, b) })
	}),
	fuzzValue("ConfigChange", func(v *ConfigChange, w []byte, r *TableReport) bool { return ConfigChangeLoad(v, w, r) }, func(v *ConfigChange, w []byte, r *TableReport) bool { return ConfigChangeFromJson(v, w, r) }, func(v *ConfigChange) []byte {
		return fuzzImage(ConfigChangeMeasure(v), func(b []byte) int64 { return ConfigChangeSave(v, b) })
	}, func(v *ConfigChange) []byte {
		return fuzzImage(ConfigChangeToJsonMeasure(v), func(b []byte) int64 { return ConfigChangeToJson(v, b) })
	}),
	fuzzValue("ConfigRow", func(v *ConfigRow, w []byte, r *TableReport) bool { return ConfigRowLoad(v, w, r) }, func(v *ConfigRow, w []byte, r *TableReport) bool { return ConfigRowFromJson(v, w, r) }, func(v *ConfigRow) []byte {
		return fuzzImage(ConfigRowMeasure(v), func(b []byte) int64 { return ConfigRowSave(v, b) })
	}, func(v *ConfigRow) []byte {
		return fuzzImage(ConfigRowToJsonMeasure(v), func(b []byte) int64 { return ConfigRowToJson(v, b) })
	}),
	fuzzValue("EnvName", func(v *EnvName, w []byte, r *TableReport) bool { return EnvNameLoad(v, w, r) }, func(v *EnvName, w []byte, r *TableReport) bool { return EnvNameFromJson(v, w, r) }, func(v *EnvName) []byte {
		return fuzzImage(EnvNameMeasure(v), func(b []byte) int64 { return EnvNameSave(v, b) })
	}, func(v *EnvName) []byte {
		return fuzzImage(EnvNameToJsonMeasure(v), func(b []byte) int64 { return EnvNameToJson(v, b) })
	}),
	fuzzValue("FieldPair", func(v *FieldPair, w []byte, r *TableReport) bool { return FieldPairLoad(v, w, r) }, func(v *FieldPair, w []byte, r *TableReport) bool { return FieldPairFromJson(v, w, r) }, func(v *FieldPair) []byte {
		return fuzzImage(FieldPairMeasure(v), func(b []byte) int64 { return FieldPairSave(v, b) })
	}, func(v *FieldPair) []byte {
		return fuzzImage(FieldPairToJsonMeasure(v), func(b []byte) int64 { return FieldPairToJson(v, b) })
	}),
	fuzzValue("FileHeader", func(v *FileHeader, w []byte, r *TableReport) bool { return FileHeaderLoad(v, w, r) }, func(v *FileHeader, w []byte, r *TableReport) bool { return FileHeaderFromJson(v, w, r) }, func(v *FileHeader) []byte {
		return fuzzImage(FileHeaderMeasure(v), func(b []byte) int64 { return FileHeaderSave(v, b) })
	}, func(v *FileHeader) []byte {
		return fuzzImage(FileHeaderToJsonMeasure(v), func(b []byte) int64 { return FileHeaderToJson(v, b) })
	}),
	fuzzValue("HostGroup", func(v *HostGroup, w []byte, r *TableReport) bool { return HostGroupLoad(v, w, r) }, func(v *HostGroup, w []byte, r *TableReport) bool { return HostGroupFromJson(v, w, r) }, func(v *HostGroup) []byte {
		return fuzzImage(HostGroupMeasure(v), func(b []byte) int64 { return HostGroupSave(v, b) })
	}, func(v *HostGroup) []byte {
		return fuzzImage(HostGroupToJsonMeasure(v), func(b []byte) int64 { return HostGroupToJson(v, b) })
	}),
	fuzzValue("HostName", func(v *HostName, w []byte, r *TableReport) bool { return HostNameLoad(v, w, r) }, func(v *HostName, w []byte, r *TableReport) bool { return HostNameFromJson(v, w, r) }, func(v *HostName) []byte {
		return fuzzImage(HostNameMeasure(v), func(b []byte) int64 { return HostNameSave(v, b) })
	}, func(v *HostName) []byte {
		return fuzzImage(HostNameToJsonMeasure(v), func(b []byte) int64 { return HostNameToJson(v, b) })
	}),
	fuzzValue("HostView", func(v *HostView, w []byte, r *TableReport) bool { return HostViewLoad(v, w, r) }, func(v *HostView, w []byte, r *TableReport) bool { return HostViewFromJson(v, w, r) }, func(v *HostView) []byte {
		return fuzzImage(HostViewMeasure(v), func(b []byte) int64 { return HostViewSave(v, b) })
	}, func(v *HostView) []byte {
		return fuzzImage(HostViewToJsonMeasure(v), func(b []byte) int64 { return HostViewToJson(v, b) })
	}),
	fuzzValue("Inventory", func(v *Inventory, w []byte, r *TableReport) bool { return InventoryLoad(v, w, r) }, func(v *Inventory, w []byte, r *TableReport) bool { return InventoryFromJson(v, w, r) }, func(v *Inventory) []byte {
		return fuzzImage(InventoryMeasure(v), func(b []byte) int64 { return InventorySave(v, b) })
	}, func(v *Inventory) []byte {
		return fuzzImage(InventoryToJsonMeasure(v), func(b []byte) int64 { return InventoryToJson(v, b) })
	}),
	fuzzValue("InventoryLoop", func(v *InventoryLoop, w []byte, r *TableReport) bool { return InventoryLoopLoad(v, w, r) }, func(v *InventoryLoop, w []byte, r *TableReport) bool { return InventoryLoopFromJson(v, w, r) }, func(v *InventoryLoop) []byte {
		return fuzzImage(InventoryLoopMeasure(v), func(b []byte) int64 { return InventoryLoopSave(v, b) })
	}, func(v *InventoryLoop) []byte {
		return fuzzImage(InventoryLoopToJsonMeasure(v), func(b []byte) int64 { return InventoryLoopToJson(v, b) })
	}),
	fuzzValue("KindRev", func(v *KindRev, w []byte, r *TableReport) bool { return KindRevLoad(v, w, r) }, func(v *KindRev, w []byte, r *TableReport) bool { return KindRevFromJson(v, w, r) }, func(v *KindRev) []byte {
		return fuzzImage(KindRevMeasure(v), func(b []byte) int64 { return KindRevSave(v, b) })
	}, func(v *KindRev) []byte {
		return fuzzImage(KindRevToJsonMeasure(v), func(b []byte) int64 { return KindRevToJson(v, b) })
	}),
	fuzzValue("MachineBeat", func(v *MachineBeat, w []byte, r *TableReport) bool { return MachineBeatLoad(v, w, r) }, func(v *MachineBeat, w []byte, r *TableReport) bool { return MachineBeatFromJson(v, w, r) }, func(v *MachineBeat) []byte {
		return fuzzImage(MachineBeatMeasure(v), func(b []byte) int64 { return MachineBeatSave(v, b) })
	}, func(v *MachineBeat) []byte {
		return fuzzImage(MachineBeatToJsonMeasure(v), func(b []byte) int64 { return MachineBeatToJson(v, b) })
	}),
	fuzzValue("MachineView", func(v *MachineView, w []byte, r *TableReport) bool { return MachineViewLoad(v, w, r) }, func(v *MachineView, w []byte, r *TableReport) bool { return MachineViewFromJson(v, w, r) }, func(v *MachineView) []byte {
		return fuzzImage(MachineViewMeasure(v), func(b []byte) int64 { return MachineViewSave(v, b) })
	}, func(v *MachineView) []byte {
		return fuzzImage(MachineViewToJsonMeasure(v), func(b []byte) int64 { return MachineViewToJson(v, b) })
	}),
	fuzzValue("MemberBeat", func(v *MemberBeat, w []byte, r *TableReport) bool { return MemberBeatLoad(v, w, r) }, func(v *MemberBeat, w []byte, r *TableReport) bool { return MemberBeatFromJson(v, w, r) }, func(v *MemberBeat) []byte {
		return fuzzImage(MemberBeatMeasure(v), func(b []byte) int64 { return MemberBeatSave(v, b) })
	}, func(v *MemberBeat) []byte {
		return fuzzImage(MemberBeatToJsonMeasure(v), func(b []byte) int64 { return MemberBeatToJson(v, b) })
	}),
	fuzzValue("RowFields", func(v *RowFields, w []byte, r *TableReport) bool { return RowFieldsLoad(v, w, r) }, func(v *RowFields, w []byte, r *TableReport) bool { return RowFieldsFromJson(v, w, r) }, func(v *RowFields) []byte {
		return fuzzImage(RowFieldsMeasure(v), func(b []byte) int64 { return RowFieldsSave(v, b) })
	}, func(v *RowFields) []byte {
		return fuzzImage(RowFieldsToJsonMeasure(v), func(b []byte) int64 { return RowFieldsToJson(v, b) })
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
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "config", "fuzz", "*")), fuzzImages(t)
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

func FuzzConfigwire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "configwire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
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

// TestWriteFuzzConfigwireSeeds writes testdata/fuzz/FuzzConfigwire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzConfigwireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzConfigwire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzConfigwire")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	sels, ins := fuzzSeedList(t)
	for i := range ins {
		body := fmt.Sprintf("go test fuzz v1\nbyte('\\x%02x')\n[]byte(%s)\n", sels[i], strconv.Quote(string(ins[i])))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("seed-%04d", i)), []byte(body), 0o644))
	}
}
