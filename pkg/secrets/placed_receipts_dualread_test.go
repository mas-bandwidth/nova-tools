package secrets

import (
	"encoding/base64"
	"errors"
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The secrets place receipt read readReceipts reads is read alike from the
// binary image of the seatrec PlacedReceipt table, the padded standard base64
// of that image, and the legacy tab-separated form every writer writes today
// (S2), refused by name when damaged and counted per form (C13).

// receiptForms is one value in every form: the binary image and its base64
// made by the generated writer from the schema JSON text, and the legacy
// fixture exactly as writeReceipt writes a line.
type receiptForms struct {
	binary, base64, legacy []byte
}

func receiptImage(t testing.TB, model map[string]*string) (image, text []byte) {
	t.Helper()
	in := []byte("{")
	first := true
	for k, v := range model {
		if !first {
			in = append(in, ',')
		}
		first = false
		in = append(in, []byte(`"`+k+`":`+"\""+*v+"\"")...)
	}
	in = append(in, '}')
	var b wire.PlacedReceiptBuilder
	var r wire.TableReport
	require.True(t, b.Init() && wire.PlacedReceiptFromJson(&b, in, &r) && b.Lock(), "schema JSON text %s loads", in)
	v := b.AsConst()
	n := wire.PlacedReceiptMeasure(v)
	require.Positive(t, n)
	image = make([]byte, n)
	require.Equal(t, n, wire.PlacedReceiptSave(v, image))
	m := wire.PlacedReceiptToJsonMeasure(v)
	require.Positive(t, m)
	text = make([]byte, m)
	require.Equal(t, m, wire.PlacedReceiptToJson(v, text))
	return image, text
}

func receiptFormsOf(t testing.TB, rec placedReceipt) receiptForms {
	t.Helper()
	model := map[string]*string{}
	for k, v := range map[string]string{
		"secret": rec.Secret, "path": rec.Path, "file": rec.File,
		"head": rec.Head, "blob": rec.Blob, "stamp": rec.Stamp,
	} {
		val := v
		if val == "" {
			val = "-"
		}
		model[k] = &val
	}
	image, _ := receiptImage(t, model)
	legacy := []byte(rec.Secret + "\t" + rec.Path + "\t" + rec.File + "\t" + rec.Head + "\t" + rec.Blob + "\t" + rec.Stamp + "\n")
	return receiptForms{binary: image, base64: []byte(base64.StdEncoding.EncodeToString(image)), legacy: legacy}
}

func writeReceiptFile(t *testing.T, dir, machine string, body []byte) string {
	t.Helper()
	path := receiptPath(dir, machine)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

func receiptReads(c *schemaread.Counts) map[schemaread.Form]uint64 {
	return map[schemaread.Form]uint64{
		schemaread.FormBinary: c.Reads(schemaread.FormBinary), schemaread.FormBase64: c.Reads(schemaread.FormBase64),
		schemaread.FormLegacy: c.Reads(schemaread.FormLegacy),
	}
}

func TestPlacedReceiptsDualRead(t *testing.T) {
	t.Parallel()

	full := placedReceipt{Secret: "TOKEN", Path: "/srv/web/.config/nova-secrets/TOKEN.env", File: "seat.yaml", Head: "40hex", Blob: "40hex", Stamp: "2026-01-01T00:00:00Z"}
	forms := receiptFormsOf(t, full)

	t.Run("every form reads to one equal record", func(t *testing.T) {
		for _, f := range []struct {
			name string
			in   []byte
		}{{"binary", forms.binary}, {"base64", forms.base64}, {"legacy", forms.legacy}} {
			dir := t.TempDir()
			writeReceiptFile(t, dir, "web-1", f.in)
			got, err := readReceipts(dir, "web-1")
			require.NoError(t, err, "%s form", f.name)
			require.Len(t, got, 1)
			assert.Equal(t, full, got[0], "%s form reads to the record", f.name)
		}
	})

	t.Run("damage is refused by name in every form", func(t *testing.T) {
		mid := len(forms.binary) / 2
		flipped := append([]byte(nil), forms.binary...)
		flipped[mid] ^= 0xff
		clamp := append([]byte(nil), forms.binary...)
		clamp[0] = 9
		for _, tc := range []struct {
			name, want string
			in         []byte
		}{
			{"binary truncated", "REFUSED:", forms.binary[:len(forms.binary)-1]},
			{"binary flipped", "REFUSED:", flipped},
			{"base64 truncated", "REFUSED:", forms.base64[:len(forms.base64)-3]},
			{"base64 of the flipped image", "REFUSED:", []byte(base64.StdEncoding.EncodeToString(flipped))},
			{"legacy bad count", "want 6 tab-separated fields", []byte("TOKEN\t/path\tseat.yaml\tHEAD\textra\n")},
			{"clamped binary", "REFUSED:", clamp},
		} {
			dir := t.TempDir()
			path := writeReceiptFile(t, dir, "web-1", tc.in)
			_, err := readReceipts(dir, "web-1")
			require.Error(t, err, "%s", tc.name)
			assert.Contains(t, err.Error(), tc.want, "%s refusal", tc.name)
			assert.Contains(t, err.Error(), path, "%s names the receipt", tc.name)
		}
	})

	t.Run("reads are counted per form", func(t *testing.T) {
		r := newPlacedReceiptReader()
		for _, f := range []struct {
			in []byte
			form schemaread.Form
		}{{forms.binary, schemaread.FormBinary}, {forms.base64, schemaread.FormBase64}, {forms.legacy, schemaread.FormLegacy}} {
			_, _, err := r.r.Read(f.in)
			require.NoError(t, err)
			assert.Equal(t, uint64(1), r.counts.Reads(f.form), "%s form counted once", f.form)
		}
		assert.Equal(t, map[schemaread.Form]uint64{
			schemaread.FormBinary: 1, schemaread.FormBase64: 1, schemaread.FormLegacy: 1,
		}, receiptReads(r.counts))
	})

	t.Run("a malformed value is never read as absent, empty or zero", func(t *testing.T) {
		r := newPlacedReceiptReader()
		bad := [][]byte{
			forms.binary[:len(forms.binary)-1],
			[]byte("TOKEN\t/path\n"),
			[]byte(""),
		}
		for i, in := range bad {
			d, _, err := r.r.Read(in)
			if err == nil {
				continue
			}
			var refusal *schemaread.Refusal
			require.True(t, errors.As(err, &refusal), "case %d is a named refusal", i)
			assert.Zero(t, d.rec, "case %d never reads as a record", i)
		}
	})
}
