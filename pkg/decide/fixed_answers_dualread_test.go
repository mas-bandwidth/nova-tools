package decide_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestFixedAnswersDualRead is the reader half of the fixed answers file's table
// adoption (v1.5 aspect 13; the owner's rules S2 and S4): the file ParseFixed
// reads is read alike from its binary image (decidewire FixedTable), the padded
// standard base64 of that image, the schema JSON text form (a list of named
// rows) and the legacy form people write today (a map keyed by question); a
// damaged or clamped value of each form is refused by name
// (`FIXED_ANSWERS REFUSED: <reason>; run: <help>`), never read as absent,
// empty or zero; and each read is counted under the form it arrived in.
func TestFixedAnswersDualRead(t *testing.T) {
	t.Parallel()
	want := fixedSample()
	wire := fixedImage(t, want)
	text := fixedJSONText(t, wire)
	forms := []formInput{
		{"binary", wire},
		{"base64", []byte(base64.StdEncoding.EncodeToString(wire))},
		{"json", text},
		{"legacy", fixedLegacy},
	}
	formOf := map[string]schemaread.Form{"binary": schemaread.FormBinary, "base64": schemaread.FormBase64, "json": schemaread.FormJSON, "legacy": schemaread.FormLegacy}

	// Each form, written to a file and read back through ParseFixed as the
	// tools read it (os.ReadFile, then ParseFixed), is the one Fixed value.
	dir := t.TempDir()
	for _, in := range forms {
		path := filepath.Join(dir, "answers-"+in.form)
		require.NoError(t, os.WriteFile(path, in.in, 0o600))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		got, err := decide.ParseFixed(raw)
		require.NoError(t, err, "the %s answers file reads", in.form)
		assert.Equal(t, want, got, "the answers file reads alike from its %s", in.form)
	}

	// Every form is counted once under its own form.
	rd := decide.NewFixedReader()
	for _, in := range forms {
		got, res, err := rd.Read(in.in)
		require.NoError(t, err, in.form)
		assert.Equal(t, formOf[in.form], res.Form, "%s is read as its own form", in.form)
		assert.Equal(t, want, got, in.form)
	}
	for _, in := range forms {
		assert.Equal(t, uint64(1), rd.Counts().Reads(formOf[in.form]), "one %s read counted", in.form)
	}
	assert.Zero(t, refusals(rd.Counts()))

	// The legacy file after leading white space is still the legacy form, as
	// the json decoder read it; a legacy file whose first question is named
	// answers is still the legacy form, not the table's JSON text.
	rd = decide.NewFixedReader()
	for _, pre := range whiteSpacePrefixes {
		got, err := decide.ParseFixed(append([]byte(pre), fixedLegacy...))
		require.NoError(t, err, "legacy after %q", pre)
		assert.Equal(t, want, got)
		_, res, err := rd.Read(append([]byte(pre), fixedLegacy...))
		require.NoError(t, err)
		assert.Equal(t, schemaread.FormLegacy, res.Form)
	}
	named, err := decide.ParseFixed([]byte(`{"answers":{"noul":0.5}}`))
	require.NoError(t, err)
	require.NotNil(t, named.Table["answers"].Noul)
	assert.InDelta(t, 0.5, *named.Table["answers"].Noul, 0)

	// A truncated, a flipped-byte and an appended value of every form is
	// refused by name and counted under the form it arrived in. The JSON text
	// is cut no shorter than its first key and list, which is what names the
	// form; a shorter cut is a legacy text, refused as the legacy read refuses.
	rd = decide.NewFixedReader()
	reads := map[schemaread.Form]uint64{}
	var refused uint64
	for _, in := range forms {
		corpus := damage(in.form, in.in)
		if in.form == "json" {
			corpus = fixedJSONDamage(in.in)
		}
		for _, m := range corpus {
			got, res, err := rd.Read(m)
			require.NotNil(t, refusal(t, err, "FIXED_ANSWERS"), "a damaged %s (%d bytes) was read as %v", in.form, len(m), got)
			assert.Equal(t, decide.Fixed{}, got, "a refused %s hands back nothing", in.form)
			assert.Equal(t, formOf[in.form], res.Form, "a damaged %s is counted as %s", in.form, in.form)
			reads[formOf[in.form]]++
			refused++
			_, err = decide.ParseFixed(m)
			require.NotNil(t, refusal(t, err, "FIXED_ANSWERS"), "ParseFixed refuses a damaged %s by name", in.form)
		}
	}
	for f, n := range reads {
		assert.Equal(t, n, rd.Counts().Reads(f), "every damaged %s read is counted", f)
	}
	assert.Equal(t, refused, refusals(rd.Counts()), "every damaged read is refused, by reason")

	// An unknown key is refused in the JSON text as in the legacy file (the
	// base read refused it there): a damaged key never reads as an absent field.
	for _, in := range [][]byte{
		[]byte(`{"defect":{"yes":1}}`),
		[]byte(`{"answers":[{"question":"A","answer":{"chioce":"x"}}]}`),
		[]byte(`{"answers":[{"question":"A","answer":{"choice":"x"}}],"extra":1}`),
	} {
		_, err := decide.ParseFixed(in)
		require.NotNil(t, refusal(t, err, "FIXED_ANSWERS"), "%s", in)
	}
	_, err = decide.ParseFixed([]byte(`{"defect":{"yes":1}}`))
	assert.ErrorContains(t, err, "unknown field", "the legacy refusal keeps the decoder's reason")

	// A question or an option named twice in a typed image is refused, never
	// read as one entry.
	for _, in := range [][]byte{fixedDupImage(t, "question"), fixedDupImage(t, "option")} {
		_, err := decide.ParseFixed(in)
		ref := refusal(t, err, "FIXED_ANSWERS")
		if ref != nil {
			assert.Equal(t, schemaread.ReasonMalformed, ref.Reason)
		}
	}

	// A probability past [0, 1] (a p or a noul) is refused as clamped in every
	// form, never read into the table (Schema.Check holds the same bound when
	// it is asked).
	for _, over := range []decide.Fixed{
		{Table: map[string]decide.FixedAnswer{"A": {Choice: "x", P: map[string]float64{"x": 1.5}}}},
		{Table: map[string]decide.FixedAnswer{"A": {Noul: ptr(-0.25)}}},
	} {
		overWire := fixedImage(t, over)
		for _, in := range []formInput{
			{"binary", overWire},
			{"base64", []byte(base64.StdEncoding.EncodeToString(overWire))},
			{"json", fixedJSONText(t, overWire)},
			{"legacy", fixedLegacyOf(t, over)},
		} {
			got, err := decide.ParseFixed(in.in)
			ref := refusal(t, err, "FIXED_ANSWERS")
			if ref != nil {
				assert.Equal(t, schemaread.ReasonClamped, ref.Reason, "a clamped %s", in.form)
			}
			assert.Equal(t, decide.Fixed{}, got, "a clamped %s hands back nothing", in.form)
		}
	}

	// No flip of the image panics, and none moves the shape of the table
	// silently.
	fr := decide.NewFixedReader()
	flipProbe(t, "FIXED_ANSWERS", want, wire, func(_ *decide.Parser, in []byte) (decide.Fixed, error) {
		v, _, err := fr.Read(in)
		return v, err
	})
}

// fixedLegacy is fixedSample as people write the file today. The question names
// differ by one bit (A, B, C), so a flip that turns one into another is seen.
var fixedLegacy = []byte(`{"A":{"choice":"done","p":{"done":0.75,"open":0.25}},"B":{"noul":0.5},"C":{"choice":"held-one","p":{"held-one":0.125}}}`)

func fixedSample() decide.Fixed {
	return decide.Fixed{Table: map[string]decide.FixedAnswer{
		"A": {Choice: "done", P: map[string]float64{"done": 0.75, "open": 0.25}},
		"B": {Noul: ptr(0.5)},
		"C": {Choice: "held-one", P: map[string]float64{"held-one": 0.125}},
	}}
}

func ptr(v float64) *float64 { return &v }

// fixedImage is f's FixedTable image, its rows and options in name order.
func fixedImage(t *testing.T, f decide.Fixed) []byte {
	t.Helper()
	var b decidewire.FixedTableBuilder
	require.True(t, b.Init())
	w, root := &b.Main, b.GetRoot()
	for _, q := range slices.Sorted(maps.Keys(f.Table)) {
		a := f.Table[q]
		n := decidewire.FixedTableAnswersAdd(w, &root.Answers)
		require.NotNil(t, n)
		setS(w, &n.Question, q)
		setS(w, &n.Answer.Choice, a.Choice)
		for _, opt := range slices.Sorted(maps.Keys(a.P)) {
			o := decidewire.FixedAnswerPAdd(w, &n.Answer.P)
			require.NotNil(t, o)
			setS(w, &o.Option, opt)
			o.P = a.P[opt]
		}
		if a.Noul != nil {
			n.Answer.Noul, n.Answer.NoulPresent = *a.Noul, true
		}
	}
	return wireSave(t, &b, decidewire.FixedTableMeasure, decidewire.FixedTableSave)
}

// fixedJSONText is the schema JSON text of a FixedTable image.
func fixedJSONText(t *testing.T, wire []byte) []byte {
	t.Helper()
	var b decidewire.FixedTableBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	var rep decidewire.TableReport
	require.True(t, decidewire.FixedTableLoadBuilder(&b, wire, &rep))
	require.True(t, b.Lock())
	root := b.AsConst()
	n := decidewire.FixedTableToJsonMeasure(root)
	require.Positive(t, n)
	out := make([]byte, n)
	require.Equal(t, n, decidewire.FixedTableToJson(root, out))
	return out
}

// fixedLegacyOf is f as a legacy answers file.
func fixedLegacyOf(t *testing.T, f decide.Fixed) []byte {
	t.Helper()
	out := []byte("{")
	for i, q := range slices.Sorted(maps.Keys(f.Table)) {
		if i > 0 {
			out = append(out, ',')
		}
		a := f.Table[q]
		row := map[string]any{}
		if a.Choice != "" {
			row["choice"] = a.Choice
		}
		if len(a.P) > 0 {
			row["p"] = a.P
		}
		if a.Noul != nil {
			row["noul"] = *a.Noul
		}
		out = append(out, flatKey(t, q, row)...)
	}
	return append(out, '}')
}

func flatKey(t *testing.T, k string, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{k: v})
	require.NoError(t, err)
	return raw[1 : len(raw)-1]
}

// fixedJSONDamage is the JSON text's malformed corpus: every cut of the value
// past its first key and list (the opening that names the form) short of its
// closing brace, appended tails, and its closing bracket and brace flipped.
func fixedJSONDamage(in []byte) [][]byte {
	head := bytes.IndexByte(in, '[') + 1
	end := len(bytes.TrimRight(in, " \t\r\n"))
	var out [][]byte
	for n := head; n < end; n++ {
		out = append(out, bytes.Clone(in[:n]))
	}
	for _, tail := range []string{"\x00", "x", "AAAA", "{}"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return append(out, flipByte(in, end-1), flipByte(in, bytes.LastIndexByte(in, ']')))
}

// fixedDupImage is a FixedTable image naming a question, or an answer's
// option, twice.
func fixedDupImage(t *testing.T, field string) []byte {
	t.Helper()
	var b decidewire.FixedTableBuilder
	require.True(t, b.Init())
	w, root := &b.Main, b.GetRoot()
	questions, options := []string{"A", "B"}, []string{"x", "y"}
	if field == "question" {
		questions = []string{"A", "A"}
	} else {
		options = []string{"x", "x"}
	}
	for _, q := range questions {
		n := decidewire.FixedTableAnswersAdd(w, &root.Answers)
		setS(w, &n.Question, q)
		setS(w, &n.Answer.Choice, "x")
		for _, opt := range options {
			o := decidewire.FixedAnswerPAdd(w, &n.Answer.P)
			setS(w, &o.Option, opt)
			o.P = 0.5
		}
	}
	return wireSave(t, &b, decidewire.FixedTableMeasure, decidewire.FixedTableSave)
}
