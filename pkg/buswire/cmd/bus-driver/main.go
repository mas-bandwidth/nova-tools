// Command bus-driver is the bus area's Go leg for the schema conformance
// harness's wire fuzz (make schemawire-conformance; the wire-fuzz driver
// contract, test/conformance/README.md in the schema repository). It is one
// process on a pipe that only reads: the harness makes every mutant and holds
// the oracle, and this leg loads each mutant through the generated tolerant
// read of pkg/buswire, saves what it decoded, and answers what happened. It
// decides nothing about what a mutant means.
//
//	bus-driver wire-fuzz
//
// It carries one codec, the Envelope of unit bus in the file form (form byte
// 1) without retention; every other roster entry is answered as absent.
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/buswire"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "wire-fuzz" {
		fmt.Fprintln(os.Stderr, "usage: bus-driver wire-fuzz (the schema conformance harness's wire-fuzz leg, read on stdin, answered on stdout)")
		os.Exit(2)
	}
	if err := wireFuzz(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bus-driver:", err)
		os.Exit(1)
	}
}

// region is an aligned buffer of exactly n bytes; zero bytes is allocated as
// one, as the contract says.
func region(n int64) []byte {
	size := max(n, 1)
	raw := make([]byte, size+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// reply is one mutant's answer, in the contract's order.
type reply struct {
	loaded                                             bool
	unknown, kindMismatch, widened, clamped, duplicate int32
	malformed, refused                                 bool
	measure                                            int64
	retained, retainLost                               int32
	saved                                              int64
}

func envelope(wire []byte) (reply, []byte) {
	rep := reply{measure: buswire.EnvelopeLoadMeasure(wire), saved: -1}
	var r buswire.TableReport
	var v *buswire.Envelope
	if rep.measure >= 0 {
		v = buswire.EnvelopeLoad(region(rep.measure), wire, &r)
	}
	rep.loaded = v != nil
	rep.unknown, rep.kindMismatch, rep.widened, rep.clamped, rep.duplicate = r.Unknown, r.KindMismatch, r.Widened, r.Clamped, r.Duplicate
	rep.malformed, rep.refused = r.Malformed, r.Verdict == buswire.TableOpenRefused
	if v == nil {
		return rep, nil
	}
	n := buswire.EnvelopeMeasure(v)
	if n < 0 {
		return rep, nil
	}
	buf := make([]byte, max(n, 1))[:n]
	rep.saved = buswire.EnvelopeSave(v, buf)
	if rep.saved <= 0 {
		return rep, nil
	}
	return rep, buf[:rep.saved]
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
	carried := make([]bool, count)
	for i := range carried {
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
		carried[i] = unit == "bus" && root == "Envelope" && form == 1 && retain == 0
		b := byte(0)
		if carried[i] {
			b = 1
		}
		if err := out.WriteByte(b); err != nil {
			return err
		}
	}
	if err := out.Flush(); err != nil {
		return err
	}
	for {
		var index, size uint32
		if err := read(&index); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := read(&size); err != nil {
			return err
		}
		if uint64(index) >= uint64(len(carried)) || !carried[index] {
			return fmt.Errorf("a mutant for roster entry %d, which this leg does not carry", index)
		}
		wire := make([]byte, max(size, 1))[:size]
		if _, err := io.ReadFull(in, wire); err != nil {
			return err
		}
		rep, saved := envelope(wire)
		for _, v := range []any{rep.loaded, rep.unknown, rep.kindMismatch, rep.widened, rep.clamped, rep.duplicate, rep.malformed, rep.refused, rep.measure, rep.retained, rep.retainLost, rep.saved} {
			if err := write(v); err != nil {
				return err
			}
		}
		if _, err := out.Write(saved); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
}
