package hookwire_test

// The golden test of the hook area's Layer 1 definitions (schemas/hook): the
// send receipt (Ack), the beat's hot scalars (BeatPulse) and the hook lease
// stamp (HookStamp). Each is read through pkg/schemaread, the one strict read,
// with a Codec built here from the generated functions: the binary wire,
// padded standard base64 of the wire and the schema JSON text all round-trip;
// each named refusal of the schema-tables union's section 4.6 that applies to
// these tables fires on its bound; each renders to the name-keyed text form;
// and today's legacy text of each (the SEND OK line with its NOTE push= line,
// the JSON beat record, the JSON lease stamp) is read by the same reader to
// the same value, the dual read a cutover needs, with the per-form counter as
// its evidence. No real time, socket or network: every time is a fixed
// millisecond count.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/hookwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxMs is the largest millisecond count a stamp or a beat carries: 2^53-1,
// exact in every JSON reader.
const maxMs = 1<<53 - 1

// gen is one generated table's functions, the shape every table here shares.
type gen[T any] struct {
	reset         func(*T)
	measureReason func([]byte) (int64, error)
	load          func(region, wire []byte, r *hookwire.TableReport) *T
	measure       func(*T, ...hookwire.TableWriteContext) int64
	save          func(*T, []byte, ...hookwire.TableWriteContext) int64
	fromJSON      func(text []byte, r *hookwire.TableReport) (T, bool)
	toJSONMeasure func(*T, ...hookwire.TableAllocator) int64
	toJSON        func(*T, []byte, ...hookwire.TableAllocator) int64
}

// reportOf narrows the generated TableReport to what the strict read decides
// on. The counters stay counters; any other refusal the report names (an
// unknown form, damaged framing) is its Reason.
func reportOf(r *hookwire.TableReport) schemaread.Report {
	rep := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed}
	if err := r.Err(); err != nil {
		var reason hookwire.TableRefuseReason
		if errors.As(err, &reason) {
			switch reason {
			case hookwire.TableRefuseMalformed, hookwire.TableRefuseClamped, hookwire.TableRefuseKindMismatch:
				return rep
			}
		}
		rep.Reason = err.Error()
	}
	return rep
}

// codecOf binds g into the strict read's Codec under name and help.
func codecOf[T any](name, help string, g gen[T], constraints func(*T, *schemaread.Checker)) *schemaread.Codec[T] {
	return &schemaread.Codec[T]{
		Name:  name,
		Help:  help,
		Reset: g.reset,
		Measure: func(wire []byte) error {
			_, err := g.measureReason(wire)
			return err
		},
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			n, err := g.measureReason(wire)
			if err != nil {
				return schemaread.Report{Reason: err.Error()}
			}
			var r hookwire.TableReport
			p := g.load(region(n), wire, &r)
			rep := reportOf(&r)
			if p == nil {
				if rep.Reason == "" && !rep.Malformed {
					rep.Reason = schemaread.ReasonWireDamaged
				}
				return rep
			}
			*v = *p // the rows hold no pointer: a copy owns everything
			return rep
		},
		LoadJSON: func(v *T, text []byte) schemaread.Report {
			var r hookwire.TableReport
			got, ok := g.fromJSON(text, &r)
			rep := reportOf(&r)
			if !ok && rep.Reason == "" {
				rep.Malformed = true
			}
			*v = got
			return rep
		},
		Constraints: constraints,
		SaveWire: func(v *T) []byte {
			n := g.measure(v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			return buf[:g.save(v, buf)]
		},
		SaveJSON: func(v *T) []byte {
			n := g.toJSONMeasure(v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			return buf[:g.toJSON(v, buf)]
		},
	}
}

// region is n bytes at a 64-byte boundary, the alignment a table region keeps.
func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func setText(dst []byte, n *int32, s string) { *n = int32(copy(dst, s)) }

func text(src []byte, n int32) string { return string(src[:n]) }

// --- the three tables' codecs --------------------------------------------------

const (
	ackName   = "ACK"
	ackHelp   = "nova-bus send -h"
	beatName  = "BEAT"
	beatHelp  = "nova-sprint fleet beat -h"
	stampName = "HOOK STAMP"
	stampHelp = "nova-hook status -h"
)

var ackGen = gen[hookwire.Ack]{
	reset: hookwire.AckReset, measureReason: hookwire.AckLoadMeasureReason, load: hookwire.AckLoad,
	measure: hookwire.AckMeasure, save: hookwire.AckSave,
	fromJSON: func(text []byte, r *hookwire.TableReport) (hookwire.Ack, bool) {
		var b hookwire.AckBuilder
		if !b.Init() {
			return hookwire.Ack{}, false
		}
		defer b.Shutdown()
		ok := hookwire.AckFromJson(&b, text, r)
		return *b.GetRoot(), ok
	},
	toJSONMeasure: hookwire.AckToJsonMeasure, toJSON: hookwire.AckToJson,
}

func ackConstraints(v *hookwire.Ack, c *schemaread.Checker) {
	c.Required("id", v.IdLength > 0)
	c.Text("id", text(v.Id[:], v.IdLength), 64)
	c.Required("stage", v.Stage != hookwire.StageNone)
	c.Enum("stage", int(v.Stage), int(hookwire.StageMax)+1)
	c.Required("push", v.Push != hookwire.PushStateNone)
	c.Enum("push", int(v.Push), int(hookwire.PushStateMax)+1)
	c.Text("reason", text(v.Reason[:], v.ReasonLength), 152)
}

var beatGen = gen[hookwire.BeatPulse]{
	reset: hookwire.BeatPulseReset, measureReason: hookwire.BeatPulseLoadMeasureReason, load: hookwire.BeatPulseLoad,
	measure: hookwire.BeatPulseMeasure, save: hookwire.BeatPulseSave,
	fromJSON: func(text []byte, r *hookwire.TableReport) (hookwire.BeatPulse, bool) {
		var b hookwire.BeatPulseBuilder
		if !b.Init() {
			return hookwire.BeatPulse{}, false
		}
		defer b.Shutdown()
		ok := hookwire.BeatPulseFromJson(&b, text, r)
		return *b.GetRoot(), ok
	},
	toJSONMeasure: hookwire.BeatPulseToJsonMeasure, toJSON: hookwire.BeatPulseToJson,
}

func beatConstraints(v *hookwire.BeatPulse, c *schemaread.Checker) {
	c.Required("name", v.NameLength > 0)
	c.Text("name", text(v.Name[:], v.NameLength), 64)
	c.Required("at_ms", v.AtMsPresent)
	c.Bound("at_ms", v.AtMs, 0, maxMs)
	// a percent of all cores: a NaN, an infinity or a value outside [0, 100] is
	// not a load, and the checker refuses it as a clamp of the declared range
	if l := v.LoadPct; math.IsNaN(l) || math.IsInf(l, 0) || l < 0 || l > 100 {
		c.Bound("load_pct", 1, 0, 0)
	}
	c.Bound("cores", int64(v.Cores), 0, 65536)
	c.Text("how", text(v.How[:], v.HowLength), 16)
	if v.PongMsPresent {
		c.Bound("pong_ms", v.PongMs, 0, maxMs)
	}
	c.Required("stop_returns", v.StopReturnsPresent)
	c.Bound("stop_returns", int64(v.StopReturns), 0, 10000)
	c.Text("no_room", text(v.NoRoom[:], v.NoRoomLength), 200)
}

var stampGen = gen[hookwire.HookStamp]{
	reset: hookwire.HookStampReset, measureReason: hookwire.HookStampLoadMeasureReason, load: hookwire.HookStampLoad,
	measure: hookwire.HookStampMeasure, save: hookwire.HookStampSave,
	fromJSON: func(text []byte, r *hookwire.TableReport) (hookwire.HookStamp, bool) {
		var b hookwire.HookStampBuilder
		if !b.Init() {
			return hookwire.HookStamp{}, false
		}
		defer b.Shutdown()
		ok := hookwire.HookStampFromJson(&b, text, r)
		return *b.GetRoot(), ok
	},
	toJSONMeasure: hookwire.HookStampToJsonMeasure, toJSON: hookwire.HookStampToJson,
}

func stampConstraints(v *hookwire.HookStamp, c *schemaread.Checker) {
	c.Required("epoch", v.EpochPresent)
	c.Bound("epoch", v.Epoch, 1, maxMs) // epochs start at 1
	c.Required("at_ms", v.AtMsPresent)
	c.Bound("at_ms", v.AtMs, 0, maxMs)
	c.Text("by", text(v.By[:], v.ByLength), 128)
}

// --- the legacy forms: today's text, read by the same reader -------------------

// The send receipt today: `SEND OK id=<id> ...` and, when the push is not up,
// a second line `NOTE push=<down|stale> <why>`. A SEND OK means the message is
// on its streams: queued.
func ackLegacy(in []byte) bool { return bytes.HasPrefix(in, []byte("SEND OK ")) }

func ackLoadLegacy(v *hookwire.Ack, in []byte) schemaread.Report {
	var rep schemaread.Report
	lines := strings.Split(strings.TrimRight(string(in), "\n"), "\n")
	for _, f := range strings.Fields(lines[0]) {
		if id, ok := strings.CutPrefix(f, "id="); ok {
			if len(id) > len(v.Id)-1 {
				rep.Clamped++
				id = id[:len(v.Id)-1]
			}
			setText(v.Id[:], &v.IdLength, id)
		}
	}
	v.Stage, v.Push = hookwire.StageQueued, hookwire.PushStateUp
	for _, l := range lines[1:] {
		note, ok := strings.CutPrefix(l, "NOTE push=")
		if !ok {
			rep.Malformed = true
			return rep
		}
		state, why, _ := strings.Cut(note, " ")
		switch state {
		case "up":
			v.Push = hookwire.PushStateUp
		case "down":
			v.Push = hookwire.PushStateDown
		case "stale":
			v.Push = hookwire.PushStateStale
		default:
			rep.Malformed = true
			return rep
		}
		if len(why) > len(v.Reason)-1 {
			rep.Clamped++
			why = why[:len(v.Reason)-1]
		}
		setText(v.Reason[:], &v.ReasonLength, why)
	}
	return rep
}

func ackCodec() *schemaread.Codec[hookwire.Ack] {
	c := codecOf(ackName, ackHelp, ackGen, ackConstraints)
	c.Legacy, c.LoadLegacy = ackLegacy, ackLoadLegacy
	return c
}

// legacyJSON is a JSON object whose keys include "at": today's beat record and
// lease stamp. The table's text form spells the time at_ms, so the two never
// meet.
func legacyJSON(in []byte) bool {
	t := bytes.TrimSpace(in)
	if len(t) == 0 || t[0] != '{' {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(t, &m) != nil {
		return false
	}
	_, ok := m["at"]
	return ok
}

// legacyBeat is the stored beat record's JSON (nova-sprint's sprint.Beat), the
// part of it the pulse carries.
type legacyBeat struct {
	At          time.Time `json:"at"`
	Load        float64   `json:"load"`
	Cores       int       `json:"cores,omitempty"`
	How         string    `json:"how,omitempty"`
	Pong        time.Time `json:"pong,omitzero"`
	StopReturns int       `json:"stop_returns,omitempty"`
	NoRoom      string    `json:"no_room,omitempty"`
	Draining    bool      `json:"draining,omitempty"`
}

// beatCodec reads the pulse of name: the legacy record carries no name, since
// its key does, so the legacy reader is told whose record it reads.
func beatCodec(name string) *schemaread.Codec[hookwire.BeatPulse] {
	c := codecOf(beatName, beatHelp, beatGen, beatConstraints)
	c.Legacy = legacyJSON
	c.LoadLegacy = func(v *hookwire.BeatPulse, in []byte) schemaread.Report {
		var b legacyBeat
		if json.Unmarshal(in, &b) != nil {
			return schemaread.Report{Malformed: true}
		}
		setText(v.Name[:], &v.NameLength, name)
		v.AtMs, v.AtMsPresent = b.At.UnixMilli(), !b.At.IsZero()
		v.LoadPct = b.Load
		if b.Cores < 0 || b.Cores > math.MaxUint32 {
			return schemaread.Report{Clamped: 1}
		}
		v.Cores = uint32(b.Cores)
		var rep schemaread.Report
		for _, f := range []struct {
			dst  []byte
			n    *int32
			text string
		}{{v.How[:], &v.HowLength, b.How}, {v.NoRoom[:], &v.NoRoomLength, b.NoRoom}} {
			if len(f.text) > len(f.dst)-1 {
				rep.Clamped++
				continue
			}
			setText(f.dst, f.n, f.text)
		}
		if !b.Pong.IsZero() {
			v.PongMs, v.PongMsPresent = b.Pong.UnixMilli(), true
		}
		if b.StopReturns < 0 || b.StopReturns > math.MaxUint32 {
			rep.Clamped++
		}
		// the legacy record omits a zero count: zero is what it said
		v.StopReturns, v.StopReturnsPresent = uint32(b.StopReturns), true
		v.Draining = b.Draining
		return rep
	}
	return c
}

// legacyStamp is today's lease or proof value, as the store's script writes it:
// {"epoch":<n>,"at":<ms>,"by":<json string>}.
type legacyStamp struct {
	Epoch *int64 `json:"epoch"`
	At    *int64 `json:"at"`
	By    string `json:"by"`
}

func stampCodec() *schemaread.Codec[hookwire.HookStamp] {
	c := codecOf(stampName, stampHelp, stampGen, stampConstraints)
	c.Legacy = legacyJSON
	c.LoadLegacy = func(v *hookwire.HookStamp, in []byte) schemaread.Report {
		var s legacyStamp
		if json.Unmarshal(in, &s) != nil {
			return schemaread.Report{Malformed: true}
		}
		if s.Epoch != nil {
			v.Epoch, v.EpochPresent = *s.Epoch, true
		}
		if s.At != nil {
			v.AtMs, v.AtMsPresent = *s.At, true
		}
		if len(s.By) > len(v.By)-1 {
			return schemaread.Report{Clamped: 1}
		}
		setText(v.By[:], &v.ByLength, s.By)
		return schemaread.Report{}
	}
	return c
}

// --- the values ----------------------------------------------------------------

const (
	atMs   = 1760000000000 // 2025-10-09T08:53:20Z, a fixed instant
	pongMs = 1759999990000
)

func newAck(id string, stage hookwire.Stage, push hookwire.PushState, reason string) hookwire.Ack {
	var a hookwire.Ack
	hookwire.AckReset(&a)
	setText(a.Id[:], &a.IdLength, id)
	a.Stage, a.Push = stage, push
	setText(a.Reason[:], &a.ReasonLength, reason)
	return a
}

func newBeat(name string) hookwire.BeatPulse {
	var b hookwire.BeatPulse
	hookwire.BeatPulseReset(&b)
	setText(b.Name[:], &b.NameLength, name)
	b.AtMs, b.AtMsPresent = atMs, true
	b.LoadPct, b.Cores = 42.5, 16
	setText(b.How[:], &b.HowLength, "cpu")
	b.PongMs, b.PongMsPresent = pongMs, true
	b.StopReturns, b.StopReturnsPresent = 2, true
	setText(b.NoRoom[:], &b.NoRoomLength, "disk under its floor")
	b.Draining = true
	return b
}

func newStamp(epoch, at int64, by string) hookwire.HookStamp {
	var s hookwire.HookStamp
	hookwire.HookStampReset(&s)
	s.Epoch, s.EpochPresent = epoch, true
	s.AtMs, s.AtMsPresent = at, true
	setText(s.By[:], &s.ByLength, by)
	return s
}

const ackID = "01JZ8Q4S5N6M7K8J9H0G1F2E3D"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".b64"))
	require.NoError(t, err)
	return bytes.TrimSpace(b)
}

// refused is the refusal reason and field of err, which must be a refusal in
// the house style.
func refused(t *testing.T, name, help string, err error) (reason, field string) {
	t.Helper()
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.Equal(t, name, r.Name)
	assert.True(t, strings.HasPrefix(err.Error(), name+" REFUSED: "+r.Reason), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+help), err.Error())
	return r.Reason, r.Field
}

// --- the test ------------------------------------------------------------------

// golden is what one table's round trip and refusal battery needs.
type golden[T any] struct {
	name, help string
	codec      *schemaread.Codec[T]
	values     []T
	keys       []string // the text form's keys, the schema's field names
}

func (g golden[T]) roundTrips(t *testing.T) {
	r := schemaread.NewReader(g.codec)
	for i, v := range g.values {
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&v, form)
			require.NoError(t, err, "%s #%d as %s", g.name, i, form)
			got, res, err := r.Read(out)
			require.NoError(t, err, "%s #%d as %s: %q", g.name, i, form, out)
			assert.Equal(t, form, res.Form, "%s #%d", g.name, i)
			assert.Equal(t, v, got, "%s #%d as %s", g.name, i, form)
		}
		// base64 is the wire, whole: the same bytes as the binary form
		wire, _ := r.Write(&v, schemaread.FormBinary)
		b64, _ := r.Write(&v, schemaread.FormBase64)
		assert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(b64), g.name)
		assert.Equal(t, byte(1), wire[0], "%s: the file form byte leads the wire", g.name)
	}
	n := uint64(len(g.values))
	assert.Equal(t, map[string]uint64{"binary": n, "base64": n, "json": n, "legacy": 0, "unknown_fields": 0}, r.Counts().Snapshot(), g.name)
}

// rendersNameKeyed: the JSON text is the name-keyed text form, every key one of
// the schema's field names, and an enum spelled by its variant's name.
func (g golden[T]) rendersNameKeyed(t *testing.T, want map[string]string) {
	r := schemaread.NewReader(g.codec)
	v := g.values[0]
	out, err := r.Write(&v, schemaread.FormJSON)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m), "%s: %s", g.name, out)
	for k := range m {
		assert.Contains(t, g.keys, k, "%s: key %q is a field name", g.name, k)
	}
	for k, w := range want {
		assert.Equal(t, w, fmt.Sprint(m[k]), "%s.%s in %s", g.name, k, out)
	}
	assert.True(t, bytes.HasSuffix(out, []byte("}\n")), "%s: one newline ends the text", g.name)
}

// refuses reads in and wants reason (and field, when one is named).
func (g golden[T]) refuses(t *testing.T, label string, in []byte, reason, field string) {
	r := schemaread.NewReader(g.codec)
	_, _, err := r.Read(in)
	require.Error(t, err, "%s %s: %q is refused", g.name, label, in)
	got, gotField := refused(t, g.name, g.help, err)
	assert.Equal(t, reason, got, "%s %s: %v", g.name, label, err)
	if field != "" {
		assert.Equal(t, field, gotField, "%s %s: %v", g.name, label, err)
	}
	assert.Equal(t, uint64(1), r.Counts().Refused(reason), "%s %s: the refusal is counted", g.name, label)
}

// damages runs the wire refusals every table shares: an unknown form byte,
// every truncation, trailing bytes, base64 that does not decode.
func (g golden[T]) damages(t *testing.T) {
	r := schemaread.NewReader(g.codec)
	v := g.values[0]
	wire, err := r.Write(&v, schemaread.FormBinary)
	require.NoError(t, err)
	newer := bytes.Clone(wire)
	newer[0] = 9
	g.refuses(t, "a form byte this reader does not carry", newer, schemaread.ReasonUnknownForm, "")
	g.refuses(t, "the same, in base64", []byte(base64.StdEncoding.EncodeToString(newer)), schemaread.ReasonUnknownForm, "")
	g.refuses(t, "trailing bytes after the wire", append(bytes.Clone(wire), 0x00, 0x01), schemaread.ReasonWireDamaged, "")
	g.refuses(t, "the form byte alone", wire[:1], schemaread.ReasonWireDamaged, "")
	b64 := base64.StdEncoding.EncodeToString(wire)
	g.refuses(t, "base64 cut short", []byte(b64[:len(b64)-3]), schemaread.ReasonMalformed, "")
	g.refuses(t, "base64 with junk after it", []byte(b64+" junk"), schemaread.ReasonMalformed, "")
	g.refuses(t, "nothing", nil, schemaread.ReasonUnknownForm, "")
	g.refuses(t, "JSON that is not JSON", []byte(`{"id": `), schemaread.ReasonMalformed, "")
	for i := 1; i < len(wire); i++ {
		_, _, err := schemaread.NewReader(g.codec).Read(wire[:i])
		assert.Error(t, err, "%s cut at byte %d of %d is refused, never read as a shorter value", g.name, i, len(wire))
	}
}

// writerRefuses: what the reader refuses, the writer refuses before writing,
// in every form, and writes nothing.
func (g golden[T]) writerRefuses(t *testing.T, label string, v T, reason, field string) {
	r := schemaread.NewReader(g.codec)
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		out, err := r.Write(&v, form)
		assert.Nil(t, out, "%s %s as %s: nothing is written", g.name, label, form)
		require.Error(t, err, "%s %s as %s", g.name, label, form)
		got, gotField := refused(t, g.name, g.help, err)
		assert.Equal(t, reason, got, "%s %s", g.name, label)
		assert.Equal(t, field, gotField, "%s %s", g.name, label)
	}
}

// unsafeWire writes v's wire with no check: what a writer without the facade
// would put on the bus, for the reader to refuse.
func unsafeWire[T any](g gen[T], v T) []byte {
	buf := make([]byte, g.measure(&v))
	return buf[:g.save(&v, buf)]
}

func TestLayer1AckBeatHook(t *testing.T) {
	t.Parallel()

	ack := golden[hookwire.Ack]{
		name: ackName, help: ackHelp, codec: ackCodec(),
		values: []hookwire.Ack{
			newAck(ackID, hookwire.StagePushed, hookwire.PushStateUp, ""),
			newAck(ackID, hookwire.StageQueued, hookwire.PushStateDown, "no live lease: its holder is not running"),
			newAck(strings.Repeat("x", 64), hookwire.StageActed, hookwire.PushStateStale, strings.Repeat("r", 152)),
			newAck("a", hookwire.StageClaimed, hookwire.PushStateUp, "ünïcödé"),
		},
		keys: []string{"id", "stage", "push", "reason"},
	}
	beat := golden[hookwire.BeatPulse]{
		name: beatName, help: beatHelp, codec: beatCodec("ada"),
		values: func() []hookwire.BeatPulse {
			full := newBeat("ada")
			var bare hookwire.BeatPulse // the least a pulse says: who, when, and a count of zero, said
			hookwire.BeatPulseReset(&bare)
			setText(bare.Name[:], &bare.NameLength, "ada")
			bare.AtMs, bare.AtMsPresent = atMs, true
			bare.StopReturnsPresent = true
			edge := newBeat(strings.Repeat("n", 64))
			edge.AtMs, edge.LoadPct, edge.Cores, edge.StopReturns = maxMs, 100, 65536, 10000
			edge.PongMs = 0
			zero := newBeat("z")
			zero.AtMs = 0 // the epoch instant is a time, and present
			return []hookwire.BeatPulse{full, bare, edge, zero}
		}(),
		keys: []string{"name", "at_ms", "load_pct", "cores", "how", "pong_ms", "stop_returns", "no_room", "draining"},
	}
	stamp := golden[hookwire.HookStamp]{
		name: stampName, help: stampHelp, codec: stampCodec(),
		values: []hookwire.HookStamp{
			newStamp(7, atMs, "nova-friend daemon"),
			newStamp(1, 0, ""),
			newStamp(maxMs, maxMs, strings.Repeat("b", 128)),
		},
		keys: []string{"epoch", "at_ms", "by"},
	}

	t.Run("round trips as binary wire, base64 and JSON", func(t *testing.T) {
		ack.roundTrips(t)
		beat.roundTrips(t)
		stamp.roundTrips(t)
	})

	t.Run("renders to the name-keyed text form", func(t *testing.T) {
		ack.rendersNameKeyed(t, map[string]string{"id": ackID, "stage": "Pushed", "push": "Up"})
		beat.rendersNameKeyed(t, map[string]string{"name": "ada", "at_ms": "1.76e+12", "stop_returns": "2", "draining": "true"})
		stamp.rendersNameKeyed(t, map[string]string{"epoch": "7", "at_ms": "1.76e+12", "by": "nova-friend daemon"})
	})

	t.Run("wire damage is refused by name", func(t *testing.T) {
		ack.damages(t)
		beat.damages(t)
		stamp.damages(t)
	})

	t.Run("kind_mismatch: a field a writer changed the kind of", func(t *testing.T) {
		ack.refuses(t, "stage as a string", fixture(t, "ack-changed-kind"), schemaread.ReasonKindMismatch, "")
		beat.refuses(t, "cores as a string", fixture(t, "beat-changed-kind"), schemaread.ReasonKindMismatch, "")
		stamp.refuses(t, "epoch as a string", fixture(t, "stamp-changed-kind"), schemaread.ReasonKindMismatch, "")
		ack.refuses(t, "a stage as a number in JSON", []byte(`{"id": "x", "stage": 2, "push": "Up"}`), schemaread.ReasonKindMismatch, "")
	})

	t.Run("clamped: a value past its bound", func(t *testing.T) {
		// from a writer with wider bounds, on the wire
		ack.refuses(t, "a 100-byte id", fixture(t, "ack-oversize-id"), schemaread.ReasonClamped, "")
		beat.refuses(t, "70000 cores", fixture(t, "beat-cores-over"), schemaread.ReasonClamped, "")
		stamp.refuses(t, "a negative time", fixture(t, "stamp-negative-at"), schemaread.ReasonClamped, "")
		// in the text form
		beat.refuses(t, "stop_returns over 10000 in JSON", []byte(`{"name": "ada", "at_ms": 1, "stop_returns": 10001}`), schemaread.ReasonClamped, "")
		stamp.refuses(t, "an at_ms past 2^53-1 in JSON", []byte(`{"epoch": 1, "at_ms": 9007199254740992}`), schemaread.ReasonClamped, "")
		// by the facade's bound, where the schema's range must reach zero or a
		// float is not ranged
		stamp.refuses(t, "epoch 0", unsafeWire(stampGen, newStamp(0, atMs, "x")), schemaread.ReasonClamped, "epoch")
		over := newBeat("ada")
		over.LoadPct = 100.5
		beat.refuses(t, "load over 100", unsafeWire(beatGen, over), schemaread.ReasonClamped, "load_pct")
		over.LoadPct = math.NaN()
		beat.refuses(t, "a NaN load", unsafeWire(beatGen, over), schemaread.ReasonClamped, "load_pct")
		over.LoadPct = -1
		beat.refuses(t, "a negative load", unsafeWire(beatGen, over), schemaread.ReasonClamped, "load_pct")
	})

	t.Run("absent is refused where zero is no value", func(t *testing.T) {
		ack.refuses(t, "no id", unsafeWire(ackGen, newAck("", hookwire.StageQueued, hookwire.PushStateUp, "")), schemaread.ReasonAbsentRequired, "id")
		ack.refuses(t, "no stage", unsafeWire(ackGen, newAck(ackID, hookwire.StageNone, hookwire.PushStateUp, "")), schemaread.ReasonAbsentRequired, "stage")
		ack.refuses(t, "no push", unsafeWire(ackGen, newAck(ackID, hookwire.StageQueued, hookwire.PushStateNone, "")), schemaread.ReasonAbsentRequired, "push")
		ack.refuses(t, "no stage in JSON", []byte(`{"id": "x", "push": "Up"}`), schemaread.ReasonAbsentRequired, "stage")
		// a variant a newer writer added is an unknown name to this reader: skipped,
		// counted, and the stage reads absent, which is refused
		ack.refuses(t, "a stage variant this build does not know", []byte(`{"id": "x", "stage": "Landed", "push": "Up"}`), schemaread.ReasonAbsentRequired, "stage")
		noName := newBeat("")
		beat.refuses(t, "no name", unsafeWire(beatGen, noName), schemaread.ReasonAbsentRequired, "name")
		noAt := newBeat("ada")
		noAt.AtMs, noAt.AtMsPresent = 0, false
		beat.refuses(t, "no at_ms", unsafeWire(beatGen, noAt), schemaread.ReasonAbsentRequired, "at_ms")
		noStops := newBeat("ada")
		noStops.StopReturns, noStops.StopReturnsPresent = 0, false
		beat.refuses(t, "no stop_returns", unsafeWire(beatGen, noStops), schemaread.ReasonAbsentRequired, "stop_returns")
		beat.refuses(t, "no stop_returns in JSON", []byte(`{"name": "ada", "at_ms": 1}`), schemaread.ReasonAbsentRequired, "stop_returns")
		noEpoch := newStamp(7, atMs, "x")
		noEpoch.Epoch, noEpoch.EpochPresent = 0, false
		stamp.refuses(t, "no epoch", unsafeWire(stampGen, noEpoch), schemaread.ReasonAbsentRequired, "epoch")
		noStampAt := newStamp(7, atMs, "x")
		noStampAt.AtMs, noStampAt.AtMsPresent = 0, false
		stamp.refuses(t, "no at_ms", unsafeWire(stampGen, noStampAt), schemaread.ReasonAbsentRequired, "at_ms")
		stamp.refuses(t, "an empty JSON stamp", []byte(`{}`), schemaread.ReasonAbsentRequired, "epoch")
	})

	t.Run("malformed text", func(t *testing.T) {
		nul := newAck(ackID, hookwire.StageQueued, hookwire.PushStateUp, "a\x00b")
		ack.refuses(t, "a NUL in the reason", unsafeWire(ackGen, nul), schemaread.ReasonMalformed, "")
		ack.refuses(t, "a NOTE that is not a push state", []byte("SEND OK id=x to=bob\nNOTE push=sideways"), schemaread.ReasonMalformed, "")
		stamp.refuses(t, "a legacy stamp that is not JSON", []byte(`{"epoch":7,"at":`), schemaread.ReasonMalformed, "")
	})

	t.Run("the writer refuses what the reader refuses, and writes nothing", func(t *testing.T) {
		ack.writerRefuses(t, "no stage", newAck(ackID, hookwire.StageNone, hookwire.PushStateUp, ""), schemaread.ReasonAbsentRequired, "stage")
		ack.writerRefuses(t, "a stage past the enum", newAck(ackID, hookwire.StageMax+1, hookwire.PushStateUp, ""), schemaread.ReasonClamped, "stage")
		ack.writerRefuses(t, "a reason that is not UTF-8", newAck(ackID, hookwire.StageQueued, hookwire.PushStateUp, "\xff"), schemaread.ReasonMalformed, "reason")
		over := newBeat("ada")
		over.LoadPct = 101
		beat.writerRefuses(t, "load over 100", over, schemaread.ReasonClamped, "load_pct")
		beat.writerRefuses(t, "cores over 65536", func() hookwire.BeatPulse { b := newBeat("ada"); b.Cores = 65537; return b }(), schemaread.ReasonClamped, "cores")
		stamp.writerRefuses(t, "epoch 0", newStamp(0, atMs, "x"), schemaread.ReasonClamped, "epoch")
		stamp.writerRefuses(t, "a negative time", newStamp(1, -1, "x"), schemaread.ReasonClamped, "at_ms")
	})

	t.Run("a newer writer's field is skipped and counted", func(t *testing.T) {
		ra := schemaread.NewReader(ackCodec())
		a, res, err := ra.Read(fixture(t, "ack-newer-field"))
		require.NoError(t, err)
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, newAck("01JZ0000000000000000000000", hookwire.StagePushed, hookwire.PushStateUp, ""), a)
		rb := schemaread.NewReader(beatCodec("ada"))
		b, res, err := rb.Read(fixture(t, "beat-newer-field"))
		require.NoError(t, err)
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, []any{"ada", int64(atMs), 42.5, uint32(16), true}, []any{text(b.Name[:], b.NameLength), b.AtMs, b.LoadPct, b.Cores, b.StopReturnsPresent})
		rs := schemaread.NewReader(stampCodec())
		s, res, err := rs.Read(fixture(t, "stamp-newer-field"))
		require.NoError(t, err)
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, newStamp(7, atMs, "holder-1"), s)
		for _, c := range []*schemaread.Counts{ra.Counts(), rb.Counts(), rs.Counts()} {
			assert.Equal(t, uint64(1), c.Unknown())
		}
	})

	t.Run("dual read: the legacy form and the table form read alike", func(t *testing.T) {
		dualReadAck(t)
		dualReadBeat(t)
		dualReadStamp(t)
	})

	t.Run("writer switch: old readers keep working, and never misread", func(t *testing.T) {
		writerSwitch(t)
	})
}

// --- dual read -----------------------------------------------------------------

func dualReadAck(t *testing.T) {
	r := schemaread.NewReader(ackCodec())
	for _, tc := range []struct {
		legacy string
		want   hookwire.Ack
	}{
		{"SEND OK id=" + ackID + " to=bob cc= at=2025-10-09T08:53:20Z bytes=5 sha256=00",
			newAck(ackID, hookwire.StageQueued, hookwire.PushStateUp, "")},
		{"SEND OK id=" + ackID + " to=bob cc= at=2025-10-09T08:53:20Z bytes=5 sha256=00\nNOTE push=down no live lease",
			newAck(ackID, hookwire.StageQueued, hookwire.PushStateDown, "no live lease")},
		{"SEND OK id=" + ackID + " to=bob cc= at=2025-10-09T08:53:20Z bytes=5 sha256=00\nNOTE push=stale holder stopped answering",
			newAck(ackID, hookwire.StageQueued, hookwire.PushStateStale, "holder stopped answering")},
	} {
		old, res, err := r.Read([]byte(tc.legacy))
		require.NoError(t, err, tc.legacy)
		assert.Equal(t, schemaread.FormLegacy, res.Form)
		assert.Equal(t, tc.want, old, tc.legacy)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&old, form)
			require.NoError(t, err)
			typed, _, err := r.Read(out)
			require.NoError(t, err)
			assert.Equal(t, old, typed, "%s: the legacy and the %s form read alike", tc.legacy, form)
		}
	}
	// a refused send is no receipt: today a substring match cannot tell it from
	// a stale push; the reader refuses it by name
	_, _, err := r.Read([]byte(`SEND REFUSED: subject over 200 bytes; run: nova-bus send -h`))
	require.Error(t, err)
	reason, _ := refused(t, ackName, ackHelp, err)
	assert.Equal(t, schemaread.ReasonMalformed, reason)
	assert.Equal(t, uint64(3), r.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(3), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(3), r.Counts().Reads(schemaread.FormJSON))
}

func dualReadBeat(t *testing.T) {
	r := schemaread.NewReader(beatCodec("ada"))
	at := time.UnixMilli(atMs).UTC()
	for _, lb := range []legacyBeat{
		{At: at, Load: 42.5, Cores: 16, How: "cpu", Pong: time.UnixMilli(pongMs).UTC(), StopReturns: 2, NoRoom: "disk under its floor", Draining: true},
		{At: at}, // a machine's beat with nothing more to say: stop_returns omitted is zero
	} {
		raw, err := json.Marshal(lb)
		require.NoError(t, err)
		old, res, err := r.Read(raw)
		require.NoError(t, err, "%s", raw)
		assert.Equal(t, schemaread.FormLegacy, res.Form)
		assert.Equal(t, int64(atMs), old.AtMs)
		assert.True(t, old.StopReturnsPresent)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&old, form)
			require.NoError(t, err)
			typed, _, err := r.Read(out)
			require.NoError(t, err)
			assert.Equal(t, old, typed, "%s: the legacy and the %s form read alike", raw, form)
		}
	}
	assert.Equal(t, newBeat("ada"), func() hookwire.BeatPulse {
		raw, _ := json.Marshal(legacyBeat{At: at, Load: 42.5, Cores: 16, How: "cpu", Pong: time.UnixMilli(pongMs).UTC(), StopReturns: 2, NoRoom: "disk under its floor", Draining: true})
		v, _, err := r.Read(raw)
		require.NoError(t, err)
		return v
	}(), "the legacy record reads as the pulse the table form carries")
	// a legacy record with no time is refused, never read as the epoch instant
	_, _, err := r.Read([]byte(`{"at":"0001-01-01T00:00:00Z","load":1}`))
	require.Error(t, err)
	reason, field := refused(t, beatName, beatHelp, err)
	assert.Equal(t, []string{schemaread.ReasonAbsentRequired, "at_ms"}, []string{reason, field})
	// four legacy reads, the refused one included: a read counts on its form
	assert.Equal(t, uint64(4), r.Counts().Reads(schemaread.FormLegacy))
}

func dualReadStamp(t *testing.T) {
	r := schemaread.NewReader(stampCodec())
	for _, tc := range []struct {
		legacy string
		want   hookwire.HookStamp
	}{
		{`{"epoch":7,"at":1760000000000,"by":"nova-friend daemon"}`, newStamp(7, atMs, "nova-friend daemon")},
		{`{"epoch":1,"at":0,"by":""}`, newStamp(1, 0, "")},
	} {
		old, res, err := r.Read([]byte(tc.legacy))
		require.NoError(t, err, tc.legacy)
		assert.Equal(t, schemaread.FormLegacy, res.Form)
		assert.Equal(t, tc.want, old)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := r.Write(&old, form)
			require.NoError(t, err)
			typed, _, err := r.Read(out)
			require.NoError(t, err)
			assert.Equal(t, old, typed, "%s: the legacy and the %s form read alike", tc.legacy, form)
		}
	}
	// a legacy stamp without an epoch proved nothing before and proves nothing now
	_, _, err := r.Read([]byte(`{"at":1760000000000,"by":"x"}`))
	require.Error(t, err)
	reason, field := refused(t, stampName, stampHelp, err)
	assert.Equal(t, []string{schemaread.ReasonAbsentRequired, "epoch"}, []string{reason, field})
}

// --- writer switch -------------------------------------------------------------

// oldStampReader is today's reader of a lease or proof value (the hook
// package's parseStamp): a value that does not unmarshal proves nothing.
func oldStampReader(raw string) (epoch, at int64, by string, ok bool) {
	var s struct {
		Epoch int64  `json:"epoch"`
		At    int64  `json:"at"`
		By    string `json:"by"`
	}
	if raw == "" || json.Unmarshal([]byte(raw), &s) != nil {
		return 0, 0, "", false
	}
	return s.Epoch, s.At, s.By, true
}

// oldAckReader is today's reader of a send receipt: the id after SEND OK id=.
func oldAckReader(out string) (id string, ok bool) {
	_, rest, ok := strings.Cut(out, "SEND OK id=")
	if !ok {
		return "", false
	}
	id, _, _ = strings.Cut(rest, " ")
	return id, true
}

// stampWriter writes a lease stamp: today's JSON until switched, then the
// table wire as padded base64, the form a text value in the store carries.
type stampWriter struct{ switched bool }

func (w stampWriter) write(t *testing.T, r *schemaread.Reader[hookwire.HookStamp], s hookwire.HookStamp) string {
	if !w.switched {
		return fmt.Sprintf(`{"epoch":%d,"at":%d,"by":%q}`, s.Epoch, s.AtMs, text(s.By[:], s.ByLength))
	}
	out, err := r.Write(&s, schemaread.FormBase64)
	require.NoError(t, err)
	return string(out)
}

func writerSwitch(t *testing.T) {
	r := schemaread.NewReader(stampCodec())
	var w stampWriter
	stamps := []hookwire.HookStamp{newStamp(7, atMs, "a"), newStamp(8, atMs+1, "b"), newStamp(9, atMs+2, "c"), newStamp(10, atMs+3, "d")}
	for i, s := range stamps {
		if i == 2 {
			w.switched = true // the readers adopted fleet-wide; now the writer switches
		}
		raw := w.write(t, r, s)
		got, _, err := r.Read([]byte(raw))
		require.NoError(t, err, "the new reader reads stamp %d whichever form wrote it: %s", i, raw)
		assert.Equal(t, s, got)
		e, at, by, ok := oldStampReader(raw)
		if !w.switched {
			assert.True(t, ok, "before the switch the old reader reads stamp %d", i)
			assert.Equal(t, []any{s.Epoch, s.AtMs, text(s.By[:], s.ByLength)}, []any{e, at, by})
		} else {
			// after the switch an old reader that was missed refuses the value
			// (it proves nothing, so the gate fails closed and says so) and never
			// reads another epoch or time out of it
			assert.False(t, ok, "an old reader never misreads a switched stamp: %s", raw)
		}
	}
	// the per-form counter is the cutover's evidence: two legacy reads before
	// the switch, none after it
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBase64))

	// the schema JSON text is not the form to switch an old reader's value to:
	// today's reader unmarshals it, finds the epoch and reads no time. The
	// writer switch writes base64 for that reason, which the old reader refuses.
	s := stamps[0]
	j, err := r.Write(&s, schemaread.FormJSON)
	require.NoError(t, err)
	e, at, _, ok := oldStampReader(string(j))
	assert.Equal(t, []any{true, int64(7), int64(0)}, []any{ok, e, at}, "the hazard the base64 switch avoids: %s", j)

	// the receipt: an old reader finds no SEND OK in a table receipt and reads
	// it as no receipt, never as a receipt for another message
	ra := schemaread.NewReader(ackCodec())
	a := newAck(ackID, hookwire.StagePushed, hookwire.PushStateUp, "")
	for _, form := range []schemaread.Form{schemaread.FormBase64, schemaread.FormJSON} {
		out, err := ra.Write(&a, form)
		require.NoError(t, err)
		_, ok := oldAckReader(string(out))
		assert.False(t, ok, "the old receipt reader does not misread the %s form", form)
	}
	id, ok := oldAckReader("SEND OK id=" + ackID + " to=bob")
	assert.Equal(t, []any{ackID, true}, []any{id, ok}, "and still reads today's line")
}
