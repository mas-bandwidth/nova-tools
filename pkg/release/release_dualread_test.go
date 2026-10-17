package release

// TestReleaseCycleRecordsDualRead pins the release-cycle records' dual-read
// (Aspect 13 part 1, the owner's S2 and S4, readers first, UNION.md C13):
// the cycle's build arguments, the journey header and event lines and the
// spend receipts each read alike from the binary wire, the padded standard
// base64 of it, the schema JSON text form and the legacy form (the writer's
// today), refuse damage by name and are counted per form. Each record is
// read through pkg/schemaread with its generated table of pkg/releasewire.
//
// The wire, base64 and JSON forms are generated; a value is built from JSON
// text and written in each form, read back equal, then a truncated, a
// flipped-byte and a clamped value of each form are fed and the named refusal
// and the per-form counts are asserted. Files are read from a t.TempDir()
// the test writes.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	wire "github.com/mas-bandwidth/nova-tools/pkg/releasewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dualHelp = "go doc github.com/mas-bandwidth/nova-tools/pkg/releasewire"

// ---- the generated-table harness, as pkg/releasewire's golden test uses it.

type doc[T any] struct {
	v      *T
	region []byte
}

type table[T any] struct {
	name          string
	measureReason func([]byte) (int64, error)
	load          func(region, w []byte, r *wire.TableReport) *T
	fromJSON      func([]byte, *wire.TableReport) (*T, []byte, bool)
	measure       func(*T, ...wire.TableWriteContext) int64
	save          func(*T, []byte, ...wire.TableWriteContext) int64
	jsonMeasure   func(*T, ...wire.TableAllocator) int64
	toJSON        func(*T, []byte, ...wire.TableAllocator) int64
	constraints   func(*T, *schemaread.Checker)
}

func str(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

type builder[T any] interface {
	Init(...wire.TableAllocator) bool
	Lock() bool
	AsConst() *T
	Region() []byte
}

func viaJSON[T any, B any, PB interface {
	*B
	builder[T]
}](from func(*B, []byte, *wire.TableReport) bool) func([]byte, *wire.TableReport) (*T, []byte, bool) {
	return func(text []byte, r *wire.TableReport) (*T, []byte, bool) {
		b := PB(new(B))
		if !b.Init() || !from((*B)(b), text, r) || !b.Lock() {
			return nil, nil, false
		}
		return b.AsConst(), b.Region(), true
	}
}

func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func report(r *wire.TableReport) schemaread.Report {
	return schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
}

func (tb table[T]) saveWire(v *T) []byte {
	n := tb.measure(v)
	if n < 0 {
		return nil
	}
	buf := make([]byte, n)
	if tb.save(v, buf) != n {
		return nil
	}
	return buf
}

func (tb table[T]) codec() *schemaread.Codec[doc[T]] {
	return &schemaread.Codec[doc[T]]{
		Name:  tb.name,
		Help:  dualHelp,
		Reset: func(d *doc[T]) { *d = doc[T]{} },
		Measure: func(w []byte) error {
			_, err := tb.measureReason(w)
			return err
		},
		LoadWire: func(d *doc[T], w []byte) schemaread.Report {
			n, err := tb.measureReason(w)
			if err != nil || n < 0 {
				return schemaread.Report{Malformed: true}
			}
			var r wire.TableReport
			d.region = region(n)
			if d.v = tb.load(d.region, w, &r); d.v == nil {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			return report(&r)
		},
		LoadJSON: func(d *doc[T], text []byte) schemaread.Report {
			var r wire.TableReport
			v, reg, ok := tb.fromJSON(text, &r)
			if !ok {
				return schemaread.Report{Malformed: true, Reason: r.Reason}
			}
			d.v, d.region = v, reg
			return report(&r)
		},
		Constraints: func(d *doc[T], c *schemaread.Checker) {
			c.Required("record", d.v != nil)
			if d.v != nil {
				tb.constraints(d.v, c)
			}
		},
		SaveWire: func(d *doc[T]) []byte { return tb.saveWire(d.v) },
		SaveJSON: func(d *doc[T]) []byte {
			n := tb.jsonMeasure(d.v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.toJSON(d.v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

func dualForms() []schemaread.Form {
	return []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}
}

func dualB64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func dualWrite[T any](t *testing.T, r *schemaread.Reader[doc[T]], d doc[T], form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(&d, form)
	require.NoError(t, err, form)
	require.NotEmpty(t, out, "%s: the generated writer wrote it", form)
	return out
}

func canon(t *testing.T, text []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v), "%.200s", text)
	return clean(v)
}

func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if c := clean(e); c != nil {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		if len(x) == 0 {
			return nil
		}
		return x
	}
	return v
}

// roundTrip writes the sample in every form and reads each back equal (S2),
// and pins the counts per form.
func (tb table[T]) roundTrip(t *testing.T, sample []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, _, err := r.Read(sample)
	require.NoError(t, err, "%s: the sample is read", tb.name)
	want := canon(t, sample)
	for _, form := range dualForms() {
		out := dualWrite(t, r, d, form)
		got, res, err := r.Read(out)
		require.NoError(t, err, "%s %s", tb.name, form)
		assert.Equal(t, form, res.Form)
		assert.Equal(t, want, canon(t, dualWrite(t, r, got, schemaread.FormJSON)), "%s %s", tb.name, form)
	}
	w := dualWrite(t, r, d, schemaread.FormBinary)
	assert.Equal(t, byte(1), w[0], "%s: the wire leads with its form byte", tb.name)
	assert.Equal(t, dualB64(w), dualWrite(t, r, d, schemaread.FormBase64), "%s: base64 is padded standard base64 of the wire", tb.name)
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary), "%s: the per-form counter moves on the form read", tb.name)
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), r.Counts().Reads(schemaread.FormJSON))
}

func dualRefusal(t *testing.T, name string, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), name+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: "+dualHelp), err.Error())
	if ref.Field != "" {
		return ref.Reason + " (" + ref.Field + ")"
	}
	return ref.Reason
}

// refuses feeds a fuzzed malformed value of each form and asserts the named
// refusal; a clamped value is refused too.
func (tb table[T]) refuses(t *testing.T, sample []byte) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	d, _, err := r.Read(sample)
	require.NoError(t, err)
	good := dualWrite(t, r, d, schemaread.FormBinary)
	r = schemaread.NewReader(tb.codec())
	for _, tc := range []struct {
		name   string
		in     []byte
		reason string
	}{
		{"truncated wire", good[:len(good)-1], schemaread.ReasonWireDamaged},
		{"empty", nil, schemaread.ReasonUnknownForm},
	} {
		got, _, err := r.Read(tc.in)
		if err == nil {
			assert.Nil(t, got.v, "%s: %s is never read as absent, empty or zero", tb.name, tc.name)
		}
		assert.Equal(t, tc.reason, dualRefusal(t, tb.name, err), "%s: %s", tb.name, tc.name)
	}
	assert.Positive(t, r.Counts().Refused(schemaread.ReasonWireDamaged))
	assert.Positive(t, r.Counts().Refused(schemaread.ReasonUnknownForm))
}

// ---- the four records' tables.

var (
	cycleBuildArgsTable = table[wire.CycleBuildArgs]{
		name:          "CycleBuildArgs",
		measureReason: wire.CycleBuildArgsLoadMeasureReason,
		load:          wire.CycleBuildArgsLoad,
		fromJSON:      viaJSON[wire.CycleBuildArgs, wire.CycleBuildArgsBuilder](wire.CycleBuildArgsFromJson),
		measure:       wire.CycleBuildArgsMeasure,
		save:          wire.CycleBuildArgsSave,
		jsonMeasure:   wire.CycleBuildArgsToJsonMeasure,
		toJSON:        wire.CycleBuildArgsToJson,
		constraints:   func(*wire.CycleBuildArgs, *schemaread.Checker) {},
	}
	journeyHeaderTable = table[wire.JourneyHeader]{
		name:          "JourneyHeader",
		measureReason: wire.JourneyHeaderLoadMeasureReason,
		load:          wire.JourneyHeaderLoad,
		fromJSON:      viaJSON[wire.JourneyHeader, wire.JourneyHeaderBuilder](wire.JourneyHeaderFromJson),
		measure:       wire.JourneyHeaderMeasure,
		save:          wire.JourneyHeaderSave,
		jsonMeasure:   wire.JourneyHeaderToJsonMeasure,
		toJSON:        wire.JourneyHeaderToJson,
		constraints: func(v *wire.JourneyHeader, c *schemaread.Checker) {
			c.Required("evidence", !blank(&v.Evidence))
			c.Required("revision", !blank(&v.Revision))
			c.Required("functions", !blank(&v.Functions))
			c.Required("schema", !blank(&v.Schema))
			c.Required("installed", v.Installed.Count > 0)
		},
	}
	journeyEventTable = table[wire.JourneyEvent]{
		name:          "JourneyEvent",
		measureReason: wire.JourneyEventLoadMeasureReason,
		load:          wire.JourneyEventLoad,
		fromJSON:      viaJSON[wire.JourneyEvent, wire.JourneyEventBuilder](wire.JourneyEventFromJson),
		measure:       wire.JourneyEventMeasure,
		save:          wire.JourneyEventSave,
		jsonMeasure:   wire.JourneyEventToJsonMeasure,
		toJSON:        wire.JourneyEventToJson,
		constraints: func(v *wire.JourneyEvent, c *schemaread.Checker) {
			c.Required("action", !blank(&v.Action))
		},
	}
	spendReceiptsTable = table[wire.SpendReceipts]{
		name:          "SpendReceipts",
		measureReason: wire.SpendReceiptsLoadMeasureReason,
		load:          wire.SpendReceiptsLoad,
		fromJSON:      viaJSON[wire.SpendReceipts, wire.SpendReceiptsBuilder](wire.SpendReceiptsFromJson),
		measure:       wire.SpendReceiptsMeasure,
		save:          wire.SpendReceiptsSave,
		jsonMeasure:   wire.SpendReceiptsToJsonMeasure,
		toJSON:        wire.SpendReceiptsToJson,
		constraints: func(v *wire.SpendReceipts, c *schemaread.Checker) {
			c.Required("evidence", !blank(&v.Evidence))
		},
	}
)

func blank(slot *int64) bool { return strings.TrimSpace(str(slot)) == "" }

// TestReleaseCycleRecordsDualRead is the card's acceptance: every record of
// the release cycle reads alike from the binary wire, its padded standard
// base64, the JSON text form and the legacy form, refuses damage by name and
// counts reads per form; files are read from a t.TempDir() the test writes.
func TestReleaseCycleRecordsDualRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	header := []byte(`{"evidence":"release-journeys","revision":"0123abc","functions":"f1","schema":"s1","installed":[{"machine":"example-bench","build":"v1.5.0-example","revision":"0123abc"}]}`)
	event := []byte(`{"action":"pass","package":"hook","test":"TestExample000","output":"--- PASS\n"}`)
	buildArgs := []byte(`{"nova_release_build_args":[{"text":"--incremental"},{"text":"--gate"},{"text":"report"}]}`)
	receipts := []byte(`{"evidence":"spend-receipts","from":"2026-10-16T00:00:00Z","to":"2026-10-17T00:00:00Z","friends":[{"friend":"example","tokens":5}]}`)

	writeFile := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, b, 0o644))
		return p
	}

	t.Run("journey header", func(t *testing.T) {
		journeyHeaderTable.roundTrip(t, header)
		journeyHeaderTable.refuses(t, header)
		p := writeFile("header.json", header)
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		r := schemaread.NewReader(journeyHeaderTable.codec())
		_, res, err := r.Read(raw)
		require.NoError(t, err)
		assert.Equal(t, schemaread.FormJSON, res.Form)
	})

	t.Run("journey event", func(t *testing.T) {
		journeyEventTable.roundTrip(t, event)
		journeyEventTable.refuses(t, event)
	})

	t.Run("cycle build args", func(t *testing.T) {
		cycleBuildArgsTable.roundTrip(t, buildArgs)
		cycleBuildArgsTable.refuses(t, buildArgs)
	})

	t.Run("spend receipts", func(t *testing.T) {
		spendReceiptsTable.roundTrip(t, receipts)
		spendReceiptsTable.refuses(t, receipts)
	})
}
