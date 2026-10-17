package update

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/require"
)

// TestUpdateManifestDualRead pins the one read site (Load) reading the update
// manifest alike from its binary image, its padded standard base64, its schema
// JSON text and today's legacy TSV (S1, S2). A malformed or clamped value of
// each form is refused by name, never read as absent, empty or zero, and reads
// are counted per record and per form (S3).
func TestUpdateManifestDualRead(t *testing.T) {
	t.Parallel()

	sample := []byte(`{"entries":[{"name":"go","kind":"tool","installed":[{"text":"go"},{"text":"version"}],"latest":"local:go version","apply":[{"text":"go"},{"text":"update"}],"owner":"caller"}]}`)
	want := []Entry{{Name: "go", Kind: "tool", Installed: []string{"go", "version"}, Latest: "local:go version", Apply: []string{"go", "update"}, Owner: "caller"}}

	images := func(j []byte) (bin, b64 []byte) {
		d, _, err := manifestRead.Read(j)
		require.NoError(t, err)
		bin, err = manifestRead.Write(&d, schemaread.FormBinary)
		require.NoError(t, err)
		b64, err = manifestRead.Write(&d, schemaread.FormBase64)
		require.NoError(t, err)
		return bin, b64
	}
	bin, b64 := images(sample)
	legacy := []byte(Header + "\n# a comment is skipped\ngo\ttool\tgo version\tlocal:go version\tgo update\tcaller\n")

	flip := func(in []byte, at int) []byte {
		out := append([]byte(nil), in...)
		out[at] ^= 0x01
		return out
	}

	for _, tc := range []struct {
		name string
		form schemaread.Form
		in   []byte
		count func() uint64
	}{{"binary", schemaread.FormBinary, bin, func() uint64 { return manifestRead.Counts().Reads(schemaread.FormBinary) }}, {"base64", schemaread.FormBase64, b64, func() uint64 { return manifestRead.Counts().Reads(schemaread.FormBase64) }}, {"json", schemaread.FormJSON, sample, func() uint64 { return manifestRead.Counts().Reads(schemaread.FormJSON) }}, {"legacy", schemaread.FormLegacy, legacy, func() uint64 { return manifestLegacyReads.Load() }}} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.count()
			got, err := Load(strings.NewReader(string(tc.in)))
			require.NoError(t, err)
			require.Equal(t, want, got, "%s: the four forms decode to one equal value", tc.name)
			require.GreaterOrEqual(t, tc.count()-before, uint64(1), "%s: reads are counted per form", tc.name)
		})
	}

	t.Run("file", func(t *testing.T) {
		p := t.TempDir() + "/manifest.bin"
		require.NoError(t, os.WriteFile(p, bin, 0o600))
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		got, err := Load(strings.NewReader(string(b)))
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"truncated wire", bin[:len(bin)-1]},
		{"flipped wire", flip(bin, 2)},
		{"truncated base64", b64[:len(b64)-1]},
		{"flipped base64", flip(b64, len(b64)/2)},
		{"truncated json", sample[:len(sample)-1]},
		{"flipped json", flip(sample, 0)},
		{"clamped json", []byte(`{"entries":[{"name":"go","kind":"bogus","installed":[{"text":"go"}],"latest":"local:go version","owner":"caller"}]}`)},
	} {
		t.Run("refused "+tc.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(string(tc.in)))
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), "UpdateManifest REFUSED: "), "%s: refused by name: %v", tc.name, err)
		})
	}

	t.Run("legacy refused", func(t *testing.T) {
		_, err := Load(strings.NewReader(Header + "\nx\tgadget\tgo version\tlocal:go version\tnone\tme\n"))
		var me *ManifestError
		require.True(t, errors.As(err, &me))
		require.Contains(t, err.Error(), "unknown kind gadget")
	})
}
