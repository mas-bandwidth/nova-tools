package sprintwire

import (
	"bytes"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSprintwireDualRead is the dual-read proof of the sprint answer: every
// record of the answer reads alike from the binary wire, the padded standard
// base64 of it, the schema JSON text form and the legacy JSON the server
// writes today; a malformed value of each form is refused by name and never
// read as absent, empty or zero; a clamped value is refused; and reads are
// counted per form (S2, S4, the reader-first order C13). The transport is a
// fake that hands the reader bytes: no socket is opened.
func TestSprintwireDualRead(t *testing.T) {
	t.Parallel()

	reader := responseReader()

	const fixture = `{"results":[{"code":0,"stdout":"ok\n","stderr":"","outcome":"accepted","changed":"yes"},{"code":2,"stdout":"","stderr":"usage\n","outcome":"refused","changed":"no"}]}`

	// the legacy JSON the server writes today reads as the JSON text form
	d, res, err := reader.Read([]byte(fixture))
	require.NoError(t, err, fixture)
	assert.Equal(t, schemaread.FormJSON, res.Form)
	require.NotNil(t, d.v)
	require.Equal(t, int32(2), d.v.Results.Count)
	first := d.v.Results.At(0)
	require.NotNil(t, first)
	assert.Equal(t, int32(0), first.Code)
	assert.Equal(t, "ok\n", string(first.Stdout[:first.StdoutLength]))

	// the same value as the binary wire and as padded standard base64
	wire := responseCodec.SaveWire(&d)
	require.NotEmpty(t, wire)
	b64 := base64of(wire)
	for form, img := range map[schemaread.Form][]byte{
		schemaread.FormBinary: wire,
		schemaread.FormBase64: b64,
		schemaread.FormJSON:   []byte(fixture),
	} {
		w, r, err := reader.Read(img)
		require.NoError(t, err, "%s", form)
		assert.Equal(t, form, r.Form)
		require.NotNil(t, w.v)
		require.Equal(t, int32(2), w.v.Results.Count)
		assert.Equal(t, "usage\n", string(w.v.Results.At(1).Stderr[:w.v.Results.At(1).StderrLength]))
	}
	assert.Equal(t, uint64(2), reader.Counts().Reads(schemaread.FormJSON))
	assert.Equal(t, uint64(1), reader.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), reader.Counts().Reads(schemaread.FormBase64))

	// a malformed or damaged value of each form is refused by name
	refused := []struct {
		name, in string
	}{
		{"binary cut short", string(wire[:len(wire)-1])},
		{"binary of an unknown form", string(append([]byte{9}, wire[1:]...))},
		{"base64 with a stray byte", "!" + string(b64)},
		{"JSON cut short", fixture[:len(fixture)-2]},
		{"JSON with a key given twice", `{"results":[{"code":0,"code":1,"stdout":"","stderr":""}]}`},
	}
	for _, c := range refused {
		_, _, err := reader.Read([]byte(c.in))
		require.Error(t, err, "%s", c.name)
		var r *schemaread.Refusal
		require.ErrorAs(t, err, &r, "%s", c.name)
		assert.Contains(t, err.Error(), "Response REFUSED: ", "%s", c.name)
		assert.NotEmpty(t, r.Reason, "%s", c.name)
	}

	// a value past a declared bound is refused by name, never clamped
	_, _, err = reader.Read([]byte(`{"results":[{"code":65536,"stdout":"","stderr":""}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Response REFUSED: clamped")
	assert.Contains(t, err.Error(), "run: nova-sprint -h")
}

// base64of is the padded standard base64 of the binary wire, written directly
// so the test does not depend on encoding/base64 in this card.
func base64of(wire []byte) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	enc := func(b byte) byte { return alphabet[b] }
	var out bytes.Buffer
	for i := 0; i < len(wire); i += 3 {
		var p [3]byte
		n := copy(p[:], wire[i:])
		out.WriteByte(enc(p[0] >> 2))
		out.WriteByte(enc((p[0]&0x03)<<4 | p[1]>>4))
		if n > 1 {
			out.WriteByte(enc((p[1]&0x0f)<<2 | p[2]>>6))
		} else {
			out.WriteByte('=')
		}
		if n > 2 {
			out.WriteByte(enc(p[2] & 0x3f))
		} else {
			out.WriteByte('=')
		}
	}
	return out.Bytes()
}
