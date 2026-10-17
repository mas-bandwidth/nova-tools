package member

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// takeRefusalLine is the shape of every refusal of the take record.
var takeRefusalLine = regexp.MustCompile(`^TAKE_OUT REFUSED: [a-z_]+( \([a-z]+\))?; run: nova-swarm member -h$`)

// TestMemberTakeAnswerDualRead is the reader half of Aspect 11's take answer
// (the worker and server answers parsed as text; the owner's rules S2 and S4):
// the take answer pkg/member reads (takeOut; the TakeOut table of
// schemas/answer) is read alike from its binary table image, the padded standard
// base64 of that image, the schema JSON text and the legacy JSON nova-sprint's
// take --json writes today (one and the same text, since the schema mirrors the
// take shape), while every writer still writes the legacy form; a malformed
// value of each form (every strict prefix, every flipped byte, a clamped value)
// is refused by name (`TAKE_OUT REFUSED: <reason>; run: nova-swarm member -h`)
// and never read as absent, empty or zero; reads are counted per form and
// refusals per reason.
//
// Unit tier: no clock, no socket, no program, no store.
func TestMemberTakeAnswerDualRead(t *testing.T) {
	t.Parallel()

	r := takeReader()
	takes := map[string]takeOut{
		"one": {Packets: []Packet{
			{Card: "c1", Kind: "work", As: "m", Primary: "p-c1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 7, Branch: "work/c1", Notes: []string{}},
		}},
		"two": {Packets: []Packet{
			{Card: "c1", Kind: "work", As: "m", Primary: "p-c1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 7, Branch: "work/c1", Notes: []string{}},
			{Card: "c2", Kind: "read", As: "m", Primary: "p-c2", Stream: "a", Attempt: 1, Gen: 1, Epoch: 8, Branch: "work/c2", Notes: []string{}},
		}},
	}
	images := map[string]map[string][]byte{}
	for name, want := range takes {
		forms := map[string][]byte{
			"binary": takeWrite(t, r, want, schemaread.FormBinary),
			"base64": takeWrite(t, r, want, schemaread.FormBase64),
			// the schema JSON text, compacted to the one line text reader sees
			"json": takeCompact(t, takeWrite(t, r, want, schemaread.FormJSON)),
			// the legacy form is the JSON nova-sprint writes today, and it still does
			"legacy": takeLegacyFixture(t, want),
		}
		images[name] = forms
		formOf := map[string]schemaread.Form{
			"binary": schemaread.FormBinary, "base64": schemaread.FormBase64,
			"json": schemaread.FormJSON, "legacy": schemaread.FormJSON,
		}
		for form, body := range forms {
			got, res, err := r.Read(body)
			require.NoError(t, err, "%s %s", name, form)
			assert.Equal(t, formOf[form], res.Form, "%s %s", name, form)
			assert.Equal(t, want, got, "%s %s", name, form)
		}
	}
	// binary, base64 and json each read once per take; the legacy fixture reads as json.
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(4), r.Counts().Reads(schemaread.FormJSON))

	// Every strict prefix of every form is refused by name, and every flipped byte is
	// refused by name or reads as a whole take: never as absent, empty or zero.
	bad := takeReader()
	refused := uint64(0)
	for form, body := range images["one"] {
		for n := 0; n < len(body); n++ {
			_, _, err := bad.Read(body[:n])
			takeRefused(t, err, "one %s prefix %d", form, n)
			refused++
		}
	}
	for name, forms := range images {
		for form, body := range forms {
			for i := range body {
				for _, x := range []byte{0x01, 0xff} {
					flipped := bytes.Clone(body)
					flipped[i] ^= x
					got, _, err := bad.Read(flipped)
					if err != nil {
						takeRefused(t, err, "%s %s flip %d^%#x", name, form, i, x)
						refused++
						continue
					}
					assert.NotEqual(t, takeOut{}, got, "%s %s flip %d^%#x", name, form, i, x)
					assert.NoError(t, bad.Check(&got), "%s %s flip %d^%#x read %+v", name, form, i, x, got)
				}
			}
		}
	}

	// A clamped value is refused: a card over its 160-byte bound in the JSON and
	// legacy forms.
	clamped := "{\"packets\":[{\"card\":\"" + strings.Repeat("x", 200) + "\",\"kind\":\"work\",\"as\":\"m\"}]}"
	for form, body := range map[string][]byte{
		"json":   []byte(clamped),
		"legacy": []byte(clamped),
	} {
		_, _, err := bad.Read(body)
		reason := takeRefused(t, err, "clamped %s", form)
		assert.Equal(t, schemaread.ReasonClamped, reason, form)
		refused++
	}

	var total uint64
	for k, v := range bad.Counts().Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			total += v
		}
	}
	assert.Equal(t, refused, total, "every refusal is counted by reason: %v", bad.Counts().Snapshot())
	assert.NotZero(t, bad.Counts().Reads(schemaread.FormBinary))
	assert.NotZero(t, bad.Counts().Reads(schemaread.FormBase64))
	assert.NotZero(t, bad.Counts().Reads(schemaread.FormJSON))
}

// FuzzMemberTakeAnswerRead is the fuzzed malformed value of the take record:
// whatever the bytes, the strict read refuses them by name or reads a take that
// passes its own checks; never a zero take.
func FuzzMemberTakeAnswerRead(f *testing.F) {
	r := takeReader()
	want := takeOut{Packets: []Packet{{Card: "c1", Kind: "work", As: "m", Attempt: 1, Epoch: 7}}}
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		b, err := r.Write(&want, form)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add(takeLegacyFixture(f, want))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		got, _, err := r.Read(in)
		if err != nil {
			takeRefused(t, err, "fuzzed %q", in)
			return
		}
		assert.NotEqual(t, takeOut{}, got)
		assert.NoError(t, r.Check(&got))
	})
}

// takeWrite is want written by the strict writer in form.
func takeWrite(t *testing.T, r *schemaread.Reader[takeOut], want takeOut, form schemaread.Form) []byte {
	t.Helper()
	b, err := r.Write(&want, form)
	require.NoError(t, err)
	require.NotEmpty(t, b)
	return b
}

// takeRefused asserts err is the take's refusal by name and returns its reason.
func takeRefused(t *testing.T, err error, msg string, args ...any) string {
	t.Helper()
	var ref *schemaread.Refusal
	if !assert.True(t, errors.As(err, &ref), append([]any{msg + ": not a refusal: %v"}, append(args, err)...)...) {
		return ""
	}
	assert.Equal(t, "TAKE_OUT", ref.Name)
	assert.Regexp(t, takeRefusalLine, err.Error())
	return ref.Reason
}

// takeLegacyFixture is want as the legacy JSON nova-sprint's take --json writes
// today.
func takeLegacyFixture(t interface {
	Helper()
	Fatalf(string, ...any)
}, want takeOut) []byte {
	t.Helper()
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal legacy take: %v", err)
	}
	return b
}

// takeCompact is the schema JSON text b on one line.
func takeCompact(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, json.Compact(&out, b))
	return out.Bytes()
}
