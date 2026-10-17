package fuse_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/ledgerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFuseBoxDualRead pins the one read site (internal/fuse ReadBox) reading
// the fuse box alike from its generated FuseBox table image (pkg/ledgerwire),
// the padded standard base64 of that image, and the legacy JSON form every
// writer writes today (the owner's rule S2, readers first S4). Damage to any
// form is refused by name, never read as absent, empty or zero, and every read
// is counted per form (UNION.md C13).
func TestFuseBoxDualRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	legacy := `{"lockdown":{"at":"2026-10-17T00:00:00Z","reason":"the person you work with"},"quarantine":{"Discord":{"at":"t","reason":"r"}}}`
	var want fuse.Box
	require.NoError(t, json.Unmarshal([]byte(legacy), &want))

	wire := boxImage(t)
	b64 := []byte(base64.StdEncoding.EncodeToString(wire))

	for name, form := range map[string][]byte{
		"legacy": []byte(legacy),
		"binary": wire,
		"base64": b64,
	} {
		path := filepath.Join(dir, name+".box")
		require.NoError(t, os.WriteFile(path, form, 0o644))
		got, err := fuse.ReadBox(path)
		require.NoError(t, err, name)
		require.Equal(t, want, got, name)
	}

	r := fuse.NewBoxDualReadReader()
	require.NoError(t, readOK(r, []byte(legacy)), "legacy read")
	require.NoError(t, readOK(r, wire), "binary read")
	require.NoError(t, readOK(r, b64), "base64 read")
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))

	// Damage of each form is refused by name, never read as absent or empty.
	malformed := map[string][]byte{
		"truncated wire": wire[:len(wire)-4],
		"flipped wire":   flip(wire),
		"truncated b64": b64[:len(b64)-4],
		"flipped b64":   []byte(base64.StdEncoding.EncodeToString(flip(wire))),
		"truncated legacy": []byte(`{"lockdown":{"at":"x"`),
		"not a box legacy":  []byte(`null`),
	}
	for name, in := range malformed {
		_, _, err := r.Read(in)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "REFUSED", name)
	}
}

func readOK(r *schemaread.Reader[fuse.Box], in []byte) error {
	_, _, err := r.Read(in)
	return err
}

func flip(b []byte) []byte {
	out := append([]byte(nil), b...)
	out[len(out)-1] ^= 0x80
	return out
}

// boxImage is the canonical FuseBox table image of the box the test reads, made
// by loading the table-shaped JSON into a fresh builder and saving it.
func boxImage(t *testing.T) []byte {
	t.Helper()
	const tableJSON = `{"lockdown":{"at":"2026-10-17T00:00:00Z","reason":"the person you work with"},"quarantine":[{"surface":"Discord","fuse":{"at":"t","reason":"r"}}]}`
	var bb ledgerwire.FuseBoxBuilder
	require.True(t, bb.Init())
	defer bb.Shutdown()
	var rep ledgerwire.TableReport
	require.True(t, ledgerwire.FuseBoxFromJson(&bb, []byte(tableJSON), &rep), "FuseBoxFromJson: verdict=%d err=%v", rep.Verdict, rep.Err())
	require.True(t, bb.Lock())
	c := bb.AsConst()
	require.NotNil(t, c)
	n := ledgerwire.FuseBoxMeasure(c)
	require.Positive(t, n)
	out := make([]byte, n)
	require.Equal(t, n, ledgerwire.FuseBoxSave(c, out))
	return out
}
