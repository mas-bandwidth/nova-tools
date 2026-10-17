package converge

// TestConvergeStateDualRead is the reader half of the convergence state's
// table adoption (v1.5 aspect 13; the owner's rules S2 and S4): the state is
// read alike from its fixed binary image (the ConvergeState table wire), the
// padded standard base64 of that image, and the legacy form its writer writes
// today; every damaged value of each form is refused by name
// (`CONVERGE_STATE REFUSED: <reason>; run: nova-check -h`) and never read as
// absent, empty or zero; a value past a bound is refused as clamped; and each
// read is counted under its form. A file is read through LoadState from the
// test's own t.TempDir().
//
// Unit tier: no clock, no socket, no program, no store.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/mas-bandwidth/nova-tools/pkg/tablemodelwire"
)

func TestConvergeStateDualRead(t *testing.T) {
	t.Parallel()

	want := State{Streams: map[string]StreamState{
		"LANDING": {Now: 1234.5, At: "2026-10-17T00:00:00Z", Widening: 2},
		"CLASSES": {Now: 99, At: "2026-10-17T00:00:01Z", Widening: 0},
		"PRS":     {Now: 1.25, At: "2026-10-17T00:00:02Z", Widening: 1},
	}}
	binary := stateImage(t, want)
	legacy, err := json.Marshal(want)
	require.NoError(t, err)

	forms := []struct {
		name string
		in   []byte
		form schemaread.Form
	}{
		{"binary", binary, schemaread.FormBinary},
		{"base64", []byte(base64.StdEncoding.EncodeToString(binary)), schemaread.FormBase64},
		{"legacy", legacy, schemaread.FormLegacy},
	}

	// each form reads alike to one equal value, counted once under its form
	r := schemaread.NewReader(stateCodec())
	for _, f := range forms {
		got, res, err := r.Read(f.in)
		require.NoError(t, err, f.name)
		require.Equal(t, f.form, res.Form, f.name)
		assert.Equal(t, want, got, "%s reads alike", f.name)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary), "each read counted once under its form")
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
	assert.Zero(t, r.Counts().Reads(schemaread.FormJSON))

	// the read site: LoadState reads a file in each form from the test's temp dir
	dir := t.TempDir()
	for _, f := range forms {
		path := filepath.Join(dir, f.name)
		require.NoError(t, os.WriteFile(path, f.in, 0o600))
		got, err := LoadState(path)
		require.NoError(t, err, "%s file", f.name)
		assert.Equal(t, want, got, "%s file reads alike", f.name)
	}

	// every damaged value of every form is refused by name, never read as
	// absent, empty or zero: each truncated prefix, a flipped byte and bytes
	// after its end
	tries := 0
	for _, f := range forms {
		for _, m := range damagedState(f.in) {
			got, _, err := r.Read(m)
			ref := stateRefusal(t, err, f.name, len(m))
			assert.Zero(t, got, "%s: a refused read hands back nothing", f.name)
			if ref != nil {
				tries++
			} else {
				assert.Fail(t, "%s: a damaged value (%d bytes) was read as %v", f.name, len(m), got)
			}
		}
	}
	assert.Equal(t, uint64(tries), refusedTally(r), "every damaged read is counted as refused, by reason")
	assert.Positive(t, tries, "the damage cases ran")

	// a clamped value is refused in every form
	clamped := State{Streams: map[string]StreamState{"X": {Now: 1, At: "2026-10-17T00:00:00Z", Widening: -1}}}
	cb := stateImage(t, clamped)
	cl, err := json.Marshal(clamped)
	require.NoError(t, err)
	for name, in := range map[string][]byte{
		"binary": cb,
		"base64": []byte(base64.StdEncoding.EncodeToString(cb)),
		"legacy": cl,
	} {
		got, _, err := r.Read(in)
		ref := stateRefusal(t, err, name, len(in))
		if assert.NotNil(t, ref, "%s: a clamped value was read as %v", name, got) {
			assert.Equal(t, schemaread.ReasonClamped, ref.Reason, name)
			assert.Zero(t, got, name)
		}
	}
}

// stateImage is the state as the ConvergeState table wire.
func stateImage(t *testing.T, st State) []byte {
	t.Helper()
	var b tablemodelwire.ConvergeStateBuilder
	require.True(t, b.Init(), "the converge state builder did not start")
	defer b.Shutdown()
	root := b.GetRoot()
	names := make([]string, 0, len(st.Streams))
	for n := range st.Streams {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := tablemodelwire.ConvergeStateStreamsAdd(&b.Main, &root.Streams)
		e.Now = st.Streams[n].Now
		e.Widening = int64(st.Streams[n].Widening)
		tablemodelwire.TableStringEmplace(&b.Main, &e.Name, []byte(n))
		tablemodelwire.TableStringEmplace(&b.Main, &e.At, []byte(st.Streams[n].At))
	}
	require.True(t, b.Lock(), "the converge state builder did not lock")
	n := tablemodelwire.ConvergeStateMeasure(b.AsConst())
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, tablemodelwire.ConvergeStateSave(b.AsConst(), buf))
	return buf
}

// stateRefusal is err as the reader's named refusal.
func stateRefusal(t *testing.T, err error, name string, n int) *schemaread.Refusal {
	t.Helper()
	require.Error(t, err, "%s: a damaged value (%d bytes) was not refused", name, n)
	var ref *schemaread.Refusal
	ok := errors.As(err, &ref)
	require.True(t, ok, "%s: want a named refusal, got %v", name, err)
	assert.Equal(t, "CONVERGE_STATE", ref.Name, name)
	return ref
}

// refusedTally is the total refusals counted across all reasons.
func refusedTally(r *schemaread.Reader[State]) uint64 {
	total := uint64(0)
	for k, v := range r.Counts().Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			total += v
		}
	}
	return total
}

// damagedState is in cut at each length (every length up to a cap, a stride
// past it), with a flipped byte in the middle, and with bytes after its end.
func damagedState(in []byte) [][]byte {
	out := make([][]byte, 0, len(in)+4)
	if len(in) <= 768 {
		for n := 0; n < len(in); n++ {
			out = append(out, bytes.Clone(in[:n]))
		}
	} else {
		for n := 0; n < 256; n++ {
			out = append(out, bytes.Clone(in[:n]), bytes.Clone(in[:len(in)-256+n]))
		}
		for n := 256; n < len(in)-256; n += len(in)/97 + 1 {
			out = append(out, bytes.Clone(in[:n]))
		}
	}
	if len(in) > 0 {
		// the form byte (first byte) flipped: no form knows it, so the read is
		// refused, never misread as a valid state
		flipped := bytes.Clone(in)
		flipped[0] ^= 0xff
		out = append(out, flipped)
	}
	for _, tail := range []string{"\x00", "x", "AAAA"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return out
}
