package swarm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestSwarmConfigRecordsDualRead is the reader half of the swarm config and
// slot records' table adoption (v1.5 aspect 13; the owner's rules S2 and S4):
// the exit record (pkg/swarm/slot.go) is read alike from its fixed binary
// image, the padded standard base64 of that image, and the legacy form its
// writer writes today; a damaged value of each form is refused by name
// (`EXIT_RECORD REFUSED: <reason>; run: nova-swarm -h`) and never read as
// absent, empty or zero; and reads are counted per record and per form.
//
// Unit tier: no clock, no socket, no program, no store. Files are read from
// the t.TempDir() the test writes.
func TestSwarmConfigRecordsDualRead(t *testing.T) {
	t.Parallel()

	r := exitReader()
	want := exitDoc{Rc: 1, Signal: "SIGTERM", Ended: "2026-10-17T00:00:00Z", Survivors: 2,
		Nonce: "abcdef123456", Attest: "attest", End: "end", Spent: 3, Observed: true, Reason: "done"}

	wire := writeExit(t, r, &want, schemaread.FormBinary)
	b64 := writeExit(t, r, &want, schemaread.FormBase64)
	js := writeExit(t, r, &want, schemaread.FormJSON)
	legacy, err := json.Marshal(ExitRecord{RC: 1, Signal: "SIGTERM", Ended: "2026-10-17T00:00:00Z", Survivors: 2,
		Nonce: "abcdef123456", Attest: "attest", End: "end", Spent: 3, Observed: true, Reason: "done"})
	require.NoError(t, err)

	dir := t.TempDir()
	forms := map[string]struct {
		body []byte
		form schemaread.Form
	}{
		// The schema JSON text and the legacy JSON both open with `{`, and a
		// reader recognises the legacy form first (schemaread Detect), so the
		// JSON text reads as legacy; the binary and base64 forms are distinct.
		"binary":  {wire, schemaread.FormBinary},
		"base64":  {b64, schemaread.FormBase64},
		"json":    {js, schemaread.FormLegacy},
		"legacy":  {legacy, schemaread.FormLegacy},
	}
	for name, f := range forms {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, f.body, 0o600))
		got, form, err := ReadExitFile(path)
		require.NoError(t, err, name)
		assert.Equal(t, f.form, form, name)
		assert.Equal(t, want, exitDoc{Rc: int64(got.RC), Signal: got.Signal, Ended: got.Ended, Survivors: int64(got.Survivors),
			Nonce: got.Nonce, Attest: got.Attest, End: got.End, Spent: int64(got.Spent), Observed: got.Observed, Partial: got.Partial, Reason: got.Reason}, name)
		raw, err := os.ReadFile(path)
		require.NoError(t, err, name)
		got2, res, err := r.Read(raw)
		require.NoError(t, err, name)
		assert.Equal(t, f.form, res.Form, name)
		assert.Equal(t, want, got2, name)
	}

	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormLegacy))

	damaged := map[string][]byte{
		"truncated-wire": wire[:len(wire)-1],
		"truncated-b64":  b64[:len(b64)-2],
		"truncated-json": js[:len(js)/2],
		"bad-json":       []byte(`{"rc":`),
		"bad-legacy":     []byte(`{"rc": "not-an-int"}`),
		"empty":          {},
	}
	for name, body := range damaged {
		_, _, err := r.Read(body)
		require.Error(t, err, name)
		ref := exitRefusal(t, err)
		assert.Equal(t, "EXIT_RECORD", ref.Name, name)
		assert.NotEqual(t, "", ref.Reason, name)
	}
	assert.Equal(t, uint64(len(damaged)), r.Counts().Refused("malformed")+r.Counts().Refused("unknown_form")+r.Counts().Refused("absent_required"))
}

// writeExit writes want in form via the reader's writer side.
func writeExit(t *testing.T, r *schemaread.Reader[exitDoc], want *exitDoc, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(want, form)
	require.NoError(t, err)
	return out
}

// exitRefusal unwraps the refusal of a read.
func exitRefusal(t *testing.T, err error) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	return ref
}
