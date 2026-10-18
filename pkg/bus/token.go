package bus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// A send's token (SPEC-BUS.md, a-lost-send-response-is-safe-to-retry.w1):
// the caller's word for one logical send, the same on every retry of it. The
// send writes a record of it at SentOf(from, token) in the transaction that
// writes the message, and a send that finds the record writes nothing and
// answers the message the record names, so a write that committed and whose
// response was lost is retried without a second message. The record holds the
// fingerprint of the send's arguments: the same token with other arguments is
// refused. The model is tla/BusSendOnce.tla.

// SentPrefix is the record of a send's token: one string key per sender and
// token, its value the record's JSON, expiring at the token's cleanup.
const SentPrefix = "bus2:sent:"

// SentOf is the key of the record of from's token.
func SentOf(from, token string) string { return SentPrefix + from + ":" + token }

// MaxToken is the bytes of a token at most.
const MaxToken = 128

// The token's settings, when the Bus names none (Bus.TokenLife, TokenCleanup).
const (
	// DefaultTokenLife is how long a retry under a token answers the
	// original: a day, longer than any sender's retry loop runs.
	DefaultTokenLife = 24 * time.Hour
	// DefaultTokenCleanup is when the store drops the record (its key
	// expires): a week. Between the life and the cleanup a retry is refused,
	// naming the message that went, never sent again.
	DefaultTokenCleanup = 7 * 24 * time.Hour
)

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// CheckToken says why s is no token, "" when it is one; the empty token is
// a send with none.
func CheckToken(s string) string {
	switch {
	case s == "":
		return ""
	case len(s) > MaxToken:
		return fmt.Sprintf("the token is %d bytes, at most %d", len(s), MaxToken)
	case !tokenRe.MatchString(s):
		return fmt.Sprintf("the token %q is not letters, digits, '.', '_', ':' and '-'", s)
	}
	return ""
}

func (b *Bus) tokenLife() time.Duration {
	if b.TokenLife > 0 {
		return b.TokenLife
	}
	return DefaultTokenLife
}

// tokenCleanup is never before the life ends: a record is kept as long as a
// retry under it is honoured.
func (b *Bus) tokenCleanup() time.Duration {
	c := b.TokenCleanup
	if c <= 0 {
		c = DefaultTokenCleanup
	}
	return max(c, b.tokenLife())
}

// sentRecord is what the store keeps of a send under a token.
type sentRecord struct {
	Fingerprint string    `json:"fingerprint"`
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
}

// The send-token record's strict read (the owner's rules S2 and S4, readers
// first, UNION.md C13): replay reads the record a send under a token left
// through pkg/schemaread and the generated table answerwire.SentRecord
// (schemas/answer/token.schema), in each form the record may arrive in: the
// table's binary wire led by its form byte, the padded standard base64 of
// that wire, the schema JSON text form, and the legacy JSON sendOnce writes
// today. A damaged or out-of-bound record is refused as `SENT_RECORD REFUSED:
// <reason>; run: nova-bus send -h`, never read as no record, and every read is
// counted under the form it arrived in: the cutover's evidence. Every writer
// still writes the legacy form.

// sentRecordName is the prefix of every refusal of a send-token record.
const sentRecordName = "SENT_RECORD"

// sentRecordHelp is the command a send-token record refusal points at.
const sentRecordHelp = "nova-bus send -h"

// sentRecordForm is the form byte the record's wire leads with: the table
// wire's first form.
const sentRecordForm byte = 1

// sentRecordLegacy opens the legacy form: json.Marshal of sentRecord writes
// its keys in declaration order with no space, and the schema JSON text form
// is indented, so the two are told apart by their first bytes.
var sentRecordLegacy = []byte(`{"fingerprint":`)

// sentRecords is the reader replay reads every send-token record with; its
// counts are the per-form reads of this record.
var sentRecords = schemaread.NewReader(sentRecordCodec())

// sentRecordBound is a string field's declared bound, read from the generated
// descriptors, never typed twice.
func sentRecordBound(name string) int {
	for _, f := range answerwire.SentRecordTableType().Fields {
		if f.Name == name {
			return int(f.ArrayBound)
		}
	}
	panic("the send-token record has no field " + name)
}

// sentRecordText is a string field of a loaded row, its used length kept
// inside its buffer.
func sentRecordText(b []byte, n int32) string {
	return string(b[:min(max(int(n), 0), len(b))])
}

// sentRecordOf is the bus's record of a loaded row. A zero stamp is no stamp:
// it reads as the zero time, which the constraints refuse as absent, never as
// 1970.
func sentRecordOf(v *answerwire.SentRecord) sentRecord {
	rec := sentRecord{Fingerprint: sentRecordText(v.Fingerprint[:], v.FingerprintLength), ID: sentRecordText(v.Id[:], v.IdLength)}
	if v.At != 0 {
		rec.At = v.AtTime()
	}
	return rec
}

func sentRecordReport(r *answerwire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		// a key given twice in the JSON text is ambiguous: refused, never last-wins
		Malformed: r.Malformed || r.Duplicate > 0,
		Reason:    r.Reason,
	}
}

func sentRecordLoadWire(rec *sentRecord, wire []byte) schemaread.Report {
	n := answerwire.SentRecordLoadMeasure(wire)
	if n < 0 {
		return schemaread.Report{Malformed: true}
	}
	var r answerwire.TableReport
	v := answerwire.SentRecordLoad(alignedRegion(n), wire, &r)
	if v == nil {
		return schemaread.Report{Malformed: true}
	}
	*rec = sentRecordOf(v)
	return sentRecordReport(&r)
}

func sentRecordLoadJSON(rec *sentRecord, text []byte) schemaread.Report {
	var b answerwire.SentRecordBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var r answerwire.TableReport
	if !answerwire.SentRecordFromJson(&b, text, &r) || !b.Lock() {
		return schemaread.Report{Malformed: true, Reason: r.Reason}
	}
	*rec = sentRecordOf(b.AsConst())
	return sentRecordReport(&r)
}

// sentRecordLoadLegacy reads the JSON sendOnce writes today: one value and
// nothing after it. A stamp the table's timestamp cannot hold (outside
// Time.UnixNano) is clamped, as the table would clamp it. An unknown key is
// passed over, as encoding/json passes it over.
func sentRecordLoadLegacy(rec *sentRecord, text []byte) schemaread.Report {
	dec := json.NewDecoder(bytes.NewReader(text))
	if err := dec.Decode(rec); err != nil {
		return schemaread.Report{Malformed: true}
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return schemaread.Report{Malformed: true}
	}
	var row answerwire.SentRecord
	if !rec.At.IsZero() && row.SetAtTime(rec.At) != nil {
		return schemaread.Report{Clamped: 1}
	}
	return schemaread.Report{}
}

// sentRecordConstraints are the record's bounds: every field a written record
// always has is required (a variable table reads an absent field as zero, and
// a record with no id or stamp must never answer a retry), the strings are
// held to the table's bounds, and the fingerprint is the SHA-256 hex digest
// fingerprint writes.
func sentRecordConstraints(rec *sentRecord, c *schemaread.Checker) {
	c.Required("fingerprint", rec.Fingerprint != "")
	c.Required("id", rec.ID != "")
	c.Required("at", !rec.At.IsZero())
	c.Text("fingerprint", rec.Fingerprint, sentRecordBound("fingerprint"))
	c.Text("id", rec.ID, sentRecordBound("id"))
	if rec.Fingerprint != "" && !isDigest(rec.Fingerprint) {
		c.Text("fingerprint", "\x00", sentRecordBound("fingerprint"))
	}
}

// isDigest says s is a SHA-256 digest as hex.EncodeToString spells it.
func isDigest(s string) bool {
	if len(s) != 2*sha256.Size {
		return false
	}
	for i := range len(s) {
		if b := s[i]; (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

// sentRecordCodec is the generated table's read functions as pkg/schemaread
// takes them. It carries no writer: every writer writes the legacy form until
// the writer switch.
func sentRecordCodec() *schemaread.Codec[sentRecord] {
	return &schemaread.Codec[sentRecord]{
		Name:  sentRecordName,
		Help:  sentRecordHelp,
		Reset: func(rec *sentRecord) { *rec = sentRecord{} },
		Forms: []byte{sentRecordForm},
		Measure: func(wire []byte) error {
			_, err := answerwire.SentRecordLoadMeasureReason(wire)
			return err
		},
		LoadWire:    sentRecordLoadWire,
		LoadJSON:    sentRecordLoadJSON,
		Legacy:      func(in []byte) bool { return bytes.HasPrefix(in, sentRecordLegacy) },
		LoadLegacy:  sentRecordLoadLegacy,
		Constraints: sentRecordConstraints,
	}
}

// readSentRecord is the record prior holds, read strictly by r. A refusal is
// a *schemaread.Refusal naming the record and the reason, never a zero record.
func readSentRecord(r *schemaread.Reader[sentRecord], prior string) (sentRecord, error) {
	rec, _, err := r.Read([]byte(prior))
	return rec, err
}

// fingerprint is the send's arguments as check normalised them (recipients
// sorted and once each, the kind spelled out), so a retry that names them in
// another order is the same send. Each field is length-prefixed: no two
// argument lists have one encoding.
func fingerprint(m Message) string {
	h := sha256.New()
	for _, f := range []string{m.From, strings.Join(m.To, ","), strings.Join(m.CC, ","), m.Subject, m.Re, m.KindName(), m.Body} {
		h.Write([]byte(strconv.Itoa(len(f)) + ":" + f + ";"))
	}
	if m.Wait != 0 { // an ask's wait; left out otherwise, so a token's record from before asks still matches
		h.Write([]byte("wait:" + m.Wait.String() + ";"))
	}
	if m.Card != "" {
		h.Write([]byte("card:" + strconv.Itoa(len(m.Card)) + ":" + m.Card + ";"))
	}
	if m.Head != "" {
		h.Write([]byte("head:" + strconv.Itoa(len(m.Head)) + ":" + m.Head + ";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sendOnce writes m under its token as the entry fields, or answers the
// message an earlier send under it wrote. now is the store's time the send was
// checked at.
func (b *Bus) sendOnce(ctx context.Context, m Message, now time.Time, streams []string, fields map[string]string, marks []Mark) (Message, error) {
	fp := fingerprint(m)
	raw, err := json.Marshal(sentRecord{Fingerprint: fp, ID: m.ID, At: m.At})
	if err != nil {
		return Message{}, err
	}
	key := m.Once
	if key == "" {
		key = SentOf(m.From, m.Token)
	}
	prior, found, err := b.Store.AddOnce(ctx, key, string(raw), b.tokenCleanup(), streams, fields, marks...)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return m, nil
	}
	return b.replay(m, fp, prior, now)
}

// replay is the answer to a send whose token has a record: the message it
// names when the arguments are the same and the token is inside its life,
// else a refusal saying which message went.
func (b *Bus) replay(m Message, fp, prior string, now time.Time) (Message, error) {
	rec, err := readSentRecord(sentRecords, prior)
	if err != nil { // the record is refused by name, never read as no record
		return Message{}, &Refusal{[]string{err.Error(), fmt.Sprintf("the token %q has a record that is not one (%s holds %q); a new message wants a new token", m.Token, SentOf(m.From, m.Token), prior)}}
	}
	at := rec.At.UTC().Format(time.RFC3339)
	if rec.Fingerprint != fp {
		return Message{}, &Refusal{[]string{fmt.Sprintf("the token %q already sent %s at %s with other arguments; a retry repeats them exactly, and a new message wants a new token", m.Token, rec.ID, at)}}
	}
	if now.Sub(rec.At) >= b.tokenLife() { // honoured while now < at + life: the record outlives it (tla/BusSendOnce.tla Step)
		return Message{}, &Refusal{[]string{fmt.Sprintf("the token %q sent %s at %s, past its life of %s: the message went, and is not sent again; a new message wants a new token", m.Token, rec.ID, at, b.tokenLife())}}
	}
	m.ID, m.At = rec.ID, rec.At.UTC()
	if m.Kind == KindAsk {
		m.Due = m.At.Add(m.Wait)
	}
	return m, nil
}
