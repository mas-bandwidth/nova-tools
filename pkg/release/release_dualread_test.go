package release

// TestReleaseCycleRecordsDualRead pins the release cycle's record readers
// (records.go; Aspect 13 part 1, the owner's S2 and S4, readers first,
// UNION.md C13): the cycle's build arguments, the journey evidence header and
// event lines and the spend receipts each read alike from the binary wire, the
// padded standard base64 of it, the schema JSON text form and the legacy form
// the writer writes today; a damaged value of each form is refused by name and
// never read as absent, empty or zero; a clamped value is refused; and reads
// are counted per record and per form. Files are read from a t.TempDir() the
// test writes. The test uses no clock, no socket and no network.

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dualRecord is one record under test: its reader, a value, and the value's
// legacy text as its writer writes it today.
type dualRecord[T any] struct {
	name   string
	reader func(*Records) *schemaread.Reader[T]
	value  T
	legacy []byte
	// legacyForm is the form the legacy text is counted under: legacy, or
	// json where the legacy text is the schema JSON text form byte for byte.
	legacyForm schemaread.Form
}

var typedForms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

// named asserts err is the reader's named refusal and returns its reason.
func named(t *testing.T, name string, err error) string {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "%s: every refusal is a *schemaread.Refusal: %v", name, err)
	assert.True(t, strings.HasPrefix(err.Error(), name+" REFUSED: "), err.Error())
	assert.Contains(t, err.Error(), "; run: nova-update release ")
	return ref.Reason
}

func isZero[T any](v T) bool { var zero T; return reflect.DeepEqual(v, zero) }

// check runs one record through every form, from files in dir.
func (d dualRecord[T]) check(t *testing.T, dir string) {
	t.Helper()
	recs := NewRecords()
	r := d.reader(recs)
	images := map[schemaread.Form][]byte{}
	for _, form := range typedForms {
		out, err := r.Write(&d.value, form)
		require.NoError(t, err, "%s %s: the writer writes it", d.name, form)
		require.NotEmpty(t, out)
		images[form] = out
	}
	assert.Equal(t, byte(1), images[schemaread.FormBinary][0], "%s: the wire leads with its form byte", d.name)
	assert.Equal(t, base64.StdEncoding.EncodeToString(images[schemaread.FormBinary]), string(images[schemaread.FormBase64]),
		"%s: base64 is padded standard base64 of the wire", d.name)

	// each form, and the legacy text, read back equal from a file
	read := func(form string, img []byte, want schemaread.Form) {
		p := filepath.Join(dir, d.name+"."+form)
		require.NoError(t, os.WriteFile(p, img, 0o600))
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		got, res, err := r.Read(raw)
		require.NoError(t, err, "%s %s", d.name, form)
		assert.Equal(t, want, res.Form, "%s %s: read as its form", d.name, form)
		assert.Equal(t, d.value, got, "%s %s: reads equal", d.name, form)
	}
	for _, form := range typedForms {
		read(form.String(), images[form], form)
	}
	read("legacy", d.legacy, d.legacyForm)

	// reads are counted per record and per form
	for _, form := range typedForms {
		want := uint64(1)
		if form == d.legacyForm {
			want++
		}
		assert.Equal(t, want, r.Counts().Reads(form), "%s: reads of %s are counted", d.name, form)
	}
	if d.legacyForm == schemaread.FormLegacy {
		assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormLegacy), "%s: legacy reads are counted", d.name)
	}

	// every truncation of every form is refused by name; every flipped byte is
	// refused by name or reads a whole value, never an absent, empty or zero one
	images[schemaread.FormLegacy] = d.legacy
	for form, img := range images {
		for cut := 0; cut < len(img); cut++ {
			got, _, err := r.Read(img[:cut])
			require.Error(t, err, "%s %s cut at %d is refused", d.name, form, cut)
			named(t, d.name, err)
			assert.True(t, isZero(got), "%s %s: a refused read returns no value", d.name, form)
		}
		for i := range img {
			bad := append([]byte(nil), img...)
			bad[i] ^= 0xff
			got, _, err := r.Read(bad)
			if err != nil {
				named(t, d.name, err)
				assert.True(t, isZero(got))
				continue
			}
			assert.False(t, isZero(got), "%s %s flipped at %d is never read as zero", d.name, form, i)
		}
	}
	wireBad := append([]byte(nil), images[schemaread.FormBinary]...)
	wireBad[0] = 0x7f
	_, _, err := r.Read(wireBad)
	assert.Equal(t, schemaread.ReasonUnknownForm, named(t, d.name, err), "%s: an unknown form byte", d.name)
	assert.Positive(t, r.Counts().Refused(schemaread.ReasonMalformed)+r.Counts().Refused(schemaread.ReasonWireDamaged),
		"%s: refusals are counted by reason", d.name)
}

func TestReleaseCycleRecordsDualRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	from := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

	t.Run("cycle build args", func(t *testing.T) {
		dualRecord[[]string]{
			name:       "CYCLE_BUILD_ARGS",
			reader:     func(r *Records) *schemaread.Reader[[]string] { return r.BuildArgs },
			value:      []string{"--incremental", "--gate", "report", DogfoodReasonFlag, "the example reason"},
			legacy:     []byte(`{"nova_release_build_args":["--incremental","--gate","report","` + DogfoodReasonFlag + `","the example reason"]}`),
			legacyForm: schemaread.FormLegacy,
		}.check(t, dir)
		// an argument exec cannot pass is refused by name, before any play
		_, err := NewRecords().ReadBuildArgs([]byte(`{"nova_release_build_args":["a\u0000b"]}`))
		assert.Equal(t, schemaread.ReasonMalformed, named(t, "CYCLE_BUILD_ARGS", err))
		_, err = NewRecords().ReadBuildArgs([]byte(`{"other":["x"]}`))
		assert.Equal(t, schemaread.ReasonAbsentRequired, named(t, "CYCLE_BUILD_ARGS", err))
	})

	t.Run("journey header", func(t *testing.T) {
		dualRecord[EvidenceHeader]{
			name:   "JOURNEY_HEADER",
			reader: func(r *Records) *schemaread.Reader[EvidenceHeader] { return r.Header },
			value: EvidenceHeader{Evidence: EvidenceKind, Revision: "0123abc", Functions: "f1", Schema: "s1",
				Installed: []InstalledReceipt{{Machine: "example-bench", Build: "v1.5.0-example", Revision: "0123abc"}}},
			legacy:     []byte(`{"evidence":"release-journeys","revision":"0123abc","functions":"f1","schema":"s1","installed":[{"machine":"example-bench","build":"v1.5.0-example","revision":"0123abc"}]}`),
			legacyForm: schemaread.FormJSON,
		}.check(t, dir)
		_, err := NewRecords().ReadHeader([]byte(`{"evidence":"something-else"}`))
		assert.Equal(t, "absent_required", named(t, "JOURNEY_HEADER", err))
	})

	t.Run("journey event", func(t *testing.T) {
		dualRecord[testEvent]{
			name:       "JOURNEY_EVENT",
			reader:     func(r *Records) *schemaread.Reader[testEvent] { return r.Event },
			value:      testEvent{Action: "output", Package: "github.com/example/hook", Test: "TestExample", Output: "--- PASS: TestExample\n"},
			legacy:     []byte(`{"Time":"2026-10-17T00:00:00Z","Action":"output","Package":"github.com/example/hook","Test":"TestExample","Output":"--- PASS: TestExample\n"}`),
			legacyForm: schemaread.FormLegacy,
		}.check(t, dir)
	})

	t.Run("spend receipts", func(t *testing.T) {
		value := spendReceipts{Evidence: ReceiptsKind, From: from, To: to, Friends: map[string]int64{"example": 5, "other": 7}}
		dualRecord[spendReceipts]{
			name:       "SPEND_RECEIPTS",
			reader:     func(r *Records) *schemaread.Reader[spendReceipts] { return r.Receipts },
			value:      value,
			legacy:     []byte(`{"evidence":"spend-receipts","from":"2026-10-16T00:00:00Z","to":"2026-10-17T00:00:00Z","friends":{"example":5,"other":7}}`),
			legacyForm: schemaread.FormLegacy,
		}.check(t, dir)

		// a clamped value (a negative token count) is refused on every form,
		// and the writer refuses it before a byte is written
		recs := NewRecords()
		neg := value
		neg.Friends = map[string]int64{"example": -1}
		_, err := recs.Receipts.Write(&neg, schemaread.FormBinary)
		assert.Equal(t, schemaread.ReasonClamped, named(t, "SPEND_RECEIPTS", err))
		c := receiptsCodec()
		w := c.SaveWire(&neg)
		require.NotEmpty(t, w)
		for _, in := range [][]byte{
			w,
			[]byte(base64.StdEncoding.EncodeToString(w)),
			c.SaveJSON(&neg),
			[]byte(`{"evidence":"spend-receipts","from":"2026-10-16T00:00:00Z","to":"2026-10-17T00:00:00Z","friends":{"example":-1}}`),
		} {
			got, _, err := recs.Receipts.Read(in)
			assert.Equal(t, schemaread.ReasonClamped, named(t, "SPEND_RECEIPTS", err), "%.60q", in)
			assert.True(t, isZero(got))
		}
		assert.Equal(t, uint64(5), recs.Receipts.Counts().Refused(schemaread.ReasonClamped))

		// a friends record absent, or a friend named twice, is refused by name
		_, err = recs.ReadReceipts([]byte(`{"evidence":"spend-receipts","from":"2026-10-16T00:00:00Z","to":"2026-10-17T00:00:00Z"}`))
		assert.Equal(t, schemaread.ReasonAbsentRequired, named(t, "SPEND_RECEIPTS", err))
		_, err = recs.ReadReceipts([]byte(`{"evidence":"spend-receipts","from":"2026-10-16T00:00:00Z","to":"2026-10-17T00:00:00Z","friends":[{"friend":"a","tokens":1},{"friend":"a","tokens":2}]}`))
		assert.Equal(t, schemaread.ReasonMalformed, named(t, "SPEND_RECEIPTS", err))
	})

	// the read sites: the gates read each form and refuse damage by name
	t.Run("journey evidence file", func(t *testing.T) {
		recs := NewRecords()
		h := EvidenceHeader{Evidence: EvidenceKind, Revision: "0123abc", Functions: "f1", Schema: "s1",
			Installed: []InstalledReceipt{{Machine: "example-bench", Build: "v1", Revision: "0123abc"}}}
		header, err := recs.Header.Write(&h, schemaread.FormBase64)
		require.NoError(t, err)
		e := testEvent{Action: "pass", Package: "hook", Test: "TestExample"}
		ev, err := recs.Event.Write(&e, schemaread.FormJSON)
		require.NoError(t, err)
		evB64, err := recs.Event.Write(&e, schemaread.FormBase64)
		require.NoError(t, err)
		legacy := `{"Action":"pass","Package":"hook","Test":"TestExample"}`
		p := filepath.Join(dir, "evidence.mixed")
		require.NoError(t, os.WriteFile(p, []byte(string(header)+"\n"+string(ev)+"\n"+string(evB64)+"\n"+legacy+"\n"), 0o600))
		gotH, events, err := readJourneyEvidence(p)
		require.NoError(t, err)
		assert.Equal(t, h, gotH)
		assert.Equal(t, []testEvent{e, e, e}, events)

		require.NoError(t, os.WriteFile(p, []byte(string(header)+"\n"+string(evB64[:len(evB64)-4])+"\n"), 0o600))
		_, _, err = readJourneyEvidence(p)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "line 2 is not a go test -json event")
		assert.Contains(t, err.Error(), "JOURNEY_EVENT REFUSED: ")

		require.NoError(t, os.WriteFile(p, []byte(string(header[:len(header)-4])+"\n"), 0o600))
		_, _, err = readJourneyEvidence(p)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JOURNEY_HEADER REFUSED: ")
	})

	t.Run("spend receipts file", func(t *testing.T) {
		recs := NewRecords()
		v := spendReceipts{Evidence: ReceiptsKind, From: from, To: to, Friends: map[string]int64{"example": 5}}
		w := SpendWindowFrom(from, to)
		for _, form := range typedForms {
			img, err := recs.Receipts.Write(&v, form)
			require.NoError(t, err)
			p := filepath.Join(dir, "receipts."+form.String())
			require.NoError(t, os.WriteFile(p, img, 0o600))
			got, err := FileReceipts{Path: p}.Tokens(context.Background(), w)
			require.NoError(t, err, form)
			assert.Equal(t, v.Friends, got, form)

			require.NoError(t, os.WriteFile(p, img[:len(img)-2], 0o600))
			_, err = FileReceipts{Path: p}.Tokens(context.Background(), w)
			require.Error(t, err, form)
			assert.Contains(t, err.Error(), "SPEND_RECEIPTS REFUSED: ", form)
		}
	})
}

// FuzzReleaseCycleRecordReads holds every reader to its contract on any
// input: a read is a value or a named refusal, never a panic, and a refused
// read returns no value.
func FuzzReleaseCycleRecordReads(f *testing.F) {
	recs := NewRecords()
	v := spendReceipts{Evidence: ReceiptsKind, From: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), Friends: map[string]int64{"a": 1}}
	for _, form := range typedForms {
		img, err := recs.Receipts.Write(&v, form)
		require.NoError(f, err)
		f.Add(img)
	}
	f.Add([]byte(`{"nova_release_build_args":["--gate"]}`))
	f.Add([]byte(`{"Action":"pass","Package":"hook","Test":"T"}`))
	f.Add([]byte(`{"evidence":"release-journeys","revision":"r","functions":"f","schema":"s"}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		r := NewRecords()
		refused := func(name string, err error, zero bool) {
			if err == nil {
				return
			}
			var ref *schemaread.Refusal
			require.True(t, errors.As(err, &ref), "%s: %v is a named refusal", name, err)
			require.Equal(t, name, ref.Name)
			require.True(t, zero, "%s: a refused read returns no value", name)
		}
		a, err := r.ReadBuildArgs(in)
		refused("CYCLE_BUILD_ARGS", err, a == nil)
		h, err := r.ReadHeader(in)
		refused("JOURNEY_HEADER", err, isZero(h))
		e, err := r.ReadEvent(in)
		refused("JOURNEY_EVENT", err, isZero(e))
		s, err := r.ReadReceipts(in)
		refused("SPEND_RECEIPTS", err, isZero(s))
	})
}
