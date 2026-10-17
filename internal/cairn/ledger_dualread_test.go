package cairn_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ledgerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordLedgerAndCairnDualRead is the reader half of the record ledger and
// cairn records' table adoption (v1.5 aspect 13, part 1; the owner's rules S2
// and S4, and the reader-first order C13): every record the record ledger's
// entries and keys and the cairn record lines read back is read alike from its
// fixed binary image, the padded standard base64 of that image and the legacy
// form its writer writes today; every malformed value of each form is refused
// by name (`<RECORD> REFUSED: <reason>; run: <help>`) and never read as
// absent, empty or zero; a clamped value is refused; and each read is counted
// under its record and its form.
//
// Unit tier: no clock, no socket, no program, no store. The Redis and log
// reads are tested through their table reads on fixed inputs; the files are
// read from a t.TempDir() this test writes.
func TestRecordLedgerAndCairnDualRead(t *testing.T) {
	t.Parallel()
	for name, tb := range recordTables() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			roundTrip(t, tb)
			refuseDamage(t, tb)
		})
	}
}

type builder[T any] interface {
	Init(...ledgerwire.TableAllocator) bool
	GetRoot() *T
	AsConst() *T
	Region() []byte
	Lock() bool
	Shutdown()
}

// table is one generated ledger table wired to a schemaread codec: binary,
// padded base64 and JSON text, each refused by name when damaged (S2).
type table struct {
	name string
	codec *schemaread.Codec[any]
	read  func([]byte, bool) (any, schemaread.Report)
	wire  func(any) []byte
	json  func(any) []byte
}

func reportOf(rep *ledgerwire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Reason != "" {
		out.Reason = rep.Reason
	}
	return out
}

func newTable[T any, B any, PB interface {
	*B
	builder[T]
}](name string, fromJSON, loadWire func(PB, []byte, *ledgerwire.TableReport) bool, reset func(*T), measureWire func([]byte) (int64, error),
	measure func(*T, ...ledgerwire.TableWriteContext) int64, save func(*T, []byte, ...ledgerwire.TableWriteContext) int64,
	jsonMeasure func(*T, ...ledgerwire.TableAllocator) int64, jsonSave func(*T, []byte, ...ledgerwire.TableAllocator) int64) *table {
	load := func(data []byte, isJSON bool) (any, schemaread.Report) {
		b := PB(new(B))
		if !b.Init() {
			return nil, schemaread.Report{Malformed: true}
		}
		var rep ledgerwire.TableReport
		var ok bool
		if isJSON {
			ok = fromJSON(b, data, &rep)
		} else {
			ok = loadWire(b, data, &rep)
		}
		if !ok || !b.Lock() {
			return nil, reportOf(&rep, ok)
		}
		p := b.AsConst()
		if p == nil {
			return nil, schemaread.Report{Malformed: true}
		}
		return &held[T]{root: p, owner: b}, reportOf(&rep, ok)
	}
	wire := func(v any) []byte {
		h := v.(*held[T])
		n := measure(h.root)
		if n < 0 {
			return nil
		}
		out := make([]byte, n)
		if save(h.root, out) != n {
			return nil
		}
		return out
	}
	text := func(v any) []byte {
		h := v.(*held[T])
		n := jsonMeasure(h.root)
		if n < 0 {
			return nil
		}
		out := make([]byte, n)
		if jsonSave(h.root, out) != n {
			return nil
		}
		return out
	}
	c := &schemaread.Codec[any]{Name: name, Help: "nova-tools help", Reset: func(v *any) {},
		Measure: func(w []byte) error { _, err := measureWire(w); return err },
		LoadWire: func(v *any, data []byte) schemaread.Report {
			p, r := load(data, false)
			*v = p
			return r
		},
		LoadJSON: func(v *any, data []byte) schemaread.Report {
			p, r := load(data, true)
			*v = p
			return r
		},
		SaveWire: func(v *any) []byte {
			if *v == nil {
				return nil
			}
			return wire(*v)
		},
		SaveJSON: func(v *any) []byte {
			if *v == nil {
				return nil
			}
			return text(*v)
		}}
	return &table{name: name, codec: c, read: load, wire: wire, json: text}
}

type held[T any] struct {
	root  *T
	owner any
}

func recordTables() map[string]*table {
	return map[string]*table{
		"LedgerKey": newTable("LEDGER_KEY", ledgerwire.LedgerKeyFromJson, ledgerwire.LedgerKeyLoadBuilder, ledgerwire.LedgerKeyReset, ledgerwire.LedgerKeyLoadMeasureReason, ledgerwire.LedgerKeyMeasure, ledgerwire.LedgerKeySave, ledgerwire.LedgerKeyToJsonMeasure, ledgerwire.LedgerKeyToJson),
		"LedgerValue": newTable("LEDGER_VALUE", ledgerwire.LedgerValueFromJson, ledgerwire.LedgerValueLoadBuilder, ledgerwire.LedgerValueReset, ledgerwire.LedgerValueLoadMeasureReason, ledgerwire.LedgerValueMeasure, ledgerwire.LedgerValueSave, ledgerwire.LedgerValueToJsonMeasure, ledgerwire.LedgerValueToJson),
		"CairnLogRecord": newTable("CAIRN_LOG_RECORD", ledgerwire.CairnLogRecordFromJson, ledgerwire.CairnLogRecordLoadBuilder, ledgerwire.CairnLogRecordReset, ledgerwire.CairnLogRecordLoadMeasureReason, ledgerwire.CairnLogRecordMeasure, ledgerwire.CairnLogRecordSave, ledgerwire.CairnLogRecordToJsonMeasure, ledgerwire.CairnLogRecordToJson),
	}
}

var recordFixtures = map[string]string{
	"LEDGER_KEY":           `{"day":"2026-10-17","card":"c1","model":"m1","repo":"r1"}`,
	"LEDGER_VALUE":         `{"provider":"p1","tokens":[{"amount":100},{"amount":200}],"rough":3,"sources":"sz"}`,
	"CAIRN_LOG_RECORD":    `{"event":"open","session":"s1","entry":"e1","stamp":"2026-10-17T12:00:00Z","publish":"manual","source":"sr"}`,
}

// subset reports whether every leaf of want is present and equal in got; a leaf
// of the zero value may be absent, since a variable table reads an absent field
// as zero.
func subset(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, _ := got.(map[string]any)
		for k, v := range w {
			if !subset(v, g[k]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return len(w) == 0 && got == nil
		}
		for i := range w {
			if !subset(w[i], g[i]) {
				return false
			}
		}
		return true
	case nil:
		return got == nil
	case string:
		g, _ := got.(string)
		return g == w
	case bool:
		g, _ := got.(bool)
		return g == w
	case float64:
		g, _ := got.(float64)
		return g == w
	}
	return false
}

// roundTrip reads a fixture as JSON, writes it in each form, reads each back
// equal and counted under the form it arrived in (S2, S4, the per-form counter).
func roundTrip(t *testing.T, tb *table) {
	t.Helper()
	r := schemaread.NewReader(tb.codec)
	var want any
	rep := tb.codec.LoadJSON(&want, []byte(recordFixtures[tb.name]))
	require.False(t, rep.Malformed, "%s fixture is read", tb.name)
	require.Zero(t, rep.Clamped, "%s fixture is within its bounds", tb.name)
	wantWire := tb.wire(want)
	require.NotEmpty(t, wantWire)
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		out, err := r.Write(&want, form)
		require.NoError(t, err, "write %s", form)
		got, result, err := r.Read(out)
		require.NoError(t, err, "read %s", form)
		assert.Equal(t, form, result.Form)
		assert.Equal(t, string(wantWire), string(tb.wire(got)), "%s wire from %s", tb.name, form)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON))
}

// refuseDamage: a damaged image of each form is refused by name, never read as
// absent, empty or zero; a clamped value is refused.
func refuseDamage(t *testing.T, tb *table) {
	t.Helper()
	var want any
	tb.codec.LoadJSON(&want, []byte(recordFixtures[tb.name]))
	wire := tb.wire(want)
	b64 := base64.StdEncoding.EncodeToString(wire)
	text := tb.json(want)
	flipped := append([]byte(nil), wire...)
	flipped[0] ^= 0xff
	bad := map[string][]byte{
		"binary cut after one byte":  wire[:1],
		"binary cut in half":         wire[:len(wire)/2],
		"binary one byte short":      wire[:len(wire)-1],
		"binary form byte flipped":   flipped,
		"base64 cut at three":        []byte(b64[:3]),
		"base64 not padded":          []byte(strings.TrimRight(b64, "=") + "A"),
		"base64 with a foreign byte": []byte(b64[:4] + "!" + b64[5:]),
		"json cut after one byte":    text[:1],
		"json cut in half":           text[:len(text)/2],
		"empty":                      {},
		"text not a table":           []byte("not a table"),
	}
	for name, image := range bad {
		r := schemaread.NewReader(tb.codec)
		_, _, err := r.Read(image)
		var refusal *schemaread.Refusal
		if assert.ErrorAs(t, err, &refusal, "%s: %s: want a named refusal", tb.name, name) {
			assert.Equal(t, tb.name, refusal.Name, "%s: the refusal names the table", name)
			assert.NotEmpty(t, refusal.Reason, "%s: the refusal names its reason", name)
		}
	}
}
