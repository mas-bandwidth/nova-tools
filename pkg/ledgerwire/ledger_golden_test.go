package ledgerwire

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var retainedBuilders []any
var retainedBuildersMu sync.Mutex

func keepBuilder(v any) {
	retainedBuildersMu.Lock()
	retainedBuilders = append(retainedBuilders, v)
	retainedBuildersMu.Unlock()
}
func reportOf(rep *TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		if err := rep.Err(); err != nil {
			out.Reason = err.Error()
		}
	}
	return out
}
func rebase(src, dst unsafe.Pointer, info *TableCookInfo) {
	for _, f := range info.Fields {
		s, d := unsafe.Add(src, uintptr(f.Offset)), unsafe.Add(dst, uintptr(f.Offset))
		if f.IsPointer || (info.Name == "FuseBox" && f.Name == "quarantine") {
			a, b := (*int64)(s), (*int64)(d)
			if *a != 0 {
				*b = *a + int64(uintptr(s)-uintptr(d))
			}
			continue
		}
		if f.Record == nil || f.Storage != TableCookStorageRecord {
			continue
		}
		n := int32(1)
		if f.IsArray {
			n = f.ArrayBound
		}
		for i := int32(0); i < n; i++ {
			rebase(unsafe.Add(s, uintptr(i*f.ElemSize)), unsafe.Add(d, uintptr(i*f.ElemSize)), f.Record())
		}
	}
}

type decodeBuilder[T any, B any] func(*B, []byte, *TableReport) bool
type encode[T any] func(*T) []byte

func makeCodec[T comparable, B any](name, help string, input []byte, newBuilder func() *B, init func(*B) bool, root func(*B) *T, fromJSON, loadWire decodeBuilder[T, B], cook *TableCookInfo, reset func(*T), measure func([]byte) (int64, error), saveWire, saveJSON encode[T]) (*schemaread.Codec[T], *T) {
	makeValue := func(data []byte, load decodeBuilder[T, B]) (*T, schemaread.Report) {
		zero := new(T)
		b := newBuilder()
		if !init(b) {
			return zero, schemaread.Report{Malformed: true}
		}
		var rep TableReport
		ok := load(b, data, &rep)
		p := root(b)
		if p != nil {
			*zero = *p
			rebase(unsafe.Pointer(p), unsafe.Pointer(zero), cook)
		}
		keepBuilder(b)
		return zero, reportOf(&rep, ok)
	}
	want, rep := makeValue(input, fromJSON)
	if rep.Malformed || rep.Clamped != 0 {
		panic(fmt.Sprintf("%s fixture rejected: %v", name, rep))
	}
	c := &schemaread.Codec[T]{Name: name, Help: help, Reset: reset, Measure: func(wire []byte) error { _, err := measure(wire); return err },
		LoadWire: func(v *T, data []byte) schemaread.Report {
			p, result := makeValue(data, loadWire)
			*v = *p
			rebase(unsafe.Pointer(p), unsafe.Pointer(v), cook)
			return result
		},
		LoadJSON: func(v *T, data []byte) schemaread.Report {
			p, result := makeValue(data, fromJSON)
			*v = *p
			rebase(unsafe.Pointer(p), unsafe.Pointer(v), cook)
			return result
		}, SaveWire: saveWire, SaveJSON: saveJSON}
	return c, want
}
func roundTrip[T comparable](t *testing.T, c *schemaread.Codec[T], want *T) {
	t.Helper()
	r := schemaread.NewReader(c)
	wantWire, wantJSON := c.SaveWire(want), c.SaveJSON(want)
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		out, err := r.Write(want, form)
		require.NoError(t, err, "write %s", form)
		got, result, err := r.Read(out)
		require.NoError(t, err, "read %s", form)
		assert.Equal(t, form, result.Form)
		gotWire, gotJSON := c.SaveWire(&got), c.SaveJSON(&got)
		assert.Equal(t, string(wantJSON), string(gotJSON), "JSON round trip from %s", form)
		assert.Equal(t, string(wantWire), string(gotWire), "wire round trip from %s", form)
	}
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBinary))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON))
	bad := []struct {
		form  schemaread.Form
		image []byte
	}{
		{schemaread.FormBinary, c.SaveWire(want)[:1]},
		{schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(c.SaveWire(want))[:3])},
		{schemaread.FormJSON, c.SaveJSON(want)[:1]},
	}
	for _, tc := range bad {
		_, _, err := r.Read(tc.image)
		var refusal *schemaread.Refusal
		assert.ErrorAs(t, err, &refusal, "damaged %s: want named refusal", tc.form)
	}
}

func testTable[T comparable](t *testing.T, name string, c *schemaread.Codec[T], want *T) {
	t.Run(name, func(t *testing.T) { t.Parallel(); roundTrip(t, c, want) })
}

func refuseJSON[T any](t *testing.T, c *schemaread.Codec[T], text string, field string) {
	t.Helper()
	_, _, err := schemaread.NewReader(c).Read([]byte(text))
	var refusal *schemaread.Refusal
	require.ErrorAs(t, err, &refusal, "%s: want named refusal", field)
	assert.Equal(t, schemaread.ReasonClamped, refusal.Reason, "%s", field)
}

func TestLedgersRecordsGolden(t *testing.T) {
	t.Parallel()
	key, keyWant := makeCodec("LEDGER_KEY", "ledger key", []byte(`{"day":"2026-10-17","card":"a13-defs-ledgers.w2","model":"gpt-6-luna","repo":"mas-bandwidth/nova-tools"}`), func() *LedgerKeyBuilder { return &LedgerKeyBuilder{} }, func(b *LedgerKeyBuilder) bool { return b.Init() }, func(b *LedgerKeyBuilder) *LedgerKey { return b.GetRoot() }, LedgerKeyFromJson, LedgerKeyLoadBuilder, (&LedgerKeyCook{}).Type(), LedgerKeyReset, LedgerKeyLoadMeasureReason, func(v *LedgerKey) []byte {
		n := LedgerKeyMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if LedgerKeySave(v, o) != n {
			return nil
		}
		return o
	}, func(v *LedgerKey) []byte {
		n := LedgerKeyToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if LedgerKeyToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "LedgerKey", key, keyWant)

	token, tokenWant := makeCodec("TOKEN_VALUE", "token value", []byte(`{"amount":123}`), func() *TokenValueBuilder { return &TokenValueBuilder{} }, func(b *TokenValueBuilder) bool { return b.Init() }, func(b *TokenValueBuilder) *TokenValue { return b.GetRoot() }, TokenValueFromJson, TokenValueLoadBuilder, (&TokenValueCook{}).Type(), TokenValueReset, TokenValueLoadMeasureReason, func(v *TokenValue) []byte {
		n := TokenValueMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if TokenValueSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *TokenValue) []byte {
		n := TokenValueToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if TokenValueToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "TokenValue", token, tokenWant)

	ledgerValue, ledgerValueWant := makeCodec("LEDGER_VALUE", "ledger value", []byte(`{"provider":"openai","tokens":[{"amount":100},{"amount":20},{"amount":3},{"amount":4},{"amount":5}],"rough":132,"sources":"fixture"}`), func() *LedgerValueBuilder { return &LedgerValueBuilder{} }, func(b *LedgerValueBuilder) bool { return b.Init() }, func(b *LedgerValueBuilder) *LedgerValue { return b.GetRoot() }, LedgerValueFromJson, LedgerValueLoadBuilder, (&LedgerValueCook{}).Type(), LedgerValueReset, LedgerValueLoadMeasureReason, func(v *LedgerValue) []byte {
		n := LedgerValueMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if LedgerValueSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *LedgerValue) []byte {
		n := LedgerValueToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if LedgerValueToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "LedgerValue", ledgerValue, ledgerValueWant)

	cairn, cairnWant := makeCodec("CAIRN_LOG_RECORD", "cairn log", []byte(`{"event_kind":"open","session":"session-1","entry":"","stamp":"2026-10-17T12:00:00Z","publish":"manual","source":"fixture"}`), func() *CairnLogRecordBuilder { return &CairnLogRecordBuilder{} }, func(b *CairnLogRecordBuilder) bool { return b.Init() }, func(b *CairnLogRecordBuilder) *CairnLogRecord { return b.GetRoot() }, CairnLogRecordFromJson, CairnLogRecordLoadBuilder, (&CairnLogRecordCook{}).Type(), CairnLogRecordReset, CairnLogRecordLoadMeasureReason, func(v *CairnLogRecord) []byte {
		n := CairnLogRecordMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if CairnLogRecordSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *CairnLogRecord) []byte {
		n := CairnLogRecordToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if CairnLogRecordToJson(v, o) != n {
			return nil
		}
		return o
	})
	cairn.Constraints = func(v *CairnLogRecord, check *schemaread.Checker) {
		check.Bound("event_kind", int64(v.EventKind), 1, 2)
		check.Bound("publish", int64(v.Publish), 1, 4)
	}
	testTable(t, "CairnLogRecord", cairn, cairnWant)

	fuse, fuseWant := makeCodec("FUSE", "fuse", []byte(`{"at":"2026-10-17T12:00:00Z","reason":"fixture"}`), func() *FuseBuilder { return &FuseBuilder{} }, func(b *FuseBuilder) bool { return b.Init() }, func(b *FuseBuilder) *Fuse { return b.GetRoot() }, FuseFromJson, FuseLoadBuilder, (&FuseCook{}).Type(), FuseReset, FuseLoadMeasureReason, func(v *Fuse) []byte {
		n := FuseMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if FuseSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *Fuse) []byte {
		n := FuseToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if FuseToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "Fuse", fuse, fuseWant)

	entry, entryWant := makeCodec("QUARANTINE_ENTRY", "quarantine entry", []byte(`{"surface":"fixture","fuse":{"at":"2026-10-17T12:00:00Z","reason":"fixture"}}`), func() *QuarantineEntryBuilder { return &QuarantineEntryBuilder{} }, func(b *QuarantineEntryBuilder) bool { return b.Init() }, func(b *QuarantineEntryBuilder) *QuarantineEntry { return b.GetRoot() }, QuarantineEntryFromJson, QuarantineEntryLoadBuilder, (&QuarantineEntryCook{}).Type(), QuarantineEntryReset, QuarantineEntryLoadMeasureReason, func(v *QuarantineEntry) []byte {
		n := QuarantineEntryMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if QuarantineEntrySave(v, o) != n {
			return nil
		}
		return o
	}, func(v *QuarantineEntry) []byte {
		n := QuarantineEntryToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if QuarantineEntryToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "QuarantineEntry", entry, entryWant)

	box, boxWant := makeCodec("FUSE_BOX", "fuse box", []byte(`{"lockdown":{"at":"2026-10-17T12:00:00Z","reason":"fixture"},"quarantine":[{"surface":"fixture","fuse":{"at":"2026-10-17T12:00:00Z","reason":"quarantined"}}]}`), func() *FuseBoxBuilder { return &FuseBoxBuilder{} }, func(b *FuseBoxBuilder) bool { return b.Init() }, func(b *FuseBoxBuilder) *FuseBox { return b.GetRoot() }, FuseBoxFromJson, FuseBoxLoadBuilder, (&FuseBoxCook{}).Type(), FuseBoxReset, FuseBoxLoadMeasureReason, func(v *FuseBox) []byte {
		n := FuseBoxMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if FuseBoxSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *FuseBox) []byte {
		n := FuseBoxToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if FuseBoxToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "FuseBox", box, boxWant)

	id, idWant := makeCodec("RECEIPT_ID", "receipt id", []byte(`{"value":"deadbeef"}`), func() *ReceiptIDBuilder { return &ReceiptIDBuilder{} }, func(b *ReceiptIDBuilder) bool { return b.Init() }, func(b *ReceiptIDBuilder) *ReceiptID { return b.GetRoot() }, ReceiptIDFromJson, ReceiptIDLoadBuilder, (&ReceiptIDCook{}).Type(), ReceiptIDReset, ReceiptIDLoadMeasureReason, func(v *ReceiptID) []byte {
		n := ReceiptIDMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if ReceiptIDSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *ReceiptID) []byte {
		n := ReceiptIDToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if ReceiptIDToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "ReceiptID", id, idWant)

	receipt, receiptWant := makeCodec("DOGFOOD_RECEIPT", "dogfood receipt", []byte(`{"tool":"nova-tools","verb":"test","by":"fixture","at":"2026-10-17T12:00:00Z","ok":true,"notes":"fixture","issue":12,"closes":{"value":"deadbeef"}}`), func() *DogfoodReceiptBuilder { return &DogfoodReceiptBuilder{} }, func(b *DogfoodReceiptBuilder) bool { return b.Init() }, func(b *DogfoodReceiptBuilder) *DogfoodReceipt { return b.GetRoot() }, DogfoodReceiptFromJson, DogfoodReceiptLoadBuilder, (&DogfoodReceiptCook{}).Type(), DogfoodReceiptReset, DogfoodReceiptLoadMeasureReason, func(v *DogfoodReceipt) []byte {
		n := DogfoodReceiptMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if DogfoodReceiptSave(v, o) != n {
			return nil
		}
		return o
	}, func(v *DogfoodReceipt) []byte {
		n := DogfoodReceiptToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		o := make([]byte, n)
		if DogfoodReceiptToJson(v, o) != n {
			return nil
		}
		return o
	})
	testTable(t, "DogfoodReceipt", receipt, receiptWant)
	refuseJSON(t, key, `{"day":"2026-10-17x","card":"x","model":"x","repo":"x"}`, "day")
	refuseJSON(t, ledgerValue, `{"provider":"openai","tokens":[{"amount":1},{"amount":2},{"amount":3},{"amount":4},{"amount":5},{"amount":6}],"rough":21,"sources":"x"}`, "tokens")
	longID := strings.Repeat("x", 129)
	refuseJSON(t, cairn, `{"event_kind":"open","session":"`+longID+`","entry":"","stamp":"x","publish":"manual","source":"x"}`, "session")
	refuseJSON(t, cairn, `{"event_kind":"append","session":"session","entry":"`+longID+`","stamp":"x","publish":"manual","source":"x"}`, "entry")
	refuseJSON(t, cairn, `{"event_kind":"not-a-cairn-event","session":"session","entry":"","publish":"manual"}`, "event_kind")
	refuseJSON(t, cairn, `{"event_kind":"open","session":"session","entry":"","publish":"not-a-policy"}`, "publish")
	refuseJSON(t, id, `{"value":"123456789"}`, "value")
	refuseJSON(t, receipt, `{"tool":"x","verb":"x","by":"x","at":"x","ok":true,"issue":-1}`, "issue")
	refuseJSON(t, receipt, `{"tool":"x","verb":"x","by":"x","at":"x","ok":true,"issue":9223372036854775808}`, "issue")
}
