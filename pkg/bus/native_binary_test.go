package bus

// The native binary form of the bus (SPEC-BUS.md, the native form): a typed
// envelope rides in its own stream field as the raw binary wire, no base64,
// beside the legacy text fields while writers switch; a reader reads the field
// when it is there, the legacy fields when it is not, and the text forms a
// text transport carries, counting each read under the form read.

import (
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nativeRig is a bus over a fresh fake whose sends carry the native field.
func nativeRig(t *testing.T) (*Bus, *Fake) {
	t.Helper()
	b, f := rig(t, "ada", "bob", "cy")
	b.Native = true
	return b, f
}

// sendOne sends m and answers the message as sent and the one entry the log holds of it.
func sendOne(t *testing.T, b *Bus, f *Fake, m Message) (Message, Entry) {
	t.Helper()
	sent, err := b.Send(context.Background(), m)
	require.NoError(t, err)
	log, err := f.Range(context.Background(), LogKey, "-", "+", 100)
	require.NoError(t, err)
	for _, e := range log {
		if e.Fields["id"] == sent.ID {
			return sent, e
		}
	}
	t.Fatalf("the log holds no entry %s", sent.ID)
	return Message{}, Entry{}
}

// onEntry is the message as its entry holds it: the token, the wait, the
// once-key and the marks are the sender's, never on the entry.
func onEntry(m Message) Message {
	m.Token, m.Wait, m.Once, m.Marks = "", 0, "", nil
	return m
}

// everyKind is one message of each kind, the ask with its wait.
func everyKind() []Message {
	var out []Message
	for _, k := range Kinds {
		if k == KindTimeout {
			continue // the bus's own word: no sender sends it
		}
		m := Message{From: "ada", To: []string{"cy", "bob"}, CC: []string{"cy"}, Subject: "#202 HOLD at 3f9c", Kind: k,
			Re: "01JA2B3C4D5E6F7G8H9J0KMNPA", Body: "the reader found it broken:\n- a line\n"}
		if k == KindAsk {
			m.Wait = 10 * time.Minute
		}
		out = append(out, m)
	}
	return out
}

func refusedAs(reason, field string) string {
	if field != "" {
		reason += " (" + field + ")"
	}
	return "ENVELOPE REFUSED: " + reason + "; run: " + EnvelopeHelp
}

// TestEnvelopeNativeBinaryField: a typed envelope is stored as the raw binary
// wire in its own stream field (no base64), read back byte-identical, refused
// by name when damaged, and counted as form binary; a reader still reads the
// legacy fields and the text forms; the old form in and the new form in read
// identically, and the writer switch leaves old readers reading as they did.
func TestEnvelopeNativeBinaryField(t *testing.T) {
	t.Parallel()
	t.Run("a send stores the raw wire in its own field, beside the legacy fields", func(t *testing.T) {
		t.Parallel()
		b, f := nativeRig(t)
		for _, m := range everyKind() {
			m.Token = "retry-" + m.Kind
			sent, e := sendOne(t, b, f, m)
			raw, ok := e.Fields[EnvelopeField]
			require.True(t, ok, "%s: the entry holds the %s field", m.Kind, EnvelopeField)
			want, err := EnvelopeWire(onEntry(sent))
			require.NoError(t, err)
			assert.Equal(t, want, []byte(raw), "%s: the field is the binary wire, byte for byte", m.Kind)
			assert.Equal(t, EnvelopeForm, raw[0], "%s: the wire leads with its form byte", m.Kind)
			_, b64err := base64.StdEncoding.DecodeString(raw)
			assert.Error(t, b64err, "%s: the field is no base64 text", m.Kind)
			legacy := maps.Clone(e.Fields)
			delete(legacy, EnvelopeField)
			assert.Equal(t, sent.Fields(), legacy, "%s: the legacy fields are written beside it, unchanged", m.Kind)
			// every stream the message is on holds the same bytes
			for _, n := range []string{"bob", "cy"} {
				es, err := f.Range(context.Background(), StreamOf(n), "-", "+", 100)
				require.NoError(t, err)
				i := slices.IndexFunc(es, func(x Entry) bool { return x.Fields["id"] == sent.ID })
				require.GreaterOrEqual(t, i, 0, n)
				assert.Equal(t, raw, es[i].Fields[EnvelopeField], "%s on %s", m.Kind, n)
			}
		}
	})
	t.Run("read back byte-identical and counted as form binary", func(t *testing.T) {
		t.Parallel()
		b, f := nativeRig(t)
		r := NewEnvelopeReader()
		ms := everyKind()
		for _, m := range ms {
			sent, e := sendOne(t, b, f, m)
			got, form, err := r.ReadEntry(e.Fields)
			require.NoError(t, err, m.Kind)
			assert.Equal(t, schemaread.FormBinary, form, m.Kind)
			assert.Equal(t, onEntry(sent), got, "%s: the field reads as the message sent", m.Kind)
			again, err := EnvelopeWire(got)
			require.NoError(t, err)
			assert.Equal(t, e.Fields[EnvelopeField], string(again), "%s: the read message writes the same bytes", m.Kind)
		}
		n := uint64(len(ms))
		assert.Equal(t, n, r.Reads(schemaread.FormBinary))
		for _, form := range []schemaread.Form{schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy} {
			assert.Zero(t, r.Reads(form), form.String())
		}
		assert.Equal(t, n, r.Snapshot()["binary"])
	})
	t.Run("a damaged field is refused by name, never read as a shorter envelope", func(t *testing.T) {
		t.Parallel()
		b, f := nativeRig(t)
		r := NewEnvelopeReader()
		_, e := sendOne(t, b, f, msg("ada", "bob"))
		good := []byte(e.Fields[EnvelopeField])
		with := func(v []byte) map[string]string {
			fields := maps.Clone(e.Fields)
			fields[EnvelopeField] = string(v)
			return fields
		}
		newer := slices.Clone(good)
		newer[0] = 9
		for _, tc := range []struct {
			name   string
			in     []byte
			reason string
		}{
			{"truncated", good[:len(good)-1], refusedAs(schemaread.ReasonWireDamaged, "")},
			{"cut to the form byte", good[:1], refusedAs(schemaread.ReasonWireDamaged, "")},
			{"trailing junk", append(slices.Clone(good), 0, 1), refusedAs(schemaread.ReasonWireDamaged, "")},
			{"a form byte this reader does not know", newer, refusedAs(schemaread.ReasonUnknownForm, "")},
			{"empty", nil, refusedAs(schemaread.ReasonUnknownForm, EnvelopeField)},
			{"base64 text in the binary field", []byte(base64.StdEncoding.EncodeToString(good)), refusedAs(schemaread.ReasonUnknownForm, EnvelopeField)},
			{"the JSON text in the binary field", textOf(t, r, schemaread.FormJSON), refusedAs(schemaread.ReasonUnknownForm, EnvelopeField)},
		} {
			got, _, err := r.ReadEntry(with(tc.in))
			require.Error(t, err, tc.name)
			assert.Equal(t, tc.reason, err.Error(), tc.name)
			var ref *schemaread.Refusal
			assert.True(t, errors.As(err, &ref), "%s: every refusal is a *schemaread.Refusal", tc.name)
			assert.Equal(t, Message{}, got, "%s: a refused read answers no message", tc.name)
		}
		for i := range len(good) {
			_, _, err := r.ReadEntry(with(good[:i]))
			assert.Error(t, err, "every cut is refused, never read as a shorter envelope: cut at %d", i)
		}
		// a damaged field is refused although the legacy fields beside it read:
		// the field, when there, is the message
		assert.NotEmpty(t, e.Fields["subject"])
		assert.Positive(t, r.Snapshot()["refused_wire_damaged"])
		assert.Positive(t, r.Snapshot()["refused_unknown_form"])
	})
	t.Run("a reader reads the text forms and refuses the legacy form", func(t *testing.T) {
		t.Parallel()
		b, f := rig(t, "ada", "bob")
		r := NewEnvelopeReader()
		sent, e := sendOne(t, b, f, msg("ada", "bob"))
		_, native := e.Fields[EnvelopeField]
		assert.True(t, native, "every bus writer writes the native field")
		got, form, err := r.ReadEntry(e.Fields)
		require.NoError(t, err)
		assert.Equal(t, schemaread.FormBinary, form)
		assert.Equal(t, onEntry(sent), got)
		for _, form := range []schemaread.Form{schemaread.FormBase64, schemaread.FormJSON, schemaread.FormBinary} {
			text, err := r.WriteText(got, form)
			require.NoError(t, err, form.String())
			back, read, err := r.ReadText(text)
			require.NoError(t, err, form.String())
			assert.Equal(t, form, read)
			assert.Equal(t, got, back, form.String())
		}
		legacy := maps.Clone(e.Fields)
		delete(legacy, EnvelopeField)
		_, _, err = r.ReadEntry(legacy)
		assert.EqualError(t, err, "ENVELOPE REFUSED: legacy (envelope); run: "+EnvelopeHelp, "the retired legacy form is refused by name")
		assert.Equal(t, uint64(1), r.Reads(schemaread.FormLegacy))
		assert.Equal(t, uint64(1), r.Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(1), r.Reads(schemaread.FormJSON))
		assert.Equal(t, uint64(1), r.Snapshot()["legacy"])
		_, _, err = r.ReadText([]byte("#202 HOLD at 3f9c"))
		assert.EqualError(t, err, refusedAs(schemaread.ReasonUnknownForm, ""), "a text that is no form is refused by name")
	})
	t.Run("a switched writer refuses past the envelope's bounds and writes nothing", func(t *testing.T) {
		t.Parallel()
		b, f := nativeRig(t)
		m := msg("ada", "bob")
		m.Subject = strings.Repeat("s", 201)
		_, err := b.Check(context.Background(), m)
		var ref *Refusal
		require.True(t, errors.As(err, &ref), "a dry run names it: %v", err)
		assert.EqualError(t, err, "ENVELOPE REFUSED: subject over 200 bytes; run: "+EnvelopeHelp)
		_, err = b.Send(context.Background(), m)
		require.True(t, errors.As(err, &ref), "a send names it: %v", err)
		m.Token = "retry-1"
		_, err = b.Send(context.Background(), m)
		require.True(t, errors.As(err, &ref), "a send under a token names it: %v", err)
		log, err := f.Range(context.Background(), LogKey, "-", "+", 10)
		require.NoError(t, err)
		assert.Empty(t, log, "nothing is written")
		// every writer, switched or not, refuses the same bounds
		legacy, _ := rig(t, "ada", "bob")
		m.Token = ""
		_, err = legacy.Send(context.Background(), m)
		require.True(t, errors.As(err, &ref), "an unswitched writer refuses too: %v", err)
		assert.EqualError(t, err, "ENVELOPE REFUSED: subject over 200 bytes; run: "+EnvelopeHelp)
	})
}

// textOf is a good envelope in a text form.
func textOf(t *testing.T, r *EnvelopeReader, form schemaread.Form) []byte {
	t.Helper()
	m := onEntry(msg("ada", "bob"))
	m.ID, m.Kind, m.At = "01JA2B3C4D5E6F7G8H9J0KMNPQ", KindStatus, start
	out, err := r.WriteText(m, form)
	require.NoError(t, err)
	return out
}
