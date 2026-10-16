package friend

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// PongLine is a session's pong as it travels on the bus: pong <nonce>
// queue=<n> working=<n> width=<n>. A session sends it to answer a reach step
// or a delivery check (nova-friend pong); liveness is the nova-hook record's
// alone, never a pong. It is the one pong writer, and it writes the legacy
// line: readers change first (PongReader), writers after.
func PongLine(nonce string, queue, working, width int) string {
	return fmt.Sprintf("pong %s queue=%d working=%d width=%d", nonce, queue, working, width)
}

// ParsePong reads a pong line the way readers before the dual read do; ok is
// false when the text is no pong. It reads a malformed pong as no pong, so a
// production read goes through PongReader, which refuses it by name; this is
// kept as the old reader a writer switch must keep working.
func ParsePong(text string) (nonce string, queue, working, width int, ok bool) {
	n, err := fmt.Sscanf(text, "pong %s queue=%d working=%d width=%d", &nonce, &queue, &working, &width)
	if err != nil || n < 1 {
		var only string
		if k, _ := fmt.Sscanf(text, "pong %s", &only); k == 1 {
			return only, 0, 0, 0, true // the numbers are optional
		}
		return "", 0, 0, 0, false
	}
	return nonce, queue, working, width, true
}

// PongHelp is the command a pong refusal points at: the writer's.
const PongHelp = "nova-friend pong -h"

// pongPrefix opens every legacy pong line.
const pongPrefix = "pong "

// Pong is one pong as read, whichever form it arrived in: the answering
// friend, the nonce it answers, its counts, and the time it was sent. A
// legacy line names neither the friend nor the time, so those come from the
// bus message around it; a table pong carries both.
type Pong struct {
	Friend                string
	Nonce                 string
	Queue, Working, Width int
	Sent                  time.Time // UTC, to the millisecond
}

// IsPong says m is meant as a pong: its subject is the pong subject (matched
// without case) or its body opens as a legacy pong line. A pong that is meant
// as one is read by PongReader and refused by name when damaged, never taken
// for an ordinary message.
func IsPong(m bus.Message) bool {
	return strings.EqualFold(strings.TrimSpace(m.Subject), PongSubject) || strings.HasPrefix(strings.TrimSpace(m.Body), pongPrefix)
}

// pongTable holds one pong table record in package friend. pkg/pongwire's
// internal test (pkg/pongwire/pong_test.go) imports pkg/friend for PongLine
// and ParsePong, so pkg/friend does not import pkg/pongwire directly; callers
// wire pkg/pongwire in through RegisterPongWire.
type pongTable struct {
	FriendName       [65]byte
	FriendNameLength int32
	Nonce            [33]byte
	NonceLength      int32
	Queue            uint32
	QueuePresent     bool
	Working          uint32
	WorkingPresent   bool
	Width            uint32
	WidthPresent     bool
	SentMs           int64
	SentMsPresent    bool
}

type pongBound struct {
	ArrayBound         int
	RangeMin, RangeMax int64
}

type pongWireHooks struct {
	measure  func(wire []byte) error
	loadWire func(v *pongTable, wire []byte) schemaread.Report
	loadJSON func(v *pongTable, text []byte) schemaread.Report
	saveWire func(v *pongTable) []byte
	saveJSON func(v *pongTable) []byte
}

// The pong's declared bounds, verified against the generated descriptors when
// RegisterPongWire wires pkg/pongwire so the reader and the definition cannot
// drift apart.
var (
	pongWireMu    sync.RWMutex
	pongFriendMax = 64
	pongNonceMax  = 32
	pongQueue     = pongBound{RangeMin: 0, RangeMax: 100000}
	pongWorking   = pongBound{RangeMin: 0, RangeMax: 10000}
	pongWidth     = pongBound{RangeMin: 0, RangeMax: 1024}
	pongHooks     = pongWireHooks{
		loadWire: func(*pongTable, []byte) schemaread.Report {
			return schemaread.Report{Reason: schemaread.ReasonUnknownForm}
		},
		loadJSON: func(*pongTable, []byte) schemaread.Report {
			return schemaread.Report{Reason: schemaread.ReasonUnknownForm}
		},
	}
)

// pongBuilderPtr is the generated PongBuilder pointer interface.
type pongBuilderPtr[T, B, A any] interface {
	*B
	Init(...A) bool
	Shutdown()
	GetRoot() *T
}

// pongReportPtr is the generated TableReport pointer interface.
type pongReportPtr[R any] interface {
	*R
	Err() error
}

// RegisterPongWire hooks the generated pkg/pongwire table functions into
// NewPongReader. pkg/pongwire's internal test (pkg/pongwire/pong_test.go)
// imports pkg/friend for PongLine and ParsePong, so pkg/friend does not import
// pkg/pongwire directly; callers in cmd/nova-friend and pkg/friend's test wire
// pkg/pongwire here without a test import cycle.
func RegisterPongWire[T, B, R, F, W, A any, PB pongBuilderPtr[T, B, A], PR pongReportPtr[R]](
	fields []F,
	reset func(*T),
	measureReason func([]byte) (int64, error),
	loadWire func(*B, []byte, *R) bool,
	loadJSON func(*B, []byte, *R) bool,
	measureWire func(*T, ...W) int64,
	saveWire func(*T, []byte, ...W) int64,
	measureJSON func(*T, ...A) int64,
	saveJSON func(*T, []byte, ...A) int64,
) {
	byName := map[string]pongBound{}
	fv := reflect.ValueOf(fields)
	for i := range fv.Len() {
		f := fv.Index(i)
		byName[f.FieldByName("Name").String()] = pongBound{
			ArrayBound: int(f.FieldByName("ArrayBound").Int()),
			RangeMin:   int64(f.FieldByName("RangeMin").Float()),
			RangeMax:   int64(f.FieldByName("RangeMax").Float()),
		}
	}
	mustField := func(name string) pongBound {
		b, ok := byName[name]
		if !ok {
			panic("pongwire.Pong declares no field " + name)
		}
		return b
	}
	tt := reflect.TypeFor[T]()
	idx := func(t reflect.Type, name string) int {
		f, ok := t.FieldByName(name)
		if !ok {
			panic(t.String() + " declares no field " + name)
		}
		return f.Index[0]
	}
	iFriend, iFriendLen := idx(tt, "FriendName"), idx(tt, "FriendNameLength")
	iNonce, iNonceLen := idx(tt, "Nonce"), idx(tt, "NonceLength")
	iQueue, iQueuePres := idx(tt, "Queue"), idx(tt, "QueuePresent")
	iWork, iWorkPres := idx(tt, "Working"), idx(tt, "WorkingPresent")
	iWidth, iWidthPres := idx(tt, "Width"), idx(tt, "WidthPresent")
	iSent, iSentPres := idx(tt, "SentMs"), idx(tt, "SentMsPresent")

	rt := reflect.TypeFor[R]()
	rVerdict := idx(rt, "Verdict")
	rUnknown := idx(rt, "Unknown")
	rKind := idx(rt, "KindMismatch")
	rClamped := idx(rt, "Clamped")
	rDup := idx(rt, "Duplicate")
	rMalformed := idx(rt, "Malformed")

	fromWire := func(dst *pongTable, src *T) {
		sv := reflect.ValueOf(src).Elem()
		reflect.Copy(reflect.ValueOf(&dst.FriendName).Elem(), sv.Field(iFriend))
		dst.FriendNameLength = int32(sv.Field(iFriendLen).Int())
		reflect.Copy(reflect.ValueOf(&dst.Nonce).Elem(), sv.Field(iNonce))
		dst.NonceLength = int32(sv.Field(iNonceLen).Int())
		dst.Queue = uint32(sv.Field(iQueue).Uint())
		dst.QueuePresent = sv.Field(iQueuePres).Bool()
		dst.Working = uint32(sv.Field(iWork).Uint())
		dst.WorkingPresent = sv.Field(iWorkPres).Bool()
		dst.Width = uint32(sv.Field(iWidth).Uint())
		dst.WidthPresent = sv.Field(iWidthPres).Bool()
		dst.SentMs = sv.Field(iSent).Int()
		dst.SentMsPresent = sv.Field(iSentPres).Bool()
	}
	toWire := func(dst *T, src *pongTable) {
		reset(dst)
		dv := reflect.ValueOf(dst).Elem()
		reflect.Copy(dv.Field(iFriend), reflect.ValueOf(src.FriendName))
		dv.Field(iFriendLen).SetInt(int64(src.FriendNameLength))
		reflect.Copy(dv.Field(iNonce), reflect.ValueOf(src.Nonce))
		dv.Field(iNonceLen).SetInt(int64(src.NonceLength))
		dv.Field(iQueue).SetUint(uint64(src.Queue))
		dv.Field(iQueuePres).SetBool(src.QueuePresent)
		dv.Field(iWork).SetUint(uint64(src.Working))
		dv.Field(iWorkPres).SetBool(src.WorkingPresent)
		dv.Field(iWidth).SetUint(uint64(src.Width))
		dv.Field(iWidthPres).SetBool(src.WidthPresent)
		dv.Field(iSent).SetInt(src.SentMs)
		dv.Field(iSentPres).SetBool(src.SentMsPresent)
	}
	viaBuilder := func(v *pongTable, load func(*B, *R) bool) schemaread.Report {
		var b B
		pb := PB(&b)
		if !pb.Init() {
			return schemaread.Report{Malformed: true}
		}
		defer pb.Shutdown()
		var rep R
		ok := load(&b, &rep)
		if root := pb.GetRoot(); root != nil {
			fromWire(v, root)
		}
		rv := reflect.ValueOf(&rep).Elem()
		out := schemaread.Report{
			Unknown:      int(rv.Field(rUnknown).Int()),
			KindMismatch: int(rv.Field(rKind).Int()),
			Clamped:      int(rv.Field(rClamped).Int()),
			Malformed:    rv.Field(rMalformed).Bool() || rv.Field(rDup).Int() > 0 || !ok,
		}
		// TableOpenRefused (1) and TableOpenDamaged (2) carry a named reason.
		if verdict := rv.Field(rVerdict).Uint(); verdict == 1 || verdict == 2 {
			if err := PR(&rep).Err(); err != nil {
				out.Reason = err.Error()
			}
		}
		return out
	}

	pongWireMu.Lock()
	defer pongWireMu.Unlock()
	pongFriendMax = mustField("friend_name").ArrayBound
	pongNonceMax = mustField("nonce").ArrayBound
	pongQueue = mustField("queue")
	pongWorking = mustField("working")
	pongWidth = mustField("width")
	pongHooks = pongWireHooks{
		measure: func(wire []byte) error {
			_, err := measureReason(wire)
			return err
		},
		loadWire: func(v *pongTable, wire []byte) schemaread.Report {
			return viaBuilder(v, func(b *B, rep *R) bool { return loadWire(b, wire, rep) })
		},
		loadJSON: func(v *pongTable, text []byte) schemaread.Report {
			return viaBuilder(v, func(b *B, rep *R) bool { return loadJSON(b, text, rep) })
		},
		saveWire: func(v *pongTable) []byte {
			var w T
			toWire(&w, v)
			n := measureWire(&w)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if saveWire(&w, buf) != n {
				return nil
			}
			return buf
		},
		saveJSON: func(v *pongTable) []byte {
			var w T
			toWire(&w, v)
			n := measureJSON(&w)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if saveJSON(&w, buf) != n {
				return nil
			}
			return buf
		},
	}
}

// PongReader is the friend machine's pong reader: the legacy line PongLine
// writes, or the table pong (pkg/pongwire, schemas/pong) as the binary wire,
// padded standard base64 of the wire, or the JSON text, each read strictly
// through pkg/schemaread. A malformed, clamped or incomplete pong of either
// form is refused by name (PONG REFUSED: <reason>; run: nova-friend pong -h)
// and is never read as no pong. Each read is counted once under its form,
// the evidence a cutover reads before writers switch. It is safe for
// concurrent use.
type PongReader struct {
	mu  sync.Mutex
	env pongEnvelope // the message around the read in progress, mu held
	r   *schemaread.Reader[pongTable]
}

// pongEnvelope is what the bus message around a legacy line carries.
type pongEnvelope struct {
	from   string
	sentMs int64
}

// NewPongReader returns a pong reader with its counters at zero.
func NewPongReader() *PongReader {
	pongWireMu.RLock()
	hooks := pongHooks
	pongWireMu.RUnlock()
	p := &PongReader{}
	p.r = schemaread.NewReader(&schemaread.Codec[pongTable]{
		Name:        "PONG",
		Help:        PongHelp,
		Reset:       func(v *pongTable) { *v = pongTable{} },
		Measure:     hooks.measure,
		LoadWire:    hooks.loadWire,
		LoadJSON:    hooks.loadJSON,
		Legacy:      func(in []byte) bool { return bytes.HasPrefix(in, []byte(pongPrefix)) },
		LoadLegacy:  func(v *pongTable, in []byte) schemaread.Report { return pongLoadLegacy(v, string(in), p.env) },
		Constraints: pongConstraints,
		SaveWire:    hooks.saveWire,
		SaveJSON:    hooks.saveJSON,
	})
	return p
}

// Counts is the reader's per-form read counters and refusal tallies.
func (p *PongReader) Counts() *schemaread.Counts { return p.r.Counts() }

// CountsLine is the counters as one line, `name=count` in name order: the
// forms (base64, binary, json, legacy), unknown_fields, and refused_<reason>
// for each reason seen.
func (p *PongReader) CountsLine() string {
	snap := p.r.Counts().Snapshot()
	names := make([]string, 0, len(snap))
	for k := range snap {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, k := range names {
		parts[i] = k + "=" + strconv.FormatUint(snap[k], 10)
	}
	return strings.Join(parts, " ")
}

// Message reads m's body as a pong, the friend and the send time from m.
func (p *PongReader) Message(m bus.Message) (Pong, schemaread.Form, error) {
	return p.Read(m.From, m.At, []byte(strings.TrimSpace(m.Body)))
}

// Read reads body as a pong in whichever form it arrived. from and at are
// the bus message's sender and time, which a legacy line takes as its friend
// and send time. A refusal is a *schemaread.Refusal and the pong is the zero
// value.
func (p *PongReader) Read(from string, at time.Time, body []byte) (Pong, schemaread.Form, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.env = pongEnvelope{from: from}
	if !at.IsZero() {
		p.env.sentMs = at.UnixMilli()
	}
	defer func() { p.env = pongEnvelope{} }()
	v, res, err := p.r.Read(body)
	if err != nil {
		return Pong{}, res.Form, err
	}
	return pongOf(&v), res.Form, nil
}

// write checks pg and writes it as a table pong in form. Writers stay legacy
// (PongLine); this is the switch's writer, used by the tests that prove the
// two forms read alike.
func (p *PongReader) write(pg Pong, form schemaread.Form) ([]byte, error) {
	var v pongTable
	if len(pg.Friend) > len(v.FriendName)-1 || len(pg.Nonce) > len(v.Nonce)-1 {
		return nil, &schemaread.Refusal{Name: "PONG", Reason: schemaread.ReasonClamped, Help: PongHelp}
	}
	v.FriendNameLength = int32(copy(v.FriendName[:], pg.Friend))
	v.NonceLength = int32(copy(v.Nonce[:], pg.Nonce))
	for _, n := range []struct {
		dst     *uint32
		present *bool
		v       int
		field   string
	}{
		{&v.Queue, &v.QueuePresent, pg.Queue, "queue"},
		{&v.Working, &v.WorkingPresent, pg.Working, "working"},
		{&v.Width, &v.WidthPresent, pg.Width, "width"},
	} {
		if n.v < 0 || n.v > math.MaxUint32 {
			return nil, &schemaread.Refusal{Name: "PONG", Reason: schemaread.ReasonClamped, Field: n.field, Help: PongHelp}
		}
		*n.dst, *n.present = uint32(n.v), true
	}
	if !pg.Sent.IsZero() {
		v.SentMs, v.SentMsPresent = pg.Sent.UnixMilli(), true
	}
	return p.r.Write(&v, form)
}

func pongOf(v *pongTable) Pong {
	return Pong{
		Friend:  string(v.FriendName[:v.FriendNameLength]),
		Nonce:   string(v.Nonce[:v.NonceLength]),
		Queue:   int(v.Queue),
		Working: int(v.Working),
		Width:   int(v.Width),
		Sent:    time.UnixMilli(v.SentMs).UTC(),
	}
}

// pongLoadLegacy reads the legacy line strictly: `pong <nonce>` and then
// queue=, working= and width= in that order, each a plain decimal, single
// spaces, nothing after. A count it does not carry is absent and refused by
// the constraints; any other shape is malformed; a count outside 32 bits is a
// clamp. The friend and the send time come from the message around the line.
func pongLoadLegacy(v *pongTable, text string, env pongEnvelope) schemaread.Report {
	words := strings.Split(text, " ")
	if len(words) < 2 || len(words) > 5 || words[0] != "pong" || words[1] == "" {
		return schemaread.Report{Malformed: true}
	}
	nonce := words[1]
	if strings.ContainsFunc(nonce, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return schemaread.Report{Malformed: true}
	}
	if len(nonce) > pongNonceMax || len(env.from) > pongFriendMax {
		return schemaread.Report{Clamped: 1}
	}
	v.NonceLength = int32(copy(v.Nonce[:], nonce))
	v.FriendNameLength = int32(copy(v.FriendName[:], env.from))
	counts := []struct {
		key     string
		dst     *uint32
		present *bool
	}{
		{"queue=", &v.Queue, &v.QueuePresent},
		{"working=", &v.Working, &v.WorkingPresent},
		{"width=", &v.Width, &v.WidthPresent},
	}
	for i, w := range words[2:] {
		digits, ok := strings.CutPrefix(w, counts[i].key)
		if !ok {
			return schemaread.Report{Malformed: true}
		}
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != digits {
			return schemaread.Report{Malformed: true}
		}
		if n < 0 || n > math.MaxUint32 {
			return schemaread.Report{Clamped: 1}
		}
		*counts[i].dst, *counts[i].present = uint32(n), true
	}
	if env.sentMs != 0 {
		v.SentMs, v.SentMsPresent = env.sentMs, true
	}
	return schemaread.Report{}
}

// pongConstraints are the pong's rules after a load and before a write: a
// variable table reads an absent field as zero, so every field a liveness
// count reads is required (a zero queue is an answer, an absent queue is a
// malformed pong), and every bound the schema declares is refused past.
func pongConstraints(v *pongTable, c *schemaread.Checker) {
	c.Required("friend_name", v.FriendNameLength > 0)
	c.Text("friend_name", pongText(v.FriendName[:], v.FriendNameLength), pongFriendMax)
	c.Required("nonce", v.NonceLength > 0)
	c.Text("nonce", pongText(v.Nonce[:], v.NonceLength), pongNonceMax)
	c.Required("queue", v.QueuePresent)
	c.Bound("queue", int64(v.Queue), pongQueue.RangeMin, pongQueue.RangeMax)
	c.Required("working", v.WorkingPresent)
	c.Bound("working", int64(v.Working), pongWorking.RangeMin, pongWorking.RangeMax)
	c.Required("width", v.WidthPresent)
	c.Bound("width", int64(v.Width), pongWidth.RangeMin, pongWidth.RangeMax)
	c.Required("sent_ms", v.SentMsPresent && v.SentMs > 0)
}

// pongText is a counted string field's text; a length past the buffer reads
// as an over-long text so the bound check sees it.
func pongText(buf []byte, n int32) string {
	if n < 0 {
		return ""
	}
	if int(n) > len(buf) {
		return string(buf) + strings.Repeat("?", int(n)-len(buf))
	}
	return string(buf[:n])
}
