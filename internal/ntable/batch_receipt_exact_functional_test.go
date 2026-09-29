//go:build functional

package ntable_test

// The receipt's size is exact and known before the first write, and the digest
// rule is exact at 64 against 65 bytes in every place a receipt is kept.

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func manifestWithActor(rev, op, actor, members string) string {
	a, _ := json.Marshal(actor)
	return `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"` + op + `","actor":` + string(a) + `,"members":[` + members + `]}`
}

// 128 creates at the longest score a double prints: the largest accepted batch
// has a receipt of exactly the bound, and one more byte of actor is refused with
// the computed size one over, leaving the store as it was.
func TestBatchReceiptSizeIsExactAtItsBound(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	var creates []string
	for i := 0; i < 128; i++ {
		creates = append(creates, fmt.Sprintf(`{"id":"m%03d","expect":{"absent":true},"create":{"row":"build","col":"ready","score":-1.7976931348623157e+308}}`, i))
	}
	body := strings.Join(creates, ",")
	deltaLen := func(ans []any) int { return len(fmt.Sprint(ans[1].([]any)[6])) }

	// the receipt grows one byte for each byte of actor (the operation ids below
	// have one length); find the pad that fills it
	probe := manifestWithActor(probeRev(ctx, c), "op-p", strings.Repeat("a", 1000), body)
	ans, err := rawApply(ctx, c, probe)
	if err != nil || ans[0] != "OK" {
		t.Fatalf("probe: %.200v %v", ans, err)
	}
	pad := 1000 + ntable.LimitReceiptBytes - deltaLen(ans)
	for i := 0; i < 128; i++ { // put the members back so the creates are fresh
		c.Del(ctx, ntable.MemberKey(fmt.Sprintf("m%03d", i)), ntable.CellKey("demo", "build", "ready"))
	}
	if err := c.Del(ctx, ntable.DefKey("demo")+":ops").Err(); err != nil {
		t.Fatal(err)
	}

	before := storeImage(t, c)
	over := manifestWithActor(probeRev(ctx, c), "op-o", strings.Repeat("a", pad+1), body)
	ans, err = rawApply(ctx, c, over)
	if err != nil || len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "LIMIT" || ans[2] != "receipt bytes" || fmt.Sprint(ans[4]) != fmt.Sprint(ntable.LimitReceiptBytes+1) {
		t.Fatalf("one byte over: %.200v %v; want LIMIT receipt bytes, size %d", ans, err, ntable.LimitReceiptBytes+1)
	}
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refusal changed the store")
	}
	at := manifestWithActor(probeRev(ctx, c), "op-a", strings.Repeat("a", pad), body)
	ans, err = rawApply(ctx, c, at)
	if err != nil || ans[0] != "OK" {
		t.Fatalf("at the bound: %.200v %v", ans, err)
	}
	if got := deltaLen(ans); got != ntable.LimitReceiptBytes {
		t.Errorf("the largest accepted receipt is %d bytes, want exactly %d", got, ntable.LimitReceiptBytes)
	}
}

// A value of 64 bytes is kept in full and one of 65 is its length and SHA-1, by
// bytes and not runes, before and after, in the receipt, the change event and the
// record's result. The record's request is the manifest as sent: replay compares
// bytes. A replay returns the receipt unchanged, marked REPLAY.
func TestBatchDigestRuleIsExactInEveryRecordAndTheRequestIsKeptAsSent(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	b64, b65 := strings.Repeat("B", 64), strings.Repeat("C", 65)
	u64 := strings.Repeat("€", 21) + "x" // 64 bytes, 22 runes
	u66 := strings.Repeat("é", 33)       // 66 bytes, 33 runes
	n64, n65 := strings.Repeat("N", 64), strings.Repeat("M", 65)
	if err := c.HSet(ctx, ntable.MemberKey("a"), "b64", b64, "b65", b65, "u64", u64, "u66", u66).Err(); err != nil {
		t.Fatal(err)
	}
	raw := manifestWith(probeRev(ctx, c), "dg", `{"id":"a","expect":{},"unset":["b64","b65","u64","u66"],"set":{"n64":"`+n64+`","n65":"`+n65+`"}}`)
	ans, err := rawApply(ctx, c, raw)
	if err != nil || ans[0] != "OK" {
		t.Fatalf("apply %.300v %v", ans, err)
	}
	event := fmt.Sprint(c.XRevRangeN(ctx, ntable.DefKey("demo")+":changes", "+", "-", 1).Val()[0].Values)
	var record struct {
		Request string `json:"request"`
		Result  any    `json:"result"`
	}
	if err := json.Unmarshal([]byte(c.HGet(ctx, ntable.DefKey("demo")+":ops", "0:dg").Val()), &record); err != nil {
		t.Fatal(err)
	}
	if record.Request != raw {
		t.Errorf("the record's request is not the manifest as sent")
	}
	for what, text := range map[string]string{"reply": fmt.Sprint(ans), "event": event, "record result": fmt.Sprint(record.Result)} {
		for name, v := range map[string]string{"b64": b64, "u64": u64, "n64": n64} {
			if !strings.Contains(text, v) {
				t.Errorf("%s lacks the 64-byte %s in full", what, name)
			}
		}
		for name, v := range map[string]string{"b65": b65, "u66": u66, "n65": n65} {
			if strings.Contains(text, v) {
				t.Errorf("%s holds the %d-byte %s in full", what, len(v), name)
			}
			sum := sha1.Sum([]byte(v))
			if !strings.Contains(text, hex.EncodeToString(sum[:])) {
				t.Errorf("%s lacks the SHA-1 of %s", what, name)
			}
		}
	}
	again, err := rawApply(ctx, c, raw)
	if err != nil || len(again) != 3 || again[2] != "REPLAY" || !reflect.DeepEqual(ans[1], again[1]) {
		t.Errorf("replay: %.300v %v", again, err)
	}
}
