package friend

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/pongwire"
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
	r   *schemaread.Reader[pongwire.Pong]
}

// pongEnvelope is what the bus message around a legacy line carries.
type pongEnvelope struct {
	from   string
	sentMs int64
}

// The pong's declared bounds, read from the generated descriptors so the
// reader and the definition cannot drift apart.
var (
	pongFriendMax = int(pongField("friend_name").ArrayBound)
	pongNonceMax  = int(pongField("nonce").ArrayBound)
	pongQueue     = pongField("queue")
	pongWorking   = pongField("working")
	pongWidth     = pongField("width")
)

func pongField(name string) pongwire.TableFieldInfo {
	for _, f := range pongwire.PongTableFields {
		if f.Name == name {
			return f
		}
	}
	panic("pongwire.Pong declares no field " + name)
}

// NewPongReader returns a pong reader with its counters at zero.
func NewPongReader() *PongReader {
	p := &PongReader{}
	p.r = schemaread.NewReader(&schemaread.Codec[pongwire.Pong]{
		Name:  "PONG",
		Help:  PongHelp,
		Reset: pongwire.PongReset,
		Measure: func(wire []byte) error {
			_, err := pongwire.PongLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *pongwire.Pong, wire []byte) schemaread.Report {
			return pongViaBuilder(v, func(b *pongwire.PongBuilder, rep *pongwire.TableReport) bool {
				return pongwire.PongLoadBuilder(b, wire, rep)
			})
		},
		LoadJSON: func(v *pongwire.Pong, text []byte) schemaread.Report {
			return pongViaBuilder(v, func(b *pongwire.PongBuilder, rep *pongwire.TableReport) bool {
				return pongwire.PongFromJson(b, text, rep)
			})
		},
		Legacy:      func(in []byte) bool { return bytes.HasPrefix(in, []byte(pongPrefix)) },
		LoadLegacy:  func(v *pongwire.Pong, in []byte) schemaread.Report { return pongLoadLegacy(v, string(in), p.env) },
		Constraints: pongConstraints,
		SaveWire:    pongSaveWire,
		SaveJSON:    pongSaveJSON,
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
	var v pongwire.Pong
	pongwire.PongReset(&v)
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

func pongOf(v *pongwire.Pong) Pong {
	return Pong{
		Friend:  string(v.FriendName[:v.FriendNameLength]),
		Nonce:   string(v.Nonce[:v.NonceLength]),
		Queue:   int(v.Queue),
		Working: int(v.Working),
		Width:   int(v.Width),
		Sent:    time.UnixMilli(v.SentMs).UTC(),
	}
}

// pongViaBuilder runs a generated load into a builder and reduces its report
// to what the strict read decides on. A key given twice in the JSON text is
// ambiguous, so it is refused as malformed, never read last-wins.
func pongViaBuilder(v *pongwire.Pong, load func(*pongwire.PongBuilder, *pongwire.TableReport) bool) schemaread.Report {
	var b pongwire.PongBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep pongwire.TableReport
	ok := load(&b, &rep)
	if root := b.GetRoot(); root != nil {
		*v = *root
	}
	out := schemaread.Report{
		Unknown:      int(rep.Unknown),
		KindMismatch: int(rep.KindMismatch),
		Clamped:      int(rep.Clamped),
		Malformed:    rep.Malformed || rep.Duplicate > 0 || !ok,
	}
	if rep.Verdict == pongwire.TableOpenRefused || rep.Verdict == pongwire.TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// pongLoadLegacy reads the legacy line strictly: `pong <nonce>` and then
// queue=, working= and width= in that order, each a plain decimal, single
// spaces, nothing after. A count it does not carry is absent and refused by
// the constraints; any other shape is malformed; a count outside 32 bits is a
// clamp. The friend and the send time come from the message around the line.
func pongLoadLegacy(v *pongwire.Pong, text string, env pongEnvelope) schemaread.Report {
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
func pongConstraints(v *pongwire.Pong, c *schemaread.Checker) {
	c.Required("friend_name", v.FriendNameLength > 0)
	c.Text("friend_name", pongText(v.FriendName[:], v.FriendNameLength), pongFriendMax)
	c.Required("nonce", v.NonceLength > 0)
	c.Text("nonce", pongText(v.Nonce[:], v.NonceLength), pongNonceMax)
	c.Required("queue", v.QueuePresent)
	c.Bound("queue", int64(v.Queue), int64(pongQueue.RangeMin), int64(pongQueue.RangeMax))
	c.Required("working", v.WorkingPresent)
	c.Bound("working", int64(v.Working), int64(pongWorking.RangeMin), int64(pongWorking.RangeMax))
	c.Required("width", v.WidthPresent)
	c.Bound("width", int64(v.Width), int64(pongWidth.RangeMin), int64(pongWidth.RangeMax))
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

// pongSaveWire is the generated Measure and Save; the reader checks every
// bound before it calls this, because the generated writer checks none.
func pongSaveWire(v *pongwire.Pong) []byte {
	n := pongwire.PongMeasure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if pongwire.PongSave(v, buf) != n {
		return nil
	}
	return buf
}

func pongSaveJSON(v *pongwire.Pong) []byte {
	n := pongwire.PongToJsonMeasure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if pongwire.PongToJson(v, buf) != n {
		return nil
	}
	return buf
}
