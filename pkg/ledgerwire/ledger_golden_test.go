package ledgerwire

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	cairnsource "github.com/mas-bandwidth/nova-tools/internal/cairn"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/pkg/dogfood"
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
	t.Run("source ledger value keeps five positional nullable slots", func(t *testing.T) {
		t.Parallel()
		entry := record.LedgerEntry{
			Provider: "fixture", Tokens: [5]int64{11, 0, 33, 0, 55},
			Known: [5]bool{true, false, true, false, true}, Rough: 132, Sources: "ledger-source",
		}
		// This is the persisted shape of internal/record/ledger.go:179-184;
		// derive the five nullable positions from the public source row.
		type persistedValue struct {
			Provider string    `json:"provider"`
			Tokens   [5]*int64 `json:"tokens"`
			Rough    int       `json:"rough"`
			Sources  string    `json:"sources"`
		}
		persisted := persistedValue{Provider: entry.Provider, Rough: entry.Rough, Sources: entry.Sources}
		for i := range entry.Tokens {
			if entry.Known[i] {
				n := entry.Tokens[i]
				persisted.Tokens[i] = &n
			}
		}
		sourceJSON, err := json.Marshal(persisted)
		require.NoError(t, err)
		assert.JSONEq(t, `{"provider":"fixture","tokens":[11,null,33,null,55],"rough":132,"sources":"ledger-source"}`, string(sourceJSON))
		// TokenValue is the schema's explicit wrapper around each nullable
		// source slot. Bridge [n,null,...] to [{amount:n},{amount:null},...]
		// without compacting away any absent positions.
		var sourceShape struct {
			Provider string    `json:"provider"`
			Tokens   [5]*int64 `json:"tokens"`
			Rough    int       `json:"rough"`
			Sources  string    `json:"sources"`
		}
		require.NoError(t, json.Unmarshal(sourceJSON, &sourceShape))
		type tokenValue struct {
			Amount *int64 `json:"amount,omitempty"`
		}
		bridge := struct {
			Provider string        `json:"provider"`
			Tokens   [5]tokenValue `json:"tokens"`
			Rough    int           `json:"rough"`
			Sources  string        `json:"sources"`
		}{Provider: sourceShape.Provider, Rough: sourceShape.Rough, Sources: sourceShape.Sources}
		for i, amount := range sourceShape.Tokens {
			bridge.Tokens[i].Amount = amount
		}
		bridgeJSON, err := json.Marshal(bridge)
		require.NoError(t, err)
		var mapped struct {
			Tokens [5]tokenValue `json:"tokens"`
		}
		require.NoError(t, json.Unmarshal(bridgeJSON, &mapped))
		for i := range entry.Tokens {
			if entry.Known[i] {
				require.NotNil(t, mapped.Tokens[i].Amount, "slot %d remains present", i)
				assert.Equal(t, entry.Tokens[i], *mapped.Tokens[i].Amount, "slot %d retains its value", i)
			} else {
				assert.Nil(t, mapped.Tokens[i].Amount, "slot %d remains absent in place", i)
			}
		}
		var tokenField *TableFieldInfo
		for i := range LedgerValueTableFields {
			if LedgerValueTableFields[i].Name == "tokens" {
				tokenField = &LedgerValueTableFields[i]
			}
		}
		require.NotNil(t, tokenField)
		assert.Equal(t, "TokenValue", tokenField.TypeName)
		assert.Equal(t, int32(5), tokenField.ArrayBound)
	})

	cairn, cairnWant := makeCodec("CAIRN_LOG_RECORD", "cairn log", []byte(`{"event":"open","session":"session-1","entry":"","stamp":"2026-10-17T12:00:00Z","publish":"manual","source":"fixture"}`), func() *CairnLogRecordBuilder { return &CairnLogRecordBuilder{} }, func(b *CairnLogRecordBuilder) bool { return b.Init() }, func(b *CairnLogRecordBuilder) *CairnLogRecord { return b.GetRoot() }, CairnLogRecordFromJson, CairnLogRecordLoadBuilder, (&CairnLogRecordCook{}).Type(), CairnLogRecordReset, CairnLogRecordLoadMeasureReason, func(v *CairnLogRecord) []byte {
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
	t.Run("source cairn log maps event and publish", func(t *testing.T) {
		t.Parallel()
		store := t.TempDir()
		now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
		require.NoError(t, cairnsource.Open(store, "session-1", "fixture", now, cairnsource.PublishDeferred))
		_, err := cairnsource.Append(store, "session-1", "entry-1", "source entry", "", now.Add(time.Minute), cairnsource.PublishDeferred)
		require.NoError(t, err)
		data, err := os.ReadFile(store + "/log.jsonl")
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		require.Len(t, lines, 2)
		for i, wantEvent := range []string{"open", "append"} {
			var source map[string]string
			require.NoError(t, json.Unmarshal([]byte(lines[i]), &source))
			assert.Equal(t, wantEvent, source["event"])
			assert.Equal(t, cairnsource.PublishDeferred, source["publish"])
			assert.Equal(t, CairnEvent(i+1), map[string]CairnEvent{"open": CairnEventopen, "append": CairnEventappend}[source["event"]])
			assert.Equal(t, PublishPolicydeferred, map[string]PublishPolicy{"deferred": PublishPolicydeferred}[source["publish"]])
		}
		var eventField *TableFieldInfo
		for i := range CairnLogRecordTableFields {
			if CairnLogRecordTableFields[i].Name == "event_kind" {
				eventField = &CairnLogRecordTableFields[i]
			}
		}
		require.NotNil(t, eventField)
		assert.Equal(t, "event", eventField.Json, "the table field preserves the source key")
	})

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

	receipt, receiptWant := makeCodec("DOGFOOD_RECEIPT", "dogfood receipt", []byte(`{"tool":"nova-tools","verb":"test","by":"fixture","at":"2026-10-17T12:00:00Z","ok":true,"notes":"fixture","issue":12,"closes":"deadbeef"}`), func() *DogfoodReceiptBuilder { return &DogfoodReceiptBuilder{} }, func(b *DogfoodReceiptBuilder) bool { return b.Init() }, func(b *DogfoodReceiptBuilder) *DogfoodReceipt { return b.GetRoot() }, DogfoodReceiptFromJson, DogfoodReceiptLoadBuilder, (&DogfoodReceiptCook{}).Type(), DogfoodReceiptReset, DogfoodReceiptLoadMeasureReason, func(v *DogfoodReceipt) []byte {
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
	t.Run("source receipt preserves accepted closes text", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			closes string
		}{
			{name: "absent"},
			{name: "plain", closes: "1234abcd"},
			{name: "padded", closes: " 1234abcd "},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				source := dogfood.Receipt{Tool: "nova-tools", Verb: "test", By: "fixture", At: "2026-10-17T12:00:00Z", OK: true, Notes: "source fixture", Closes: tc.closes}
				assert.Empty(t, source.Validate(), "source validation accepts this closes shape")
				sourceJSON, err := json.Marshal(source)
				require.NoError(t, err)
				var sourceRoundTrip dogfood.Receipt
				require.NoError(t, json.Unmarshal(sourceJSON, &sourceRoundTrip))
				assert.Equal(t, source.Closes, sourceRoundTrip.Closes, "source JSON round trip preserves the original text")
				var builder DogfoodReceiptBuilder
				require.True(t, builder.Init())
				var tableReport TableReport
				require.True(t, DogfoodReceiptFromJson(&builder, sourceJSON, &tableReport), "the generated table accepts source JSON")
				assert.Zero(t, tableReport.Clamped, "the schema does not narrow accepted source text")
				assert.Zero(t, tableReport.KindMismatch)
				wireSize := DogfoodReceiptMeasure(builder.GetRoot(), &builder.Arena)
				require.Positive(t, wireSize)
				wire := make([]byte, wireSize)
				require.Equal(t, wireSize, DogfoodReceiptSave(builder.GetRoot(), wire, &builder.Arena))
				got, err := DogfoodReceiptReadStrict(wire)
				require.NoError(t, err, "generated wire round trip")
				jsonSize := DogfoodReceiptToJsonMeasure(got)
				require.Positive(t, jsonSize)
				generatedJSON := make([]byte, jsonSize)
				require.Equal(t, jsonSize, DogfoodReceiptToJson(got, generatedJSON))
				var generated map[string]any
				require.NoError(t, json.Unmarshal(generatedJSON, &generated))
				if tc.closes == "" {
					assert.True(t, generated["closes"] == nil || generated["closes"] == "", "absent closes stays empty after wire round trip")
				} else {
					assert.Equal(t, tc.closes, generated["closes"], "generated wire round trip preserves accepted closes text")
				}
				var closesField *TableFieldInfo
				for i := range DogfoodReceiptTableFields {
					if DogfoodReceiptTableFields[i].Name == "closes" {
						closesField = &DogfoodReceiptTableFields[i]
					}
				}
				require.NotNil(t, closesField)
				assert.Equal(t, "string", closesField.TypeName)
				assert.Equal(t, "closes", closesField.Json)
			})
		}
	})
	refuseJSON(t, key, `{"day":"2026-10-17x","card":"x","model":"x","repo":"x"}`, "day")
	refuseJSON(t, ledgerValue, `{"provider":"openai","tokens":[{"amount":1},{"amount":2},{"amount":3},{"amount":4},{"amount":5},{"amount":6}],"rough":21,"sources":"x"}`, "tokens")
	longID := strings.Repeat("x", 129)
	refuseJSON(t, cairn, `{"event":"open","session":"`+longID+`","entry":"","stamp":"x","publish":"manual","source":"x"}`, "session")
	refuseJSON(t, cairn, `{"event":"append","session":"session","entry":"`+longID+`","stamp":"x","publish":"manual","source":"x"}`, "entry")
	refuseJSON(t, cairn, `{"event":"not-a-cairn-event","session":"session","entry":"","publish":"manual"}`, "event")
	refuseJSON(t, cairn, `{"event":"open","session":"session","entry":"","publish":"not-a-policy"}`, "publish")
	refuseJSON(t, receipt, `{"tool":"x","verb":"x","by":"x","at":"x","ok":true,"issue":-1}`, "issue")
	refuseJSON(t, receipt, `{"tool":"x","verb":"x","by":"x","at":"x","ok":true,"issue":9223372036854775808}`, "issue")
}
