package update

// TestAdoptChecksDualRead pins the adopt checks reader (Aspect 13; the
// owner's S2 and S4, readers first, UNION.md C13): LoadAdopt reads the checks
// file alike from the generated AdoptChecks table's binary wire, the padded
// standard base64 of that wire, the schema JSON text form and the legacy TSV
// people write by hand; a truncated or flipped value of each form is refused
// by name (`AdoptChecks REFUSED: <reason>; run: <help>`) and is never read as
// absent, empty or zero; and reads are counted per record and per form. The
// test hands the read site a strings.Reader and t.TempDir() files. The test
// uses no clock, no socket and no network.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adoptRefusal asserts err is the checks reader's named refusal.
func adoptRefusal(t *testing.T, err error) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), "AdoptChecks REFUSED: "), err.Error())
	assert.Contains(t, err.Error(), "; run: ")
	return ref
}

func TestAdoptChecksDualRead(t *testing.T) {
	t.Parallel()

	// The legacy file, exactly as people write it by hand: the three-column
	// header, a # comment, then one check per line.
	legacy := []byte(
		"check\tcommand\towner\n" +
			"# adoption checks\n" +
			"healthy\tnova-bus health\tsre\n" +
			"audit\tgo vet ./...\tcicd\n")
	want := []AdoptCheck{
		{Name: "healthy", Owner: "sre", Command: []string{"nova-bus", "health"}},
		{Name: "audit", Owner: "cicd", Command: []string{"go", "vet", "./..."}},
	}

	// The one value reads back equal from every form.
	doc, _, err := adoptRead.Read(legacy)
	require.NoError(t, err)
	assert.Equal(t, want, adoptOf(doc))

	typed := []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}
	images := map[schemaread.Form][]byte{}
	for _, form := range typed {
		img, err := adoptRead.Write(&doc, form)
		require.NoError(t, err, "the writer writes the %s form", form)
		require.NotEmpty(t, img)
		images[form] = img
	}
	assert.Equal(t, byte(1), images[schemaread.FormBinary][0], "the wire leads with its form byte")
	assert.Equal(t, base64.StdEncoding.EncodeToString(images[schemaread.FormBinary]), string(images[schemaread.FormBase64]),
		"base64 is padded standard base64 of the wire")

	// Each typed form reads back equal from a t.TempDir() file; the legacy
	// reads equal from a strings.Reader. Reads are counted per form.
	dir := t.TempDir()
	for _, form := range typed {
		p := filepath.Join(dir, "checks."+form.String())
		require.NoError(t, os.WriteFile(p, images[form], 0o600))
		before := adoptRead.Counts().Reads(form)
		f, err := os.Open(p)
		require.NoError(t, err)
		got, err := LoadAdopt(f)
		f.Close()
		require.NoError(t, err, "%s: the read site reads it", form)
		assert.Equal(t, want, got, "%s: reads equal", form)
		assert.GreaterOrEqual(t, adoptRead.Counts().Reads(form), before+1, "%s: the read is counted", form)
	}

	before := adoptRead.Counts().Reads(schemaread.FormLegacy)
	got, err := LoadAdopt(strings.NewReader(string(legacy)))
	require.NoError(t, err)
	assert.Equal(t, want, got, "legacy: reads equal")
	assert.GreaterOrEqual(t, adoptRead.Counts().Reads(schemaread.FormLegacy), before+1, "legacy: the read is counted")

	// A truncated or flipped value of each form is refused by name and never
	// read as absent, empty or zero.
	all := map[schemaread.Form][]byte{schemaread.FormLegacy: legacy}
	for form, img := range images {
		all[form] = img
	}
	for form, img := range all {
		trimmed := bytes.TrimRight(img, " \t\r\n")
		value, _, err := adoptRead.Read(trimmed[:len(trimmed)/2])
		require.Error(t, err, "%s cut is refused", form)
		adoptRefusal(t, err)
		assert.Nil(t, value.v, "%s cut returns no value", form)
		for i := range img {
			bad := append([]byte(nil), img...)
			bad[i] ^= 0xff
			value, _, err := adoptRead.Read(bad)
			if err != nil {
				adoptRefusal(t, err)
				assert.Nil(t, value.v, "%s flipped at %d returns no value", form, i)
				continue
			}
			assert.NotNil(t, value.v, "%s flipped at %d reads a whole value", form, i)
		}
	}

	// Reads are counted per record and per form, refusals by reason: one good
	// and one damaged read of each form on a reader of the test's own.
	r := schemaread.NewReader(adoptCodec())
	good := map[schemaread.Form][]byte{
		schemaread.FormBinary: images[schemaread.FormBinary],
		schemaread.FormBase64: images[schemaread.FormBase64],
		schemaread.FormJSON:   images[schemaread.FormJSON],
		schemaread.FormLegacy: legacy,
	}
	for form, in := range good {
		_, res, err := r.Read(in)
		require.NoError(t, err, form)
		assert.Equal(t, form, res.Form, "%s: read under its form", form)
	}
	for form, in := range good {
		_, _, err := r.Read(in[:len(in)/2])
		require.Error(t, err, "%s: damage is refused", form)
		adoptRefusal(t, err)
	}
	var total uint64
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy} {
		assert.GreaterOrEqual(t, r.Counts().Reads(form), uint64(1), "%s reads are counted", form)
		total += r.Counts().Reads(form)
	}
	assert.Equal(t, uint64(8), total, "every read is counted once under the form it arrived in")
	var refused uint64
	for _, reason := range []string{schemaread.ReasonMalformed, schemaread.ReasonWireDamaged, schemaread.ReasonUnknownForm, schemaread.ReasonAbsentRequired} {
		refused += r.Counts().Refused(reason)
	}
	assert.Positive(t, refused, "refusals are counted by reason")
}
