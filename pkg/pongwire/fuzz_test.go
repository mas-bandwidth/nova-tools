package pongwire

// FuzzPongwire feeds hostile bytes, in three forms (binary wire, padded standard
// base64, JSON text), to every generated root. go test runs the checked-in seeds only;
// nightly: go test -run='^$' -fuzz='^FuzzPongwire$' -fuzztime=5m ./pkg/pongwire
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
	fuzzBuilder("Pong", func(b *PongBuilder, w []byte, r *TableReport) bool { return PongLoadBuilder(b, w, r) }, func(b *PongBuilder, w []byte, r *TableReport) bool { return PongFromJson(b, w, r) }, func(v *Pong) []byte { return fuzzImage(PongMeasure(v), func(b []byte) int64 { return PongSave(v, b) }) }, func(v *Pong) []byte {
		return fuzzImage(PongToJsonMeasure(v), func(b []byte) int64 { return PongToJson(v, b) })
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
	corpus, others := fuzzImages(t, filepath.Join("..", "..", "schemas", "pong", "fuzz", "*")), fuzzImages(t, "testdata/writers/*.bin")
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

func FuzzPongwire(f *testing.F) {
	fuzzSeedList(f) // fills fuzzSeeds; the seeds themselves are the checked-in files
	f.Fuzz(func(t *testing.T, sel uint8, in []byte) {
		root := fuzzRoots[int(sel)%len(fuzzRoots)]
		for form := fuzzWire; form <= fuzzJSON; form++ {
			wire, text, rep, err := root.load(form, in)
			if err != nil {
				require.True(t, strings.HasPrefix(fmt.Sprintf("%T", err), "pongwire.Table"), "%s as %d %q: a refusal with no name: %T %v", root.name, form, in, err, err)
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

// TestWriteFuzzPongwireSeeds writes testdata/fuzz/FuzzPongwire when NOVA_FUZZ_WRITE_SEEDS=1.
func TestWriteFuzzPongwireSeeds(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FUZZ_WRITE_SEEDS") != "1" {
		t.Skip("set NOVA_FUZZ_WRITE_SEEDS=1 to write testdata/fuzz/FuzzPongwire")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzPongwire")
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
		if !strings.HasPrefix(fmt.Sprintf("%T", err), "pongwire.Table") {
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

// TestFuzzPongwireNeighbours proves fuzzNeighbours on every root: the real reader
// passes at its reset value, and two planted readers fail, one that accepts
// the reset wire with a byte appended and one that accepts a strict prefix.
func TestFuzzPongwireNeighbours(t *testing.T) {
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
