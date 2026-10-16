// Command pong-driver is the Go leg the schema compiler's wire fuzzer drives
// over the pong unit (make schemawire-conformance). It speaks the wire-fuzz
// driver contract of the schema repository's test/conformance/README.md: one
// process for the whole run, on a pipe, that only reads. The harness generates
// every mutant and holds the oracle; this leg loads each mutant through the
// generated tolerant read of pkg/pongwire, saves what it decoded and answers
// what happened. It decides nothing about what a mutant means.
//
// Usage: pong-driver wire-fuzz
//
// The stream is little-endian. In, once: a u32 roster count, then per root a
// u16-prefixed unit key, a u16-prefixed root table name, the u8 form and the
// u8 retain flag. Out, once: one byte per roster entry, 1 when this leg reads
// it (unit pong, root Pong, form 1, no retention), else 0. Then per mutant,
// in: u32 roster index, u32 length, the bytes; out: u8 loaded; i32 unknown,
// kind_mismatch, widened, clamped, duplicate; u8 malformed; u8 refused; i64
// measure; i32 retained, retain_lost; i64 saved, then that many bytes.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/pongwire"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "wire-fuzz" {
		fmt.Fprintln(os.Stderr, "usage: pong-driver wire-fuzz (the schema wire-fuzz driver contract, on stdin and stdout)")
		os.Exit(2)
	}
	if err := wireFuzz(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pong-driver:", err)
		os.Exit(1)
	}
}

// reply is one mutant's answer, in the order the contract writes it.
type reply struct {
	loaded                                             bool
	unknown, kindMismatch, widened, clamped, duplicate int32
	malformed, refused                                 bool
	measure                                            int64
	retained, retainLost                               int32
	saved                                              []byte // nil when Measure or Save refused
}

// readPong runs the generated tolerant read over one mutant: the region is
// sized from PongLoadMeasure and allocated at exactly that size, 64-byte
// aligned, then PongLoad fills it; whatever was decoded is saved back.
func readPong(wire []byte) reply {
	var r reply
	r.measure = pongwire.PongLoadMeasure(wire)
	if r.measure < 0 {
		// a refused measure is a refused load: the region cannot be sized,
		// so nothing loads and the report stays silent, as the schema
		// repository's own Go leg answers
		return r
	}
	raw := make([]byte, r.measure+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	region := raw[off : off+uintptr(r.measure)]
	var rep pongwire.TableReport
	v := pongwire.PongLoad(region, wire, &rep)
	r.loaded = v != nil
	r.copyReport(&rep)
	if v == nil {
		return r
	}
	if n := pongwire.PongMeasure(v); n >= 0 {
		buf := make([]byte, n)
		if pongwire.PongSave(v, buf) == n {
			r.saved = buf
		}
	}
	return r
}

func (r *reply) copyReport(rep *pongwire.TableReport) {
	r.unknown, r.kindMismatch, r.widened, r.clamped, r.duplicate = rep.Unknown, rep.KindMismatch, rep.Widened, rep.Clamped, rep.Duplicate
	r.malformed = rep.Malformed
	r.refused = rep.Verdict == pongwire.TableOpenRefused
	r.retained, r.retainLost = rep.Retained, rep.RetainLost
}

func wireFuzz(stdin io.Reader, stdout io.Writer) error {
	in, out := bufio.NewReader(stdin), bufio.NewWriter(stdout)
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
	known := make([]bool, count)
	for i := range known {
		unit, err := text()
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
		known[i] = unit == "pong" && root == "Pong" && form == 1 && retain == 0
		available := byte(0)
		if known[i] {
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
		if uint64(index) >= uint64(len(known)) || !known[index] {
			return fmt.Errorf("unsupported roster index %d", index)
		}
		wire := make([]byte, size)
		if _, err := io.ReadFull(in, wire); err != nil {
			return err
		}
		r := readPong(wire)
		saved := int64(-1)
		if r.saved != nil {
			saved = int64(len(r.saved))
		}
		for _, v := range []any{r.loaded, r.unknown, r.kindMismatch, r.widened, r.clamped, r.duplicate, r.malformed, r.refused, r.measure, r.retained, r.retainLost, saved} {
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
