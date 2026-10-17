package member

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// takeRefusalLine is the shape of every refusal of the take answer record.
var takeRefusalLine = regexp.MustCompile(`^TAKE_ANSWER REFUSED: [a-z_]+( \([a-z]+\))?; run: nova-sprint take --json$`)

// TestMemberTakeAnswerDualRead is the reader half of the take answer's table adoption
// (v1.5 aspect 11; the owner's rules S2 and S4): the take answer (takeOut) Member.take
// reads (member.go, take) is read alike from its binary table image, the padded standard
// base64 of that image, the schema JSON text and the legacy text nova-sprint's take
// prints today, while the writer still prints the legacy form; a malformed value of each
// form (every strict prefix, every flipped byte, a clamped value) is refused by name
// (`TAKE_ANSWER REFUSED: <reason>; run: nova-sprint take --json`) and never read as
// absent, empty or zero; and reads are counted per form.
//
// Unit tier: no clock, no socket, no program, no store.
func TestMemberTakeAnswerDualRead(t *testing.T) {
	t.Parallel()

	r := takeReader()
	want := takeOut{Packets: []Packet{withNotes(pk("c1")), withNotes(pk("c2"))}}
	// the legacy text is the same JSON nova-sprint's take prints today
	forms := map[string][]byte{
		"binary": takeWrite(t, r, want, schemaread.FormBinary),
		"base64": takeWrite(t, r, want, schemaread.FormBase64),
		"json":   takeWrite(t, r, want, schemaread.FormJSON),
		"legacy": []byte(takeJSON(t, withNotes(pk("c1")), withNotes(pk("c2")))),
	}
	for form, body := range forms {
		got, res, err := r.Read(body)
		require.NoError(t, err, "%s", form)
		assert.Equal(t, want, got, "%s", form)
		switch form {
		case "binary":
			assert.Equal(t, schemaread.FormBinary, res.Form, form)
		case "base64":
			assert.Equal(t, schemaread.FormBase64, res.Form, form)
		default:
			assert.Equal(t, schemaread.FormJSON, res.Form, form)
		}
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormJSON))

	// Every strict prefix of the binary image is refused by name, and every flipped byte
	// of every form is refused by name or reads as a whole take: never as absent, empty
	// or zero.
	bad := takeReader()
	refused := uint64(0)
	b := forms["binary"]
	for n := 0; n < len(b); n++ {
		_, _, err := bad.Read(b[:n])
		takeRefused(t, err, "binary prefix %d", n)
		refused++
	}
	for form, body := range forms {
		for i := range body {
			for _, x := range []byte{0x01, 0xff} {
				flipped := bytes.Clone(body)
				flipped[i] ^= x
				got, _, err := bad.Read(flipped)
				if err != nil {
					takeRefused(t, err, "%s flip %d^%#x", form, i, x)
					refused++
					continue
				}
				assert.NotEqual(t, takeOut{}, got, "%s flip %d^%#x", form, i, x)
			}
		}
	}

	// A clamped value (a packet attempt over the table's max of 100000) is refused in
	// the text forms, which the reader parses directly. The binary forms cannot carry one:
	// the writer's own path builds them through the JSON reader, which clamps the value
	// before it is saved, so a binary image always holds an in-range value.
	clamped := takeOut{Packets: []Packet{{Card: "c", Kind: "work", As: "m", Primary: "p-c", Stream: "a", Attempt: 200000, Gen: 1, Epoch: 7, Notes: []string{}}}}
	cj := takeWrite(t, r, clamped, schemaread.FormJSON)
	for form, body := range map[string][]byte{
		"json":   cj,
		"legacy": cj,
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

// withNotes is p with a non-nil empty notes list, so its JSON text carries an array,
// never null.
func withNotes(p Packet) Packet {
	p.Notes = []string{}
	return p
}

// takeWrite is want written by the strict writer in form.
func takeWrite(t *testing.T, r *schemaread.Reader[takeOut], want takeOut, form schemaread.Form) []byte {
	t.Helper()
	b, err := r.Write(&want, form)
	require.NoError(t, err)
	require.NotEmpty(t, b)
	return b
}

// takeRefused asserts err is the take answer's refusal by name and returns its reason.
func takeRefused(t *testing.T, err error, msg string, args ...any) string {
	t.Helper()
	var ref *schemaread.Refusal
	if !assert.True(t, errors.As(err, &ref), append([]any{msg + ": not a refusal: %v"}, append(args, err)...)...) {
		return ""
	}
	assert.Equal(t, "TAKE_ANSWER", ref.Name)
	assert.Regexp(t, takeRefusalLine, err.Error())
	return ref.Reason
}
