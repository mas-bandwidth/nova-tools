package pongwire

// TestLayer1Pong is the golden test of the pong definitions (schemas/pong):
// the generated Pong round-trips as the binary wire, as padded standard base64
// of that wire and as the JSON text form, each read strictly through
// pkg/schemaread; every named refusal that applies to a pong fires on its
// bound; every pong renders to the name-keyed text form; and the legacy pong
// line and the table pong are read alike by the new reader while the old
// reader keeps working through the writer switch.
//
// The fixtures are never written by hand: the corpus wires in schemas/pong/fuzz
// and the newer writers' wires in testdata/writers are what the pinned
// compiler's `schema pack`, an engine independent of the generated Go, writes
// from their JSON. With <pin> the module@version of generate.go's line, and
// the JSON copied to <tree>/Pong.json (a variable root is that one file):
//
//	go run <pin> pack --root Pong --out schemas/pong/fuzz/<name>.bin <tree> schemas/pong/pong.schema
//	go run <pin> pack --root Pong --out pkg/pongwire/testdata/writers/<case>.bin <tree> pkg/pongwire/testdata/writers/<case>/pong.schema
//
// Each newer writer's pong.schema is the unit's own with one edit: newer adds
// a field (lanes), wide lets the nonce run to 64 bytes, changed makes width a
// string.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pongHelp = "nova-friend pong -h"

// The bounds the schema declares, read from the generated descriptors so the
// test and the definition cannot drift apart.
func fieldOf(t *testing.T, name string) TableFieldInfo {
	t.Helper()
	for _, f := range PongTableFields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("Pong declares no field %s", name)
	return TableFieldInfo{}
}

// envelope is what the bus message around a legacy pong line carries: the
// legacy line names neither the friend nor the send time, the message does.
type envelope struct {
	from   string
	sentMs int64
}

// codec is the strict pong read and write over the generated functions. The
// facade rules are UNION.md section 4.6: refuse every clamp, refuse absence
// where zero is not a value (friend_name, nonce, sent_ms) and absence of an
// optional a liveness count reads (queue, working, width), and validate before
// writing because the generated writer checks no bound.
func codec(t *testing.T, env envelope) *schemaread.Codec[Pong] {
	friendMax := int(fieldOf(t, "friend_name").ArrayBound)
	nonceMax := int(fieldOf(t, "nonce").ArrayBound)
	queue, working, width := fieldOf(t, "queue"), fieldOf(t, "working"), fieldOf(t, "width")
	return &schemaread.Codec[Pong]{
		Name:  "PONG",
		Help:  pongHelp,
		Reset: PongReset,
		Measure: func(wire []byte) error {
			_, err := PongLoadMeasureReason(wire)
			return err
		},
		LoadWire: func(v *Pong, wire []byte) schemaread.Report {
			return viaBuilder(v, func(b *PongBuilder, rep *TableReport) bool { return PongLoadBuilder(b, wire, rep) })
		},
		LoadJSON: func(v *Pong, text []byte) schemaread.Report {
			return viaBuilder(v, func(b *PongBuilder, rep *TableReport) bool { return PongFromJson(b, text, rep) })
		},
		Legacy: func(in []byte) bool { return bytes.HasPrefix(in, []byte("pong ")) },
		LoadLegacy: func(v *Pong, in []byte) schemaread.Report {
			return loadLegacy(v, string(in), env)
		},
		Constraints: func(v *Pong, c *schemaread.Checker) {
			c.Required("friend_name", v.FriendNameLength > 0)
			c.Text("friend_name", textOf(v.FriendName[:], v.FriendNameLength), friendMax)
			c.Required("nonce", v.NonceLength > 0)
			c.Text("nonce", textOf(v.Nonce[:], v.NonceLength), nonceMax)
			c.Required("queue", v.QueuePresent)
			c.Bound("queue", int64(v.Queue), int64(queue.RangeMin), int64(queue.RangeMax))
			c.Required("working", v.WorkingPresent)
			c.Bound("working", int64(v.Working), int64(working.RangeMin), int64(working.RangeMax))
			c.Required("width", v.WidthPresent)
			c.Bound("width", int64(v.Width), int64(width.RangeMin), int64(width.RangeMax))
			c.Required("sent_ms", v.SentMsPresent && v.SentMs > 0)
		},
		SaveWire: saveWire,
		SaveJSON: toJSON,
	}
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

func viaBuilder(v *Pong, load func(*PongBuilder, *TableReport) bool) schemaread.Report {
	var b PongBuilder
	if !b.Init() {
		return schemaread.Report{Malformed: true}
	}
	defer b.Shutdown()
	var rep TableReport
	ok := load(&b, &rep)
	if root := b.GetRoot(); root != nil {
		*v = *root
	}
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// saveWire is the generated Measure and Save, unchecked: the facade checks
// before it calls this, and the hostile cases call it directly to write what
// a broken or older writer could write.
func saveWire(v *Pong) []byte {
	n := PongMeasure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if PongSave(v, buf) != n {
		return nil
	}
	return buf
}

func toJSON(v *Pong) []byte {
	n := PongToJsonMeasure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if PongToJson(v, buf) != n {
		return nil
	}
	return buf
}

// loadLegacy reads today's pong line, `pong <nonce> queue=N working=N width=N`
// (pkg/friend PongLine), strictly: the line must be exactly what PongLine
// writes for the values it carries, or it is refused as legacy_shape. The
// friend and the send time come from the bus message around the line.
func loadLegacy(v *Pong, text string, env envelope) schemaread.Report {
	var nonce string
	var queue, working, width int
	n, err := fmt.Sscanf(text, "pong %s queue=%d working=%d width=%d", &nonce, &queue, &working, &width)
	if err != nil || n != 4 || friend.PongLine(nonce, queue, working, width) != text ||
		queue < 0 || working < 0 || width < 0 || queue > 1<<32-1 || working > 1<<32-1 || width > 1<<32-1 {
		return schemaread.Report{Reason: "legacy_shape"}
	}
	set(v, env.from, nonce, uint32(queue), uint32(working), uint32(width), env.sentMs)
	return schemaread.Report{}
}

// set fills a pong with every field present.
func set(v *Pong, friendName, nonce string, queue, working, width uint32, sentMs int64) {
	PongReset(v)
	v.FriendNameLength = int32(copy(v.FriendName[:len(v.FriendName)-1], friendName))
	v.NonceLength = int32(copy(v.Nonce[:len(v.Nonce)-1], nonce))
	v.Queue, v.QueuePresent = queue, true
	v.Working, v.WorkingPresent = working, true
	v.Width, v.WidthPresent = width, true
	v.SentMs, v.SentMsPresent = sentMs, true
}

func pong(friendName, nonce string, queue, working, width uint32, sentMs int64) Pong {
	var v Pong
	set(&v, friendName, nonce, queue, working, width, sentMs)
	return v
}

// refusal is the reason a read was refused by, checking the house style.
func refusal(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), "PONG REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+pongHelp), err.Error())
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

var b64 = base64.StdEncoding.EncodeToString

func TestLayer1Pong(t *testing.T) {
	t.Parallel()
	env := envelope{from: "aurora", sentMs: 1792000000000}

	// The corpus is the golden: the JSON text of each instance and the wire
	// `schema pack` wrote from it. The generated Save writes the same bytes
	// and the generated ToJson writes the same text, the name-keyed text form.
	t.Run("golden round trip in every form", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec(t, env))
		names := []string{"pong-typical", "pong-bounds", "pong-zeros"}
		want := map[string]Pong{
			"pong-typical": pong("aurora", "abc123", 5, 0, 8, 1792000000000),
			"pong-bounds":  pong(strings.Repeat("f", 64), strings.Repeat("z", 32), 100000, 10000, 1024, 1<<63-1),
			"pong-zeros":   pong("f", "123456", 0, 0, 0, 1),
		}
		for _, name := range names {
			wire := readFile(t, filepath.Join("..", "..", "schemas", "pong", "fuzz", name+".bin"))
			text := readFile(t, filepath.Join("..", "..", "schemas", "pong", "fuzz", name+".json"))
			v := want[name]

			assert.Equal(t, wire, saveWire(&v), "%s: the generated Save writes the packed wire", name)
			assert.Equal(t, string(text), string(toJSON(&v)), "%s: the generated ToJson writes the corpus text", name)
			for _, key := range []string{`"friend_name": `, `"nonce": `, `"queue": `, `"working": `, `"width": `, `"sent_ms": `} {
				assert.Contains(t, string(text), key, "%s: the text form is keyed by field name", name)
			}

			for _, in := range []struct {
				form schemaread.Form
				data []byte
			}{
				{schemaread.FormBinary, wire},
				{schemaread.FormBase64, []byte(b64(wire))},
				{schemaread.FormJSON, text},
			} {
				got, res, err := r.Read(in.data)
				require.NoError(t, err, "%s as %s", name, in.form)
				assert.Equal(t, in.form, res.Form, name)
				assert.Equal(t, v, got, "%s as %s reads the instance", name, in.form)
			}
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				out, err := r.Write(&v, form)
				require.NoError(t, err, "%s written as %s", name, form)
				got, res, err := r.Read(out)
				require.NoError(t, err, "%s written then read as %s", name, form)
				assert.Equal(t, form, res.Form, name)
				assert.Equal(t, v, got, "%s round-trips as %s", name, form)
			}
			assert.Equal(t, b64(wire), mustWrite(t, r, &v, schemaread.FormBase64), "%s: the base64 text is the padded standard base64 of the whole wire", name)
		}
		c := r.Counts()
		assert.Equal(t, uint64(6), c.Reads(schemaread.FormBinary), "each read is counted once, on its form")
		assert.Equal(t, uint64(6), c.Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(6), c.Reads(schemaread.FormJSON))
		assert.Equal(t, uint64(0), c.Reads(schemaread.FormLegacy))
	})

	// Every refusal of UNION.md section 4.6 that applies to a pong, on its
	// bound. The over-bound values are written by the generated writer called
	// directly (it checks no bound), or by a newer writer whose schema allows
	// them (testdata/writers), exactly what a reader can meet on the bus.
	t.Run("named refusals fire on their bounds", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec(t, env))
		good := pong("aurora", "abc123", 5, 3, 8, 1792000000000)
		goodWire := saveWire(&good)
		with := func(edit func(*Pong)) []byte {
			v := good
			edit(&v)
			return saveWire(&v)
		}
		newerForm := append([]byte{}, goodWire...)
		newerForm[0] = 9
		withJSON := func(old, new string) []byte {
			text := strings.Replace(string(toJSON(&good)), old, new, 1)
			require.NotEqual(t, string(toJSON(&good)), text, "the edit %q applies", old)
			return []byte(text)
		}
		cases := []struct {
			name   string
			in     []byte
			reason string
		}{
			// the bounds, each one past and each one at
			// The generated reader clamps and counts without naming the field
			// (TableReport carries a count only), so a read refusal names the
			// reason alone; the writer, which checks the value itself, names
			// the field too (the writer subtest below).
			{"queue over 100000", with(func(v *Pong) { v.Queue = 100001 }), "clamped"},
			{"working over 10000", with(func(v *Pong) { v.Working = 10001 }), "clamped"},
			{"width over 1024", with(func(v *Pong) { v.Width = 1025 }), "clamped"},
			{"queue over 100000 as JSON text", withJSON(`"queue": 5`, `"queue": 100001`), "clamped"},
			{"queue over 100000 as base64 text", []byte(b64(with(func(v *Pong) { v.Queue = 100001 }))), "clamped"},
			{"nonce over 32 bytes from a newer writer", readFile(t, "testdata/writers/wide.bin"), "clamped"},
			{"a field that changed kind", readFile(t, "testdata/writers/changed.bin"), "kind_mismatch"},
			// absence where zero is no answer
			{"nonce absent", with(func(v *Pong) { v.NonceLength = 0 }), "absent_required (nonce)"},
			{"friend absent", with(func(v *Pong) { v.FriendNameLength = 0 }), "absent_required (friend_name)"},
			{"queue absent", with(func(v *Pong) { v.Queue, v.QueuePresent = 0, false }), "absent_required (queue)"},
			{"working absent", with(func(v *Pong) { v.Working, v.WorkingPresent = 0, false }), "absent_required (working)"},
			{"width absent", with(func(v *Pong) { v.Width, v.WidthPresent = 0, false }), "absent_required (width)"},
			{"sent_ms absent", with(func(v *Pong) { v.SentMs, v.SentMsPresent = 0, false }), "absent_required (sent_ms)"},
			{"queue absent from the JSON text", withJSON("  \"queue\": 5,\n", ""), "absent_required (queue)"},
			{"a NUL in the nonce", with(func(v *Pong) { v.Nonce[2] = 0 }), "malformed"},
			// framing and form
			{"truncated wire", goodWire[:len(goodWire)-1], "wire_damaged"},
			{"trailing junk in the wire", append(append([]byte{}, goodWire...), 0, 1), "wire_damaged"},
			{"the form byte alone", goodWire[:1], "wire_damaged"},
			{"an unknown newer form", newerForm, "unknown_form"},
			{"base64 that does not decode", []byte(b64(goodWire)[:len(b64(goodWire))-3]), "malformed"},
			{"base64 with trailing junk", []byte(b64(goodWire) + " junk"), "malformed"},
			{"damaged JSON text", withJSON(`"queue": 5`, `"queue": 5"`), "malformed"},
			{"empty", nil, "unknown_form"},
			{"no pong at all", []byte("hello pong abc123"), "malformed"},
			// the legacy line
			{"a legacy pong without its numbers", []byte("pong abc123"), "legacy_shape"},
			{"a legacy pong with a bad number", []byte("pong abc123 queue=x working=3 width=8"), "legacy_shape"},
			{"a legacy pong with trailing junk", []byte(friend.PongLine("abc123", 5, 3, 8) + " junk"), "legacy_shape"},
		}
		for _, tc := range cases {
			got, _, err := r.Read(tc.in)
			assert.Equal(t, tc.reason, refusal(t, err), tc.name)
			assert.Equal(t, Pong{}, got, "%s: a refused read returns nothing, never a partial pong", tc.name)
		}
		// at each bound the pong reads
		at := pong("aurora", "abc123", 100000, 10000, 1024, 1)
		_, _, err := r.Read(saveWire(&at))
		require.NoError(t, err, "every bound itself is a pong")

		// every truncation of a good wire is refused, never read as a shorter pong
		for i := range len(goodWire) {
			_, _, err := r.Read(goodWire[:i])
			assert.Error(t, err, "cut at byte %d", i)
		}
		// a newer writer's field this build does not know is skipped and counted
		got, res, err := r.Read(readFile(t, "testdata/writers/newer.bin"))
		require.NoError(t, err, "a newer writer's pong reads")
		assert.Equal(t, 1, res.Unknown)
		assert.Equal(t, pong("aurora", "abc123", 5, 3, 8, 1792000000000), got)
		assert.Equal(t, uint64(1), r.Counts().Unknown())
		assert.Equal(t, uint64(7), r.Counts().Refused("absent_required"), "every absence case is counted under its reason")
	})

	// The writer refuses what its reader refuses, and writes nothing.
	t.Run("the writer refuses before it writes", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec(t, env))
		for _, tc := range []struct {
			name   string
			v      Pong
			reason string
		}{
			{"queue over 100000", pong("aurora", "abc123", 100001, 3, 8, 1), "clamped (queue)"},
			{"working over 10000", pong("aurora", "abc123", 5, 10001, 8, 1), "clamped (working)"},
			{"width over 1024", pong("aurora", "abc123", 5, 3, 1025, 1), "clamped (width)"},
			{"no nonce", pong("aurora", "", 5, 3, 8, 1), "absent_required (nonce)"},
			{"no friend", pong("", "abc123", 5, 3, 8, 1), "absent_required (friend_name)"},
			{"no send time", pong("aurora", "abc123", 5, 3, 8, 0), "absent_required (sent_ms)"},
			{"a nonce that is not UTF-8", pong("aurora", "ab\xffc", 5, 3, 8, 1), "malformed (nonce)"},
		} {
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				out, err := r.Write(&tc.v, form)
				assert.Nil(t, out, "%s as %s writes nothing", tc.name, form)
				assert.Equal(t, tc.reason, refusal(t, err), "%s as %s", tc.name, form)
			}
		}
	})

	// The cutover (owner's rule S4): readers first, then writers. The legacy
	// line and the table pong are read identically by the new reader, the
	// per-form counter shows which form arrived, and the old reader keeps
	// reading every pong written before the writer switch.
	t.Run("dual read through the writer switch", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec(t, env))
		for _, tc := range []struct {
			nonce                 string
			queue, working, width int
		}{
			{"abc123", 5, 3, 8},
			{"k3x9q0", 0, 0, 0},
			{"123456", 100000, 10000, 1024},
			{strings.Repeat("z", 32), 1, 1, 1},
		} {
			// before the switch: old writers, old and new readers
			line := friend.PongLine(tc.nonce, tc.queue, tc.working, tc.width)
			n, q, w, wd, ok := friend.ParsePong(line)
			require.True(t, ok, "the old reader reads the old writer")
			assert.Equal(t, []any{tc.nonce, tc.queue, tc.working, tc.width}, []any{n, q, w, wd})
			legacy, res, err := r.Read([]byte(line))
			require.NoError(t, err, line)
			assert.Equal(t, schemaread.FormLegacy, res.Form)

			// after the switch: new writers, new readers
			typed := pong(env.from, tc.nonce, uint32(tc.queue), uint32(tc.working), uint32(tc.width), env.sentMs)
			for _, form := range []schemaread.Form{schemaread.FormBase64, schemaread.FormJSON, schemaread.FormBinary} {
				out := mustWriteBytes(t, r, &typed, form)
				got, res, err := r.Read(out)
				require.NoError(t, err)
				assert.Equal(t, form, res.Form)
				assert.Equal(t, legacy, got, "the old form in and the new form in read identically (%s)", form)

				// an old reader meets a new pong only if a writer switched
				// before every reader: it does not read it as a pong (never as
				// a pong of zeros), which is why readers go first
				_, _, _, _, ok := friend.ParsePong(string(out))
				assert.False(t, ok, "the old reader does not misread a %s table pong", form)
			}
		}
		c := r.Counts()
		assert.Equal(t, uint64(4), c.Reads(schemaread.FormLegacy), "the counter shows the legacy reads")
		assert.Equal(t, uint64(4), c.Reads(schemaread.FormBase64))
		assert.Equal(t, uint64(4), c.Reads(schemaread.FormJSON))
		assert.Equal(t, uint64(4), c.Reads(schemaread.FormBinary))
		assert.Equal(t, uint64(0), c.Refused("legacy_shape"))
	})

	// A deterministic mutation pass over the wire, the base64 text and the
	// JSON text: every mutant either is refused by name or reads a pong that
	// passes every constraint. Nothing reads as silence and nothing panics.
	t.Run("mutants are refused by name or read whole", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(codec(t, env))
		good := pong("aurora", "abc123", 5, 3, 8, 1792000000000)
		inputs := [][]byte{saveWire(&good), []byte(b64(saveWire(&good))), toJSON(&good)}
		mutants := 0
		for _, in := range inputs {
			for i := range in {
				for _, b := range []byte{0x00, in[i] ^ 0x01, in[i] ^ 0x80} {
					m := append([]byte{}, in...)
					m[i] = b
					mutants++
					got, _, err := r.Read(m)
					if err != nil {
						var ref *schemaread.Refusal
						require.True(t, errors.As(err, &ref), "mutant %d: %v", mutants, err)
						continue
					}
					require.NoError(t, r.Check(&got), "a mutant that reads passes every constraint")
				}
			}
		}
		assert.Greater(t, mutants, 900)
	})
}

func mustWriteBytes(t *testing.T, r *schemaread.Reader[Pong], v *Pong, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(v, form)
	require.NoError(t, err)
	return out
}

func mustWrite(t *testing.T, r *schemaread.Reader[Pong], v *Pong, form schemaread.Form) string {
	t.Helper()
	return string(mustWriteBytes(t, r, v, form))
}
