package member

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// dualSha is a full sha-1 head for the carry records below.
const dualSha = "0123456789abcdef0123456789abcdef01234567"

// carryRefusalLine is the shape of every refusal of the carry record.
var carryRefusalLine = regexp.MustCompile(`^MEMBER_CARRY REFUSED: [a-z_]+( \([a-z]+\))?; run: nova-swarm member -h$`)

// TestMemberOwnRecordsDualRead is the reader half of the member's own record's table
// adoption (v1.5 aspect 13; the owner's rules S2 and S4): the CARRY: record pkg/member
// writes into a brief header (CarryLine) and reads back (CarryOf) is read alike from its
// binary table image, the padded standard base64 of that image, the schema JSON text and
// the legacy words CarryLine writes today, while CarryLine still writes the legacy form; a
// malformed value of each form (every strict prefix, every flipped byte, a clamped
// attempt) is refused by name (`MEMBER_CARRY REFUSED: <reason>; run: nova-swarm member
// -h`) and never read as absent, empty or zero; and reads are counted per form and
// refusals per reason. The queue and take answers pkg/member parses are nova-sprint's
// (aspect 11) and are not read here.
//
// Unit tier: no clock, no socket, no program, no store. Files are read from the
// t.TempDir() the test writes.
func TestMemberOwnRecordsDualRead(t *testing.T) {
	t.Parallel()

	r := carryReader()
	dir := t.TempDir()
	carries := map[string]Carry{
		"widen":     {Card: "recut-widen-r.w1", Attempt: 2, Head: dualSha},
		"finish-pr": {Head: dualSha, Branch: "sprint/a13-defs-swarm.w4.g1.e21"},
	}
	images := map[string]map[string][]byte{}
	for name, want := range carries {
		forms := map[string][]byte{
			"binary": carryWrite(t, r, want, schemaread.FormBinary),
			"base64": carryWrite(t, r, want, schemaread.FormBase64),
			// the schema JSON text, compacted to the one line a brief header holds
			"json": carryCompact(t, carryWrite(t, r, want, schemaread.FormJSON)),
			// the legacy form is what the writer writes today, and it still does
			"legacy": []byte(strings.TrimPrefix(CarryLine(want), CarryKey+" ")),
		}
		images[name] = forms
		for form, body := range forms {
			path := filepath.Join(dir, name+"."+form)
			if form == "binary" {
				// the binary image is a file of its own: a brief header is lines of text
				require.NoError(t, os.WriteFile(path, body, 0o600))
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				got, res, err := r.Read(raw)
				require.NoError(t, err, "%s %s", name, form)
				assert.Equal(t, schemaread.FormBinary, res.Form, "%s %s", name, form)
				assert.Equal(t, want, got, "%s %s", name, form)
				continue
			}
			brief := "RESULT: s1-1 sha=000000000000\n" + CarryKey + " " + string(body) + "\n\nprose\n"
			require.NoError(t, os.WriteFile(path, []byte(brief), 0o600))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			got, found, err := carryOf(string(raw), r)
			require.NoError(t, err, "%s %s: %q", name, form, body)
			assert.True(t, found, "%s %s", name, form)
			assert.Equal(t, want, got, "%s %s", name, form)
			// the binary's own door reads it the same way
			got2, ok := CarryOf(string(raw))
			assert.True(t, ok, "%s %s", name, form)
			assert.Equal(t, want, got2, "%s %s", name, form)
		}
	}
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormJSON))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormLegacy))

	// No carry line is no carry, and is not a refusal.
	_, found, err := carryOf("RESULT: s1-1 sha=000000000000\n\nprose\n", r)
	require.NoError(t, err)
	assert.False(t, found)

	// Every strict prefix of every form of the widen carry is refused by name, and every
	// flipped byte of every form of both carries is refused by name or reads as a whole
	// carry: never as absent, empty or zero.
	bad := carryReader()
	refused := uint64(0)
	for form, body := range images["widen"] {
		for n := 0; n < len(body); n++ {
			_, _, err := bad.Read(body[:n])
			carryRefused(t, err, "widen %s prefix %d", form, n)
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
						carryRefused(t, err, "%s %s flip %d^%#x", name, form, i, x)
						refused++
						continue
					}
					assert.NotEqual(t, Carry{}, got, "%s %s flip %d^%#x", name, form, i, x)
					assert.True(t, len(got.Head) == len(dualSha), "%s %s flip %d^%#x read %+v", name, form, i, x, got)
				}
			}
		}
	}

	// A clamped attempt (under the table's min of 0) is refused in every form; the legacy
	// words never held a negative attempt, and parseCarry refuses one.
	clampedWire := carryNegativeWire(t)
	for form, body := range map[string][]byte{
		"binary": clampedWire,
		"base64": []byte(base64Std(clampedWire)),
		"json":   []byte(`{"card":"c","attempt":-1,"head":"` + dualSha + `"}`),
		"legacy": []byte("c attempt -1 head=" + dualSha),
	} {
		_, _, err := bad.Read(body)
		reason := carryRefused(t, err, "clamped %s", form)
		refused++
		if form == "legacy" {
			assert.Equal(t, schemaread.ReasonMalformed, reason, form)
		} else {
			assert.Equal(t, schemaread.ReasonClamped, reason, form)
		}
	}
	// And CarryOf never reads a refused line as a carry.
	_, ok := CarryOf("RESULT: c\n" + CarryKey + " " + string(images["widen"]["base64"][:8]) + "\n")
	assert.False(t, ok)

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
	assert.NotZero(t, bad.Counts().Reads(schemaread.FormLegacy))
}

// FuzzMemberCarryRead is the fuzzed malformed value of the carry record: whatever the
// bytes, the strict read refuses them by name or reads a carry that passes its own checks;
// never a zero carry. Its seeds (every form of a carry) run with the unit suite.
func FuzzMemberCarryRead(f *testing.F) {
	r := carryReader()
	want := Carry{Card: "recut-widen-r.w1", Attempt: 2, Head: dualSha, Branch: "sprint/x"}
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		b, err := r.Write(&want, form)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(strings.TrimPrefix(CarryLine(want), CarryKey+" ")))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		got, _, err := r.Read(in)
		if err != nil {
			carryRefused(t, err, "fuzzed %q", in)
			return
		}
		assert.NotEqual(t, Carry{}, got)
		assert.NoError(t, r.Check(&got))
	})
}

// carryWrite is want written by the strict writer in form.
func carryWrite(t *testing.T, r *schemaread.Reader[Carry], want Carry, form schemaread.Form) []byte {
	t.Helper()
	b, err := r.Write(&want, form)
	require.NoError(t, err)
	require.NotEmpty(t, b)
	return b
}

// carryRefused asserts err is the carry's refusal by name and returns its reason.
func carryRefused(t *testing.T, err error, msg string, args ...any) string {
	t.Helper()
	var ref *schemaread.Refusal
	if !assert.True(t, errors.As(err, &ref), append([]any{msg + ": not a refusal: %v"}, append(args, err)...)...) {
		return ""
	}
	assert.Equal(t, "MEMBER_CARRY", ref.Name)
	assert.Regexp(t, carryRefusalLine, err.Error())
	return ref.Reason
}

// carryNegativeWire is a MemberCarry table image whose attempt is -1. The generated
// writer refuses a value past a bound, so the image of an attempt of a marker value is
// written and the marker's eight little-endian bytes, which occur once, are replaced by
// those of -1.
func carryNegativeWire(t *testing.T) []byte {
	t.Helper()
	const marker = 0x0123456789abcd
	wire := carryWrite(t, carryReader(), Carry{Card: "c", Attempt: marker, Head: dualSha}, schemaread.FormBinary)
	from, to := binary.LittleEndian.AppendUint64(nil, marker), binary.LittleEndian.AppendUint64(nil, ^uint64(0))
	require.Equal(t, 1, bytes.Count(wire, from))
	return bytes.Replace(wire, from, to, 1)
}

// carryCompact is the schema JSON text b on one line.
func carryCompact(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, json.Compact(&out, b))
	return out.Bytes()
}

// base64Std is the padded standard base64 of b.
func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
