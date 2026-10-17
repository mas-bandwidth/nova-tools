package update

// TestUpdateSnapshotDualRead pins the update snapshot's reader (Aspect 13
// part 2; the owner's S2 and S4, readers first, UNION.md C13): the update
// snapshot is read alike from the generated table's binary wire, the padded
// standard base64 of that wire, the schema JSON text form and the legacy JSON
// the tool writes today; a truncated, flipped or misspelled value of each form
// is refused by name (`UpdateSnapshot REFUSED: <reason>; run: <help>`) and is
// never read as absent, empty or zero; and reads are counted per record and
// per form. The files are ones the test writes in a t.TempDir(). The test uses
// no clock, no socket and no network.

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

// snapshotRefusal asserts err is the update snapshot reader's named refusal.
func snapshotRefusal(t *testing.T, err error) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), "UpdateSnapshot REFUSED: "), err.Error())
	assert.Contains(t, err.Error(), "; run: nova-update report -h")
	return ref
}

func TestUpdateSnapshotDualRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sample := emptySnapshot()
	sample.Observed = map[string]observed{
		"Tool": {Raw: "1.2.3", Status: "known", At: "2026-10-17T00:00:00Z"},
		"tool": {Raw: "3.2.1", Status: "unknown", At: "2026-10-17T01:02:03Z"},
	}
	sample.Delivered = map[string]delivery{
		`["fixture","integrator","example-bench"]`: {
			Observed: map[string]observed{
				"Tool": {Raw: "1.2.3", Status: "known", At: "2026-10-17T00:00:00Z"},
			},
			ID: "delivery-1",
			At: "2026-10-17T02:03:04Z",
		},
	}
	doc := snapDoc{legacy: sample}

	// one value, written in each typed form by the table's own writer
	typed := []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}
	images := map[schemaread.Form][]byte{}
	for _, form := range typed {
		img, err := snapshotRead.Write(&doc, form)
		require.NoError(t, err, "the writer writes the %s form", form)
		require.NotEmpty(t, img)
		images[form] = img
	}
	assert.Equal(t, byte(1), images[schemaread.FormBinary][0], "the wire leads with its form byte")
	assert.Equal(t, base64.StdEncoding.EncodeToString(images[schemaread.FormBinary]), string(images[schemaread.FormBase64]),
		"base64 is padded standard base64 of the wire")

	// the writer still writes the legacy form: an object of maps, not lists
	legacyPath := filepath.Join(dir, "legacy.json")
	require.NoError(t, writeSnapshot(legacyPath, sample))
	legacy, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	assert.Contains(t, string(legacy), `"observed":{`, "the writer writes the legacy map form")
	assert.Contains(t, string(legacy), `"delivered":{`, "the writer writes the legacy map form")
	assert.NotContains(t, string(legacy), `"observed":[`, "the writer does not write the table text form")

	// each form, and the legacy text, reads back equal from a file
	for _, form := range typed {
		p := filepath.Join(dir, "snapshot."+form.String())
		require.NoError(t, os.WriteFile(p, images[form], 0o600))
		before := snapshotRead.Counts().Reads(form)
		got, err := readSnapshot(p)
		require.NoError(t, err, "%s: the read site reads it", form)
		assert.Equal(t, sample, got, "%s: reads equal", form)
		assert.GreaterOrEqual(t, snapshotRead.Counts().Reads(form), before+1, "%s: the read is counted", form)
	}
	before := snapshotRead.Counts().Reads(schemaread.FormLegacy)
	got, err := readSnapshot(legacyPath)
	require.NoError(t, err, "legacy: the read site reads it")
	assert.Equal(t, sample, got, "legacy: reads equal")
	assert.GreaterOrEqual(t, snapshotRead.Counts().Reads(schemaread.FormLegacy), before+1, "legacy: the read is counted")

	// every form, legacy included, refuses every truncation by name and
	// never yields a value for it
	all := map[schemaread.Form][]byte{}
	for form, img := range images {
		all[form] = img
	}
	all[schemaread.FormLegacy] = bytes.TrimSpace(legacy)
	for form, img := range all {
		trimmed := bytes.TrimRight(img, " \t\r\n")
		for cut := 0; cut < len(trimmed); cut++ {
			value, _, err := snapshotRead.Read(trimmed[:cut])
			require.Error(t, err, "%s cut at %d is refused", form, cut)
			snapshotRefusal(t, err)
			assert.True(t, value.v == nil && value.legacy == nil, "%s cut at %d returns no value", form, cut)
		}
		// a flipped byte is refused by name, or reads a whole value; it is
		// never read as the empty snapshot
		for i := range img {
			bad := append([]byte(nil), img...)
			bad[i] ^= 0xff
			value, _, err := snapshotRead.Read(bad)
			if err != nil {
				snapshotRefusal(t, err)
				assert.True(t, value.v == nil && value.legacy == nil, "%s flipped at %d returns no value", form, i)
				continue
			}
			assert.True(t, value.v != nil || value.legacy != nil, "%s flipped at %d returns a whole value", form, i)
		}
	}

	// a value missing one of the two maps is refused, not read as an empty map
	partial := filepath.Join(dir, "partial.json")
	require.NoError(t, os.WriteFile(partial, []byte(`{"observed":{"x":{"raw":"1","status":"known","at":"t"}}}`), 0o600))
	value, err := readSnapshot(partial)
	require.Error(t, err, "a snapshot lacking delivered is refused")
	assert.Equal(t, schemaread.ReasonAbsentRequired, snapshotRefusal(t, err).Reason)
	assert.Nil(t, value)

	// a misspelled or folded member is refused as the legacy reader always
	// refused it, not read as absent
	for name, body := range map[string]string{
		"misspelled map":   `{"observed":{},"deliverd":{}}`,
		"folded map":       `{"observed":{},"Observed":{},"delivered":{}}`,
		"folded leaf":      `{"observed":{"x":{"raw":"one","Raw":"two","status":"known","at":"t"}},"delivered":{}}`,
		"duplicate key":    `{"observed":{},"observed":{"x":{"raw":"one","status":"known","at":"t"}},"delivered":{}}`,
		"trailing content": `{"observed":{},"delivered":{}} {"observed":{},"delivered":{}}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		value, err := readSnapshot(p)
		require.Errorf(t, err, "%s is refused", name)
		snapshotRefusal(t, err)
		assert.Nil(t, value, "%s returns no value", name)
	}

	// the table declares no bound: every text is uncapped and every list is
	// open (schemas/release/release.schema, the area's variability rule), so
	// there is no clamped value of this record to refuse. The legacy shape
	// walk's own depth bound is refused as malformed, as it always was.

	// reads are counted per record and per form, refusals by reason: one good
	// and one damaged read of each form on a reader of the test's own.
	r := schemaread.NewReader(snapshotCodec())
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
		snapshotRefusal(t, err)
	}
	var total uint64
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy} {
		assert.GreaterOrEqual(t, r.Counts().Reads(form), uint64(1), "%s reads are counted", form)
		total += r.Counts().Reads(form)
	}
	assert.Equal(t, uint64(8), total, "every read is counted once under the form it arrived in")
	refused := uint64(0)
	for _, reason := range []string{schemaread.ReasonMalformed, schemaread.ReasonWireDamaged, schemaread.ReasonUnknownForm, schemaread.ReasonAbsentRequired} {
		refused += r.Counts().Refused(reason)
	}
	assert.Positive(t, refused, "refusals are counted by reason")
}
