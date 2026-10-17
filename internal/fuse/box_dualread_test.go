package fuse_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// form is refused by name (`FUSE_BOX REFUSED: <reason>; run: <help>`), never
// read as absent, empty or zero, every read is counted per form (UNION.md
// C13), and a damaged box file stays CANNOT TELL through ReadBox.
func TestFuseBoxDualRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	legacy := `{"lockdown":{"at":"2026-10-17T00:00:00Z","reason":"the person you work with"},"quarantine":{"Discord":{"at":"t","reason":"r"}}}`
	var want fuse.Box
	require.NoError(t, json.Unmarshal([]byte(legacy), &want))

	wire := boxImage(t)
	b64 := []byte(base64.StdEncoding.EncodeToString(wire))

	// The three forms read alike, from files the test writes in its TempDir.
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
	inputs := []struct {
		form string
		in   []byte
	}{
		{"legacy", []byte(legacy)},
		{"binary", wire},
		{"base64", b64},
	}
	for _, in := range inputs {
		_, _, err := r.Read(in.in)
		require.NoError(t, err, "%s read", in.form)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))

	// Every damaged value of every form is refused by name and never read as
	// absent, empty or zero: each prefix, each appended tail, and each flipped
	// byte. A refused read hands back the zero value and is counted refused.
	tries := 0
	refusals := uint64(0)
	for _, in := range inputs {
		variants := append(damaged(in.in), flips(in.in)...)
		for _, m := range variants {
			got, _, err := r.Read(m)
			if err == nil {
				assert.NotEqual(t, fuse.Box{}, got, "%s: a damaged %s read as empty", in.form, in.form)
				continue
			}
			ref := refusal(t, err, "FUSE_BOX")
			require.NotNil(t, ref, "%s: a damaged %s (%d bytes) was read as %v", in.form, in.form, len(m), got)
			assert.Equal(t, fuse.Box{}, got, "%s: a refused read hands back nothing", in.form)
			tries++
			refusals = countRefused(r, refusals)
		}
	}
	assert.Equal(t, uint64(tries), refusals, "every damaged read is counted refused, by reason")

	// A damaged box file stays CANNOT TELL through the read site, never a clear.
	for name, damagedBytes := range map[string][]byte{
		"binary-damaged":  wire[:len(wire)-4],
		"base64-damaged": b64[:len(b64)-4],
	} {
		p := filepath.Join(dir, name+".box")
		require.NoError(t, os.WriteFile(p, damagedBytes, 0o644))
		_, err := fuse.ReadBox(p)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "not a readable fuse box", name)
	}
}

// countRefused returns the running total of refused reads (Snapshot is cheap
// and stable for the small counter set).
func countRefused(r *schemaread.Reader[fuse.Box], prev uint64) uint64 {
	total := uint64(0)
	for k, n := range r.Counts().Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			total += n
		}
	}
	return total
}

var refusalText = regexp.MustCompile(`^[A-Z_]+ REFUSED: [^;]+; run: \S.*$`)

// refusal is err as the reader's named refusal of record name.
func refusal(t *testing.T, err error, name string) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	if !assert.True(t, errors.As(err, &ref), "%s: want a named refusal, got %v", name, err) {
		return nil
	}
	assert.Equal(t, name, ref.Name)
	assert.Regexp(t, refusalText, err.Error())
	return ref
}

// damaged is in cut at each length (every length for a short value, the first
// and last 256 and a stride between for a long one) and in with bytes after
// its end.
func damaged(in []byte) [][]byte {
	var cuts []int
	if len(in) <= 768 {
		for n := 0; n < len(in); n++ {
			cuts = append(cuts, n)
		}
	} else {
		for n := 0; n < 256; n++ {
			cuts = append(cuts, n, len(in)-256+n)
		}
		for n := 256; n < len(in)-256; n += len(in)/97 + 1 {
			cuts = append(cuts, n)
		}
	}
	out := make([][]byte, 0, len(cuts)+3)
	for _, n := range cuts {
		out = append(out, bytes.Clone(in[:n]))
	}
	for _, tail := range []string{"\x00", "x", "AAAA"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return out
}

// flips is in with each single byte flipped: damage that moves a byte while
// keeping the length.
func flips(in []byte) [][]byte {
	out := make([][]byte, 0, len(in))
	for i := range in {
		m := bytes.Clone(in)
		m[i] ^= 0xff
		out = append(out, m)
	}
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
