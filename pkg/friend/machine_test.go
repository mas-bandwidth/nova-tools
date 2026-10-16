package friend

import (
	"encoding/base64"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/pongwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

func TestPongLinesRoundTrip(t *testing.T) {
	t.Parallel()
	n, q, w, wd, ok := ParsePong(PongLine("abc123", 5, 3, 8))
	require.True(t, ok)
	assert.Equal(t, []int{5, 3, 8}, []int{q, w, wd})
	assert.Equal(t, "abc123", n)
	n, _, _, _, ok = ParsePong("pong abc123")
	assert.True(t, ok && n == "abc123", "the numbers are optional")
	_, _, _, _, ok = ParsePong("hello pong abc123")
	assert.False(t, ok)
}

// pongAt is the bus message time the dual-read tests read at: the clock
// starts at 1000 seconds, so a zero time reads as an absent send time.
var pongAt = time.Unix(1000, 0).UTC()

// tablePong is a generated pong with every field present.
func tablePong(friendName, nonce string, queue, working, width uint32, sentMs int64) pongwire.Pong {
	var v pongwire.Pong
	pongwire.PongReset(&v)
	v.FriendNameLength = int32(copy(v.FriendName[:len(v.FriendName)-1], friendName))
	v.NonceLength = int32(copy(v.Nonce[:len(v.Nonce)-1], nonce))
	v.Queue, v.QueuePresent = queue, true
	v.Working, v.WorkingPresent = working, true
	v.Width, v.WidthPresent = width, true
	v.SentMs, v.SentMsPresent = sentMs, true
	return v
}

// rawWire is the generated Measure and Save called directly: it checks no
// bound, so it writes what a broken or newer writer could put on the bus.
func rawWire(t *testing.T, v pongwire.Pong) []byte {
	t.Helper()
	n := pongwire.PongMeasure(&v)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, pongwire.PongSave(&v, buf))
	return buf
}

// rawJSON is the generated ToJson called directly, unchecked.
func rawJSON(t *testing.T, v pongwire.Pong) []byte {
	t.Helper()
	n := pongwire.PongToJsonMeasure(&v)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, pongwire.PongToJson(&v, buf))
	return buf
}

var pongB64 = base64.StdEncoding.EncodeToString

// pongRefusal is the reason and field a read was refused by, checking that
// the refusal is a named *schemaread.Refusal in the house style and that a
// refused read returns no pong at all.
func pongRefusal(t *testing.T, got Pong, err error, what string) string {
	t.Helper()
	require.Error(t, err, what)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "%s: every refusal is a *schemaread.Refusal: %v", what, err)
	assert.True(t, strings.HasPrefix(err.Error(), "PONG REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+PongHelp), err.Error())
	assert.Equal(t, Pong{}, got, "%s: a refused read returns no pong, never a partial one", what)
	if r.Field != "" {
		return r.Reason + " (" + r.Field + ")"
	}
	return r.Reason
}

// TestPongDualRead pins the friend machine's pong reader through the
// readers-first cutover: the legacy line PongLine writes and the table pong
// (pkg/pongwire) as the binary wire, padded standard base64 of it and the
// JSON text read alike; a malformed or clamped pong of either form is refused
// by name and never read as no pong; every read is counted once under its
// form; and the old reader, ParsePong, keeps reading what the writer writes
// until the writer switches.
func TestPongDualRead(t *testing.T) {
	t.Parallel()

	t.Run("legacy and table pongs read alike while PongLine writes legacy", func(t *testing.T) {
		t.Parallel()
		r := NewPongReader()
		cases := []struct {
			nonce                 string
			queue, working, width int
		}{
			{"abc123", 5, 3, 8},
			{"k3x9q0", 0, 0, 0},
			{"123456", 100000, 10000, 1024},
			{strings.Repeat("z", 32), 1, 1, 1},
		}
		for _, tc := range cases {
			line := PongLine(tc.nonce, tc.queue, tc.working, tc.width)
			require.True(t, strings.HasPrefix(line, "pong "), "the writer stays legacy: %q", line)
			want := Pong{Friend: "bob", Nonce: tc.nonce, Queue: tc.queue, Working: tc.working, Width: tc.width, Sent: pongAt}

			// before the switch: the old writer, and both readers
			n, q, w, wd, ok := ParsePong(line)
			require.True(t, ok, "the old reader reads the old writer")
			assert.Equal(t, []any{tc.nonce, tc.queue, tc.working, tc.width}, []any{n, q, w, wd})
			m := bus.Message{From: "bob", To: []string{"ada"}, Subject: PongSubject, Body: line + "\n", At: pongAt}
			require.True(t, IsPong(m))
			got, form, err := r.Message(m)
			require.NoError(t, err, line)
			assert.Equal(t, schemaread.FormLegacy, form)
			assert.Equal(t, want, got, "the legacy line reads whole, the friend and the time from the message")

			// after the switch: a table pong in each form reads identically
			v := tablePong("bob", tc.nonce, uint32(tc.queue), uint32(tc.working), uint32(tc.width), pongAt.UnixMilli())
			wire := rawWire(t, v)
			for _, in := range []struct {
				form schemaread.Form
				body []byte
			}{
				{schemaread.FormBinary, wire},
				{schemaread.FormBase64, []byte(pongB64(wire))},
				{schemaread.FormJSON, rawJSON(t, v)},
			} {
				got, form, err := r.Read("bob", pongAt, in.body)
				require.NoError(t, err, "%s", in.form)
				assert.Equal(t, in.form, form)
				assert.Equal(t, want, got, "the old form in and the new form in read identically (%s)", in.form)

				// the reader's own table writer writes what the generated writer does
				out, err := r.write(want, in.form)
				require.NoError(t, err)
				assert.Equal(t, in.body, out, "%s", in.form)

				// an old reader meets a table pong only if a writer switched
				// before every reader: it never reads one as a pong (never a pong
				// of zeros), which is why readers go first
				_, _, _, _, ok := ParsePong(string(in.body))
				assert.False(t, ok, "the old reader does not misread a %s table pong", in.form)
			}
		}
		c := r.Counts()
		for _, f := range []schemaread.Form{schemaread.FormLegacy, schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			assert.Equal(t, uint64(len(cases)), c.Reads(f), "each read is counted once, on its form (%s)", f)
		}
		assert.Equal(t, "base64=4 binary=4 json=4 legacy=4 unknown_fields=0", r.CountsLine())
	})

	t.Run("a clamp is refused in every form", func(t *testing.T) {
		t.Parallel()
		r := NewPongReader()
		at := pongAt.UnixMilli()
		for _, tc := range []struct {
			name   string
			body   []byte
			reason string
		}{
			{"queue over 100000 as the wire", rawWire(t, tablePong("bob", "abc123", 100001, 3, 8, at)), "clamped"},
			{"working over 10000 as the wire", rawWire(t, tablePong("bob", "abc123", 5, 10001, 8, at)), "clamped"},
			{"width over 1024 as the wire", rawWire(t, tablePong("bob", "abc123", 5, 3, 1025, at)), "clamped"},
			{"queue over 100000 as base64", []byte(pongB64(rawWire(t, tablePong("bob", "abc123", 100001, 3, 8, at)))), "clamped"},
			{"queue over 100000 as JSON", rawJSON(t, tablePong("bob", "abc123", 100001, 3, 8, at)), "clamped"},
			{"queue over 100000 as the legacy line", []byte(PongLine("abc123", 100001, 3, 8)), "clamped (queue)"},
			{"working over 10000 as the legacy line", []byte(PongLine("abc123", 5, 10001, 8)), "clamped (working)"},
			{"width over 1024 as the legacy line", []byte(PongLine("abc123", 5, 3, 1025)), "clamped (width)"},
			{"a negative queue as the legacy line", []byte(PongLine("abc123", -1, 3, 8)), "clamped"},
			{"a queue past 32 bits as the legacy line", []byte(PongLine("abc123", 1<<32, 3, 8)), "clamped"},
			{"a nonce over 32 bytes as the legacy line", []byte(PongLine(strings.Repeat("z", 33), 5, 3, 8)), "clamped"},
		} {
			got, _, err := r.Read("bob", pongAt, tc.body)
			assert.Equal(t, tc.reason, pongRefusal(t, got, err, tc.name), tc.name)
		}
		assert.Equal(t, uint64(11), r.Counts().Refused(schemaread.ReasonClamped))

		// each bound itself is a pong, in either form
		_, _, err := r.Read("bob", pongAt, rawWire(t, tablePong("bob", "abc123", 100000, 10000, 1024, at)))
		require.NoError(t, err)
		_, _, err = r.Read("bob", pongAt, []byte(PongLine("abc123", 100000, 10000, 1024)))
		require.NoError(t, err)

		// the reader's writer refuses a clamp before it writes
		out, err := r.write(Pong{Friend: "bob", Nonce: "abc123", Queue: 100001, Sent: pongAt}, schemaread.FormBinary)
		assert.Nil(t, out)
		assert.Equal(t, "clamped (queue)", pongRefusal(t, Pong{}, err, "write"))
	})

	t.Run("a malformed pong is refused by name, never read as no pong", func(t *testing.T) {
		t.Parallel()
		r := NewPongReader()
		at := pongAt.UnixMilli()
		good := tablePong("bob", "abc123", 5, 3, 8, at)
		goodWire := rawWire(t, good)
		with := func(edit func(*pongwire.Pong)) []byte {
			v := good
			edit(&v)
			return rawWire(t, v)
		}
		goodJSON := string(rawJSON(t, good))
		jsonWith := func(old, new string) []byte {
			text := strings.Replace(goodJSON, old, new, 1)
			require.NotEqual(t, goodJSON, text, "the edit %q applies", old)
			return []byte(text)
		}
		newerForm := append([]byte{}, goodWire...)
		newerForm[0] = 9
		for _, tc := range []struct {
			name   string
			from   string
			at     time.Time
			body   []byte
			reason string
		}{
			// the table forms
			{"nonce absent", "bob", pongAt, with(func(v *pongwire.Pong) { v.NonceLength = 0 }), "absent_required (nonce)"},
			{"friend absent", "bob", pongAt, with(func(v *pongwire.Pong) { v.FriendNameLength = 0 }), "absent_required (friend_name)"},
			{"queue absent", "bob", pongAt, with(func(v *pongwire.Pong) { v.Queue, v.QueuePresent = 0, false }), "absent_required (queue)"},
			{"sent_ms absent", "bob", pongAt, with(func(v *pongwire.Pong) { v.SentMs, v.SentMsPresent = 0, false }), "absent_required (sent_ms)"},
			{"a NUL in the nonce", "bob", pongAt, with(func(v *pongwire.Pong) { v.Nonce[2] = 0 }), "malformed"},
			{"truncated wire", "bob", pongAt, goodWire[:len(goodWire)-1], "wire_damaged"},
			{"trailing junk in the wire", "bob", pongAt, append(append([]byte{}, goodWire...), 0, 1), "wire_damaged"},
			{"an unknown newer form", "bob", pongAt, newerForm, "unknown_form"},
			{"base64 that does not decode", "bob", pongAt, []byte(pongB64(goodWire)[:len(pongB64(goodWire))-3]), "malformed"},
			{"base64 with trailing junk", "bob", pongAt, []byte(pongB64(goodWire) + " junk"), "malformed"},
			{"damaged JSON text", "bob", pongAt, jsonWith(`"queue": 5`, `"queue": 5"`), "malformed"},
			{"a JSON key given twice", "bob", pongAt, jsonWith(`"queue": 5,`, `"queue": 5, "queue": 7,`), "malformed"},
			{"queue absent from the JSON text", "bob", pongAt, jsonWith("\"queue\": 5,", ""), "absent_required (queue)"},
			{"empty", "bob", pongAt, nil, "unknown_form"},
			{"no pong at all", "bob", pongAt, []byte("not a pong"), "malformed"},
			// the legacy line
			{"a legacy pong without its numbers", "bob", pongAt, []byte("pong abc123"), "absent_required (queue)"},
			{"a legacy pong without its width", "bob", pongAt, []byte("pong abc123 queue=5 working=3"), "absent_required (width)"},
			{"a legacy pong with a bad number", "bob", pongAt, []byte("pong abc123 queue=x working=3 width=8"), "malformed"},
			{"a legacy pong with a padded number", "bob", pongAt, []byte("pong abc123 queue=05 working=3 width=8"), "malformed"},
			{"a legacy pong with its keys out of order", "bob", pongAt, []byte("pong abc123 working=3 queue=5 width=8"), "malformed"},
			{"a legacy pong with trailing junk", "bob", pongAt, []byte(PongLine("abc123", 5, 3, 8) + " junk"), "malformed"},
			{"a legacy pong with two spaces", "bob", pongAt, []byte("pong  abc123 queue=5 working=3 width=8"), "malformed"},
			{"a legacy pong with a tab in its nonce", "bob", pongAt, []byte("pong abc\t123 queue=5 working=3 width=8"), "malformed"},
			{"a legacy pong from no one", "", pongAt, []byte(PongLine("abc123", 5, 3, 8)), "absent_required (friend_name)"},
			{"a legacy pong with no send time", "bob", time.Time{}, []byte(PongLine("abc123", 5, 3, 8)), "absent_required (sent_ms)"},
		} {
			got, _, err := r.Read(tc.from, tc.at, tc.body)
			assert.Equal(t, tc.reason, pongRefusal(t, got, err, tc.name), tc.name)
		}
		// every truncation of a good pong of either form is refused, never read
		// as a shorter pong (the JSON text without the newline it ends with)
		line := PongLine("abc123", 5, 3, 8)
		for _, in := range [][]byte{goodWire, []byte(pongB64(goodWire)), []byte(strings.TrimSpace(goodJSON)), []byte(line)} {
			for i := range len(in) {
				got, _, err := r.Read("bob", pongAt, in[:i])
				pongRefusal(t, got, err, "cut at byte "+string(in[:i]))
			}
		}
		// the message entry point trims the bus body's newline and reads the
		// message's own from and time; IsPong says which messages are pongs
		assert.True(t, IsPong(bus.Message{Subject: "PONG", Body: "not a pong"}), "the pong subject makes a pong, matched without case")
		assert.True(t, IsPong(bus.Message{Subject: "hello", Body: line}), "a legacy pong line is a pong whatever the subject")
		assert.False(t, IsPong(bus.Message{Subject: "hello", Body: "I am here"}))
		got, _, err := r.Message(bus.Message{From: "bob", Subject: PongSubject, Body: "pong abc123\n", At: pongAt})
		assert.Equal(t, "absent_required (queue)", pongRefusal(t, got, err, "message"))
	})

	// A seeded, deterministic mutation pass over a pong of each form: every
	// mutant is either refused by name or reads a whole pong that passes every
	// constraint. None reads as no pong, none panics, and each read is counted
	// once, under its form or as a refusal of no form.
	t.Run("a fuzzed malformed pong is refused by name in every case", func(t *testing.T) {
		t.Parallel()
		r := NewPongReader()
		good := tablePong("bob", "abc123", 5, 3, 8, pongAt.UnixMilli())
		seeds := [][]byte{rawWire(t, good), []byte(pongB64(rawWire(t, good))), rawJSON(t, good), []byte(PongLine("abc123", 5, 3, 8))}
		known := map[string]bool{
			schemaread.ReasonMalformed: true, schemaread.ReasonClamped: true, schemaread.ReasonKindMismatch: true,
			schemaread.ReasonUnknownForm: true, schemaread.ReasonWireDamaged: true, schemaread.ReasonAbsentRequired: true,
		}
		rng := rand.New(rand.NewPCG(1000, 1))
		const perSeed = 5000
		refused, read, noForm := 0, 0, 0
		for _, seed := range seeds {
			for range perSeed {
				m := mutatePong(rng, seed)
				got, form, err := r.Read("bob", pongAt, m)
				if form == schemaread.FormNone {
					noForm++
				}
				if err != nil {
					var ref *schemaread.Refusal
					require.True(t, errors.As(err, &ref), "mutant %q: %v", m, err)
					require.True(t, known[ref.Reason], "mutant %q is refused by a shared name, not %q", m, ref.Reason)
					require.Equal(t, Pong{}, got, "mutant %q: a refusal returns no pong", m)
					refused++
					continue
				}
				read++
				require.NotEqual(t, Pong{}, got, "mutant %q: a read is a pong, never silence", m)
				require.NotEmpty(t, got.Nonce)
				require.NotEmpty(t, got.Friend)
				require.False(t, got.Sent.IsZero())
				_, err = r.write(got, schemaread.FormBinary)
				require.NoError(t, err, "mutant %q: a pong that reads passes every constraint", m)
			}
		}
		total := len(seeds) * perSeed
		assert.Equal(t, total, refused+read)
		assert.Greater(t, refused, total/2, "most mutants are damage, and each is refused")
		c := r.Counts()
		forms := c.Reads(schemaread.FormBinary) + c.Reads(schemaread.FormBase64) + c.Reads(schemaread.FormJSON) + c.Reads(schemaread.FormLegacy)
		assert.Equal(t, uint64(total-noForm), forms, "every read with a form is counted once under it")
		var tallied uint64
		for reason := range known {
			tallied += c.Refused(reason)
		}
		assert.Equal(t, uint64(refused), tallied, "every refusal is tallied under its name")
	})
}

// mutatePong is one seeded mutation of in: a byte flipped, set, dropped or
// inserted, or the input cut short, one to three times.
func mutatePong(rng *rand.Rand, in []byte) []byte {
	m := append([]byte{}, in...)
	for range 1 + rng.IntN(3) {
		if len(m) == 0 {
			return m
		}
		i := rng.IntN(len(m))
		switch rng.IntN(5) {
		case 0:
			m[i] ^= byte(1 << rng.IntN(8))
		case 1:
			m[i] = byte(rng.IntN(256))
		case 2:
			m = append(m[:i], m[i+1:]...)
		case 3:
			m = append(m[:i], append([]byte{byte(rng.IntN(256))}, m[i:]...)...)
		case 4:
			m = m[:i]
		}
	}
	return m
}

// FuzzPongDualRead is the open-ended form of the mutation pass: whatever the
// bytes, the reader refuses them by name or reads a whole pong.
func FuzzPongDualRead(f *testing.F) {
	v := tablePong("bob", "abc123", 5, 3, 8, pongAt.UnixMilli())
	n := pongwire.PongMeasure(&v)
	wire := make([]byte, n)
	pongwire.PongSave(&v, wire)
	f.Add(wire)
	f.Add([]byte(pongB64(wire)))
	f.Add([]byte(PongLine("abc123", 5, 3, 8)))
	jn := pongwire.PongToJsonMeasure(&v)
	text := make([]byte, jn)
	pongwire.PongToJson(&v, text)
	f.Add(text)
	f.Fuzz(func(t *testing.T, in []byte) {
		r := NewPongReader()
		got, _, err := r.Read("bob", pongAt, in)
		if err != nil {
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref, "%q: every refusal is named", in)
			require.Equal(t, Pong{}, got, "%q: a refusal returns no pong", in)
			return
		}
		require.NotEmpty(t, got.Nonce, "%q: read as a pong of nothing", in)
		require.NotEmpty(t, got.Friend, "%q", in)
		require.False(t, got.Sent.IsZero(), "%q", in)
		_, err = r.write(got, schemaread.FormBinary)
		require.NoError(t, err, "%q: a pong that reads passes its constraints", in)
	})
}
