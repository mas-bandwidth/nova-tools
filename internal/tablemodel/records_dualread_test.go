package tablemodel

// TestTableModelRecordsDualRead is the reader half of the table model's table
// adoption (the owner's rules S2 and S4, readers first): every record the
// table model reads back (the State record first, the record trace.json holds
// at every step) is read alike from its binary image (the table wire), the
// padded standard base64 of that image, and the legacy form its writer writes
// today; a damaged value of each form is refused by name (`STATE REFUSED:
// <reason>; run: <help>`) and never read as absent, empty or zero; and each
// read is counted under its record and its form.
//
// Unit tier: no clock, no socket, no store. The readers run over fixed byte
// slices; the files a real run writes are read from a t.TempDir() this test
// writes.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableModelRecordsDualRead(t *testing.T) {
	t.Parallel()

	want := State{
		Live:   []Phys{{"t1", 1}, {"t2", 2}},
		Rows:   []RowAt{{Phys{"t1", 1}, "r1"}, {Phys{"t2", 2}, "r2"}},
		Binds:  []Bind{{Cell{Phys{"t1", 1}, "r1", "c1"}, Cell{Phys{"external", 0}, "external", "external"}}},
		Data:   []Datum{{Cell{Phys{"t1", 1}, "r1", "c1"}, "m1", 1}, {Cell{Phys{"external", 0}, "external", "external"}, "m2", 2}},
		Place:  []Placement{{"m1", []Location{{Phys{"t1", 1}, Cell{Phys{"t1", 1}, "r1", "c1"}}, {Phys{"t2", 2}, NoPlace}}}},
		Active: 1,
		Seen:   map[string]int{"w1": 1, "w2": 1},
	}

	wire, err := stateWire(want)
	require.NoError(t, err)
	legacy, err := json.Marshal(want)
	require.NoError(t, err)

	inputs := []struct {
		form string
		in   []byte
	}{
		{"binary", wire},
		{"base64", []byte(base64.StdEncoding.EncodeToString(wire))},
		{"legacy", legacy},
	}

	r := newStateReader()
	for _, in := range inputs {
		v, _, err := r.Read(in.in)
		require.NoError(t, err, "%s: %v", in.form, err)
		tassert.Equal(t, want, v.s, "a State reads alike from its %s", in.form)
	}
	tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary), "each read counted once under its form")
	tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64), "each read counted once under its form")
	tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy), "each read counted once under its form")

	// every damaged value of every form is refused by name, never read as
	// absent, empty or zero: each prefix of the value and the value with bytes
	// after its end
	r = newStateReader()
	tries := 0
	for _, in := range inputs {
		for _, m := range damaged(in.in) {
			v, _, err := r.Read(m)
			require.NotNil(t, refusal(t, err, "STATE"), "%s: a damaged %s (%d bytes) was read as %v", "STATE", in.form, len(m), v.s)
			var zero State
			tassert.Equal(t, zero, v.s, "a refused read hands back nothing")
			tries++
		}
	}
	refusals := uint64(0)
	for k, n := range r.Counts().Snapshot() {
		if bytes.HasPrefix([]byte(k), []byte("refused_")) {
			refusals += n
		}
	}
	tassert.Equal(t, uint64(tries), refusals, "every damaged read is counted as refused, by reason")

	// an invalid base64 text is refused by name, counted under the base64 form
	r = newStateReader()
	_, _, err = r.Read([]byte("abcd===="))
	require.NotNil(t, refusal(t, err, "STATE"), "an invalid base64 text was read as a state")
	require.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
}

// refusal is err as the reader's named refusal of record name.
func refusal(t *testing.T, err error, name string) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	if !tassert.True(t, errors.As(err, &ref), "%s: want a named refusal, got %v", name, err) {
		return nil
	}
	tassert.Equal(t, name, ref.Name)
	return ref
}

// damaged is in cut at each length and in with bytes after its end.
func damaged(in []byte) [][]byte {
	var cuts []int
	for n := 0; n < len(in); n++ {
		cuts = append(cuts, n)
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
