package friend

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendWorkerViewDualRead pins the worker view reader (ParseView, the one
// read site of the worker-answer tables) reading every form: the binary wire,
// the padded standard base64 of it, the schema JSON text, and the legacy form
// the sprint server writes (nova-sprint view worker --json). Each reads back the
// same cards; a malformed, damaged or clamped value of a form is refused by name
// (WORKER VIEW REFUSED: <reason>; run: nova-sprint view worker --as <friend>
// --json), never read as absent, empty or zero, and a read is counted per form.
func TestFriendWorkerViewDualRead(t *testing.T) {
	t.Parallel()

	legacy := viewOf("friend-a",
		workCard("taken.w2", "working"),
		workCard("dealt.w3", "ready"))

	legacyBefore := workerViewReads.Counts().Snapshot()
	doc, res, err := workerViewReads.Read([]byte(legacy))
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormLegacy, res.Form)
	legacyAfter := workerViewReads.Counts().Snapshot()
	assert.Equal(t, uint64(1), legacyAfter["legacy"]-legacyBefore["legacy"], "the legacy read is counted under the legacy form")

	bin, err := workerViewReads.Write(&doc, schemaread.FormBinary)
	require.NoError(t, err)
	js, err := workerViewReads.Write(&doc, schemaread.FormJSON)
	require.NoError(t, err)
	b64 := []byte(base64.StdEncoding.EncodeToString(bin))

	want := []HeldCard{
		{Card: "taken.w2", Job: "taken.w2~15", Col: "working", Why: ViewWhy},
		{Card: "dealt.w3", Job: "dealt.w3~15", Col: "ready", Why: ViewWhy},
	}
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"binary", string(bin)},
		{"base64", string(b64)},
		{"json", string(js)},
		{"legacy", legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cards, err := ParseView("friend-a", tc.in)
			require.NoError(t, err)
			assert.Equal(t, want, cards)
		})
	}

	clamped := `{"view":"` + strings.Repeat("w", 40) + `","as":"friend-a","kind":"friend","epoch":21,"cards":[]}`
	for _, m := range []struct {
		name string
		form schemaread.Form
		in   []byte
	}{
		{"binary truncated", schemaread.FormBinary, bin[:8]},
		{"binary flipped", schemaread.FormBinary, flipByte(bin, 12)},
		{"base64 truncated", schemaread.FormBase64, b64[:5]},
		{"base64 bad char", schemaread.FormBase64, append(append([]byte(nil), b64...), '!')},
		{"json truncated", schemaread.FormJSON, js[:10]},
		{"json damaged", schemaread.FormJSON, append(js, '}')},
		{"legacy unbalanced", schemaread.FormLegacy, []byte(strings.TrimSuffix(legacy, "]}") + "]")},
		{"json clamped", schemaread.FormJSON, []byte(clamped)},
	} {
		t.Run("refused "+m.name, func(t *testing.T) {
			before := workerViewReads.Counts().Reads(m.form)
			z, res, err := workerViewReads.Read(m.in)
			require.Error(t, err, "a damaged or clamped value is refused, never read as absent, empty or zero")
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref)
			assert.Contains(t, ref.Error(), "WORKER VIEW REFUSED:")
			assert.Contains(t, ref.Error(), "; run: "+ViewHelp)
			assert.Equal(t, m.form, res.Form)
			assert.Zero(t, z.v, "a refused read leaves no value")
			assert.Equal(t, before+1, workerViewReads.Counts().Reads(m.form), "the refusal is counted under the form read")
		})
	}
}

func flipByte(b []byte, i int) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= 0xff
	return out
}
