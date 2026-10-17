package hookwire

// TestLayer1AckBeatHook is the golden test of the hook area's definitions
// (schemas/hook): the generated Ack, Beat and Stamp each round-trip as the
// binary wire, as padded standard base64 of that wire and as the JSON text
// form, each read strictly through pkg/schemaread; every named refusal of
// UNION.md section 4.6 that applies to them fires on its bound; each renders
// to the name-keyed text form; and each one's legacy form (the receipt line
// nova-bus receipts prints, the friend beat's argv as the sprint wire carries
// it, the hook stamp's JSON as the hook record holds it) and its table form
// are read alike by the new reader while the old reader keeps working through
// the writer switch.
//
// The fixtures are never written by hand: the corpus wires in schemas/hook/fuzz
// and the newer writers' wires in testdata/writers are what the pinned
// compiler's `schema pack`, an engine independent of the generated Go, writes
// from their JSON. With <pin> the module@version of generate.go's line, and
// the JSON copied to <tree>/<Root>.json (a variable root is that one file):
//
//	go run <pin> pack --root <Root> --out schemas/hook/fuzz/<name>.bin <tree> schemas/hook
//	go run <pin> pack --root <Root> --out pkg/hookwire/testdata/writers/<case>-<root>.bin <tree> pkg/hookwire/testdata/writers/<case>
//
// Each newer writer's schemas are the unit's own with one edit per table:
// newer adds a field (Ack at_ms, Beat lanes, Stamp ttl_ms), changed changes a
// field's kind (Ack reason, Beat stop_returns, Stamp by), wide widens a text
// (Ack id, Beat friend_name, Stamp by), variant appends a Stage variant.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The commands a refusal points at.
const (
	ackHelp   = "nova-bus receipts -h"
	beatHelp  = "nova-friend run -h"
	stampHelp = "nova-hook status -h"
)

var b64 = base64.StdEncoding.EncodeToString

// fieldOf is a table's declared field, read from the generated descriptors so
// the test and the definition cannot drift apart.
func fieldOf(t *testing.T, fields []TableFieldInfo, name string) TableFieldInfo {
	t.Helper()
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field %s", name)
	return TableFieldInfo{}
}

// textOf is a counted string field's text; a length past the buffer is read
// whole so the bound check sees it.
func textOf(buf []byte, n int32) string {
	if n < 0 {
		return ""
	}
	if int(n) > len(buf) {
		return string(buf) + strings.Repeat("?", int(n)-len(buf))
	}
	return string(buf[:n])
}

// setText copies s into a counted string field, keeping the trailing NUL.
func setText(buf []byte, s string) int32 { return int32(copy(buf[:len(buf)-1], s)) }

// report is the generated TableReport reduced to what the strict read decides on.
func report(rep *TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// whole runs a generated Measure and Save (or ToJsonMeasure and ToJson) into
// one exact buffer; nil when either refuses.
func whole(measure func() int64, save func([]byte) int64) []byte {
	n := measure()
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if save(buf) != n {
		return nil
	}
	return buf
}

// refusal is the reason a read or write was refused by, checking the house
// style `<NAME> REFUSED: <reason>; run: <help>`.
func refusal(t *testing.T, err error, name, help string) string {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), name+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+help), err.Error())
	if r.Field != "" {
		return r.Reason + " (" + r.Field + ")"
	}
	return r.Reason
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func corpus(t *testing.T, name string) (wire, text []byte) {
	t.Helper()
	dir := filepath.Join("..", "..", "schemas", "hook", "fuzz")
	return readFile(t, filepath.Join(dir, name+".bin")), readFile(t, filepath.Join(dir, name+".json"))
}

func writer(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join("testdata", "writers", name+".bin"))
}

var allForms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

// ---- the Ack ----------------------------------------------------------------

// ackContext is what the reader of a legacy receipt line knows beside it: the
// line carries the id and the stage, and the recipient's push is the reader's
// own hook read (the push and, when it is not up, the reason).
type ackContext struct {
	push   PushState
	reason string
}

// legacyState is the receipt state nova-bus receipts prints for each stage.
var legacyState = map[Stage]string{Stagequeued: "none", Stagedelivered: bus.Delivered, Stageread: bus.Read, Stageacted: bus.Acted}

// receiptLine is the line nova-bus receipts prints for one receipt.
func receiptLine(id, state, age string) string {
	return fmt.Sprintf("RECEIPTS RECEIPT id=%s state=%s age=%s", id, state, age)
}

// loadLegacyAck reads the receipt line strictly: it must be exactly the line
// receipts prints for the values it carries, or it is refused as
// legacy_shape. The age is relative to the read and carries no stage.
func loadLegacyAck(v *Ack, text string, ctx ackContext) schemaread.Report {
	f := strings.Fields(text)
	if len(f) != 5 || !strings.HasPrefix(f[2], "id=") || !strings.HasPrefix(f[3], "state=") || !strings.HasPrefix(f[4], "age=") {
		return schemaread.Report{Reason: "legacy_shape"}
	}
	id, state, age := f[2][3:], f[3][6:], f[4][4:]
	stage := StageNone
	for s, name := range legacyState {
		if name == state {
			stage = s
		}
	}
	_, ageErr := time.ParseDuration(age)
	if stage == StageNone || receiptLine(id, state, age) != text || (age != "-" && ageErr != nil) || (state == "none") != (age == "-") {
		return schemaread.Report{Reason: "legacy_shape"}
	}
	*v = ack(id, stage, ctx.push, ctx.reason)
	return schemaread.Report{}
}

func ack(id string, stage Stage, push PushState, reason string) Ack {
	var v Ack
	AckReset(&v)
	v.IdLength = setText(v.Id[:], id)
	v.Stage, v.Push = stage, push
	v.ReasonLength = setText(v.Reason[:], reason)
	return v
}

func ackWire(v *Ack) []byte {
	return whole(func() int64 { return AckMeasure(v) }, func(b []byte) int64 { return AckSave(v, b) })
}

func ackJSON(v *Ack) []byte {
	return whole(func() int64 { return AckToJsonMeasure(v) }, func(b []byte) int64 { return AckToJson(v, b) })
}

func ackVia(v *Ack, load func(*AckBuilder, *TableReport) bool) schemaread.Report {
	var b AckBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep TableReport
	ok := load(&b, &rep)
	if root := b.GetRoot(); root != nil {
		*v = *root
	}
	return report(&rep, ok)
}

// ackCodec is the strict Ack read and write. The facade rules are UNION.md
// section 4.6: refuse every clamp, refuse absence where zero is no answer (an
// empty id, a stage or push of None), refuse an out-of-set enum, and validate
// before writing because the generated writer checks no bound. A push that is
// not up says why.
func ackCodec(t *testing.T, ctx ackContext) *schemaread.Codec[Ack] {
	idMax := int(fieldOf(t, AckTableFields, "id").ArrayBound)
	reasonMax := int(fieldOf(t, AckTableFields, "reason").ArrayBound)
	return &schemaread.Codec[Ack]{
		Name:  "ACK",
		Help:  ackHelp,
		Reset: AckReset,
		Measure: func(wire []byte) error {
			_, err := AckLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *Ack, wire []byte) schemaread.Report {
			return ackVia(v, func(b *AckBuilder, rep *TableReport) bool { return AckLoadBuilder(b, wire, rep) })
		},
		LoadJSON: func(v *Ack, text []byte) schemaread.Report {
			return ackVia(v, func(b *AckBuilder, rep *TableReport) bool { return AckFromJson(b, text, rep) })
		},
		Legacy:     func(in []byte) bool { return bytes.HasPrefix(in, []byte("RECEIPTS RECEIPT ")) },
		LoadLegacy: func(v *Ack, in []byte) schemaread.Report { return loadLegacyAck(v, string(in), ctx) },
		Constraints: func(v *Ack, c *schemaread.Checker) {
			c.Required("id", v.IdLength > 0)
			c.Text("id", textOf(v.Id[:], v.IdLength), idMax)
			c.Required("stage", v.Stage != StageNone)
			c.Enum("stage", int(v.Stage), int(StageCount)+1)
			c.Required("push", v.Push != PushStateNone)
			c.Enum("push", int(v.Push), int(PushStateCount)+1)
			c.Text("reason", textOf(v.Reason[:], v.ReasonLength), reasonMax)
			c.Required("reason", v.Push == PushStateup || v.ReasonLength > 0)
		},
		SaveWire: ackWire,
		SaveJSON: ackJSON,
	}
}

// ---- the Beat ---------------------------------------------------------------

// beatContext is what the reader of a legacy beat knows beside it: the argv
// carries no send time, the request's arrival does.
type beatContext struct{ sentMs int64 }

// beatArgv is the friend beat's argv as the friend daemon writes it today
// (cmd/nova-friend: friend beat <name> [--until <t> --reason <r>] [--active
// <t>] [--stop-returns <n>]), RFC 3339 seconds, as the sprint wire carries it:
// a JSON array of strings.
func beatArgv(name string, active, until time.Time, reason string, stopReturns int) string {
	args := []string{"friend", "beat", name}
	if !until.IsZero() {
		args = append(args, "--until", until.UTC().Format(time.RFC3339), "--reason", reason)
	}
	if !active.IsZero() {
		args = append(args, "--active", active.UTC().Format(time.RFC3339))
	}
	if stopReturns > 0 {
		args = append(args, "--stop-returns", strconv.Itoa(stopReturns))
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(raw)
}

// loadLegacyBeat reads the argv strictly: it must be exactly what the daemon
// writes for the values it carries, or it is refused as legacy_shape.
func loadLegacyBeat(v *Beat, text []byte, ctx beatContext) schemaread.Report {
	var args []string
	if json.Unmarshal(text, &args) != nil || len(args) < 3 || args[0] != "friend" || args[1] != "beat" {
		return schemaread.Report{Reason: "legacy_shape"}
	}
	var active, until time.Time
	var reason string
	stop := 0
	for i := 3; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return schemaread.Report{Reason: "legacy_shape"}
		}
		var err error
		switch args[i] {
		case "--until":
			until, err = time.Parse(time.RFC3339, args[i+1])
		case "--reason":
			reason = args[i+1]
		case "--active":
			active, err = time.Parse(time.RFC3339, args[i+1])
		case "--stop-returns":
			stop, err = strconv.Atoi(args[i+1])
		default:
			return schemaread.Report{Reason: "legacy_shape"}
		}
		if err != nil {
			return schemaread.Report{Reason: "legacy_shape"}
		}
	}
	if stop < 0 || stop > math.MaxUint32 || beatArgv(args[2], active, until, reason, stop) != string(text) {
		return schemaread.Report{Reason: "legacy_shape"}
	}
	*v = beat(args[2], ctx.sentMs, ms(active), ms(until), reason, uint32(stop))
	return schemaread.Report{}
}

// ms is t in Unix milliseconds, 0 for the zero time (absent).
func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// beat fills a beat; activeMs and untilMs of 0 are absent.
func beat(friendName string, sentMs, activeMs, untilMs int64, reason string, stopReturns uint32) Beat {
	var v Beat
	BeatReset(&v)
	v.FriendNameLength = setText(v.FriendName[:], friendName)
	v.SentMs, v.SentMsPresent = sentMs, true
	if activeMs != 0 {
		v.ActiveMs, v.ActiveMsPresent = activeMs, true
	}
	if untilMs != 0 {
		v.UntilMs, v.UntilMsPresent = untilMs, true
	}
	v.ReasonLength = setText(v.Reason[:], reason)
	v.StopReturns, v.StopReturnsPresent = stopReturns, true
	return v
}

func beatWire(v *Beat) []byte {
	return whole(func() int64 { return BeatMeasure(v) }, func(b []byte) int64 { return BeatSave(v, b) })
}

func beatJSON(v *Beat) []byte {
	return whole(func() int64 { return BeatToJsonMeasure(v) }, func(b []byte) int64 { return BeatToJson(v, b) })
}

func beatVia(v *Beat, load func(*BeatBuilder, *TableReport) bool) schemaread.Report {
	var b BeatBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep TableReport
	ok := load(&b, &rep)
	if root := b.GetRoot(); root != nil {
		*v = *root
	}
	return report(&rep, ok)
}

// beatCodec is the strict Beat read and write: refuse every clamp, refuse
// absence where zero is no answer (friend_name, sent_ms, stop_returns), refuse
// a time of zero or less that says present, and refuse half a down beat (an
// until with no reason, or a reason with no until).
func beatCodec(t *testing.T, ctx beatContext) *schemaread.Codec[Beat] {
	friendMax := int(fieldOf(t, BeatTableFields, "friend_name").ArrayBound)
	reasonMax := int(fieldOf(t, BeatTableFields, "reason").ArrayBound)
	stop := fieldOf(t, BeatTableFields, "stop_returns")
	return &schemaread.Codec[Beat]{
		Name:  "BEAT",
		Help:  beatHelp,
		Reset: BeatReset,
		Measure: func(wire []byte) error {
			_, err := BeatLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *Beat, wire []byte) schemaread.Report {
			return beatVia(v, func(b *BeatBuilder, rep *TableReport) bool { return BeatLoadBuilder(b, wire, rep) })
		},
		LoadJSON: func(v *Beat, text []byte) schemaread.Report {
			return beatVia(v, func(b *BeatBuilder, rep *TableReport) bool { return BeatFromJson(b, text, rep) })
		},
		Legacy:     func(in []byte) bool { return bytes.HasPrefix(in, []byte(`["friend","beat",`)) },
		LoadLegacy: func(v *Beat, in []byte) schemaread.Report { return loadLegacyBeat(v, in, ctx) },
		Constraints: func(v *Beat, c *schemaread.Checker) {
			c.Required("friend_name", v.FriendNameLength > 0)
			c.Text("friend_name", textOf(v.FriendName[:], v.FriendNameLength), friendMax)
			c.Required("sent_ms", v.SentMsPresent && v.SentMs > 0)
			if v.ActiveMsPresent {
				c.Bound("active_ms", v.ActiveMs, 1, math.MaxInt64)
			}
			if v.UntilMsPresent {
				c.Bound("until_ms", v.UntilMs, 1, math.MaxInt64)
			}
			c.Text("reason", textOf(v.Reason[:], v.ReasonLength), reasonMax)
			c.Required("reason", !v.UntilMsPresent || v.ReasonLength > 0)
			c.Required("until_ms", v.ReasonLength == 0 || v.UntilMsPresent)
			c.Required("stop_returns", v.StopReturnsPresent)
			c.Bound("stop_returns", int64(v.StopReturns), int64(stop.RangeMin), int64(stop.RangeMax))
		},
		SaveWire: beatWire,
		SaveJSON: beatJSON,
	}
}

// ---- the Stamp --------------------------------------------------------------

// legacyStamp is the stamp as the hook record's scripts write it today
// (hook/redis.go, stampLua and answerLua): compact JSON, the time already in
// milliseconds, the holder a JSON string.
func legacyStamp(epoch, atMs int64, by string) string {
	raw, err := json.Marshal(by)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(`{"epoch":%d,"at":%d,"by":%s}`, epoch, atMs, raw)
}

// loadLegacyStamp reads the hook record's stamp strictly: exactly what the
// scripts write for the values it carries, or legacy_shape.
func loadLegacyStamp(v *Stamp, text []byte) schemaread.Report {
	var s struct {
		Epoch int64  `json:"epoch"`
		At    int64  `json:"at"`
		By    string `json:"by"`
	}
	d := json.NewDecoder(bytes.NewReader(text))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || legacyStamp(s.Epoch, s.At, s.By) != string(text) {
		return schemaread.Report{Reason: "legacy_shape"}
	}
	*v = stamp(s.Epoch, s.At, s.By)
	return schemaread.Report{}
}

func stamp(epoch, atMs int64, by string) Stamp {
	var v Stamp
	StampReset(&v)
	v.Epoch, v.EpochPresent = epoch, true
	v.AtMs, v.AtMsPresent = atMs, true
	v.ByLength = setText(v.By[:], by)
	return v
}

func stampWire(v *Stamp) []byte {
	return whole(func() int64 { return StampMeasure(v) }, func(b []byte) int64 { return StampSave(v, b) })
}

func stampJSON(v *Stamp) []byte {
	return whole(func() int64 { return StampToJsonMeasure(v) }, func(b []byte) int64 { return StampToJson(v, b) })
}

func stampVia(v *Stamp, load func(*StampBuilder, *TableReport) bool) schemaread.Report {
	var b StampBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep TableReport
	ok := load(&b, &rep)
	if root := b.GetRoot(); root != nil {
		*v = *root
	}
	return report(&rep, ok)
}

// stampCodec is the strict Stamp read and write: refuse every clamp and
// refuse an absent or non-positive epoch or time and an empty holder.
func stampCodec(t *testing.T) *schemaread.Codec[Stamp] {
	byMax := int(fieldOf(t, StampTableFields, "by").ArrayBound)
	return &schemaread.Codec[Stamp]{
		Name:  "STAMP",
		Help:  stampHelp,
		Reset: StampReset,
		Measure: func(wire []byte) error {
			_, err := StampLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *Stamp, wire []byte) schemaread.Report {
			return stampVia(v, func(b *StampBuilder, rep *TableReport) bool { return StampLoadBuilder(b, wire, rep) })
		},
		LoadJSON: func(v *Stamp, text []byte) schemaread.Report {
			return stampVia(v, func(b *StampBuilder, rep *TableReport) bool { return StampFromJson(b, text, rep) })
		},
		Legacy:     func(in []byte) bool { return bytes.HasPrefix(in, []byte(`{"epoch":`)) },
		LoadLegacy: func(v *Stamp, in []byte) schemaread.Report { return loadLegacyStamp(v, in) },
		Constraints: func(v *Stamp, c *schemaread.Checker) {
			c.Required("epoch", v.EpochPresent && v.Epoch > 0)
			c.Required("at_ms", v.AtMsPresent && v.AtMs > 0)
			c.Required("by", v.ByLength > 0)
			c.Text("by", textOf(v.By[:], v.ByLength), byMax)
		},
		SaveWire: stampWire,
		SaveJSON: stampJSON,
	}
}

// ---- the shared checks ------------------------------------------------------

// golden reads one corpus instance in every form and writes it back in every
// form: the generated Save writes the packed wire, ToJson writes the corpus
// text keyed by field name, and each form reads the instance.
func golden[T any](t *testing.T, r *schemaread.Reader[T], name string, want T, save, toJSON func(*T) []byte, keys []string) {
	t.Helper()
	wire, text := corpus(t, name)
	assert.Equal(t, wire, save(&want), "%s: the generated Save writes the packed wire", name)
	assert.Equal(t, string(text), string(toJSON(&want)), "%s: the generated ToJson writes the corpus text", name)
	for _, key := range keys {
		assert.Contains(t, string(text), `"`+key+`": `, "%s: the text form is keyed by field name", name)
	}
	for _, in := range []struct {
		form schemaread.Form
		data []byte
	}{{schemaread.FormBinary, wire}, {schemaread.FormBase64, []byte(b64(wire))}, {schemaread.FormJSON, text}} {
		got, res, err := r.Read(in.data)
		require.NoError(t, err, "%s as %s", name, in.form)
		assert.Equal(t, in.form, res.Form, name)
		assert.Equal(t, want, got, "%s as %s reads the instance", name, in.form)
	}
	for _, form := range allForms {
		out, err := r.Write(&want, form)
		require.NoError(t, err, "%s written as %s", name, form)
		got, res, err := r.Read(out)
		require.NoError(t, err, "%s written then read as %s", name, form)
		assert.Equal(t, form, res.Form, name)
		assert.Equal(t, want, got, "%s round-trips as %s", name, form)
		if form == schemaread.FormBase64 {
			assert.Equal(t, b64(wire), string(out), "%s: the base64 text is the padded standard base64 of the whole wire", name)
		}
	}
}

type refusalCase struct {
	name   string
	in     []byte
	reason string
}

// framing is the refusals every table shares: damage to the wire, the form
// byte and the text carriers.
func framing(good, text []byte) []refusalCase {
	newerForm := append([]byte{}, good...)
	newerForm[0] = 9
	return []refusalCase{
		{"truncated wire", good[:len(good)-1], "wire_damaged"},
		{"trailing junk in the wire", append(append([]byte{}, good...), 0, 1), "wire_damaged"},
		{"the form byte alone", good[:1], "wire_damaged"},
		{"an unknown newer form", newerForm, "unknown_form"},
		{"base64 that does not decode", []byte(b64(good)[:len(b64(good))-3]), "malformed"},
		{"base64 with trailing junk", []byte(b64(good) + " junk"), "malformed"},
		{"damaged JSON text", append(append([]byte{}, text[:len(text)-3]...), '"', '}'), "malformed"},
		{"empty", nil, "unknown_form"},
	}
}

// refuses runs each case, checks the house style and that a refused read
// returns nothing, and that every truncation of the good wire is refused.
func refuses[T any](t *testing.T, r *schemaread.Reader[T], name, help string, good []byte, cases []refusalCase) {
	t.Helper()
	var zero T
	for _, tc := range cases {
		got, _, err := r.Read(tc.in)
		if !assert.Error(t, err, tc.name) {
			continue
		}
		assert.Equal(t, tc.reason, refusal(t, err, name, help), tc.name)
		assert.Equal(t, zero, got, "%s: a refused read returns nothing, never a partial value", tc.name)
	}
	for i := range len(good) {
		_, _, err := r.Read(good[:i])
		assert.Error(t, err, "cut at byte %d", i)
	}
}

// writerRefuses checks the writer refuses v in every form, naming the field,
// and writes nothing.
func writerRefuses[T any](t *testing.T, r *schemaread.Reader[T], name, help, label string, v T, reason string) {
	t.Helper()
	for _, form := range allForms {
		out, err := r.Write(&v, form)
		assert.Nil(t, out, "%s as %s writes nothing", label, form)
		assert.Equal(t, reason, refusal(t, err, name, help), "%s as %s", label, form)
	}
}

// mutants runs a deterministic mutation pass over the inputs: every mutant is
// refused by name or reads a value that passes every constraint.
func mutants[T any](t *testing.T, r *schemaread.Reader[T], inputs [][]byte) int {
	t.Helper()
	n := 0
	for _, in := range inputs {
		for i := range in {
			for _, b := range []byte{0x00, in[i] ^ 0x01, in[i] ^ 0x80} {
				m := append([]byte{}, in...)
				m[i] = b
				n++
				got, _, err := r.Read(m)
				if err != nil {
					var ref *schemaread.Refusal
					require.True(t, errors.As(err, &ref), "mutant %d: %v", n, err)
					continue
				}
				require.NoError(t, r.Check(&got), "a mutant that reads passes every constraint")
			}
		}
	}
	return n
}

func mustWrite[T any](t *testing.T, r *schemaread.Reader[T], v *T, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(v, form)
	require.NoError(t, err)
	return out
}

func edited(t *testing.T, text []byte, old, new string) []byte {
	t.Helper()
	out := strings.Replace(string(text), old, new, 1)
	require.NotEqual(t, string(text), out, "the edit %q applies", old)
	return []byte(out)
}

func TestLayer1AckBeatHook(t *testing.T) {
	t.Parallel()
	const id = "01JZ8Q4W6V2N7K3M5X9R0T1Y2B"
	actx := ackContext{push: PushStateup}
	bctx := beatContext{sentMs: 1792000000000}
	b64id := strings.Repeat("0", 38) + id

	t.Run("golden round trip in every form", func(t *testing.T) {
		t.Parallel()
		ra := schemaread.NewReader(ackCodec(t, actx))
		ackKeys := []string{"id", "stage", "push", "reason"}
		golden(t, ra, "ack-acted", ack(id, Stageacted, PushStateup, ""), ackWire, ackJSON, ackKeys)
		golden(t, ra, "ack-bounds", ack(b64id, Stagequeued, PushStatestale, strings.Repeat("z", 152)), ackWire, ackJSON, ackKeys)

		rb := schemaread.NewReader(beatCodec(t, bctx))
		golden(t, rb, "beat-up", beat("ada", 1792000000000, 1791999990000, 0, "", 0), beatWire, beatJSON,
			[]string{"friend_name", "sent_ms", "active_ms", "reason", "stop_returns"})
		golden(t, rb, "beat-down", beat("bob", 1792000000000, 1791999990000, 1792003600000, "out of credits", 2), beatWire, beatJSON,
			[]string{"friend_name", "sent_ms", "active_ms", "until_ms", "reason", "stop_returns"})
		golden(t, rb, "beat-bounds", beat(strings.Repeat("f", 64), math.MaxInt64, 1, math.MaxInt64, strings.Repeat("z", 200), 1024), beatWire, beatJSON,
			[]string{"friend_name", "sent_ms", "active_ms", "until_ms", "reason", "stop_returns"})

		rs := schemaread.NewReader(stampCodec(t))
		golden(t, rs, "stamp-typical", stamp(7, 1792000000000, "nova-friend 4242"), stampWire, stampJSON, []string{"epoch", "at_ms", "by"})
		golden(t, rs, "stamp-bounds", stamp(math.MaxInt64, math.MaxInt64, strings.Repeat("b", 128)), stampWire, stampJSON, []string{"epoch", "at_ms", "by"})

		for _, c := range []struct {
			name      string
			counts    *schemaread.Counts
			instances uint64
		}{{"ack", ra.Counts(), 2}, {"beat", rb.Counts(), 3}, {"stamp", rs.Counts(), 2}} {
			for _, form := range allForms {
				assert.Equal(t, 2*c.instances, c.counts.Reads(form), "%s: each read is counted once, on its form (%s)", c.name, form)
			}
			assert.Equal(t, uint64(0), c.counts.Reads(schemaread.FormLegacy), c.name)
		}
	})

	t.Run("named refusals fire on their bounds", func(t *testing.T) {
		t.Parallel()

		ra := schemaread.NewReader(ackCodec(t, actx))
		goodAck := ack(id, Stageread, PushStatedown, "no live lease")
		goodAckWire, goodAckText := ackWire(&goodAck), ackJSON(&goodAck)
		withAck := func(edit func(*Ack)) []byte {
			v := goodAck
			edit(&v)
			return ackWire(&v)
		}
		refuses(t, ra, "ACK", ackHelp, goodAckWire, append([]refusalCase{
			// The generated reader clamps and counts without naming the
			// field (TableReport carries a count only), so a read refusal
			// names the reason alone; the writer names the field too.
			{"an id over 64 bytes from a newer writer", writer(t, "wide-ack"), "clamped"},
			// a variant this build does not know reads as None, and None is
			// no stage
			{"a stage variant a newer writer added", writer(t, "variant-ack"), "absent_required (stage)"},
			{"a field that changed kind", writer(t, "changed-ack"), "kind_mismatch"},
			{"stage absent", withAck(func(v *Ack) { v.Stage = StageNone }), "absent_required (stage)"},
			{"push absent", withAck(func(v *Ack) { v.Push = PushStateNone }), "absent_required (push)"},
			{"id absent", withAck(func(v *Ack) { v.IdLength = 0 }), "absent_required (id)"},
			{"a push down with no reason", withAck(func(v *Ack) { v.ReasonLength = 0 }), "absent_required (reason)"},
			{"stage absent from the JSON text", edited(t, goodAckText, "  \"stage\": \"read\",\n", ""), "absent_required (stage)"},
			{"a stage the JSON text does not name", edited(t, goodAckText, `"stage": "read"`, `"stage": "lost"`), "absent_required (stage)"},
			{"a NUL in the id", withAck(func(v *Ack) { v.Id[2] = 0 }), "malformed"},
			{"no ack at all", []byte("hello ack"), "malformed"},
			{"a receipt line without its state", []byte("RECEIPTS RECEIPT id=" + id + " age=1m"), "legacy_shape"},
			{"a receipt line with an unknown state", []byte(receiptLine(id, "lost", "1m")), "legacy_shape"},
			{"a receipt line with a bad age", []byte(receiptLine(id, bus.Read, "soon")), "legacy_shape"},
			{"a receipt line with trailing junk", []byte(receiptLine(id, bus.Read, "1m") + " junk"), "legacy_shape"},
		}, framing(goodAckWire, goodAckText)...))
		// the generated Save writes no enum value outside the declared set
		for _, v := range []Ack{ack(id, StageMax+1, PushStateup, ""), ack(id, Stageread, PushStateMax+1, "")} {
			assert.Nil(t, ackWire(&v), "an out-of-set enum is never written")
		}
		at := ack(b64id, Stageacted, PushStatestale, strings.Repeat("z", 152))
		_, _, err := ra.Read(ackWire(&at))
		require.NoError(t, err, "every bound itself is an ack")
		got, res, err := ra.Read(writer(t, "newer-ack"))
		require.NoError(t, err, "a newer writer's ack reads")
		assert.Equal(t, 1, res.Unknown, "its new field is skipped and counted")
		assert.Equal(t, ack(id, Stageread, PushStateup, ""), got)

		rb := schemaread.NewReader(beatCodec(t, bctx))
		goodBeat := beat("ada", 1792000000000, 1791999990000, 0, "", 3)
		goodBeatWire, goodBeatText := beatWire(&goodBeat), beatJSON(&goodBeat)
		withBeat := func(edit func(*Beat)) []byte {
			v := goodBeat
			edit(&v)
			return beatWire(&v)
		}
		refuses(t, rb, "BEAT", beatHelp, goodBeatWire, append([]refusalCase{
			{"stop_returns over 1024", withBeat(func(v *Beat) { v.StopReturns = 1025 }), "clamped"},
			{"stop_returns over 1024 as JSON text", edited(t, goodBeatText, `"stop_returns": 3`, `"stop_returns": 1025`), "clamped"},
			{"stop_returns over 1024 as base64 text", []byte(b64(withBeat(func(v *Beat) { v.StopReturns = 1025 }))), "clamped"},
			{"a friend name over 64 bytes from a newer writer", writer(t, "wide-beat"), "clamped"},
			{"a field that changed kind", writer(t, "changed-beat"), "kind_mismatch"},
			{"friend absent", withBeat(func(v *Beat) { v.FriendNameLength = 0 }), "absent_required (friend_name)"},
			{"sent_ms absent", withBeat(func(v *Beat) { v.SentMs, v.SentMsPresent = 0, false }), "absent_required (sent_ms)"},
			{"stop_returns absent", withBeat(func(v *Beat) { v.StopReturns, v.StopReturnsPresent = 0, false }), "absent_required (stop_returns)"},
			{"stop_returns absent from the JSON text", edited(t, goodBeatText, ",\n  \"stop_returns\": 3", ""), "absent_required (stop_returns)"},
			{"an until with no reason", withBeat(func(v *Beat) { v.UntilMs, v.UntilMsPresent = 1792003600000, true }), "absent_required (reason)"},
			{"a reason with no until", withBeat(func(v *Beat) { v.ReasonLength = setText(v.Reason[:], "away") }), "absent_required (until_ms)"},
			{"an active time before the epoch", withBeat(func(v *Beat) { v.ActiveMs = -1 }), "clamped (active_ms)"},
			{"a NUL in the friend name", withBeat(func(v *Beat) { v.FriendName[2] = 0 }), "malformed"},
			{"no beat at all", []byte("hello beat"), "malformed"},
			{"an argv with a flag it never writes", []byte(`["friend","beat","ada","--lanes","4"]`), "legacy_shape"},
			{"an argv with a time that is no RFC 3339", []byte(`["friend","beat","ada","--active","yesterday"]`), "legacy_shape"},
			{"an argv with a flag and no value", []byte(`["friend","beat","ada","--active"]`), "legacy_shape"},
			{"an argv not as the daemon writes it", []byte(`["friend","beat","ada","--stop-returns","0"]`), "legacy_shape"},
			{"an argv with no name", []byte(`["friend","beat",]`), "legacy_shape"},
		}, framing(goodBeatWire, goodBeatText)...))
		atBeat := beat("ada", 1, 1, 1, "x", 1024)
		_, _, err = rb.Read(beatWire(&atBeat))
		require.NoError(t, err, "every bound itself is a beat")
		gotBeat, res, err := rb.Read(writer(t, "newer-beat"))
		require.NoError(t, err, "a newer writer's beat reads")
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, beat("ada", 1792000000000, 1791999990000, 0, "", 0), gotBeat)

		rs := schemaread.NewReader(stampCodec(t))
		goodStamp := stamp(7, 1792000000000, "nova-friend 4242")
		goodStampWire, goodStampText := stampWire(&goodStamp), stampJSON(&goodStamp)
		withStamp := func(edit func(*Stamp)) []byte {
			v := goodStamp
			edit(&v)
			return stampWire(&v)
		}
		refuses(t, rs, "STAMP", stampHelp, goodStampWire, append([]refusalCase{
			{"a holder over 128 bytes from a newer writer", writer(t, "wide-stamp"), "clamped"},
			{"a field that changed kind", writer(t, "changed-stamp"), "kind_mismatch"},
			{"epoch absent", withStamp(func(v *Stamp) { v.Epoch, v.EpochPresent = 0, false }), "absent_required (epoch)"},
			{"epoch zero", withStamp(func(v *Stamp) { v.Epoch = 0 }), "absent_required (epoch)"},
			{"at_ms absent", withStamp(func(v *Stamp) { v.AtMs, v.AtMsPresent = 0, false }), "absent_required (at_ms)"},
			{"at_ms before the epoch", withStamp(func(v *Stamp) { v.AtMs = -5 }), "absent_required (at_ms)"},
			{"holder absent", withStamp(func(v *Stamp) { v.ByLength = 0 }), "absent_required (by)"},
			{"at_ms absent from the JSON text", edited(t, goodStampText, "  \"at_ms\": 1792000000000,\n", ""), "absent_required (at_ms)"},
			{"a NUL in the holder", withStamp(func(v *Stamp) { v.By[2] = 0 }), "malformed"},
			{"no stamp at all", []byte("hello stamp"), "malformed"},
			// the record's stamp before this wire: an RFC 3339 time, an
			// unknown key or spacing the scripts never write is refused by
			// name, never read as no stamp
			{"a legacy stamp with an RFC 3339 time", []byte(`{"epoch":7,"at":"2026-10-11T12:00:00Z","by":"x"}`), "legacy_shape"},
			{"a legacy stamp with a key it never writes", []byte(`{"epoch":7,"at":1792000000000,"by":"x","ttl":1}`), "legacy_shape"},
			{"a legacy stamp spaced as the scripts never write it", []byte(`{"epoch":7, "at":1792000000000,"by":"x"}`), "legacy_shape"},
		}, framing(goodStampWire, goodStampText)...))
		gotStamp, res, err := rs.Read(writer(t, "newer-stamp"))
		require.NoError(t, err, "a newer writer's stamp reads")
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, goodStamp, gotStamp)

		assert.Equal(t, uint64(1), ra.Counts().Unknown())
		assert.Equal(t, uint64(1), rb.Counts().Unknown())
		assert.Equal(t, uint64(1), rs.Counts().Unknown())
	})

	t.Run("the writer refuses before it writes", func(t *testing.T) {
		t.Parallel()
		ra := schemaread.NewReader(ackCodec(t, actx))
		for _, tc := range []struct {
			name   string
			v      Ack
			reason string
		}{
			{"no id", ack("", Stageread, PushStateup, ""), "absent_required (id)"},
			{"an id over 64 bytes", func() Ack { v := ack(id, Stageread, PushStateup, ""); v.IdLength = 65; return v }(), "clamped (id)"},
			{"no stage", ack(id, StageNone, PushStateup, ""), "absent_required (stage)"},
			{"a stage past the variants", ack(id, StageMax+1, PushStateup, ""), "clamped (stage)"},
			{"no push", ack(id, Stageread, PushStateNone, ""), "absent_required (push)"},
			{"a push past the variants", ack(id, Stageread, PushStateMax+1, ""), "clamped (push)"},
			{"a push down with no reason", ack(id, Stageread, PushStatedown, ""), "absent_required (reason)"},
			{"a reason that is not UTF-8", ack(id, Stageread, PushStatedown, "a\xffb"), "malformed (reason)"},
		} {
			writerRefuses(t, ra, "ACK", ackHelp, tc.name, tc.v, tc.reason)
		}
		rb := schemaread.NewReader(beatCodec(t, bctx))
		for _, tc := range []struct {
			name   string
			v      Beat
			reason string
		}{
			{"no friend", beat("", 1, 0, 0, "", 0), "absent_required (friend_name)"},
			{"no send time", beat("ada", 0, 0, 0, "", 0), "absent_required (sent_ms)"},
			{"stop_returns over 1024", beat("ada", 1, 0, 0, "", 1025), "clamped (stop_returns)"},
			{"an until with no reason", beat("ada", 1, 0, 5, "", 0), "absent_required (reason)"},
			{"a reason with no until", beat("ada", 1, 0, 0, "away", 0), "absent_required (until_ms)"},
			{"an active time before the epoch", beat("ada", 1, -1, 0, "", 0), "clamped (active_ms)"},
			{"a friend name that is not UTF-8", beat("au\xffra", 1, 0, 0, "", 0), "malformed (friend_name)"},
		} {
			writerRefuses(t, rb, "BEAT", beatHelp, tc.name, tc.v, tc.reason)
		}
		rs := schemaread.NewReader(stampCodec(t))
		for _, tc := range []struct {
			name   string
			v      Stamp
			reason string
		}{
			{"no epoch", stamp(0, 1, "x"), "absent_required (epoch)"},
			{"no time", stamp(7, 0, "x"), "absent_required (at_ms)"},
			{"no holder", stamp(7, 1, ""), "absent_required (by)"},
			{"a holder that is not UTF-8", stamp(7, 1, "x\xff"), "malformed (by)"},
		} {
			writerRefuses(t, rs, "STAMP", stampHelp, tc.name, tc.v, tc.reason)
		}
	})

	// The cutover (owner's rule S4): readers first, then writers. Each legacy
	// form and its table form are read identically by the new reader, the
	// per-form counter shows which form arrived, and the old reader keeps
	// reading what the old writer writes through the switch.
	t.Run("dual read through the writer switch", func(t *testing.T) {
		t.Parallel()

		// The ack. The old writer is the bus store's receipt (Forward), the
		// old reader its Stages read, and the legacy line what nova-bus
		// receipts prints from it.
		start := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		store := bustest.NewFake(start, "ada", "bob")
		b := &bus.Bus{Store: store}
		ctx := context.Background()
		ra := schemaread.NewReader(ackCodec(t, actx))
		ids := []string{id, "01JZ8Q4W6V2N7K3M5X9R0T1Y2C", "01JZ8Q4W6V2N7K3M5X9R0T1Y2D", "01JZ8Q4W6V2N7K3M5X9R0T1Y2E"}
		states := []string{"", bus.Delivered, bus.Read, bus.Acted}
		for i, mid := range ids {
			for _, s := range bus.StageStates[:max(0, i)] {
				_, err := store.Forward(ctx, bus.StagesOf("bob"), s, mid)
				require.NoError(t, err)
			}
		}
		store.Advance(time.Minute)
		olds, now, err := b.Stages(ctx, "bob", ids...)
		require.NoError(t, err)
		for i, s := range olds {
			require.Equal(t, states[i], s.State, "the old reader reads the old writer's receipt")
			state, age := s.State, "-"
			if state == "" {
				state = "none"
			} else {
				age = s.Age(now).Round(time.Second).String()
			}
			legacy, res, err := ra.Read([]byte(receiptLine(s.ID, state, age)))
			require.NoError(t, err)
			assert.Equal(t, schemaread.FormLegacy, res.Form)
			typed := ack(s.ID, Stage(i+1), actx.push, actx.reason)
			assert.Equal(t, typed, legacy)
			for _, form := range allForms {
				got, res, err := ra.Read(mustWrite(t, ra, &typed, form))
				require.NoError(t, err)
				assert.Equal(t, form, res.Form)
				assert.Equal(t, legacy, got, "the old form in and the new form in read identically (%s)", form)
			}
		}
		// writing the typed ack moves nothing the old reader reads
		again, _, err := b.Stages(ctx, "bob", ids...)
		require.NoError(t, err)
		assert.Equal(t, olds, again, "the old reader reads the same receipts after the writer switch")
		c := ra.Counts()
		assert.Equal(t, uint64(4), c.Reads(schemaread.FormLegacy), "the counter shows the legacy reads")
		for _, form := range allForms {
			assert.Equal(t, uint64(4), c.Reads(form))
		}

		// The beat. The old reader is the sprint wire's argv decode: an argv
		// is a JSON array of strings, and a table beat in any form is none.
		rb := schemaread.NewReader(beatCodec(t, bctx))
		active := time.Unix(1791999990, 0).UTC()
		until := time.Unix(1792003600, 0).UTC()
		for _, tc := range []struct {
			active, until time.Time
			reason        string
			stop          int
		}{
			{active, time.Time{}, "", 0},
			{time.Time{}, time.Time{}, "", 0},
			{active, until, "out of credits", 2},
			{active, time.Time{}, "", 1024},
		} {
			line := beatArgv("ada", tc.active, tc.until, tc.reason, tc.stop)
			var argv []string
			require.NoError(t, json.Unmarshal([]byte(line), &argv), "the old reader reads the old writer")
			legacy, res, err := rb.Read([]byte(line))
			require.NoError(t, err, line)
			assert.Equal(t, schemaread.FormLegacy, res.Form)
			typed := beat("ada", bctx.sentMs, ms(tc.active), ms(tc.until), tc.reason, uint32(tc.stop))
			assert.Equal(t, typed, legacy, line)
			for _, form := range allForms {
				out := mustWrite(t, rb, &typed, form)
				got, res, err := rb.Read(out)
				require.NoError(t, err)
				assert.Equal(t, form, res.Form)
				assert.Equal(t, legacy, got, "the old form in and the new form in read identically (%s)", form)
				// an old reader meets a table beat only if a writer switched
				// before every reader: it reads no argv from it
				var none []string
				assert.Error(t, json.Unmarshal(out, &none), "the old reader does not misread a %s table beat as an argv", form)
			}
		}
		assert.Equal(t, uint64(4), rb.Counts().Reads(schemaread.FormLegacy))

		// The stamp. The old writer is the hook record's scripts' JSON, the
		// old reader hook.Judge: it reads the legacy stamp as a live lease of
		// its holder. Through the switch it reads the JSON text form as the
		// same lease (it keys on epoch and by, which the text form keeps, and
		// skips at_ms), and a binary or base64 stamp as no lease: it never
		// counts a stamp it cannot read, which is why readers go first.
		rs := schemaread.NewReader(stampCodec(t))
		for _, tc := range []struct {
			epoch, at int64
			by        string
		}{
			{7, 1792000000000, "nova-friend 4242"},
			{1, 1, "x"},
			{math.MaxInt64, math.MaxInt64, `quote " and \ slash`},
		} {
			line := legacyStamp(tc.epoch, tc.at, tc.by)
			rec := map[string]string{"role": string(hook.Friend), "state": "hooked", "epoch": strconv.FormatInt(tc.epoch, 10)}
			old := hook.JudgeWith(nil, "ada", rec, []hook.PushRead{{Live: line}})
			require.Len(t, old.Pushes, 1)
			assert.Equal(t, hook.PushArmed, old.Pushes[0].State, "the old reader reads the old writer: %s", line)
			assert.Equal(t, tc.by, old.Pushes[0].By)

			legacy, res, err := rs.Read([]byte(line))
			require.NoError(t, err, line)
			assert.Equal(t, schemaread.FormLegacy, res.Form)
			typed := stamp(tc.epoch, tc.at, tc.by)
			assert.Equal(t, typed, legacy)
			for _, form := range allForms {
				out := mustWrite(t, rs, &typed, form)
				got, res, err := rs.Read(out)
				require.NoError(t, err)
				assert.Equal(t, form, res.Form)
				assert.Equal(t, legacy, got, "the old form in and the new form in read identically (%s)", form)
				meets := hook.JudgeWith(nil, "ada", rec, []hook.PushRead{{Live: string(out)}})
				if form == schemaread.FormJSON {
					assert.Equal(t, old.Pushes[0], meets.Pushes[0], "the old reader reads the JSON text stamp as the legacy one")
					continue
				}
				assert.Equal(t, hook.PushDown, meets.Pushes[0].State, "the old reader does not count a %s table stamp", form)
			}
		}
		assert.Equal(t, uint64(3), rs.Counts().Reads(schemaread.FormLegacy))
		assert.Equal(t, uint64(0), rs.Counts().Refused("legacy_shape"))
	})

	t.Run("mutants are refused by name or read whole", func(t *testing.T) {
		t.Parallel()
		ra := schemaread.NewReader(ackCodec(t, actx))
		a := ack(id, Stageread, PushStatedown, "no live lease")
		n := mutants(t, ra, [][]byte{ackWire(&a), []byte(b64(ackWire(&a))), ackJSON(&a)})
		rb := schemaread.NewReader(beatCodec(t, bctx))
		bt := beat("bob", 1792000000000, 1791999990000, 1792003600000, "out of credits", 2)
		n += mutants(t, rb, [][]byte{beatWire(&bt), []byte(b64(beatWire(&bt))), beatJSON(&bt)})
		rs := schemaread.NewReader(stampCodec(t))
		s := stamp(7, 1792000000000, "nova-friend 4242")
		n += mutants(t, rs, [][]byte{stampWire(&s), []byte(b64(stampWire(&s))), stampJSON(&s)})
		assert.Greater(t, n, 2000)
	})
}

// TestLayer1TextTransport: the generated SaveText and LoadText of Ack, Beat
// and Stamp are the base64 text transport: the text equals the padded
// standard base64 of the table's wire, and LoadText returns a value that
// writes the same wire back.
func TestLayer1TextTransport(t *testing.T) {
	t.Parallel()
	a := ack("01JA2B3C4D5E6F7G8H9J0KMNPQ", Stagedelivered, PushStateup, "")
	aw := ackWire(&a)
	require.NotNil(t, aw)
	at, err := AckSaveText(&a)
	require.NoError(t, err)
	assert.Equal(t, b64(aw), at)
	ag, err := AckLoadText(at, nil)
	require.NoError(t, err)
	assert.Equal(t, aw, ackWire(ag))

	b := beat("rowan", 1760000000000, 1760000000000, 1760000600000, "working", 2)
	bw := beatWire(&b)
	require.NotNil(t, bw)
	bt, err := BeatSaveText(&b)
	require.NoError(t, err)
	assert.Equal(t, b64(bw), bt)
	bg, err := BeatLoadText(bt, nil)
	require.NoError(t, err)
	assert.Equal(t, bw, beatWire(bg))

	s := stamp(21, 1760000000000, "rowan")
	sw := stampWire(&s)
	require.NotNil(t, sw)
	st, err := StampSaveText(&s)
	require.NoError(t, err)
	assert.Equal(t, b64(sw), st)
	sg, err := StampLoadText(st, nil)
	require.NoError(t, err)
	assert.Equal(t, sw, stampWire(sg))

	_, err = AckLoadText("not base64!", nil)
	require.Error(t, err)
}
