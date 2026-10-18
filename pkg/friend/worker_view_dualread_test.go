package friend

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendWorkerViewDualRead pins the worker view reader (ParseView) reading
// every form, S1 and S2: one value written as the binary wire, as its padded
// standard base64, as the schema JSON text form, and as the legacy JSON the
// sprint server writes today (viewOf, its extra keys included) reads back
// equal, each read counted under its form (UNION.md C13). A truncated, a
// flipped and a clamped value of each form, and a fuzzed malformed value of
// each form, are refused by name (<RECORD> REFUSED: <reason>; run: <help>),
// never read as absent, empty or zero, and each refusal is counted under its
// reason. nova-sprint is never run: its answers are the fixtures here.
func TestFriendWorkerViewDualRead(t *testing.T) {
	t.Parallel()

	want := []HeldCard{
		{Card: "a11", Job: "a11.w1~21", Col: "working", Why: ViewWhy},
		{Card: "a12", Job: "a12.w1~21", Col: "holding", Why: ViewWhy},
	}
	legacy := []byte(viewOf("rowan", want...))
	binary := workerViewImage(t, "rowan", want, 0, false)
	b64 := []byte(base64.StdEncoding.EncodeToString(binary))
	jsonForm := workerViewImage(t, "rowan", want, 0, true)
	require.Equal(t, byte(1), binary[0], "the wire leads with its form byte")

	// every form reads to the one value, counted under its form; the legacy
	// text is JSON, so it is counted under the JSON form
	r := newWorkerViewReader()
	for _, tc := range []struct {
		name string
		in   []byte
		form schemaread.Form
	}{
		{"binary", binary, schemaread.FormBinary},
		{"base64", b64, schemaread.FormBase64},
		{"json", jsonForm, schemaread.FormJSON},
		{"legacy", legacy, schemaread.FormJSON},
		{"legacy with the newline the server ends on", append(append([]byte(nil), legacy...), '\n'), schemaread.FormJSON},
	} {
		assert.Equal(t, tc.form, r.Detect(tc.in), tc.name)
		got, err := parseView(r, "rowan", string(tc.in))
		require.NoError(t, err, tc.name)
		assert.Equal(t, want, got, tc.name)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary), "binary reads counted")
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64), "base64 reads counted")
	assert.Equal(t, uint64(3), r.Counts().Reads(schemaread.FormJSON), "json reads counted: the json form and the legacy text twice")
	assert.Equal(t, uint64(0), r.Counts().Reads(schemaread.FormLegacy))
	assert.Greater(t, r.Counts().Unknown(), uint64(0), "the legacy keys the table does not hold are tolerated and counted")

	// ParseView itself reads through the package's one worker view reader
	before := workerViewReader.Counts().Reads(schemaread.FormBinary)
	got, err := ParseView("rowan", string(binary))
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Greater(t, workerViewReader.Counts().Reads(schemaread.FormBinary), before)

	// a truncated, a flipped and a clamped value of each form is refused by
	// name with its reason
	many := make([]HeldCard, 1025)
	for i := range many {
		many[i] = HeldCard{Card: "c", Job: "c.w1~21", Col: "working", Why: ViewWhy}
	}
	clampedWire := workerViewImage(t, "rowan", many, 0, false)
	clampedJSON := workerViewImage(t, "rowan", many, 0, true)
	longAs := strings.Repeat("a", 65)
	badUTF8 := append([]byte(nil), binary...)
	at := bytes.Index(badUTF8, []byte("working"))
	require.Greater(t, at, 0)
	badUTF8[at] = 0xff

	refused := map[string]uint64{}
	for _, tc := range []struct {
		name, in, reason, field string
	}{
		{"binary truncated", string(binary[:len(binary)-1]), schemaread.ReasonWireDamaged, ""},
		{"binary cut to its form byte", string(binary[:1]), schemaread.ReasonWireDamaged, ""},
		{"binary flipped form byte", string(flip(binary, 0, 0x01)), schemaread.ReasonUnknownForm, ""},
		{"binary flipped to a byte that is no UTF-8", string(badUTF8), schemaread.ReasonMalformed, ""},
		{"binary past the cards ceiling", string(clampedWire), schemaread.ReasonClamped, "cards"},
		{"base64 truncated", string(b64[:len(b64)-2]), schemaread.ReasonMalformed, ""},
		{"base64 of a cut wire", base64.StdEncoding.EncodeToString(binary[:len(binary)-3]), schemaread.ReasonWireDamaged, ""},
		{"base64 flipped to a byte that is no base64", string(flip(b64, len(b64)/2, '!'^b64[len(b64)/2])), schemaread.ReasonMalformed, ""},
		{"base64 flipped form byte", base64.StdEncoding.EncodeToString(flip(binary, 0, 0x01)), schemaread.ReasonUnknownForm, ""},
		{"base64 past the cards ceiling", base64.StdEncoding.EncodeToString(clampedWire), schemaread.ReasonClamped, "cards"},
		{"json truncated", string(bytes.TrimSpace(jsonForm)[:len(bytes.TrimSpace(jsonForm))-1]), schemaread.ReasonMalformed, ""},
		{"json flipped to a control byte", string(flip(jsonForm, len(jsonForm)/2, 0x01^jsonForm[len(jsonForm)/2])), schemaread.ReasonMalformed, ""},
		{"json with a key given twice", `{"view":"worker","view":"worker","as":"rowan","kind":"friend"}`, schemaread.ReasonMalformed, ""},
		{"json past the cards ceiling", string(clampedJSON), schemaread.ReasonClamped, "cards"},
		{"json as past 64 bytes", string(workerViewJSONWithAs(t, jsonForm, longAs)), schemaread.ReasonClamped, ""},
		{"legacy truncated", string(legacy[:len(legacy)-5]), schemaread.ReasonMalformed, ""},
		{"legacy flipped to a control byte", string(flip(legacy, len(legacy)/2, 0x01^legacy[len(legacy)/2])), schemaread.ReasonMalformed, ""},
		{"legacy as past 64 bytes", viewOf(longAs, want...), schemaread.ReasonClamped, ""},
		{"legacy kind past 32 bytes", strings.Replace(string(legacy), `"kind":"friend"`, `"kind":"`+strings.Repeat("k", 33)+`"`, 1), schemaread.ReasonClamped, ""},
		{"legacy with no view", strings.Replace(string(legacy), `"view":"worker",`, ``, 1), schemaread.ReasonAbsentRequired, "view"},
		{"empty", "", schemaread.ReasonUnknownForm, ""},
	} {
		got, err := parseView(r, "rowan", tc.in)
		assertRefused(t, tc.name, got, err, tc.reason, tc.field)
		refused[tc.reason]++
	}
	assertRefusedCounts(t, r, refused)

	// fuzz: a seeded loop of malformed values of each form, each malformed by
	// construction, is refused by name every time and never read as absent,
	// empty or zero
	rng := rand.New(rand.NewPCG(1000, 21))
	malformed := map[string]func() []byte{
		"binary": func() []byte {
			switch rng.IntN(3) {
			case 0: // a strict prefix
				return append([]byte(nil), binary[:rng.IntN(len(binary))]...)
			case 1: // an unknown form byte, the body flipped at random
				out := flip(binary, 0, byte(1+rng.IntN(255)))
				for range 1 + rng.IntN(4) {
					i := 1 + rng.IntN(len(out)-1)
					out[i] ^= byte(1 + rng.IntN(255))
				}
				return out
			default: // a byte of a string flipped to one that is no UTF-8
				out := append([]byte(nil), binary...)
				out[at+rng.IntN(len("working"))] = byte(0x80 + rng.IntN(0x40))
				return out
			}
		},
		"base64": func() []byte {
			if rng.IntN(2) == 0 { // a strict prefix
				return append([]byte(nil), b64[:rng.IntN(len(b64))]...)
			}
			// a byte flipped to one no base64 alphabet holds
			out := append([]byte(nil), b64...)
			out[rng.IntN(len(out))] = "!#$%&*,.:;<>?@^_`|~-"[rng.IntN(20)]
			return out
		},
		"json":   func() []byte { return malformedText(rng, jsonForm) },
		"legacy": func() []byte { return malformedText(rng, legacy) },
	}
	for _, form := range []string{"binary", "base64", "json", "legacy"} {
		for i := range 300 {
			in := malformed[form]()
			got, err := parseView(r, "rowan", string(in))
			assertRefused(t, fmt.Sprintf("%s fuzz %d %q", form, i, in), got, err, "", "")
			var rf *schemaread.Refusal
			if errors.As(err, &rf) {
				refused[rf.Reason]++
			}
		}
	}
	assertRefusedCounts(t, r, refused)
}

// malformedText is a malformed value of a JSON text by construction: a strict
// prefix (its closing brace is gone), or the text with a raw control byte put
// in, which JSON holds nowhere.
func malformedText(rng *rand.Rand, text []byte) []byte {
	text = bytes.TrimSpace(text)
	if rng.IntN(2) == 0 {
		return append([]byte(nil), text[:rng.IntN(len(text))]...)
	}
	out := append([]byte(nil), text...)
	out[rng.IntN(len(out))] = byte(1 + rng.IntN(8))
	return out
}

// assertRefused checks one refusal: the typed error, by the record's name and
// the reason, the help it points at, and no value read.
func assertRefused(t *testing.T, name string, got []HeldCard, err error, reason, field string) {
	t.Helper()
	require.Error(t, err, name)
	assert.Nil(t, got, "%s: a refused read gives no cards", name)
	var rf *schemaread.Refusal
	require.ErrorAs(t, err, &rf, "%s: %v", name, err)
	assert.Equal(t, "WorkerView", rf.Name, name)
	assert.NotEmpty(t, rf.Reason, name)
	assert.Contains(t, err.Error(), "WorkerView REFUSED: "+rf.Reason, name)
	assert.True(t, strings.HasSuffix(err.Error(), "; run: nova-sprint view worker --help"), "%s: %v", name, err)
	if reason != "" {
		assert.Equal(t, reason, rf.Reason, "%s: %v", name, err)
		assert.Equal(t, field, rf.Field, "%s: %v", name, err)
	}
}

// assertRefusedCounts checks the reader counted every refusal under its reason.
func assertRefusedCounts(t *testing.T, r *schemaread.Reader[workerViewRead], want map[string]uint64) {
	t.Helper()
	got := map[string]uint64{}
	for k, v := range r.Counts().Snapshot() {
		if reason, ok := strings.CutPrefix(k, "refused_"); ok {
			got[reason] = v
		}
	}
	assert.Equal(t, want, got, "refusals are counted per reason")
}

// workerViewImage builds the worker view for friend with cards and returns
// its binary wire image, or its schema JSON text image when asJSON is set:
// what the generated writer writes.
func workerViewImage(t *testing.T, friend string, cards []HeldCard, epoch uint64, asJSON bool) []byte {
	t.Helper()
	var b answerwire.WorkerViewBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	root := b.GetRoot()
	setStr(root.View[:], &root.ViewLength, "worker")
	setStr(root.AsName[:], &root.AsNameLength, friend)
	setStr(root.Kind[:], &root.KindLength, "friend")
	root.Epoch = epoch
	for _, c := range cards {
		row := answerwire.WorkerViewCardsAdd(&b.Main, &root.Cards)
		require.NotNil(t, row)
		setStr(row.Id[:], &row.IdLength, c.Card)
		setStr(row.St[:], &row.StLength, c.Col)
		setStr(row.Brief[:], &row.BriefLength, "~/"+friend+"-working/inbox/"+c.Job+"/BRIEF.md")
	}
	require.True(t, b.Lock())
	row := b.AsConst()
	require.NotNil(t, row)
	if asJSON {
		n := answerwire.WorkerViewToJsonMeasure(row)
		require.True(t, n > 0)
		buf := make([]byte, n)
		require.Equal(t, n, answerwire.WorkerViewToJson(row, buf))
		return buf
	}
	n := answerwire.WorkerViewMeasure(row)
	require.True(t, n > 0)
	buf := make([]byte, n)
	require.Equal(t, n, answerwire.WorkerViewSave(row, buf))
	return buf
}

// workerViewJSONWithAs is the JSON text with its "as" value replaced by as, a
// value the generated writer cannot write past its bound.
func workerViewJSONWithAs(t *testing.T, text []byte, as string) []byte {
	t.Helper()
	out := bytes.Replace(text, []byte(`"as": "rowan"`), []byte(`"as": "`+as+`"`), 1)
	require.NotEqual(t, text, out, "the JSON text names its as")
	return out
}

func setStr(dst []byte, length *int32, s string) {
	n := copy(dst, s)
	*length = int32(n)
}

// flip is b with the byte at i xor-ed with x.
func flip(b []byte, i int, x byte) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= x
	return out
}
