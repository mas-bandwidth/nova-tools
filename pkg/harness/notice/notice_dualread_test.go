package notice_test

// TestNoticeEntryDualRead is the reader half of the saved notice's table
// adoption (v1.5 aspect 10, the notice entries; the owner's rules S2 and S4):
// the notice the Core writes to a saved file is read alike from its binary
// wire, the padded standard base64 of it, the schema JSON text, and the legacy
// JSON the writer holds today; every damaged value of each form is refused by
// name (`NOTICE_ENVELOPE REFUSED: <reason>; run: nova-friend help`) and never
// read as absent, empty or zero; a value past a bound is refused as clamped;
// each read is counted under its form; and the files are read from a
// t.TempDir() the test writes.
//
// Unit tier: no clock, no socket, no store. The value is data under the test
// clock's origin.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice/noticeread"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleNotice is a saved notice the Core could hold: an envelope with a
// challenge, senders and recovery, the shapes the legacy JSON carries.
func sampleNotice() notice.Envelope {
	at := time.Unix(1000, 0).UTC()
	msg := bus.Message{ID: "01J0000000000000000000000A", From: "rowan", To: []string{"stella", "emma"}, CC: []string{"aurora"},
		Subject: "s", Re: "01J0000000000000000000000B", Card: "c", Head: strings.Repeat("a", 40), Kind: bus.KindAsk, At: at,
		Body: "body", Wait: 5 * time.Minute, Due: at.Add(5 * time.Minute), Token: "tok", Once: "o"}
	msg.Marks = []bus.Mark{{Key: "k", Field: "f", Value: "v", Forward: true}}
	var e notice.Envelope
	e.LocalReceiver.Identity, e.LocalReceiver.Redis, e.LocalReceiver.Proof, e.LocalReceiver.Acknowledge = "stella", "r:1", "p", "a"
	e.SavedAt, e.HookEpoch, e.HookRole, e.EventID, e.Challenge, e.Messages = at, 9, hook.Friend, "e1", &msg, 4
	e.Senders, e.RecoverySince, e.RecoveryCoverage, e.Recovery, e.PossibleDuplicates = []string{"rowan", "emma"}, "since", "cov", []bus.Message{msg, msg}, true
	return e
}

// norm is the envelope as JSON with a nil list as an empty list, the one
// difference between the struct's JSON and the table's.
func norm(e notice.Envelope) string {
	var v any
	raw, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	b, err := json.Marshal(nullListsAsEmptyAny(v))
	if err != nil {
		return ""
	}
	return string(b)
}

// nullListsAsEmptyAny is the test's copy of the friendfile golden bridge: a
// nil list is JSON null in the struct's JSON and empty in the table.
func nullListsAsEmptyAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil {
				switch k {
				case "To", "CC", "senders", "recovery_messages", "Marks":
					x[k] = []any{}
					continue
				}
			}
			x[k] = nullListsAsEmptyAny(e)
		}
	case []any:
		for i := range x {
			x[i] = nullListsAsEmptyAny(x[i])
		}
	}
	return v
}

func TestNoticeEntryDualRead(t *testing.T) {
	t.Parallel()
	want := sampleNotice()
	legacy, err := json.Marshal(want)
	require.NoError(t, err)

	t.Run("legacy", func(t *testing.T) {
		t.Parallel()
		r := noticeread.New()
		got, err := r.Read(legacy)
		require.NoError(t, err)
		assert.Equal(t, norm(want), norm(got), "the legacy JSON reads back the notice")
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy))
	})

	forms := []struct {
		name string
		form schemaread.Form
	}{
		{"binary", schemaread.FormBinary},
		{"base64", schemaread.FormBase64},
		{"json", schemaread.FormJSON},
	}
	dir := t.TempDir()
	for _, f := range forms {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			r := noticeread.New()
			raw, err := r.Write(want, f.form)
			require.NoError(t, err)
			require.NotEmpty(t, raw)
			path := filepath.Join(dir, f.name)
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			got, err := r.Read(raw)
			require.NoError(t, err, f.name)
			assert.Equal(t, norm(want), norm(got), "%s reads back the notice", f.name)
			assert.Equal(t, uint64(1), r.Counts().Reads(f.form), "%s counted once", f.name)
		})
	}

	t.Run("malformed refused by name", func(t *testing.T) {
		t.Parallel()
		wire, err := noticeread.New().Write(want, schemaread.FormBinary)
		require.NoError(t, err)
		b64 := base64.StdEncoding.EncodeToString(wire)
		schema, err := noticeread.New().Write(want, schemaread.FormJSON)
		require.NoError(t, err)
		flipped := append([]byte(nil), wire...)
		flipped[0] ^= 0xff
		bad := map[string]struct {
			image []byte
		}{
			"binary cut in half":       {wire[:len(wire)/2]},
			"binary one byte short":    {wire[:len(wire)-1]},
			"binary form byte flipped": {flipped},
			"base64 cut":               {[]byte(b64[:3])},
			"base64 foreign byte":      {[]byte(b64[:4] + "!" + b64[5:])},
			"json cut in half":         {schema[:len(schema)/2]},
			"garbage":                  {[]byte("not a notice")},
		}
		for name, tc := range bad {
			r := noticeread.New()
			_, err := r.Read(tc.image)
			var refusal *schemaread.Refusal
			if assert.True(t, errors.As(err, &refusal), "%s: want a named refusal", name) {
				assert.Equal(t, "NOTICE_ENVELOPE", refusal.Name, "%s: the refusal names the record", name)
				assert.NotEmpty(t, refusal.Reason, "%s: the refusal names its reason", name)
				assert.NotZero(t, r.Counts().Refused(refusal.Reason), "%s: the refusal is counted", name)
			}
		}
	})

	t.Run("clamped refused", func(t *testing.T) {
		t.Parallel()
		over := strings.Repeat("n", 65)
		r := noticeread.New()
		raw := strings.Replace(string(legacy), `"senders":["rowan","emma"]`, `"senders":["`+over+`"]`, 1)
		_, err := r.Read([]byte(raw))
		var refusal *schemaread.Refusal
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, schemaread.ReasonClamped, refusal.Reason, "a sender past its bound")
	})
}
