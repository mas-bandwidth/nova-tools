package friend

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/pkg/friend/pongwire"
)

// PongWirePrefix opens a schema pong in a bus body: the word and one blank, then
// the Pong table wire (schemas/friend/pong.schema, generated into
// pkg/friend/pongwire) in padded standard base64. The bus body is text, and
// base64 is the schema text form's own spelling for bytes (SPEC-TABLES.md
// §16.2: standard alphabet, padded on write).
const PongWirePrefix = "pongwire "

// The refusal names a pong is refused by. Names that come from the schema
// runtime: the generated TableRefuseReason words (wire_damaged, unknown_form,
// truncated, ...) and the read report's counters (malformed, clamped,
// kind_mismatch). PongOutOfRange is a field outside its declared bound, the
// bound read from the generated descriptors (pongwire.PongTableFields), never
// spelled here. The rest are this facade's: the text is no pong at all, the
// body is not base64, the nonce is absent, or a legacy line is not exactly
// "pong <nonce> queue=<n> working=<n> width=<n>".
const (
	PongNotAPong     = "not_a_pong"
	PongBase64       = "base64"
	PongMalformed    = "malformed"
	PongClamped      = "clamped"
	PongKindMismatch = "kind_mismatch"
	PongOutOfRange   = "out_of_range"
	PongNonceAbsent  = "nonce_absent"
	PongLegacyShape  = "legacy_shape"
)

// PongRefused is a pong refused by name: Reason is one of the names above or
// a schema runtime TableRefuseReason word. The detail never echoes the body.
type PongRefused struct {
	Reason string
	Detail string
}

func (e *PongRefused) Error() string {
	return "PONG REFUSED: " + e.Reason + ": " + e.Detail + "; run: nova-friend pong -h"
}

func refusePong(reason, detail string) error { return &PongRefused{Reason: reason, Detail: detail} }

// Pong is a pong's fields as the reader gives them back.
type Pong struct {
	Nonce                 string
	Queue, Working, Width int
	Legacy                bool // read from the legacy text line, not the table wire
}

// PongLine is a session's pong as it travels on the bus: the Pong table in
// base64 behind PongWirePrefix (EncodePong). A session sends it to answer a
// reach step or a delivery check (nova-friend pong); liveness is the nova-hook
// record's alone, never a pong. Values the table cannot carry (a negative or
// over-bound number, a nonce over 32 bytes, an empty nonce) are written as the
// legacy text line instead, which ParsePong refuses by name: the refusal lands
// at the reader, never as a silently different pong.
func PongLine(nonce string, queue, working, width int) string {
	line, err := EncodePong(nonce, queue, working, width)
	if err != nil {
		return LegacyPongLine(nonce, queue, working, width)
	}
	return line
}

// LegacyPongLine is the text pong every release before the schema pong wrote:
// pong <nonce> queue=<n> working=<n> width=<n>. ParsePong still reads it.
func LegacyPongLine(nonce string, queue, working, width int) string {
	return fmt.Sprintf("pong %s queue=%d working=%d width=%d", nonce, queue, working, width)
}

// EncodePong writes the schema pong, refusing by name what the table cannot
// carry. The generated Go writer checks no bound (it writes what it is given
// and the reader clamps), so the bounds are checked here against the
// generated descriptors before the write.
func EncodePong(nonce string, queue, working, width int) (string, error) {
	var p pongwire.Pong
	if err := fillPong(&p, nonce, queue, working, width); err != nil {
		return "", err
	}
	size := pongwire.PongMeasure(&p)
	if size <= 0 {
		return "", refusePong(PongMalformed, "the pong table did not measure")
	}
	wire := make([]byte, size)
	if n := pongwire.PongSave(&p, wire); n != size {
		return "", refusePong(PongMalformed, "the pong table did not save")
	}
	return PongWirePrefix + base64.StdEncoding.EncodeToString(wire), nil
}

// fillPong checks each value against its declared bound and fills the table.
func fillPong(p *pongwire.Pong, nonce string, queue, working, width int) error {
	if nonce == "" {
		return refusePong(PongNonceAbsent, "a pong carries the nonce it answers")
	}
	if !utf8.ValidString(nonce) {
		return refusePong(PongMalformed, "the nonce is not UTF-8, which a string field's reader refuses")
	}
	for _, f := range pongwire.PongTableFields {
		switch f.Name {
		case "nonce":
			if len(nonce) > int(f.ArrayBound) {
				return refusePong(PongOutOfRange, fmt.Sprintf("nonce is %d bytes, over its bound %d", len(nonce), f.ArrayBound))
			}
		case "queue", "working", "width":
			v := map[string]int{"queue": queue, "working": working, "width": width}[f.Name]
			if f.HasRange && (float64(v) < f.RangeMin || float64(v) > f.RangeMax) {
				return refusePong(PongOutOfRange, fmt.Sprintf("%s %d is outside [%g, %g]", f.Name, v, f.RangeMin, f.RangeMax))
			}
		}
	}
	p.NonceLength = int32(copy(p.Nonce[:], nonce))
	p.Queue, p.Working, p.Width = uint32(queue), uint32(working), uint32(width) // in range: checked above
	return nil
}

// ParsePong reads a pong; ok is false when the text is no pong or a refused
// one (ReadPong names why).
func ParsePong(text string) (nonce string, queue, working, width int, ok bool) {
	p, err := ReadPong(text)
	if err != nil {
		return "", 0, 0, 0, false
	}
	return p.Nonce, p.Queue, p.Working, p.Width, true
}

// ReadPong reads a schema pong (PongWirePrefix) or, for one release, the legacy
// text line, and refuses by name anything else. A schema pong is refused when
// the schema runtime refuses its wire or its read report says malformed,
// clamped or kind_mismatch. Unknown fields (a newer writer's) are skipped and
// counted by the runtime and are not a refusal: that is the variable table's
// promise (the schema compiler's versioning document, promises 1-3). The legacy line must be exactly
// "pong <nonce> queue=<n> working=<n> width=<n>" with every value in its bound;
// the old bare "pong <nonce>", read before as zeros, is refused.
func ReadPong(text string) (Pong, error) {
	if body, ok := strings.CutPrefix(text, PongWirePrefix); ok {
		return readPongWire(body)
	}
	if strings.HasPrefix(text, "pong ") {
		return readLegacyPong(text)
	}
	return Pong{}, refusePong(PongNotAPong, "the text is neither a schema pong nor a legacy pong line")
}

func readPongWire(body string) (Pong, error) {
	wire, err := base64.StdEncoding.Strict().DecodeString(body)
	if err != nil {
		return Pong{}, refusePong(PongBase64, "the pong body is not padded standard base64")
	}
	if _, err := pongwire.PongLoadMeasureReason(wire); err != nil {
		return Pong{}, refusePong(err.Error(), fmt.Sprintf("the schema runtime refused the %d-byte pong wire", len(wire)))
	}
	var b pongwire.PongBuilder
	if !b.Init() {
		return Pong{}, refusePong(pongwire.TableRefuseAllocationFailed.Error(), "the pong builder did not start")
	}
	defer b.Shutdown()
	var report pongwire.TableReport
	loaded := pongwire.PongLoadBuilder(&b, wire, &report)
	switch {
	case !loaded || report.Malformed:
		return Pong{}, refusePong(PongMalformed, "the schema runtime read the pong wire as damaged")
	case report.Clamped > 0:
		return Pong{}, refusePong(PongClamped, fmt.Sprintf("%d value(s) outside the declared bounds", report.Clamped))
	case report.KindMismatch > 0:
		return Pong{}, refusePong(PongKindMismatch, fmt.Sprintf("%d field(s) of a changed type", report.KindMismatch))
	}
	p := b.GetRoot()
	out := Pong{Nonce: string(p.Nonce[:p.NonceLength]), Queue: int(p.Queue), Working: int(p.Working), Width: int(p.Width)}
	if out.Nonce == "" {
		return Pong{}, refusePong(PongNonceAbsent, "a pong carries the nonce it answers")
	}
	return out, nil
}

// readLegacyPong reads the legacy line strictly: four fields in order, nothing
// after them, each value in the bound the schema declares.
func readLegacyPong(text string) (Pong, error) {
	words := strings.Split(text, " ")
	if len(words) != 5 || words[0] != "pong" {
		return Pong{}, refusePong(PongLegacyShape, fmt.Sprintf("a legacy pong is five words, this is %d", len(words)))
	}
	var nums [3]int
	for i, key := range []string{"queue", "working", "width"} {
		v, ok := strings.CutPrefix(words[2+i], key+"=")
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			return Pong{}, refusePong(PongLegacyShape, "word "+strconv.Itoa(3+i)+" is not "+key+"=<n>")
		}
		nums[i] = n
	}
	var p pongwire.Pong
	if err := fillPong(&p, words[1], nums[0], nums[1], nums[2]); err != nil {
		return Pong{}, err
	}
	return Pong{Nonce: words[1], Queue: nums[0], Working: nums[1], Width: nums[2], Legacy: true}, nil
}
