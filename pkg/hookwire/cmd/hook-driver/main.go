// Command hook-driver is the hook area's Go leg of the schema compiler's wire
// fuzz (the wire-fuzz driver contract, test/conformance/README.md in the
// schema repository): `hook-driver wire-fuzz` reads the harness's roster and
// mutants on stdin and answers each mutant on stdout through the generated
// tolerant read of pkg/hookwire, then saves what it decoded. It decides
// nothing about what a mutant means; the harness holds the oracle. `make
// schemawire-conformance` builds and drives it.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/hookwire"
)

// unit is the key the area's fuzz manifest (schemas/hook/fuzz/MANIFEST.txt)
// gives the hook unit.
const unit = "hook"

// reply is one mutant's answer, in the contract's order.
type reply struct {
	loaded                                             bool
	unknown, kindMismatch, widened, clamped, duplicate int32
	malformed, refused                                 bool
	measure                                            int64
	saved                                              []byte // nil: Measure or Save refused
}

// codec runs one root's generated read and write over one mutant.
type codec func(wire []byte) reply

// region is n bytes at a 64-byte boundary, the alignment a table region keeps.
func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func rootCodec[T any](
	loadMeasure func([]byte) int64,
	load func(region, wire []byte, r *hookwire.TableReport) *T,
	measure func(*T, ...hookwire.TableWriteContext) int64,
	save func(*T, []byte, ...hookwire.TableWriteContext) int64,
) codec {
	return func(wire []byte) reply {
		var out reply
		out.measure = loadMeasure(wire)
		var v *T
		var r hookwire.TableReport
		if out.measure >= 0 {
			v = load(region(out.measure), wire, &r)
		}
		out.loaded = v != nil
		out.unknown, out.kindMismatch, out.widened, out.clamped, out.duplicate = r.Unknown, r.KindMismatch, r.Widened, r.Clamped, r.Duplicate
		out.malformed, out.refused = r.Malformed, r.Verdict == hookwire.TableOpenRefused
		if v == nil {
			return out
		}
		if n := measure(v); n >= 0 {
			buf := make([]byte, n)
			if m := save(v, buf); m >= 0 {
				out.saved = buf[:m]
			}
		}
		return out
	}
}

var roots = map[string]codec{
	"Ack":       rootCodec(hookwire.AckLoadMeasure, hookwire.AckLoad, hookwire.AckMeasure, hookwire.AckSave),
	"BeatPulse": rootCodec(hookwire.BeatPulseLoadMeasure, hookwire.BeatPulseLoad, hookwire.BeatPulseMeasure, hookwire.BeatPulseSave),
	"HookStamp": rootCodec(hookwire.HookStampLoadMeasure, hookwire.HookStampLoad, hookwire.HookStampMeasure, hookwire.HookStampSave),
}

func wireFuzz(in *bufio.Reader, out *bufio.Writer) error {
	read := func(v any) error { return binary.Read(in, binary.LittleEndian, v) }
	write := func(v any) error { return binary.Write(out, binary.LittleEndian, v) }
	text := func() (string, error) {
		var n uint16
		if err := read(&n); err != nil {
			return "", err
		}
		b := make([]byte, n)
		_, err := io.ReadFull(in, b)
		return string(b), err
	}
	var count uint32
	if err := read(&count); err != nil {
		return err
	}
	roster := make([]codec, count)
	for i := range roster {
		u, err := text()
		if err != nil {
			return err
		}
		root, err := text()
		if err != nil {
			return err
		}
		form, err := in.ReadByte()
		if err != nil {
			return err
		}
		retain, err := in.ReadByte()
		if err != nil {
			return err
		}
		// the file form, not retaining: the only arm this leg answers
		if u == unit && form == 1 && retain == 0 {
			roster[i] = roots[root]
		}
		available := byte(0)
		if roster[i] != nil {
			available = 1
		}
		if err := out.WriteByte(available); err != nil {
			return err
		}
	}
	if err := out.Flush(); err != nil {
		return err
	}
	for {
		var index, size uint32
		if err := read(&index); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := read(&size); err != nil {
			return err
		}
		if uint64(index) >= uint64(len(roster)) || roster[index] == nil {
			return fmt.Errorf("hook-driver: roster index %d has no codec", index)
		}
		wire := make([]byte, size)
		if _, err := io.ReadFull(in, wire); err != nil {
			return err
		}
		r := roster[index](wire)
		saved := int64(-1)
		if r.saved != nil {
			saved = int64(len(r.saved))
		}
		for _, v := range []any{r.loaded, r.unknown, r.kindMismatch, r.widened, r.clamped, r.duplicate, r.malformed, r.refused, r.measure, int32(0), int32(0), saved} {
			if err := write(v); err != nil {
				return err
			}
		}
		if saved > 0 {
			if _, err := out.Write(r.saved); err != nil {
				return err
			}
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
}

func main() {
	if len(os.Args) != 2 || os.Args[1] != "wire-fuzz" {
		fmt.Fprintln(os.Stderr, "usage: hook-driver wire-fuzz (the schema wire-fuzz driver contract, on stdin and stdout)")
		os.Exit(2)
	}
	if err := wireFuzz(bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
