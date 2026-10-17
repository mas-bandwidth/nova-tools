package answerwire

// TestWorkerAnswersGolden is the golden test of the answer definitions
// (schemas/answer): every root table of the worker answers (the sprint
// request and response, the friend cards, worker view, routes, holders and
// queue answers, the take answer with its member packet), the bus send-token
// record and the hook judgment record round-trips as the binary wire, as
// padded standard base64 of that wire and as the JSON text form, each read
// strictly through pkg/schemaread (S1, S2); a value past a declared bound and
// a damaged image of each form are refused by name, never clamped; and the
// JSON text keeps the keys of the Go struct each table replaces.
//
// The fixtures are the JSON text form, written by hand from the Go structs
// the tables replace (pkg/sprintwire, pkg/friend, pkg/member, pkg/bus,
// hook): the binary and base64 images are what the generated writer writes
// from them, so no fixture is a binary file.

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const help = "nova-sprint -h"

// doc is one typed root and the region that holds it: pointer fields are
// self-relative references into that region, so the value is never copied out.
type doc[R any] struct {
	v      *R
	region []byte
}

// ops are one root table's generated functions, in the shapes pkg/schemaread
// takes them.
type ops[R any] struct {
	measureReason func([]byte) (int64, error)
	loadMeasure   func([]byte) int64
	load          func(region, wire []byte, report *TableReport) *R
	fromJSON      func(text []byte, report *TableReport) (*R, []byte, bool)
	measure       func(*R) int64
	save          func(*R, []byte) int64
	toJSONMeasure func(*R) int64
	toJSON        func(*R, []byte) int64
}

func alignedRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func reportOf(r *TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped),
		// a key given twice in the JSON text is ambiguous: refused, never last-wins
		Malformed: r.Malformed || r.Duplicate > 0, Reason: r.Reason,
	}
}

// codecOf is a root's functions as a schemaread.Codec. constraints is the
// bounds the generated Go cannot state: required fields and the counts of
// unbounded arrays (S1, schemas/answer).
func codecOf[R any](name string, o ops[R], constraints func(*R, *schemaread.Checker)) *schemaread.Codec[doc[R]] {
	return &schemaread.Codec[doc[R]]{
		Name: name, Help: help,
		Reset: func(d *doc[R]) { *d = doc[R]{} },
		Measure: func(wire []byte) error {
			_, err := o.measureReason(wire)
			return err
		},
		LoadWire: func(d *doc[R], wire []byte) schemaread.Report {
			n := o.loadMeasure(wire)
			if n < 0 {
				return schemaread.Report{Malformed: true}
			}
			var r TableReport
			d.region = alignedRegion(n)
			if d.v = o.load(d.region, wire, &r); d.v == nil {
				return schemaread.Report{Malformed: true}
			}
			return reportOf(&r)
		},
		LoadJSON: func(d *doc[R], text []byte) schemaread.Report {
			var r TableReport
			v, region, ok := o.fromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, region
			return reportOf(&r)
		},
		Constraints: func(d *doc[R], c *schemaread.Checker) {
			c.Required(name, d.v != nil)
			if d.v != nil && constraints != nil {
				constraints(d.v, c)
			}
		},
		SaveWire: func(d *doc[R]) []byte {
			n := o.measure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if o.save(d.v, buf) != n {
				return nil
			}
			return buf
		},
		SaveJSON: func(d *doc[R]) []byte {
			n := o.toJSONMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if o.toJSON(d.v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

var requestOps = ops[Request]{
	measureReason: RequestLoadMeasureReason,
	load:          RequestLoad,
	fromJSON: func(text []byte, r *TableReport) (*Request, []byte, bool) {
		var b RequestBuilder
		if !b.Init() || !RequestFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   RequestLoadMeasure,
	measure:       func(v *Request) int64 { return RequestMeasure(v) },
	save:          func(v *Request, buf []byte) int64 { return RequestSave(v, buf) },
	toJSONMeasure: func(v *Request) int64 { return RequestToJsonMeasure(v) },
	toJSON:        func(v *Request, buf []byte) int64 { return RequestToJson(v, buf) },
}

var responseOps = ops[Response]{
	measureReason: ResponseLoadMeasureReason,
	load:          ResponseLoad,
	fromJSON: func(text []byte, r *TableReport) (*Response, []byte, bool) {
		var b ResponseBuilder
		if !b.Init() || !ResponseFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   ResponseLoadMeasure,
	measure:       func(v *Response) int64 { return ResponseMeasure(v) },
	save:          func(v *Response, buf []byte) int64 { return ResponseSave(v, buf) },
	toJSONMeasure: func(v *Response) int64 { return ResponseToJsonMeasure(v) },
	toJSON:        func(v *Response, buf []byte) int64 { return ResponseToJson(v, buf) },
}

var heldAnswerOps = ops[HeldAnswer]{
	measureReason: HeldAnswerLoadMeasureReason,
	load:          HeldAnswerLoad,
	fromJSON: func(text []byte, r *TableReport) (*HeldAnswer, []byte, bool) {
		var b HeldAnswerBuilder
		if !b.Init() || !HeldAnswerFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   HeldAnswerLoadMeasure,
	measure:       func(v *HeldAnswer) int64 { return HeldAnswerMeasure(v) },
	save:          func(v *HeldAnswer, buf []byte) int64 { return HeldAnswerSave(v, buf) },
	toJSONMeasure: func(v *HeldAnswer) int64 { return HeldAnswerToJsonMeasure(v) },
	toJSON:        func(v *HeldAnswer, buf []byte) int64 { return HeldAnswerToJson(v, buf) },
}

var workerViewOps = ops[WorkerView]{
	measureReason: WorkerViewLoadMeasureReason,
	load:          WorkerViewLoad,
	fromJSON: func(text []byte, r *TableReport) (*WorkerView, []byte, bool) {
		var b WorkerViewBuilder
		if !b.Init() || !WorkerViewFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   WorkerViewLoadMeasure,
	measure:       func(v *WorkerView) int64 { return WorkerViewMeasure(v) },
	save:          func(v *WorkerView, buf []byte) int64 { return WorkerViewSave(v, buf) },
	toJSONMeasure: func(v *WorkerView) int64 { return WorkerViewToJsonMeasure(v) },
	toJSON:        func(v *WorkerView, buf []byte) int64 { return WorkerViewToJson(v, buf) },
}

var holdersViewOps = ops[HoldersView]{
	measureReason: HoldersViewLoadMeasureReason,
	load:          HoldersViewLoad,
	fromJSON: func(text []byte, r *TableReport) (*HoldersView, []byte, bool) {
		var b HoldersViewBuilder
		if !b.Init() || !HoldersViewFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   HoldersViewLoadMeasure,
	measure:       func(v *HoldersView) int64 { return HoldersViewMeasure(v) },
	save:          func(v *HoldersView, buf []byte) int64 { return HoldersViewSave(v, buf) },
	toJSONMeasure: func(v *HoldersView) int64 { return HoldersViewToJsonMeasure(v) },
	toJSON:        func(v *HoldersView, buf []byte) int64 { return HoldersViewToJson(v, buf) },
}

var routesViewOps = ops[RoutesView]{
	measureReason: RoutesViewLoadMeasureReason,
	load:          RoutesViewLoad,
	fromJSON: func(text []byte, r *TableReport) (*RoutesView, []byte, bool) {
		var b RoutesViewBuilder
		if !b.Init() || !RoutesViewFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   RoutesViewLoadMeasure,
	measure:       func(v *RoutesView) int64 { return RoutesViewMeasure(v) },
	save:          func(v *RoutesView, buf []byte) int64 { return RoutesViewSave(v, buf) },
	toJSONMeasure: func(v *RoutesView) int64 { return RoutesViewToJsonMeasure(v) },
	toJSON:        func(v *RoutesView, buf []byte) int64 { return RoutesViewToJson(v, buf) },
}

var takeOutOps = ops[TakeOut]{
	measureReason: TakeOutLoadMeasureReason,
	load:          TakeOutLoad,
	fromJSON: func(text []byte, r *TableReport) (*TakeOut, []byte, bool) {
		var b TakeOutBuilder
		if !b.Init() || !TakeOutFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   TakeOutLoadMeasure,
	measure:       func(v *TakeOut) int64 { return TakeOutMeasure(v) },
	save:          func(v *TakeOut, buf []byte) int64 { return TakeOutSave(v, buf) },
	toJSONMeasure: func(v *TakeOut) int64 { return TakeOutToJsonMeasure(v) },
	toJSON:        func(v *TakeOut, buf []byte) int64 { return TakeOutToJson(v, buf) },
}

var queueOutOps = ops[QueueOut]{
	measureReason: QueueOutLoadMeasureReason,
	load:          QueueOutLoad,
	fromJSON: func(text []byte, r *TableReport) (*QueueOut, []byte, bool) {
		var b QueueOutBuilder
		if !b.Init() || !QueueOutFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   QueueOutLoadMeasure,
	measure:       func(v *QueueOut) int64 { return QueueOutMeasure(v) },
	save:          func(v *QueueOut, buf []byte) int64 { return QueueOutSave(v, buf) },
	toJSONMeasure: func(v *QueueOut) int64 { return QueueOutToJsonMeasure(v) },
	toJSON:        func(v *QueueOut, buf []byte) int64 { return QueueOutToJson(v, buf) },
}

var sentRecordOps = ops[SentRecord]{
	measureReason: SentRecordLoadMeasureReason,
	load:          SentRecordLoad,
	fromJSON: func(text []byte, r *TableReport) (*SentRecord, []byte, bool) {
		var b SentRecordBuilder
		if !b.Init() || !SentRecordFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   SentRecordLoadMeasure,
	measure:       func(v *SentRecord) int64 { return SentRecordMeasure(v) },
	save:          func(v *SentRecord, buf []byte) int64 { return SentRecordSave(v, buf) },
	toJSONMeasure: func(v *SentRecord) int64 { return SentRecordToJsonMeasure(v) },
	toJSON:        func(v *SentRecord, buf []byte) int64 { return SentRecordToJson(v, buf) },
}

var judgmentRecOps = ops[JudgmentRec]{
	measureReason: JudgmentRecLoadMeasureReason,
	load:          JudgmentRecLoad,
	fromJSON: func(text []byte, r *TableReport) (*JudgmentRec, []byte, bool) {
		var b JudgmentRecBuilder
		if !b.Init() || !JudgmentRecFromJson(&b, text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	},
	loadMeasure:   JudgmentRecLoadMeasure,
	measure:       func(v *JudgmentRec) int64 { return JudgmentRecMeasure(v) },
	save:          func(v *JudgmentRec, buf []byte) int64 { return JudgmentRecSave(v, buf) },
	toJSONMeasure: func(v *JudgmentRec) int64 { return JudgmentRecToJsonMeasure(v) },
	toJSON:        func(v *JudgmentRec, buf []byte) int64 { return JudgmentRecToJson(v, buf) },
}

// The count bounds of the unbounded arrays, held by the reader because an
// unbounded array declares none (schemas/answer/answer.schema).
const (
	maxVerbs      = sprintwire.MaxVerbs // pkg/sprintwire MaxVerbs: the verbs of one batch
	maxWords      = 4096                // the words of one verb, a ceiling pkg/sprintwire MaxRequest (1 MiB) makes unreachable
	maxWord       = sprintwire.MaxRequest
	maxCards      = 1024 // a friend's row, the worker view, a take
	maxHolders    = 4096
	maxQueueCards = 4096
	maxRoutes     = 256
	maxNotes      = 256
	maxCardText   = 64 << 10 // a rework's finding and why, the sprint's card text bound
)

func str(slot *int64) string {
	v := TableStringAt((*TableRef)(slot))
	return string(v.Data[:v.Length])
}

func within(c *schemaread.Checker, field string, n, hi int) {
	c.Bound(field, int64(n), 0, int64(hi))
}

func linesWithin[L any](c *schemaread.Checker, field string, l *TableList[L], hi int, slot func(*L) *int64, limit int) {
	within(c, field, int(l.Count), hi)
	for i := range l.Count {
		if e := l.At(i); e != nil {
			c.Text(field, str(slot(e)), limit)
		}
	}
}

func lineSlot(l *LineRow) *int64 { return &l.S }

func requestConstraints(r *Request, c *schemaread.Checker) {
	within(c, "verbs", int(r.Verbs.Count), maxVerbs)
	for i := range r.Verbs.Count {
		if v := r.Verbs.At(i); v != nil {
			linesWithin(c, "words", &v.Words, maxWords, lineSlot, maxWord)
		}
	}
}

func responseConstraints(r *Response, c *schemaread.Checker) {
	within(c, "results", int(r.Results.Count), maxVerbs)
	for i := range r.Results.Count {
		if v := r.Results.At(i); v != nil {
			c.Required("code", v.CodePresent)
			c.Required("stdout", v.Stdout != 0)
			c.Required("stderr", v.Stderr != 0)
			c.Enum("outcome", int(v.Outcome), int(OutcomeCount)+1)
			c.Enum("changed", int(v.Changed), int(ChangedCount)+1)
		}
	}
}

func heldConstraints(a *HeldAnswer, c *schemaread.Checker) {
	c.Required("friend", a.FriendNameLength > 0)
	within(c, "cards", int(a.Cards.Count), maxCards)
}

func workerViewConstraints(v *WorkerView, c *schemaread.Checker) {
	c.Required("view", v.ViewLength > 0)
	within(c, "cards", int(v.Cards.Count), maxCards)
}

func holdersConstraints(v *HoldersView, c *schemaread.Checker) {
	c.Required("view", v.ViewLength > 0)
	within(c, "cards", int(v.Cards.Count), maxHolders)
	for i := range v.Cards.Count {
		if h := v.Cards.At(i); h != nil {
			c.Required("id", h.IdLength > 0)
		}
	}
}

func routesConstraints(v *RoutesView, c *schemaread.Checker) {
	within(c, "routes", int(v.Routes.Count), maxRoutes)
}

func packetConstraints(p *Packet, c *schemaread.Checker) {
	c.Required("card", p.CardLength > 0)
	c.Enum("kind", int(p.Kind), int(PacketKindCount)+1)
	for _, f := range []struct {
		name string
		slot *int64
	}{{"fix", &p.Fix}, {"finding", &p.Finding}, {"why", &p.Why}, {"report", &p.Report}} {
		if f.slot != nil && *f.slot != 0 {
			c.Text(f.name, str(f.slot), maxCardText)
		}
	}
	linesWithin(c, "notes", &p.Notes, maxNotes, lineSlot, maxCardText)
}

func takeConstraints(t *TakeOut, c *schemaread.Checker) {
	within(c, "packets", int(t.Packets.Count), maxCards)
	for i := range t.Packets.Count {
		if p := t.Packets.At(i); p != nil {
			packetConstraints(p, c)
		}
	}
}

func queueConstraints(q *QueueOut, c *schemaread.Checker) {
	c.Required("as", q.AsNameLength > 0)
	c.Enum("machine", int(q.Machine), int(MachineStateCount)+1)
	within(c, "cards", int(q.Cards.Count), maxQueueCards)
	for i := range q.Cards.Count {
		qc := q.Cards.At(i)
		if qc == nil {
			continue
		}
		c.Required("id", qc.IdLength > 0)
		if qc.Packet != 0 {
			if p := (*Packet)(unsafe.Pointer(uintptr(unsafe.Pointer(&qc.Packet)) + uintptr(qc.Packet))); p != nil {
				packetConstraints(p, c)
			}
		}
	}
}

func sentConstraints(s *SentRecord, c *schemaread.Checker) {
	c.Required("fingerprint", s.FingerprintLength > 0)
	c.Required("id", s.IdLength > 0)
	c.Required("at", s.At != 0)
}

func judgmentConstraints(j *JudgmentRec, c *schemaread.Checker) {
	c.Required("id", j.IdLength > 0)
	c.Required("to", j.ToCount > 0)
	c.Required("due_ms", j.DueMs != 0)
}

// bad is a value past a declared bound or a field a reader requires: the JSON
// text that holds it, and the refusal that names it.
type bad struct {
	name, json, reason, field string
	// wire says the value is also written as the binary wire and base64 by a
	// reader with no constraints, and refused when read back: a count bound is
	// the reader's, not the generated writer's.
	wire bool
}

func refusalOf(t *testing.T, err error, name, reason, field string) {
	t.Helper()
	var r *schemaread.Refusal
	require.ErrorAs(t, err, &r)
	assert.Equal(t, name, r.Name)
	assert.Equal(t, reason, r.Reason, "%v", err)
	assert.Equal(t, field, r.Field, "%v", err)
	assert.Contains(t, err.Error(), name+" REFUSED: "+reason)
}

func runRoot[R any](t *testing.T, name string, o ops[R], cons func(*R, *schemaread.Checker), fixture string, wantUnknown int, bads []bad) {
	t.Helper()
	checked := schemaread.NewReader(codecOf(name, o, cons))
	open := schemaread.NewReader(codecOf(name, o, nil))

	// the JSON text form reads, and writes the same text back
	v, res, err := checked.Read([]byte(fixture))
	require.NoError(t, err, "%s: %s", name, fixture)
	assert.Equal(t, schemaread.FormJSON, res.Form)
	jsonImg, err := checked.Write(&v, schemaread.FormJSON)
	require.NoError(t, err)
	var want, got any
	require.NoError(t, json.Unmarshal([]byte(fixture), &want))
	require.NoError(t, json.Unmarshal(jsonImg, &got), string(jsonImg))
	assert.True(t, holds(want, got), "%s: the JSON text form holds every key and value of the fixture\nfixture %s\nwritten %s", name, fixture, jsonImg)

	// the binary wire and padded standard base64 of it read, and write the same value
	wire, err := checked.Write(&v, schemaread.FormBinary)
	require.NoError(t, err)
	require.NotEmpty(t, wire)
	assert.Equal(t, byte(1), wire[0], "%s: the wire leads with its form byte", name)
	b64, err := checked.Write(&v, schemaread.FormBase64)
	require.NoError(t, err)
	for form, img := range map[schemaread.Form][]byte{schemaread.FormBinary: wire, schemaread.FormBase64: b64, schemaread.FormJSON: jsonImg} {
		w, r, err := checked.Read(img)
		require.NoError(t, err, "%s %s", name, form)
		assert.Equal(t, form, r.Form)
		again, err := checked.Write(&w, schemaread.FormBinary)
		require.NoError(t, err)
		assert.Equal(t, wire, again, "%s: %s reads to the same wire", name, form)
		text, err := checked.Write(&w, schemaread.FormJSON)
		require.NoError(t, err)
		assert.Equal(t, string(jsonImg), string(text), "%s: %s reads to the same text", name, form)
	}
	assert.Equal(t, uint64(2), checked.Counts().Reads(schemaread.FormJSON), "%s: json reads are counted", name)
	assert.Equal(t, uint64(1), checked.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), checked.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(wantUnknown), checked.Counts().Unknown())

	// a damaged image of each form is refused by name, never read
	for _, d := range []struct {
		what   string
		in     []byte
		reason string
	}{
		{"binary cut short", wire[:len(wire)-1], schemaread.ReasonWireDamaged},
		{"binary cut to the form byte", wire[:1], schemaread.ReasonWireDamaged},
		{"binary of an unknown form", append([]byte{9}, wire[1:]...), schemaread.ReasonUnknownForm},
		{"base64 with a stray byte", append([]byte("!"), b64...), ""},
		{"base64 of a cut wire", []byte(bytes.TrimRight(b64, "=")[:len(bytes.TrimRight(b64, "="))-8]), ""},
		{"JSON cut short", jsonImg[:len(jsonImg)-2], schemaread.ReasonMalformed},
		{"JSON with a key given twice", []byte(`{"` + firstKey(string(jsonImg)) + `":1,"` + firstKey(string(jsonImg)) + `":2}`), ""},
	} {
		_, _, err := checked.Read(d.in)
		require.Error(t, err, "%s: %s is refused", name, d.what)
		var r *schemaread.Refusal
		require.ErrorAs(t, err, &r, "%s: %s", name, d.what)
		assert.Contains(t, err.Error(), name+" REFUSED: ", "%s: %s", name, d.what)
		assert.NotEmpty(t, r.Reason)
		if d.reason != "" {
			assert.Equal(t, d.reason, r.Reason, "%s: %s", name, d.what)
		}
	}

	// a value past a declared bound is refused by name, never clamped
	for _, b := range bads {
		_, _, err := checked.Read([]byte(b.json))
		require.Error(t, err, "%s: %s", name, b.name)
		refusalOf(t, err, name, b.reason, b.field)
		if !b.wire {
			continue
		}
		// the count is the reader's: a reader with no constraints reads it,
		// and what it writes is refused by the reader that holds the bound,
		// and by the writer
		w, _, err := open.Read([]byte(b.json))
		require.NoError(t, err, "%s: %s reads with no constraints", name, b.name)
		_, err = checked.Write(&w, schemaread.FormBinary)
		refusalOf(t, err, name, b.reason, b.field)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64} {
			img, err := open.Write(&w, form)
			require.NoError(t, err)
			_, _, err = checked.Read(img)
			refusalOf(t, err, name, b.reason, b.field)
		}
	}
}

// holds says got carries every key and value want does: the writer also writes
// a field at its default, which the fixture leaves out.
func holds(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range w {
			if gv, found := g[k]; !found || !holds(wv, gv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !holds(w[i], g[i]) {
				return false
			}
		}
		return true
	}
	return want == got
}

func firstKey(j string) string {
	i := strings.Index(j, `"`)
	k := strings.Index(j[i+1:], `"`)
	return j[i+1 : i+1+k]
}

func many(n int, one string) string {
	return strings.TrimSuffix(strings.Repeat(one+",", n), ",")
}

func TestWorkerAnswersGolden(t *testing.T) {
	t.Parallel()

	t.Run("the pin is the repositorys", func(t *testing.T) {
		t.Parallel()
		// one compiler writes the whole tree (schemas/README.md, the pin): the
		// version this package names is the version every sibling names
		pin := func(file string) string {
			raw, err := os.ReadFile(file)
			require.NoError(t, err)
			m := regexp.MustCompile(`schema/v2/cmd/schema@(v[0-9A-Za-z.+-]+) generate`).FindSubmatch(raw)
			require.NotNil(t, m, file)
			return string(m[1])
		}
		mine := pin("generate.go")
		for _, sibling := range []string{"../pongwire", "../buswire", "../hookwire"} {
			assert.Equal(t, mine, pin(sibling+"/generate.go"), sibling)
		}
		assert.NotZero(t, ProtocolId)
	})

	t.Run("sprintwire request", func(t *testing.T) {
		t.Parallel()
		verb := `{"words":[{"s":"take"},{"s":"--as"},{"s":"friend.rowan"},{"s":"--json"}]}`
		runRoot(t, "Request", requestOps, requestConstraints,
			`{"verbs":[`+verb+`,{"words":[{"s":"queue"},{"s":"--json"}]}]}`, 0, []bad{
				{"verbs past MaxVerbs", `{"verbs":[` + many(maxVerbs+1, verb) + `]}`, schemaread.ReasonClamped, "verbs", true},
				{"words past the verb's ceiling", `{"verbs":[{"words":[` + many(maxWords+1, `{"s":"w"}`) + `]}]}`, schemaread.ReasonClamped, "words", true},
			})
	})

	t.Run("sprintwire response", func(t *testing.T) {
		t.Parallel()
		ok := `{"code":0,"stdout":"ok\n","stderr":"","outcome":"accepted","changed":"yes"}`
		runRoot(t, "Response", responseOps, responseConstraints,
			`{"results":[`+ok+`,{"code":2,"stdout":"","stderr":"usage\n","outcome":"refused","changed":"no"}]}`, 0, []bad{
				{"results past MaxVerbs", `{"results":[` + many(maxVerbs+1, ok) + `]}`, schemaread.ReasonClamped, "results", true},
				{"an exit code past 255", `{"results":[{"code":256,"stdout":"","stderr":""}]}`, schemaread.ReasonClamped, "", false},
				{"no exit code", `{"results":[{"stdout":"","stderr":""}]}`, schemaread.ReasonAbsentRequired, "code", true},
				{"no stdout", `{"results":[{"code":0,"stderr":""}]}`, schemaread.ReasonAbsentRequired, "stdout", true},
				{"no stderr", `{"results":[{"code":0,"stdout":""}]}`, schemaread.ReasonAbsentRequired, "stderr", true},
			})
	})

	t.Run("friend cards answer", func(t *testing.T) {
		t.Parallel()
		card := `{"card":"a11-defs-worker-answers","job":"a11-defs-worker-answers.w1~21","col":"working","kind":"work","branch":"sprint/a.w1.g1.e21","tier":"pro","stream":"v15-schema-tables","attempt":1,"gen":1,"epoch":21,"repo":"mas-bandwidth/nova-tools","base":"sprint/v15-schema-tables","brief":"/inbox/a/BRIEF.md","lane":2}`
		// a brief past 512 bytes and a 200 byte job (pkg/friend validJob) are held whole
		longCard := `{"card":"a","job":"` + strings.Repeat("j", 200) + `","brief":"` + strings.Repeat("b", 600) + `"}`
		runRoot(t, "HeldAnswer", heldAnswerOps, heldConstraints,
			`{"friend":"rowan","cards":[`+card+`,`+longCard+`]}`, 0, []bad{
				{"a friend name past 64 bytes", `{"friend":"` + strings.Repeat("a", 65) + `","cards":[]}`, schemaread.ReasonClamped, "", false},
				{"cards past the ceiling", `{"friend":"rowan","cards":[` + many(maxCards+1, card) + `]}`, schemaread.ReasonClamped, "cards", true},
				{"a lane past 1024", `{"friend":"rowan","cards":[{"card":"a","lane":1025}]}`, schemaread.ReasonClamped, "", false},
				{"no friend", `{"cards":[]}`, schemaread.ReasonAbsentRequired, "friend", true},
			})
		// today's struct writes the keys the table reads
		raw, err := json.Marshal(friend.HeldAnswer{Friend: "rowan", Cards: []friend.HeldCard{{Card: "a", Job: "a.w1~21", Col: "working", Epoch: 21, Brief: "/b", Lane: 2}}})
		require.NoError(t, err)
		got, res, err := schemaread.NewReader(codecOf("HeldAnswer", heldAnswerOps, heldConstraints)).Read(raw)
		require.NoError(t, err, string(raw))
		assert.Equal(t, schemaread.FormJSON, res.Form)
		assert.Equal(t, "rowan", string(got.v.FriendName[:got.v.FriendNameLength]))
		require.Equal(t, int32(1), got.v.Cards.Count)
		c := got.v.Cards.At(0)
		assert.Equal(t, "a.w1~21", string(c.Job[:c.JobLength]))
		assert.Equal(t, uint64(21), c.Epoch)
		assert.Equal(t, uint32(2), c.Lane)

		// the struct writes a 600 byte brief and a 200 byte job: both read back whole
		long := friend.HeldCard{Card: "a", Job: strings.Repeat("j", 200), Brief: strings.Repeat("b", 600)}
		raw, err = json.Marshal(friend.HeldAnswer{Friend: "rowan", Cards: []friend.HeldCard{long}})
		require.NoError(t, err)
		got, _, err = schemaread.NewReader(codecOf("HeldAnswer", heldAnswerOps, heldConstraints)).Read(raw)
		require.NoError(t, err)
		c = got.v.Cards.At(0)
		assert.Equal(t, long.Job, string(c.Job[:c.JobLength]))
	})

	t.Run("worker view", func(t *testing.T) {
		t.Parallel()
		runRoot(t, "WorkerView", workerViewOps, workerViewConstraints,
			`{"view":"worker","as":"rowan","kind":"friend","epoch":21,"cards":[{"id":"a11","st":"working","brief":"/home/inbox/a11.w1~21/BRIEF.md"}]}`, 0, []bad{
				{"an as past 64 bytes", `{"view":"worker","as":"` + strings.Repeat("a", 65) + `"}`, schemaread.ReasonClamped, "", false},
				{"cards past the ceiling", `{"view":"worker","cards":[` + many(maxCards+1, `{"id":"a"}`) + `]}`, schemaread.ReasonClamped, "cards", true},
				{"no view", `{"as":"rowan"}`, schemaread.ReasonAbsentRequired, "view", true},
			})
		// a view with a field a newer server adds is read, and counts the field
		_, res, err := schemaread.NewReader(codecOf("WorkerView", workerViewOps, workerViewConstraints)).Read([]byte(`{"view":"worker","as":"rowan","kind":"friend","epoch":21,"cards":[],"next_gen_field":1}`))
		require.NoError(t, err)
		assert.Equal(t, 1, res.Unknown)
	})

	t.Run("holders view", func(t *testing.T) {
		t.Parallel()
		runRoot(t, "HoldersView", holdersViewOps, holdersConstraints,
			`{"view":"cards","schema":1,"cards":[{"id":"a11","holder":"friend.rowan"},{"id":"a12"}]}`, 0, []bad{
				{"a holder past 64 bytes", `{"view":"cards","cards":[{"id":"a","holder":"` + strings.Repeat("h", 65) + `"}]}`, schemaread.ReasonClamped, "", false},
				{"cards past the ceiling", `{"view":"cards","cards":[` + many(maxHolders+1, `{"id":"a"}`) + `]}`, schemaread.ReasonClamped, "cards", true},
				{"a card with no id", `{"view":"cards","cards":[{"holder":"x"}]}`, schemaread.ReasonAbsentRequired, "id", true},
			})
	})

	t.Run("routes view", func(t *testing.T) {
		t.Parallel()
		route := `{"route":{"name":"codex-pro","tier":"pro","provider":"subscription-codex","model":"gpt","harness":"codex","enabled":true,"prices":{"input":"1.25","output":"10","reasoning_as_output":false,"billing":"subscription","as_of":"2026-10-01"}}}`
		runRoot(t, "RoutesView", routesViewOps, routesConstraints,
			`{"tier_routes":{"pro":{"names":[{"s":"codex-pro"}]},"flash":{"names":[]}},"routes":[`+route+`]}`, 0, []bad{
				{"a tier name past 16 bytes", `{"tier_routes":{"` + strings.Repeat("t", 17) + `":{"names":[]}}}`, schemaread.ReasonClamped, "", false},
				{"a route name past 64 bytes", `{"routes":[{"route":{"name":"` + strings.Repeat("n", 65) + `"}}]}`, schemaread.ReasonClamped, "", false},
				{"routes past the ceiling", `{"routes":[` + many(maxRoutes+1, route) + `]}`, schemaread.ReasonClamped, "routes", true},
			})
		// a price sheet that does not say reasoning_as_output stays absent, so the
		// reader can bill reasoning as output (pkg/friend RoutePriceOf)
		d, _, err := schemaread.NewReader(codecOf("RoutesView", routesViewOps, nil)).Read([]byte(`{"routes":[{"route":{"prices":{"input":"1"}}}]}`))
		require.NoError(t, err)
		assert.False(t, d.v.Routes.At(0).Route.Prices.ReasoningAsOutputPresent)
		// the prices keys are cardcost's
		raw, err := json.Marshal(map[string]any{"routes": []any{map[string]any{"route": map[string]any{"prices": cardcost.Prices{Input: "1", Output: "2", ReasoningAsOutput: true, LongContext: 200000, Billing: "plan"}}}}})
		require.NoError(t, err)
		d, _, err = schemaread.NewReader(codecOf("RoutesView", routesViewOps, nil)).Read(raw)
		require.NoError(t, err, string(raw))
		p := d.v.Routes.At(0).Route.Prices
		assert.True(t, p.ReasoningAsOutput && p.ReasoningAsOutputPresent)
		assert.Equal(t, int64(200000), p.LongContext)
		assert.Equal(t, "plan", string(p.Billing[:p.BillingLength]))
	})

	t.Run("take answer and member packet", func(t *testing.T) {
		t.Parallel()
		packet := `{"billing":"plan","card":"a11","kind":"work","as":"rowan","primary":"rowan","stream":"v15","attempt":1,"gen":1,"epoch":21,"brief":"/b","notes":[{"s":"one"},{"s":"two"}],"branch":"sprint/a","base":"sprint/v15","base_head":"abc","base_attempt":1,"worker":"rowan","head":"def","work_branch":"w","work_base":"b","report":"done","route":"r","model":"m","tokens":"1000","usd":"2.5","harness":"claude","deadline":9000,"rate_rpm":60,"rate_concurrent":4,"tier":"pro","decide_bounce":"0.2","decide_review":"0.1","decide_gate_flaky":"0.3","decide_gate_preexisting":"0.4"}`
		runRoot(t, "TakeOut", takeOutOps, takeConstraints,
			`{"packets":[`+packet+`,{"card":"a12","kind":"read","finding":"f","why":"w"},{"card":"a13","brief":"`+strings.Repeat("b", 600)+`"}]}`, 0, []bad{
				{"a card past 160 bytes", `{"packets":[{"card":"` + strings.Repeat("c", 161) + `"}]}`, schemaread.ReasonClamped, "", false},
				{"a deadline past its bound", `{"packets":[{"card":"a","deadline":100000001}]}`, schemaread.ReasonClamped, "", false},
				{"packets past the ceiling", `{"packets":[` + many(maxCards+1, `{"card":"a"}`) + `]}`, schemaread.ReasonClamped, "packets", true},
				{"notes past the ceiling", `{"packets":[{"card":"a","notes":[` + many(maxNotes+1, `{"s":"n"}`) + `]}]}`, schemaread.ReasonClamped, "notes", true},
				{"a finding past the card text bound", `{"packets":[{"card":"a","finding":"` + strings.Repeat("f", maxCardText+1) + `"}]}`, schemaread.ReasonClamped, "finding", true},
				{"a packet with no card", `{"packets":[{"kind":"work"}]}`, schemaread.ReasonAbsentRequired, "card", true},
			})
	})

	t.Run("queue answer", func(t *testing.T) {
		t.Parallel()
		runRoot(t, "QueueOut", queueOutOps, queueConstraints,
			`{"as":"rowan","epoch":21,"width":3,"cards":[{"id":"a11","table":"work","row":"a11","col":"ready","gen":1,"attempt":1,"packet":{"card":"a11","kind":"work","epoch":21}},{"id":"a12","table":"read","row":"a12","col":"working"}],"reader":false,"machine":"RUNNING"}`, 0, []bad{
				{"a width past 1024", `{"as":"rowan","width":1025}`, schemaread.ReasonClamped, "", false},
				{"cards past the ceiling", `{"as":"rowan","cards":[` + many(maxQueueCards+1, `{"id":"a"}`) + `]}`, schemaread.ReasonClamped, "cards", true},
				{"a card with no id", `{"as":"rowan","cards":[{"row":"a"}]}`, schemaread.ReasonAbsentRequired, "id", true},
				{"no name", `{"epoch":1}`, schemaread.ReasonAbsentRequired, "as", true},
			})
		// a reader flag that is absent is not false: a server from before the field
		d, _, err := schemaread.NewReader(codecOf("QueueOut", queueOutOps, queueConstraints)).Read([]byte(`{"as":"rowan"}`))
		require.NoError(t, err)
		assert.False(t, d.v.ReaderPresent)
		d, _, err = schemaread.NewReader(codecOf("QueueOut", queueOutOps, queueConstraints)).Read([]byte(`{"as":"rowan","reader":false}`))
		require.NoError(t, err)
		assert.True(t, d.v.ReaderPresent)
		assert.False(t, d.v.Reader)
	})

	t.Run("bus send-token record", func(t *testing.T) {
		t.Parallel()
		fp := strings.Repeat("ab", 32)
		runRoot(t, "SentRecord", sentRecordOps, sentConstraints,
			`{"fingerprint":"`+fp+`","id":"01J0000000000000000000000A","at":"2026-10-17T01:02:03.456789012Z"}`, 0, []bad{
				{"a fingerprint past 64 bytes", `{"fingerprint":"` + fp + `ab","id":"i","at":"2026-10-17T01:02:03Z"}`, schemaread.ReasonClamped, "", false},
				{"an id past 64 bytes", `{"fingerprint":"f","id":"` + strings.Repeat("i", 65) + `","at":"2026-10-17T01:02:03Z"}`, schemaread.ReasonClamped, "", false},
				{"no id", `{"fingerprint":"f","at":"2026-10-17T01:02:03Z"}`, schemaread.ReasonAbsentRequired, "id", true},
				{"no stamp", `{"fingerprint":"f","id":"i"}`, schemaread.ReasonAbsentRequired, "at", true},
			})
	})

	t.Run("hook judgment record", func(t *testing.T) {
		t.Parallel()
		runRoot(t, "JudgmentRec", judgmentRecOps, judgmentConstraints,
			`{"id":"01J0000000000000000000000A","to":[{"s":"rowan"},{"s":"stella"}],"subject":"review the plan","due_ms":1790000000000,"overdue":true}`, 0, []bad{
				{"a recipient past 64 bytes", `{"id":"i","to":[{"s":"` + strings.Repeat("r", 65) + `"}],"due_ms":1}`, schemaread.ReasonClamped, "", false},
				{"a subject past 200 bytes", `{"id":"i","to":[{"s":"r"}],"subject":"` + strings.Repeat("s", 201) + `","due_ms":1}`, schemaread.ReasonClamped, "", false},
				{"more than 64 recipients", `{"id":"i","to":[` + many(65, `{"s":"r"}`) + `],"due_ms":1}`, schemaread.ReasonClamped, "", false},
				{"no id", `{"to":[{"s":"r"}],"due_ms":1}`, schemaread.ReasonAbsentRequired, "id", true},
				{"no recipient", `{"id":"i","due_ms":1}`, schemaread.ReasonAbsentRequired, "to", true},
			})
	})
}
