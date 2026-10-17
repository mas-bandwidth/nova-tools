package decide_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestGradeExamplesDualRead is the grade examples file's dual read (v1.5 aspect 13; the
// owner's rules S2 and S4): the file ParseExamples reads is read alike from its binary image,
// its padded standard base64, the schema JSON text and today's legacy form; a malformed or
// clamped value of each form is refused by name (`EXAMPLE REFUSED: <reason>; run: <help>`),
// never read as absent, empty or zero; and each read is counted under its form.
//
// Unit tier: no clock, no socket, no store. Files are read from the t.TempDir() the test
// writes.
func TestGradeExamplesDualRead(t *testing.T) {
	t.Parallel()

	want := decide.Example{Card: "a13-defs-decide", Heading: "A13 the decide definitions", Paths: "pkg/decide/decide.go", Kind: "work", Label: "pro"}
	legacy, err := json.Marshal(map[string]string{"card": want.Card, "heading": want.Heading, "paths": want.Paths, "kind": want.Kind, "label": want.Label})
	require.NoError(t, err)
	schemaJSON := exampleSchemaText(t, want)
	wire := exampleImage(t, want)
	base64v := []byte(base64.StdEncoding.EncodeToString(wire))

	forms := map[string][]byte{
		"binary": wire,
		"base64": base64v,
		"json":   schemaJSON,
		"legacy": legacy,
	}

	// every form reads equal through ParseExamples, the one read site
	for name, body := range forms {
		path := filepath.Join(t.TempDir(), name+".jsonl")
		require.NoError(t, os.WriteFile(path, append(bytes.Clone(body), '\n'), 0o600))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		got, err := decide.ParseExamples(raw)
		require.NoError(t, err, "%s form", name)
		require.Len(t, got, 1, name)
		assert.Equal(t, want, got[0], "%s form reads equal", name)
	}

	// a fresh reader counts each read under the form it arrived in
	rd := decide.NewExamplesReader()
	for _, in := range [][]byte{wire, base64v, schemaJSON, legacy} {
		_, _, err := rd.Read(in)
		require.NoError(t, err)
	}
	assert.Equal(t, uint64(1), rd.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), rd.Counts().Reads(schemaread.FormBase64))
	// both the schema JSON text and the legacy form are JSON objects opening with `{`, so the
	// Legacy predicate reads the two of them and counts them under the legacy form
	assert.Zero(t, rd.Counts().Reads(schemaread.FormJSON))
	assert.Equal(t, uint64(2), rd.Counts().Reads(schemaread.FormLegacy))

	// a truncated, a flipped-byte and an appended value of every form is refused by name and
	// counted under the form it arrived in
	rd = decide.NewExamplesReader()
	wantRefused := map[string]uint64{"binary": 0, "base64": 0, "legacy": 0}
	for _, in := range []struct {
		form string
		in   []byte
	}{{"binary", wire}, {"base64", base64v}, {"legacy", legacy}} {
		for _, m := range damage(in.form, in.in) {
			got, _, err := rd.Read(m)
			refusal(t, err, "EXAMPLE") // ignored: refusal asserts the named refusal itself; the returned value is not needed here
			assert.Zero(t, got, "%s: a refused read hands back nothing", in.form)
			wantRefused[in.form]++
		}
	}
	assert.Equal(t, wantRefused["binary"], rd.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, wantRefused["base64"], rd.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, wantRefused["legacy"], rd.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, wantRefused["binary"]+wantRefused["base64"]+wantRefused["legacy"], refusals(rd.Counts()))

	// a clamped value (the label outside the three classes) is refused by name in every form
	clamped := exampleClampedImage(t)
	rd = decide.NewExamplesReader()
	for _, in := range []struct {
		form string
		in   []byte
	}{{"binary", clamped}, {"base64", []byte(base64.StdEncoding.EncodeToString(clamped))}, {"legacy", []byte(`{"card":"c","heading":"h","label":"bogus"}`)}} {
		got, _, err := rd.Read(in.in)
		ref := refusal(t, err, "EXAMPLE")
		if ref != nil {
			assert.Equal(t, schemaread.ReasonClamped, ref.Reason, "%s clamped", in.form)
			assert.Equal(t, "label", ref.Field, "%s clamped", in.form)
		}
		assert.Zero(t, got, "%s: a clamped read hands back nothing", in.form)
	}
}

// exampleSchemaText is want as the schema JSON text the generated Example builder writes: the
// label as the enum name.
func exampleSchemaText(t *testing.T, want decide.Example) []byte {
	t.Helper()
	var b decidewire.ExampleBuilder
	require.True(t, b.Init())
	w := b.GetRoot()
	setS(&b.Main, &w.Card, want.Card)
	setS(&b.Main, &w.Heading, want.Heading)
	setS(&b.Main, &w.Paths, want.Paths)
	setS(&b.Main, &w.Kind, want.Kind)
	w.Label = decidewire.ExampleLabelPro
	b.Lock()
	root := b.AsConst()
	n := decidewire.ExampleToJsonMeasure(root)
	buf := make([]byte, n)
	decidewire.ExampleToJson(root, buf)
	// the file is one value per line, so the schema JSON text is compacted to the line a
	// value holds
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, buf))
	return compact.Bytes()
}

// exampleImage is an example as its binary table image.
func exampleImage(t *testing.T, e decide.Example) []byte {
	t.Helper()
	return exampleImageLabel(t, e, decidewire.ExampleLabelPro)
}

// exampleClampedImage is an example whose label is none of the three classes.
func exampleClampedImage(t *testing.T) []byte {
	t.Helper()
	return exampleImageLabel(t, decide.Example{Card: "c", Heading: "h"}, decidewire.ExampleLabelNone)
}

func exampleImageLabel(t *testing.T, e decide.Example, label decidewire.ExampleLabel) []byte {
	t.Helper()
	var b decidewire.ExampleBuilder
	require.True(t, b.Init())
	w := b.GetRoot()
	setS(&b.Main, &w.Card, e.Card)
	setS(&b.Main, &w.Heading, e.Heading)
	setS(&b.Main, &w.Paths, e.Paths)
	setS(&b.Main, &w.Kind, e.Kind)
	w.Label = label
	return wireSave(t, &b, decidewire.ExampleMeasure, decidewire.ExampleSave)
}
