// Command write makes the reader's fixtures from the generated Go of the two
// writer definitions beside it, so no fixture is written by hand. It runs in a
// scratch module named fixtures holding this file and two packages the pinned
// compiler generates, wide (from ../wide) and changed (from ../changed); from
// that module's root:
//
//	go mod init fixtures
//	go run github.com/mas-bandwidth/schema/v2/cmd/schema@<pin> generate --lang go --out wide <testdata>/writers/wide
//	go run github.com/mas-bandwidth/schema/v2/cmd/schema@<pin> generate --lang go --out changed <testdata>/writers/changed
//	go run . <testdata>
//
// where <pin> is pkg/buswire/generate.go's pseudo-version and <testdata> is
// pkg/buswire/testdata. Each output is padded standard base64 of one wire.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fixtures/changed"
	"fixtures/wide"
)

const atMs = 1792098245000 // 2026-10-15T21:04:05Z, the tests' fixed stamp

func wideWire(f func(e *wide.Envelope, b *wide.EnvelopeBuilder)) []byte {
	var b wide.EnvelopeBuilder
	if !b.Init() {
		panic("init")
	}
	e := b.GetRoot()
	e.IdLength = int32(copy(e.Id[:], "01JA2B3C4D5E6F7G8H9J0KMNPQ"))
	e.FromLength = int32(copy(e.From[:], "sender"))
	e.To[0].SLength = int32(copy(e.To[0].S[:], "reader-a"))
	e.ToCount = 1
	e.SubjectLength = int32(copy(e.Subject[:], "s"))
	e.Kind = wide.Kindstatus
	e.AtMs, e.AtMsPresent = atMs, true
	wide.TableStringEmplace(&b.Main, &e.Body, []byte("b"))
	f(e, &b)
	if !b.Lock() {
		panic("lock")
	}
	v := b.AsConst()
	buf := make([]byte, wide.EnvelopeMeasure(v))
	if wide.EnvelopeSave(v, buf) != int64(len(buf)) {
		panic("save")
	}
	return buf
}

func changedWire() []byte {
	var b changed.EnvelopeBuilder
	if !b.Init() {
		panic("init")
	}
	e := b.GetRoot()
	e.IdLength = int32(copy(e.Id[:], "01JA2B3C4D5E6F7G8H9J0KMNPQ"))
	e.FromLength = int32(copy(e.From[:], "sender"))
	e.To[0].SLength = int32(copy(e.To[0].S[:], "reader-a"))
	e.ToCount = 1
	e.Subject = 7
	e.Kind = changed.Kindstatus
	e.AtMs, e.AtMsPresent = atMs, true
	changed.TableStringEmplace(&b.Main, &e.Body, []byte("b"))
	if !b.Lock() {
		panic("lock")
	}
	v := b.AsConst()
	buf := make([]byte, changed.EnvelopeMeasure(v))
	if changed.EnvelopeSave(v, buf) != int64(len(buf)) {
		panic("save")
	}
	return buf
}

func main() {
	out := os.Args[1]
	for name, wire := range map[string][]byte{
		"newer-field": wideWire(func(e *wide.Envelope, _ *wide.EnvelopeBuilder) { e.Priority = 3 }),
		"newer-kind":  wideWire(func(e *wide.Envelope, _ *wide.EnvelopeBuilder) { e.Kind = wide.Kindnotice }),
		"oversize-subject": wideWire(func(e *wide.Envelope, _ *wide.EnvelopeBuilder) {
			e.SubjectLength = int32(copy(e.Subject[:], strings.Repeat("s", 201)))
		}),
		"too-many-recipients": wideWire(func(e *wide.Envelope, _ *wide.EnvelopeBuilder) {
			for i := range 65 {
				e.To[i].SLength = int32(copy(e.To[i].S[:], fmt.Sprintf("n%d", i)))
			}
			e.ToCount = 65
		}),
		"oversize-name": wideWire(func(e *wide.Envelope, _ *wide.EnvelopeBuilder) {
			e.To[0].SLength = int32(copy(e.To[0].S[:], strings.Repeat("n", 65)))
		}),
		"subject-changed-kind": changedWire(),
	} {
		text := base64.StdEncoding.EncodeToString(wire) + "\n"
		if err := os.WriteFile(filepath.Join(out, name+".b64"), []byte(text), 0o644); err != nil {
			panic(err)
		}
	}
}
