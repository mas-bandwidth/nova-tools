package update

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionSnapshotDiffDualRead pins the version snapshot dual read: the
// fixed binary image of the VersionSnapshot table, its padded standard base64,
// the schema JSON text and the legacy TSV all decode to one equal map through
// readSnapshotFile (S2, C13), and a truncated, a flipped-byte and a malformed
// value of each form is refused by name, never read as absent, empty or zero,
// with the read counted per record and per form.
func TestVersionSnapshotDiffDualRead(t *testing.T) {
	t.Parallel()

	jsonText := []byte(`{"rows":[{"name":"alpha","stamp":"v1","revision":"abc","platform":"linux/amd64"},{"name":"beta","stamp":"v2","revision":"def","platform":"darwin/arm64"}]}`)
	legacy := []byte("name\tstamp\trevision\tplatform\nalpha\tv1\tabc\tlinux/amd64\n# a hand comment\nbeta\tv2\tdef\tdarwin/arm64\n")

	expected := map[string]snapRow{
		"alpha": {name: "alpha", stamp: "v1", revision: "abc", platform: "linux/amd64"},
		"beta":  {name: "beta", stamp: "v2", revision: "def", platform: "darwin/arm64"},
	}

	r := schemaread.NewReader(versionSnapshotTable.codec())
	d, _, err := r.Read(jsonText)
	require.NoError(t, err)
	bin := writeVersionSnapshot(t, r, d, schemaread.FormBinary)
	b64 := writeVersionSnapshot(t, r, d, schemaread.FormBase64)
	jsn := writeVersionSnapshot(t, r, d, schemaread.FormJSON)
	require.Equal(t, byte(1), bin[0], "the wire leads with its form byte")
	require.Equal(t, base64.StdEncoding.EncodeToString(bin), string(b64), "base64 is padded standard base64 of the wire")

	t.Run("every form reads one equal map", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		for name, form := range map[string][]byte{
			"binary":  bin,
			"base64":  b64,
			"json":    jsn,
			"legacy":  legacy,
		} {
			path := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(path, form, 0o600))
			got, err := readSnapshotFile(path)
			require.NoError(t, err, name)
			assert.Equal(t, expected, got, name)
		}
	})

	t.Run("damage is refused by name and counted per form", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name   string
			in     []byte
			reason string
		}{
			{"truncated binary", bin[:len(bin)-1], schemaread.ReasonWireDamaged},
			{"binary form byte flipped to an unknown form", func() []byte { b := bytes.Clone(bin); b[0] = 9; return b }(), schemaread.ReasonUnknownForm},
			{"truncated base64", b64[:len(b64)-3], schemaread.ReasonMalformed},
			{"truncated JSON text", jsn[:bytes.LastIndexByte(jsn, '}')], schemaread.ReasonMalformed},
			{"legacy header wrong", []byte("not the header\n"), schemaread.ReasonMalformed},
			{"legacy row of the wrong arity", []byte(snapshotHeader + "\na\tb\tc\n"), schemaread.ReasonMalformed},
		}
		// Counts are per form: one reader per case, so each count reads its own.
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				rr := newSnapshotReader()
				rows, form, err := rr.Read(tc.in)
				require.Error(t, err)
				assert.Nil(t, rows, "a refused read returns no rows")
				require.True(t, strings.HasPrefix(err.Error(), "VERSNAPSHOT REFUSED: "), err.Error())
				require.True(t, strings.HasSuffix(err.Error(), "; run: nova-version snapshot --bin <dir> --out <file.tsv>"), err.Error())
				if tc.name == "legacy header wrong" || tc.name == "legacy row of the wrong arity" {
					assert.Equal(t, schemaread.FormLegacy, form)
					assert.Equal(t, uint64(1), rr.r.Counts().Reads(schemaread.FormLegacy))
				} else {
					assert.NotEqual(t, schemaread.FormLegacy, form)
					assert.Equal(t, uint64(1), rr.r.Counts().Reads(form))
					assert.Positive(t, rr.r.Counts().Refused(tc.reason))
				}
			})
		}
	})
}

func writeVersionSnapshot(t *testing.T, r *schemaread.Reader[srDoc[wire.VersionSnapshot]], d srDoc[wire.VersionSnapshot], form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(&d, form)
	require.NoError(t, err, form)
	require.NotEmpty(t, out, "%s: the generated writer wrote it", form)
	return out
}
