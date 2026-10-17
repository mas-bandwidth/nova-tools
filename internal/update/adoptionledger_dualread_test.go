package update

// TestAdoptionLedgerDualRead pins the adoption ledger's one read site
// (loadAdoption) to pkg/schemaread and the generated releasewire table
// AdoptionLedger: the ledger written as the legacy five-column TSV, the
// table's binary image, the padded standard base64 of that image and the
// schema JSON text all read as one equal value; a damaged or out-of-list
// value of each form is refused by name and never read as absent, empty or
// zero; and every read is counted under the form it arrived in (the owner's
// S2 and S4, UNION.md section 6, C13). No clock, socket or network.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/require"
)

const adoptionLedgerHelp = "nova-update adoption -h"

// adoptionRow is one ledger row as the JSON text form spells it: the state is
// the vocabulary's own name (useful_now), not the legacy file's (useful-now).
type adoptionRow struct {
	Tool    string `json:"tool"`
	Friend  string `json:"friend"`
	State   string `json:"state"`
	Version string `json:"version"`
	Detail  string `json:"detail"`
}

// adoptionWant is the two-row value every form must read back.
var adoptionWant = []adoption{
	{Tool: "nova-bus", Friend: "rowan", State: "adopted", Version: "0.12.1", Detail: "first-use trial"},
	{Tool: "nova-tokens", Friend: "rowan", State: "useful-now", Version: "-", Detail: "keeps its own ledger"},
}

// adoptionLegacy is the file people write by hand: the exact header, a
// # comment line and five tab-separated fields per row.
func adoptionLegacy() []byte {
	return []byte(adoptionHeader + "\n" +
		"# a comment people keep in the file\n" +
		"nova-bus\trowan\tadopted\t0.12.1\tfirst-use trial\n" +
		"nova-tokens\trowan\tuseful-now\t-\tkeeps its own ledger\n")
}

// adoptionJSON is the schema JSON text form of adoptionWant.
func adoptionJSON(t *testing.T) []byte {
	t.Helper()
	rows := make([]adoptionRow, 0, len(adoptionWant))
	for _, r := range adoptionWant {
		rows = append(rows, adoptionRow{
			Tool: r.Tool, Friend: r.Friend,
			State:   strings.ReplaceAll(r.State, "-", "_"),
			Version: r.Version, Detail: r.Detail,
		})
	}
	text, err := json.Marshal(struct {
		Rows []adoptionRow `json:"rows"`
	}{Rows: rows})
	require.NoError(t, err)
	return text
}

// adoptionImage writes the JSON text as the table's binary wire through the
// generated builder, so the image is the writer's and not the test's.
func adoptionImage(t *testing.T, text []byte) []byte {
	t.Helper()
	var b wire.AdoptionLedgerBuilder
	require.True(t, b.Init())
	var rep wire.TableReport
	require.True(t, wire.AdoptionLedgerFromJson(&b, text, &rep), "from JSON: %+v", rep)
	require.True(t, b.Lock())
	v := b.AsConst()
	n := wire.AdoptionLedgerMeasure(v)
	require.Positive(t, n)
	image := make([]byte, n)
	require.EqualValues(t, n, wire.AdoptionLedgerSave(v, image))
	return image
}

func adoptionBase64(image []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(image))
}

// adoptionReasons reads one case through a reader of the test's own and
// returns the refusal and the form's count before and after.
func adoptionRead(t *testing.T, r *schemaread.Reader[adoptionLedgerValue], in []byte) ([]adoption, schemaread.Form, uint64, uint64, error) {
	t.Helper()
	before := schemaread.FormNone
	before = r.Detect(in)
	was := r.Counts().Reads(before)
	rows, err := loadAdoptionThrough(r, bytes.NewReader(in))
	return rows, before, was, r.Counts().Reads(before), err
}

func TestAdoptionLedgerDualRead(t *testing.T) {
	t.Parallel()

	legacy := adoptionLegacy()
	text := adoptionJSON(t)
	image := adoptionImage(t, text)
	b64 := adoptionBase64(image)
	want := adoptionWant

	good := []struct {
		name string
		form schemaread.Form
		in   []byte
	}{
		{"legacy TSV", schemaread.FormLegacy, legacy},
		{"binary image", schemaread.FormBinary, image},
		{"padded standard base64", schemaread.FormBase64, b64},
		{"schema JSON text", schemaread.FormJSON, text},
	}
	for _, c := range good {
		c := c
		t.Run("reads "+c.name, func(t *testing.T) {
			t.Parallel()
			got, err := loadAdoption(bytes.NewReader(c.in))
			require.NoError(t, err, c.name)
			require.Equal(t, want, got, "%s reads the one value", c.name)
		})
	}

	t.Run("reads the legacy fixture from a t.TempDir file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "adoption.tsv")
		require.NoError(t, os.WriteFile(path, legacy, 0o644))
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()
		got, err := loadAdoption(f)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("counts each read under the form read", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(adoptionLedgerCodec())
		for _, c := range good {
			got, form, before, after, err := adoptionRead(t, r, c.in)
			require.NoError(t, err, c.name)
			require.Equal(t, want, got, c.name)
			require.Equal(t, c.form, form, "%s: detected form", c.name)
			require.Equal(t, before+1, after, "%s: the %s counter moves once", c.name, form)
		}
	})

	// A state past the table's list, in each form: the wire's vocabulary id
	// names no variant, so the value reads as None and the required field
	// refuses it by name (never as zero); the legacy file's own state word is
	// refused with the old line message.
	// The state vocabulary id is the eight bytes that differ between two
	// images of the same row with two different states.
	one := adoptionImage(t, adoptionJSONOf(t, []adoptionRow{{
		Tool: "nova-bus", Friend: "rowan", State: "adopted",
		Version: "0.12.1", Detail: "first-use trial",
	}}))
	other := adoptionImage(t, adoptionJSONOf(t, []adoptionRow{{
		Tool: "nova-bus", Friend: "rowan", State: "tried",
		Version: "0.12.1", Detail: "first-use trial",
	}}))
	off := -1
	for i := range one {
		if one[i] != other[i] {
			off = i
			break
		}
	}
	require.NotEqual(t, -1, off, "the two state words differ")
	clampedImage := bytes.Clone(one)
	for i := off; i < off+8 && i < len(clampedImage); i++ {
		clampedImage[i] = 0
	}

	bad := []struct {
		name   string
		form   schemaread.Form
		in     []byte
		reason string
	}{
		{"legacy, a row cut short", schemaread.FormLegacy,
			[]byte(adoptionHeader + "\nnova-bus\trowan\n"), "2 fields"},
		{"legacy, a state it does not name", schemaread.FormLegacy,
			[]byte(adoptionHeader + "\nnova-bus\trowan\tninth\t0.1.0\ttried\n"), "unknown state"},
		{"legacy, a byte flipped in the header", schemaread.FormLegacy,
			[]byte("uool\tfriend\tstate\tversion\tdetail\n"), ""},
		{"binary, truncated", schemaread.FormBinary,
			image[:len(image)-1], schemaread.ReasonWireDamaged},
		{"binary, a byte flipped in the form", schemaread.FormBinary,
			append([]byte{9}, image[1:]...), schemaread.ReasonUnknownForm},
		{"binary, a state past the list", schemaread.FormBinary,
			clampedImage, schemaread.ReasonAbsentRequired},
		{"base64, truncated", schemaread.FormBase64,
			b64[:len(b64)-3], schemaread.ReasonMalformed},
		{"base64, a byte flipped in the form", schemaread.FormBase64,
			adoptionFlip(b64), schemaread.ReasonUnknownForm},
		{"base64, a state past the list", schemaread.FormBase64,
			adoptionBase64(clampedImage), schemaread.ReasonAbsentRequired},
		{"JSON, truncated", schemaread.FormJSON,
			bytes.TrimSpace(text)[:len(bytes.TrimSpace(text))-1], schemaread.ReasonMalformed},
		{"JSON, a byte flipped in a key", schemaread.FormJSON,
			bytes.Replace(text, []byte(`"tool"`), []byte(`"toom"`), 1), schemaread.ReasonAbsentRequired},
		{"JSON, a state past the list", schemaread.FormJSON,
			adoptionJSONOf(t, []adoptionRow{{Tool: "nova-bus", Friend: "rowan", State: "ninth", Version: "0.1.0", Detail: "tried"}}), schemaread.ReasonAbsentRequired},
	}
	for _, c := range bad {
		c := c
		t.Run("refuses "+c.name, func(t *testing.T) {
			t.Parallel()
			r := schemaread.NewReader(adoptionLedgerCodec())
			got, form, before, after, err := adoptionRead(t, r, c.in)
			require.Error(t, err, c.name)
			require.Nil(t, got, "a refused read returns no rows")
			require.Equal(t, c.form, form, "%s: detected form", c.name)
			require.Equal(t, before+1, after, "%s: counted under %s", c.name, form)
			var ref *schemaread.Refusal
			require.True(t, errors.As(err, &ref), "%s: every refusal is a *schemaread.Refusal: %v", c.name, err)
			require.Equal(t, "AdoptionLedger", ref.Name, c.name)
			require.Equal(t, adoptionLedgerHelp, ref.Help, c.name)
			require.Contains(t, err.Error(), "AdoptionLedger REFUSED: ", c.name)
			require.Contains(t, err.Error(), "; run: "+adoptionLedgerHelp, c.name)
			if c.reason != "" {
				require.Contains(t, err.Error(), c.reason, c.name)
			}
		})
	}
}

// adoptionJSONOf marshals rows without the test's fixed two-row value, for the
// damaged and past-the-list cases.
func adoptionJSONOf(t *testing.T, rows []adoptionRow) []byte {
	t.Helper()
	text, err := json.Marshal(struct {
		Rows []adoptionRow `json:"rows"`
	}{Rows: rows})
	require.NoError(t, err)
	return text
}

// adoptionFlip changes the first base64 character, which stays inside the
// alphabet: the wire it decodes to leads with a form byte this reader does not
// know.
func adoptionFlip(b64 []byte) []byte {
	out := bytes.Clone(b64)
	if out[0] == 'A' {
		out[0] = 'B'
	} else {
		out[0] = 'A'
	}
	return out
}
