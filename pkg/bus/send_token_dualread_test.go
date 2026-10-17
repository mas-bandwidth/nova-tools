package bus

// TestSendTokenRecordDualRead is the reader half of the send-token record's
// table adoption (v1.5 aspect 11; the owner's rules S2 and S4, readers first):
// the record a send under a token leaves (sentRecord, at SentOf(from, token))
// is read by replay alike from the table's binary wire, the padded standard
// base64 of that wire, the schema JSON text form and the legacy JSON sendOnce
// writes today; every damaged value of each form is refused by name
// (`SENT_RECORD REFUSED: <reason>; run: nova-bus send -h`) and never read as
// absent, empty or zero; a value past its bound is refused as clamped; and
// each read is counted under its record and its form. Every writer still
// writes the legacy form.
//
// Unit tier: no clock, no socket, no program. The store is the package's
// in-process fake (fake_test.go, the twin of pkg/bus/bustest, which this
// package's tests cannot import), its clock starting at the package's start.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// sentRefusal is the shape of every refusal of the record: the reason, and
// the field a constraint names in parentheses, or the field the generated
// JSON reader names after a colon (timestamp_not_rfc3339: at).
var sentRefusal = regexp.MustCompile(`^SENT_RECORD REFUSED: [a-z0-9_]+(: [a-z_]+)?( \([a-z_]+\))?; run: nova-bus send -h$`)

// sentForms is one record in every form a reader meets.
type sentForms struct {
	wire, b64, json, legacy []byte
}

// rowOf is the generated table's value of rec; a string longer than its
// buffer keeps the buffer and claims the longer length, as a damaged writer
// would.
func rowOf(t *testing.T, rec sentRecord) answerwire.SentRecord {
	t.Helper()
	var v answerwire.SentRecord
	answerwire.SentRecordReset(&v)
	v.FingerprintLength = int32(copy(v.Fingerprint[:], rec.Fingerprint))
	v.IdLength = int32(copy(v.Id[:], rec.ID))
	if len(rec.ID) > len(v.Id) {
		v.IdLength = int32(len(v.Id))
	}
	if !rec.At.IsZero() {
		require.NoError(t, v.SetAtTime(rec.At))
	}
	return v
}

func wireOf(t *testing.T, v *answerwire.SentRecord) []byte {
	t.Helper()
	n := answerwire.SentRecordMeasure(v)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.SentRecordSave(v, buf))
	return buf
}

func jsonOf(t *testing.T, v *answerwire.SentRecord) []byte {
	t.Helper()
	n := answerwire.SentRecordToJsonMeasure(v)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.SentRecordToJson(v, buf))
	return buf
}

func formsOf(t *testing.T, rec sentRecord) sentForms {
	t.Helper()
	v := rowOf(t, rec)
	wire := wireOf(t, &v)
	legacy, err := json.Marshal(rec) // the writer's form, as sendOnce writes it
	require.NoError(t, err)
	return sentForms{wire: wire, b64: []byte(base64.StdEncoding.EncodeToString(wire)), json: jsonOf(t, &v), legacy: legacy}
}

func (s sentForms) each() map[schemaread.Form][]byte {
	return map[schemaread.Form][]byte{schemaread.FormBinary: s.wire, schemaread.FormBase64: s.b64, schemaread.FormJSON: s.json, schemaread.FormLegacy: s.legacy}
}

// refusedAs says err is the record's named refusal, and answers its reason.
func sentRefused(t *testing.T, err error, msg string) string {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "%s: a *schemaread.Refusal, got %v", msg, err)
	assert.Equal(t, sentRecordName, ref.Name, msg)
	assert.Regexp(t, sentRefusal, err.Error(), msg)
	return ref.Reason
}

// readable says a read that was not refused holds a whole record: never
// absent, empty or zero.
func readable(t *testing.T, rec sentRecord, msg string) {
	t.Helper()
	assert.Len(t, rec.Fingerprint, 64, msg)
	assert.NotEmpty(t, rec.ID, msg)
	assert.False(t, rec.At.IsZero(), msg)
}

// setRecord replaces the record at key with value, keeping its expiry: what
// a store holding another form of the record answers the next send.
func setRecord(f *Fake, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.records[key]
	r.value = value
	f.records[key] = r
}

func TestSendTokenRecordDualRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// the record the writer writes today, from a real send under a token
	b, f := rig(t, "ada", "bob", "cy")
	first, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	key := SentOf("ada", "t-1")
	legacy, ok := f.record(key)
	require.True(t, ok, "the send wrote its token's record")
	var want sentRecord
	require.NoError(t, json.Unmarshal([]byte(legacy), &want))
	require.Equal(t, first.ID, want.ID)
	forms := formsOf(t, want)
	assert.Equal(t, legacy, string(forms.legacy), "the legacy fixture is what sendOnce writes")
	assert.Equal(t, sentRecordForm, forms.wire[0], "the wire leads with its form byte")

	t.Run("each form reads to one value", func(t *testing.T) {
		r := schemaread.NewReader(sentRecordCodec())
		for form, in := range forms.each() {
			got, res, err := r.Read(in)
			require.NoError(t, err, form.String())
			assert.Equal(t, form, res.Form, "detected as %s", form)
			assert.Equal(t, want.Fingerprint, got.Fingerprint, form.String())
			assert.Equal(t, want.ID, got.ID, form.String())
			assert.True(t, want.At.Equal(got.At), "%s: at %v, want %v", form, got.At, want.At)
		}
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy} {
			assert.Equal(t, uint64(1), r.Counts().Reads(form), "one read counted under %s", form)
		}
	})

	t.Run("replay answers the original from each form", func(t *testing.T) {
		for form, in := range forms.each() {
			setRecord(f, key, string(in))
			got, err := b.Send(ctx, tokened("t-1"))
			require.NoError(t, err, form.String())
			assert.Equal(t, first.ID, got.ID, "%s: the retry answers the original id", form)
			assert.Equal(t, first.At, got.At, "%s: and the original at", form)
			one(t, f)
		}
		// the package's reader counted replay's reads under each form
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy} {
			assert.Positive(t, sentRecords.Counts().Reads(form), "replay's reads counted under %s", form)
		}
	})

	t.Run("damage of each form is refused by name, never read as no record", func(t *testing.T) {
		r := schemaread.NewReader(sentRecordCodec())
		var reads [5]uint64
		refused := 0
		for form, in := range forms.each() {
			// every truncation (a text form's trailing newline is no damage)
			for n := 1; n < len(bytes.TrimRight(in, " \n")); n++ {
				_, res, err := r.Read(in[:n])
				reads[res.Form]++
				sentRefused(t, err, form.String()+" truncated")
				refused++
			}
			// every byte flipped, one bit at a time at both ends of the byte
			for i := range in {
				for _, mask := range []byte{0x01, 0x80} {
					bad := append([]byte(nil), in...)
					bad[i] ^= mask
					got, res, err := r.Read(bad)
					reads[res.Form]++
					if err != nil {
						sentRefused(t, err, form.String()+" flipped")
						refused++
						continue
					}
					// a flip that leaves a well-formed record (a letter of a
					// text form's string turned into another) reads whole
					readable(t, got, form.String()+" flipped")
				}
			}
		}
		_, _, err := r.Read(nil)
		assert.Equal(t, schemaread.ReasonUnknownForm, sentRefused(t, err, "nothing"))
		assert.Positive(t, refused)
		for f := schemaread.FormBinary; f <= schemaread.FormLegacy; f++ {
			assert.Equal(t, reads[f], r.Counts().Reads(f), "every read counted under %s", f)
		}
	})

	t.Run("a wire led by another form byte is refused", func(t *testing.T) {
		r := schemaread.NewReader(sentRecordCodec())
		for _, lead := range []byte{0, 2, 3, 0xff} {
			bad := append([]byte(nil), forms.wire...)
			bad[0] = lead
			_, _, err := r.Read(bad)
			sentRefused(t, err, "form byte")
			_, _, err = r.Read([]byte(base64.StdEncoding.EncodeToString(bad)))
			sentRefused(t, err, "form byte in base64")
		}
	})

	t.Run("a value past its bound is refused as clamped", func(t *testing.T) {
		r := schemaread.NewReader(sentRecordCodec())
		long := want
		long.ID = strings.Repeat("i", sentRecordBound("id")+1)
		// the generated writer refuses a string past its bound, so the wire of
		// one is a wire at the bound with its length and its bytes patched one on
		atBound := want
		atBound.ID = long.ID[1:]
		v := rowOf(t, atBound)
		good := wireOf(t, &v)
		field := append([]byte{byte(sentRecordBound("id"))}, atBound.ID...)
		require.Equal(t, 1, bytes.Count(good, field), "the id's length and bytes, once in the wire")
		wire := bytes.Replace(good, field, append([]byte{byte(len(long.ID))}, long.ID...), 1)
		legacyLong, err := json.Marshal(long)
		require.NoError(t, err)
		jsonLong := strings.Replace(string(forms.json), `"`+want.ID+`"`, `"`+long.ID+`"`, 1)
		for name, in := range map[string][]byte{
			"binary": wire, "base64": []byte(base64.StdEncoding.EncodeToString(wire)),
			"json": []byte(jsonLong), "legacy": legacyLong,
		} {
			_, _, err := r.Read(in)
			assert.Equal(t, schemaread.ReasonClamped, sentRefused(t, err, name), name)
		}
		// a stamp the table's timestamp cannot hold
		far := want
		far.At = time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)
		raw, err := json.Marshal(far)
		require.NoError(t, err)
		_, _, err = r.Read(raw)
		assert.Equal(t, schemaread.ReasonClamped, sentRefused(t, err, "an at past 2262"))
	})

	t.Run("a record missing a field is refused as absent", func(t *testing.T) {
		r := schemaread.NewReader(sentRecordCodec())
		for name, rec := range map[string]sentRecord{
			"id":          {Fingerprint: want.Fingerprint, At: want.At},
			"fingerprint": {ID: want.ID, At: want.At},
			"at":          {Fingerprint: want.Fingerprint, ID: want.ID},
		} {
			for form, in := range formsOf(t, rec).each() {
				_, _, err := r.Read(in)
				var ref *schemaread.Refusal
				require.True(t, errors.As(err, &ref), "%s without %s", form, name)
				assert.Equal(t, schemaread.ReasonAbsentRequired, ref.Reason, "%s without %s", form, name)
				assert.Equal(t, name, ref.Field, form.String())
			}
		}
		// a fingerprint that is no digest
		bad := want
		bad.Fingerprint = strings.Repeat("Z", 64)
		_, _, err := r.Read(formsOf(t, bad).legacy)
		assert.Equal(t, schemaread.ReasonMalformed, sentRefused(t, err, "no digest"))
		// a legacy value followed by another
		_, _, err = r.Read(append(append([]byte(nil), forms.legacy...), forms.legacy...))
		assert.Equal(t, schemaread.ReasonMalformed, sentRefused(t, err, "trailing data"))
	})

	t.Run("replay refuses a damaged record by name and sends nothing", func(t *testing.T) {
		for form, in := range forms.each() {
			setRecord(f, key, string(in[:len(in)/2]))
			_, err := b.Send(ctx, tokened("t-1"))
			var ref *Refusal
			require.True(t, errors.As(err, &ref), "%s: refused, never sent again", form)
			require.NotEmpty(t, ref.Problems)
			assert.Regexp(t, sentRefusal, ref.Problems[0], form.String())
			assert.Contains(t, err.Error(), `the token "t-1" has a record that is not one`)
			one(t, f)
		}
	})
}

// FuzzSentRecordRead holds the read's promise for any input: a record is
// either read whole or refused by name, never read as absent, empty or zero.
func FuzzSentRecordRead(f *testing.F) {
	rec := sentRecord{Fingerprint: strings.Repeat("ab", 32), ID: "01JA0000000000000000000000", At: start}
	v := answerwire.SentRecord{}
	answerwire.SentRecordReset(&v)
	v.FingerprintLength = int32(copy(v.Fingerprint[:], rec.Fingerprint))
	v.IdLength = int32(copy(v.Id[:], rec.ID))
	require.NoError(f, v.SetAtTime(rec.At))
	wire := make([]byte, answerwire.SentRecordMeasure(&v))
	answerwire.SentRecordSave(&v, wire)
	js := make([]byte, answerwire.SentRecordToJsonMeasure(&v))
	answerwire.SentRecordToJson(&v, js)
	legacy, err := json.Marshal(rec)
	require.NoError(f, err)
	for _, seed := range [][]byte{wire, []byte(base64.StdEncoding.EncodeToString(wire)), js, legacy, wire[:len(wire)/2], legacy[:len(legacy)-1], {}, []byte("{}")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		r := schemaread.NewReader(sentRecordCodec())
		got, _, err := r.Read(in)
		if err != nil {
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref, "%q: refused by name", in)
			require.Regexp(t, sentRefusal, err.Error(), "%q", in)
			return
		}
		readable(t, got, string(in))
	})
}
