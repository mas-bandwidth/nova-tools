package update

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/require"
)

// TestUpdateManifestDualRead pins the one read site (Load) reading the update
// manifest alike from its binary image, its padded standard base64, its schema
// JSON text and today's legacy TSV (S1, S2). A malformed or clamped value of
// each form is refused by name, never read as absent, empty or zero, and reads
// are counted per record, per form and per refusal reason (S2, S4).
func TestUpdateManifestDualRead(t *testing.T) {
	t.Parallel()

	sample := []byte(`{"entries":[{"name":"go","kind":"tool","installed":[{"text":"go"},{"text":"version"}],"latest":"local:go version","apply":[{"text":"go"},{"text":"update"}],"owner":"caller"}]}`)
	want := []Entry{{Name: "go", Kind: "tool", Installed: []string{"go", "version"}, Latest: "local:go version", Apply: []string{"go", "update"}, Owner: "caller"}}

	d, _, err := manifestRead.Read(sample)
	require.NoError(t, err)
	bin, err := manifestRead.Write(&d, schemaread.FormBinary)
	require.NoError(t, err)
	b64, err := manifestRead.Write(&d, schemaread.FormBase64)
	require.NoError(t, err)
	legacy := []byte(Header + "\n# a comment is skipped\ngo\ttool\tgo version\tlocal:go version\tgo update\tcaller\n")

	// The table wire carries an enum as the 64-bit hash of its variant name
	// (SPEC-TABLES section 5), so a kind past ManifestKindMax has no wire
	// spelling: the writer refuses it as clamped and writes nothing in any
	// form, and the reader meets it only as a variant id this build cannot
	// name. outOfRange is the binary image with the tool variant's id
	// replaced by such an id.
	toolID, ok := wire.ManifestKindtool.TableEnumId()
	require.True(t, ok)
	var id [8]byte
	binary.LittleEndian.PutUint64(id[:], toolID)
	require.Equal(t, 1, bytes.Count(bin, id[:]), "the tool variant id appears once in the image")
	outOfRange := bytes.Replace(bin, id[:], []byte{1, 2, 3, 4, 5, 6, 7, 8}, 1)

	flip := func(in []byte, at int) []byte {
		out := append([]byte(nil), in...)
		out[at] ^= 0x01
		return out
	}

	for _, tc := range []struct {
		name  string
		in    []byte
		count func() uint64
	}{
		{"binary", bin, func() uint64 { return manifestRead.Counts().Reads(schemaread.FormBinary) }},
		{"base64", b64, func() uint64 { return manifestRead.Counts().Reads(schemaread.FormBase64) }},
		{"json", sample, func() uint64 { return manifestRead.Counts().Reads(schemaread.FormJSON) }},
		{"legacy", legacy, func() uint64 { return manifestLegacyReads.Load() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.count()
			got, err := Load(strings.NewReader(string(tc.in)))
			require.NoError(t, err)
			require.Equal(t, want, got, "%s: the four forms decode to one equal value", tc.name)
			require.GreaterOrEqual(t, tc.count()-before, uint64(1), "%s: reads are counted per form", tc.name)
		})
	}

	t.Run("file", func(t *testing.T) {
		for name, in := range map[string][]byte{"manifest.bin": bin, "manifest.b64": b64, "manifest.json": sample, "manifest.tsv": legacy} {
			p := t.TempDir() + "/" + name
			require.NoError(t, os.WriteFile(p, in, 0o600))
			f, err := os.Open(p)
			require.NoError(t, err)
			got, err := Load(f)
			require.NoError(t, f.Close())
			require.NoError(t, err, name)
			require.Equal(t, want, got, name)
		}
	})

	t.Run("counted per record", func(t *testing.T) {
		before := manifestRead.Counts().Reads(schemaread.FormBinary)
		for i := 0; i < 2; i++ {
			got, err := Load(strings.NewReader(string(bin)))
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
		require.GreaterOrEqual(t, manifestRead.Counts().Reads(schemaread.FormBinary)-before, uint64(2), "each record read is counted")
	})

	t.Run("a kind past ManifestKindMax is never written", func(t *testing.T) {
		v, _, err := manifestRead.Read(sample)
		require.NoError(t, err)
		v.v.Entries.At(0).Kind = wire.ManifestKindMax + 1
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			out, err := manifestRead.Write(&v, form)
			require.Nil(t, out, "%s: nothing is written", form)
			var ref *schemaread.Refusal
			require.True(t, errors.As(err, &ref), "%s: %v", form, err)
			require.Equal(t, schemaread.ReasonClamped, ref.Reason, "%s: %v", form, err)
			require.Equal(t, "kind", ref.Field, "%s: %v", form, err)
		}
		require.Equal(t, int64(-1), wire.UpdateManifestMeasure(v.v), "the generated writer refuses it too")
	})

	for _, tc := range []struct {
		name          string
		form          schemaread.Form
		in            []byte
		reason, field string // the refusal the case pins; an empty reason pins only that one is named
	}{
		{"truncated wire", schemaread.FormBinary, bin[:len(bin)-1], "", ""},
		{"flipped wire", schemaread.FormBinary, flip(bin, 2), "", ""},
		{"out-of-range kind wire", schemaread.FormBinary, outOfRange, schemaread.ReasonAbsentRequired, "kind"},
		{"truncated base64", schemaread.FormBase64, b64[:len(b64)-1], schemaread.ReasonMalformed, ""},
		{"flipped base64", schemaread.FormBase64, flip(b64, len(b64)/2), "", ""},
		{"out-of-range kind base64", schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(outOfRange)), schemaread.ReasonAbsentRequired, "kind"},
		{"truncated json", schemaread.FormJSON, sample[:len(sample)-1], schemaread.ReasonMalformed, ""},
		{"flipped json", schemaread.FormJSON, flip(sample, len(sample)-1), schemaread.ReasonMalformed, ""},
		{"out-of-range kind json", schemaread.FormJSON, []byte(strings.Replace(string(sample), `"kind":"tool"`, `"kind":"bogus"`, 1)), schemaread.ReasonAbsentRequired, "kind"},
		{"numeric kind json", schemaread.FormJSON, []byte(strings.Replace(string(sample), `"kind":"tool"`, `"kind":6`, 1)), schemaread.ReasonKindMismatch, ""},
	} {
		t.Run("refused "+tc.name, func(t *testing.T) {
			before := manifestRead.Counts().Reads(tc.form)
			got, err := Load(strings.NewReader(string(tc.in)))
			require.Nil(t, got, "%s: a refusal is never read as absent, empty or zero", tc.name)
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), "UpdateManifest REFUSED: "), "%s: refused by name: %v", tc.name, err)
			require.Contains(t, err.Error(), "; run: ", tc.name)
			var ref *schemaread.Refusal
			require.True(t, errors.As(err, &ref), "%s: %v", tc.name, err)
			require.NotEmpty(t, ref.Reason, tc.name)
			if tc.reason != "" {
				require.Equal(t, tc.reason, ref.Reason, "%s: %v", tc.name, err)
				require.Equal(t, tc.field, ref.Field, "%s: %v", tc.name, err)
			}
			require.GreaterOrEqual(t, manifestRead.Counts().Reads(tc.form)-before, uint64(1), "%s: the refused read is counted under its form", tc.name)
			require.GreaterOrEqual(t, manifestRead.Counts().Refused(ref.Reason), uint64(1), "%s: the refusal is counted by reason", tc.name)
		})
	}

	// A table form passes the strict read and is still held to the latest
	// source the legacy form checks: a local locator is an argv.
	t.Run("refused local locator json", func(t *testing.T) {
		got, err := Load(strings.NewReader(strings.Replace(string(sample), `"latest":"local:go version"`, `"latest":"local:go  version"`, 1)))
		require.Nil(t, got)
		var me *ManifestError
		require.True(t, errors.As(err, &me), "%v", err)
		require.Contains(t, err.Error(), "latest: argv requires single spaces")
	})

	// The legacy form keeps its own refusal, *ManifestError, which names every
	// bad field by line; the verb prints it as `<verb> REFUSED: ...; run: ...`.
	for _, tc := range []struct{ name, in, want string }{
		{"truncated", Header + "\ngo\ttool\tgo version\tlocal:go version\n", "line 2: 4 fields"},
		{"flipped", Header + "\ngo\ttool\tgo version\tlocal:go version\tgo update caller\n", "line 2: 5 fields"},
		{"out-of-range kind", Header + "\nx\tgadget\tgo version\tlocal:go version\tnone\tme\n", "unknown kind gadget"},
		{"local locator", Header + "\ngo\ttool\tgo version\tlocal:go  version\tnone\tme\n", "line 2: latest: argv requires single spaces"},
	} {
		t.Run("legacy refused "+tc.name, func(t *testing.T) {
			before := manifestLegacyReads.Load()
			got, err := Load(strings.NewReader(tc.in))
			require.Nil(t, got)
			var me *ManifestError
			require.True(t, errors.As(err, &me), "%s: %v", tc.name, err)
			require.Contains(t, err.Error(), tc.want)
			require.GreaterOrEqual(t, manifestLegacyReads.Load()-before, uint64(1), "%s: the refused legacy read is counted", tc.name)
		})
	}
}
