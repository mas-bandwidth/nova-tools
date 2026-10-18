package fuse_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
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

	forms := []struct {
		name string
		in   []byte
	}{
		{"legacy", []byte(legacy)},
		{"binary", wire},
		{"base64", b64},
	}

	// The three forms read alike, from files the test writes in its TempDir.
	for _, f := range forms {
		path := filepath.Join(dir, f.name+".box")
		require.NoError(t, os.WriteFile(path, f.in, 0o644))
		got, err := fuse.ReadBox(path)
		require.NoError(t, err, f.name)
		require.Equal(t, want, got, f.name)
	}

	r := fuse.NewBoxDualReadReader()
	for _, f := range forms {
		got, _, err := r.Read(f.in)
		require.NoError(t, err, "%s read", f.name)
		require.Equal(t, want, got, f.name)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))

	// A seeded fuzz sweep of every form: every truncation, every single-byte
	// flip, the three appended tails, and a seeded pseudo-random set of the
	// same classes (so every generated mutation is one of the structural
	// classes below). The binary and base64 forms carry framing that damage
	// breaks, so every mutation of them is refused by name, hands back the zero
	// value, and is counted refused under the reason it names. The legacy JSON
	// text is permissive: a flip can land on another valid box, so a mutation
	// of it is only ever refused by name when the strict read refuses it, and
	// is never read as absent, empty or zero.
	const fuzzSeed = 1000
	r = fuse.NewBoxDualReadReader()
	tries := 0
	refusedByReason := map[string]uint64{}
	for _, f := range forms {
		for _, m := range seededMutations(fuzzSeed, f.in) {
			got, _, err := r.Read(m)
			if f.name != "legacy" {
				ref := refusal(t, err, "FUSE_BOX")
				require.NotNil(t, ref, "a damaged %s (%d bytes) must be refused", f.name, len(m))
				assert.Equal(t, fuse.Box{}, got, "a refused binary read hands back the zero value")
				assert.Contains(t, err.Error(), "run: nova-fuse status -h", "the refusal names its remedy")
				refusedByReason[ref.Reason]++
				tries++
				continue
			}
			if err == nil {
				assert.NotEqual(t, fuse.Box{}, got, "a damaged legacy is never read as empty")
				continue
			}
			ref := refusal(t, err, "FUSE_BOX")
			require.NotNil(t, ref, "a damaged legacy was read as %v", got)
			assert.Equal(t, fuse.Box{}, got, "a refused legacy read hands back the zero value")
			assert.Contains(t, err.Error(), "run: nova-fuse status -h", "the refusal names its remedy")
			refusedByReason[ref.Reason]++
			tries++
		}
	}
	refusals := uint64(0)
	for k, n := range r.Counts().Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			refusals += n
		}
	}
	assert.Equal(t, uint64(tries), refusals, "every damaged read is counted refused, by reason")
	for reason, n := range refusedByReason {
		assert.Equal(t, n, r.Counts().Refused(reason), "refused counts by reason %s", reason)
	}
	// The FuseBox schema bounds no field (schemas/ledger/ledger.schema:56-79:
	// the box's strings and quarantine map carry no length or size bound), so
	// no input in the sweep can be refused as clamped.
	assert.Equal(t, uint64(0), r.Counts().Refused(schemaread.ReasonClamped), "no bounded field in FuseBox: nothing clamps")

	// A damaged box file stays CANNOT TELL through the read site, never a clear.
	for name, damagedBytes := range map[string][]byte{
		"binary-damaged":  wire[:len(wire)-4],
		"base64-damaged": b64[:len(b64)-4],
	} {
		p := filepath.Join(dir, name+".box")
		require.NoError(t, os.WriteFile(p, damagedBytes, 0o644))
		_, err := fuse.ReadBox(p)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "is not a readable fuse box", name)
	}
}

// seededMutations is in's damage classes for the fuzz sweep: every truncation,
// every single-byte flip, the three appended tails, and a seeded pseudo-random
// set drawn from the same classes (a flip, a cut, or an appended tail). The
// seed makes the sweep deterministic for the test run.
func seededMutations(seed uint64, in []byte) [][]byte {
	rng := rand.New(rand.NewSource(int64(seed)))
	out := make([][]byte, 0, 2*len(in)+100)
	for n := 0; n < len(in); n++ {
		out = append(out, bytes.Clone(in[:n]))
	}
	for i := range in {
		m := bytes.Clone(in)
		m[i] ^= 0xff
		out = append(out, m)
	}
	tails := []string{"\x00", "x", "AAAA"}
	for _, tail := range tails {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	for k := 0; k < 64; k++ {
		m := bytes.Clone(in)
		switch rng.Intn(3) {
		case 0:
			m[rng.Intn(len(m))] ^= 0xff
		case 1:
			m = m[:rng.Intn(len(m))]
		default:
			m = append(m, tails[rng.Intn(len(tails))]...)
		}
		out = append(out, m)
	}
	return out
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
