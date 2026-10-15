package friend

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The schema pong tracer (docs/research/schema-tracer/pong.md): the pong as a
// variable table (schemas/friend/pong.schema), generated into pkg/friend/pongwire
// by the pinned compiler, written by PongLine and read by ParsePong/ReadPong.

// refusalOf is the name a text is refused by, or "" when it reads.
func refusalOf(t *testing.T, text string) string {
	t.Helper()
	_, err := ReadPong(text)
	if err == nil {
		return ""
	}
	var r *PongRefused
	require.True(t, errors.As(err, &r), "every refusal is a *PongRefused: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), "PONG REFUSED: "+r.Reason+": "), err.Error())
	return r.Reason
}

// fixture is a pong written by a generated newer writer (testdata/writers), never by hand.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("pongwire", "testdata", name+".b64"))
	require.NoError(t, err)
	return strings.TrimSpace(string(b))
}

// wireOf is a schema pong's table wire, for byte-level damage.
func wireOf(t *testing.T, line string) []byte {
	t.Helper()
	body, ok := strings.CutPrefix(line, PongWirePrefix)
	require.True(t, ok, "%q is a schema pong", line)
	wire, err := base64.StdEncoding.DecodeString(body)
	require.NoError(t, err)
	return wire
}

func asLine(wire []byte) string { return PongWirePrefix + base64.StdEncoding.EncodeToString(wire) }

func TestPongSchemaRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		nonce                 string
		queue, working, width int
	}{
		{"typical", "abc123", 5, 3, 8},
		{"zeros", "k3x9q0", 0, 0, 0},
		{"every bound", strings.Repeat("z", 32), 100000, 10000, 1024},
		{"one-byte nonce", "n", 1, 1, 1},
		{"clock-digit nonce", "123456", 2, 0, 4},
	} {
		line := PongLine(tc.nonce, tc.queue, tc.working, tc.width)
		require.True(t, strings.HasPrefix(line, PongWirePrefix), "%s: the writer writes the schema pong: %q", tc.name, line)
		assert.NotContains(t, line, "\n", tc.name)
		n, q, w, wd, ok := ParsePong(line)
		require.True(t, ok, tc.name)
		assert.Equal(t, []any{tc.nonce, tc.queue, tc.working, tc.width}, []any{n, q, w, wd}, tc.name)
		p, err := ReadPong(line)
		require.NoError(t, err, tc.name)
		assert.False(t, p.Legacy, tc.name)
	}
}

// TestPongSchemaRefusalsNamed: every hostile pong is refused, and by the name
// the schema runtime (or its descriptors) gives it.
func TestPongSchemaRefusalsNamed(t *testing.T) {
	t.Parallel()
	good := PongLine("abc123", 5, 3, 8)
	wire := wireOf(t, good)
	newerForm := append([]byte{}, wire...)
	newerForm[0] = 9 // the form byte: a form this reader does not know
	for _, tc := range []struct{ name, text, reason string }{
		{"truncated wire", asLine(wire[:len(wire)-1]), "wire_damaged"},
		{"truncated to the form byte", asLine(wire[:1]), "wire_damaged"},
		{"trailing junk in the wire", asLine(append(append([]byte{}, wire...), 0x00, 0x01)), "wire_damaged"},
		{"trailing junk after the body", good + " junk", PongBase64},
		{"truncated base64", good[:len(good)-3], PongBase64},
		{"empty body", PongWirePrefix, "wire_damaged"},
		{"unknown newer form", asLine(newerForm), "unknown_form"},
		{"oversize nonce from a newer writer", fixture(t, "oversize-nonce"), PongClamped},
		{"a field that changed kind", fixture(t, "width-changed-kind"), PongKindMismatch},
		{"no pong at all", "hello pong abc123", PongNotAPong},
		{"empty", "", PongNotAPong},
	} {
		assert.Equal(t, tc.reason, refusalOf(t, tc.text), tc.name)
		_, _, _, _, ok := ParsePong(tc.text)
		assert.False(t, ok, tc.name)
	}
	// every truncation of a good wire is refused, never read as a shorter pong
	for i := range len(wire) {
		assert.NotEmpty(t, refusalOf(t, asLine(wire[:i])), "cut at byte %d", i)
	}
}

// TestPongSchemaNewerFieldRead pins the variable table's rule (UNION.md D1): a
// newer writer's unknown field is skipped and counted by the runtime, not refused.
func TestPongSchemaNewerFieldRead(t *testing.T) {
	t.Parallel()
	n, q, w, wd, ok := ParsePong(fixture(t, "newer-field"))
	require.True(t, ok)
	assert.Equal(t, []any{"abc123", 5, 3, 8}, []any{n, q, w, wd})
}

// TestPongSchemaWriterRefusals: what the table cannot carry is refused by
// EncodePong, with the bound read from the generated descriptors; PongLine then
// writes the legacy line, which the reader refuses by the same name.
func TestPongSchemaWriterRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		nonce                 string
		queue, working, width int
		reason                string
	}{
		{"negative width", "abc123", 5, 3, -1, PongOutOfRange},
		{"queue over bound", "abc123", 100001, 3, 8, PongOutOfRange},
		{"working over bound", "abc123", 0, 10001, 8, PongOutOfRange},
		{"width over bound", "abc123", 0, 0, 1025, PongOutOfRange},
		{"oversize nonce", strings.Repeat("n", 33), 5, 3, 8, PongOutOfRange},
		{"empty nonce", "", 5, 3, 8, PongNonceAbsent},
		{"nonce not UTF-8", "\xff\xfe", 5, 3, 8, PongMalformed},
	} {
		_, err := EncodePong(tc.nonce, tc.queue, tc.working, tc.width)
		var r *PongRefused
		require.True(t, errors.As(err, &r), tc.name)
		assert.Equal(t, tc.reason, r.Reason, tc.name)
		line := PongLine(tc.nonce, tc.queue, tc.working, tc.width)
		assert.Equal(t, LegacyPongLine(tc.nonce, tc.queue, tc.working, tc.width), line, tc.name)
		assert.NotEmpty(t, refusalOf(t, line), "%s: the fallback line is refused at the reader", tc.name)
	}
}

// TestPongLegacyLineStillRead: the legacy text pong is read for one release,
// strictly.
func TestPongLegacyLineStillRead(t *testing.T) {
	t.Parallel()
	p, err := ReadPong(LegacyPongLine("abc123", 5, 3, 8))
	require.NoError(t, err)
	assert.Equal(t, Pong{Nonce: "abc123", Queue: 5, Working: 3, Width: 8, Legacy: true}, p)
	n, q, w, wd, ok := ParsePong("pong k3x9q0 queue=0 working=0 width=4")
	require.True(t, ok)
	assert.Equal(t, []any{"k3x9q0", 0, 0, 4}, []any{n, q, w, wd})
}

// TestPongLegacyRefusalsRed was the red baseline (today's lenient ParsePong read
// every one of these as a pong); each is now refused by name. The bare
// "pong <nonce>" that read as silent zeros is refused: no nova-tools writer
// produces it (PongLine always wrote all four fields).
func TestPongLegacyRefusalsRed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, text, reason string }{
		{"truncated", "pong abc123 queue=5 work", PongLegacyShape},
		{"trailing junk", "pong abc123 queue=5 working=3 width=8 junk", PongLegacyShape},
		{"oversize nonce", "pong " + strings.Repeat("n", 33) + " queue=5 working=3 width=8", PongOutOfRange},
		{"negative width", "pong abc123 queue=5 working=3 width=-1", PongOutOfRange},
		{"queue over bound", "pong abc123 queue=100001 working=3 width=8", PongOutOfRange},
		{"silent zeros", "pong abc123", PongLegacyShape},
		{"fields out of order", "pong abc123 working=3 queue=5 width=8", PongLegacyShape},
		{"double space", "pong  abc123 queue=5 working=3 width=8", PongLegacyShape},
	} {
		_, _, _, _, ok := ParsePong(tc.text)
		assert.False(t, ok, "%s: %q read as a pong", tc.name, tc.text)
		assert.Equal(t, tc.reason, refusalOf(t, tc.text), tc.name)
	}
}

// TestPongSchemaBytesOnWire records the cost against the legacy line.
func TestPongSchemaBytesOnWire(t *testing.T) {
	t.Parallel()
	legacy := LegacyPongLine("abc123", 5, 3, 8)
	line := PongLine("abc123", 5, 3, 8)
	assert.Len(t, legacy, 37)
	assert.Len(t, wireOf(t, line), 69, "table wire bytes")
	assert.Len(t, line, len(PongWirePrefix)+92, "bus body bytes")
}
