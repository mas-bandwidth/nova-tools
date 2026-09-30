//go:build functional

package ntable_test

// A receipt is bounded by its manifest, not by the values the manifest replaces:
// a field value over ReceiptValueBytes is recorded as its length and SHA-1, the
// receipt and the stored operation record stay small, and a replay still returns
// the identical receipt.

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchReceiptRecordsLongValuesByLengthAndDigest(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	const fields = 128
	long := make(map[string]any, fields)
	names := make([]string, fields)
	for i := range names {
		names[i] = fmt.Sprintf("f%d", i)
		long[names[i]] = strings.Repeat(string(rune('a'+i%26)), 64<<10)
	}
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("a"), long).Err())
	quoted := make([]string, fields)
	for i, n := range names {
		quoted[i] = `"` + n + `"`
	}
	raw := manifestWith(probeRev(ctx, c), "big-unset", `{"id":"a","expect":{},"unset":[`+strings.Join(quoted, ",")+`]}`)
	require.LessOrEqual(t, len(raw), 2000, "the manifest is %d bytes", len(raw))
	ans, err := rawApply(ctx, c, raw)
	require.True(t, replyOpens(ans, err, "OK"), "apply: %.200v %v", ans, err)
	size := len(fmt.Sprint(ans))
	assert.LessOrEqual(t, size, 64<<10, "the receipt is %d bytes for a %d-byte manifest", size, len(raw))
	record := c.HGet(ctx, ntable.DefKey("demo")+":ops", "0:big-unset").Val()
	assert.LessOrEqual(t, len(record), 120<<10, "the stored operation record is %d bytes", len(record))

	// what the receipt says of a value is its length and digest
	r, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "big-unset-2", Members: []ntable.BatchMemberEntry{
		{ID: "b", Expect: &ntable.MemberExpect{}, Set: map[string]string{"k": strings.Repeat("z", 64<<10)}}}})
	require.NoError(t, err)
	ch := r.BatchDelta.Members[0].Fields["k"]
	sum := sha1.Sum([]byte(strings.Repeat("z", 64<<10)))
	if ch.Before != nil || ch.After != nil || ch.AfterBytes != 64<<10 || ch.AfterSHA1 != hex.EncodeToString(sum[:]) || ch.BeforeBytes != 0 {
		t.Errorf("a long set value is recorded as %+v", ch)
	}
	assert.Empty(t, r.BatchDelta.Members[0].FieldsSet, "fields_set carries a long value: %d entries", len(r.BatchDelta.Members[0].FieldsSet))

	// replay returns the identical receipt
	again, err := rawApply(ctx, c, raw)
	if got, marked := asReplay(again); err != nil || !marked || !reflect.DeepEqual(got, ans) {
		t.Errorf("replay of the big receipt: %v", err)
	}
}

func TestBatchReceiptKeepsValuesUpToTheBoundInFull(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	full := strings.Repeat("s", ntable.ReceiptValueBytes)
	over := strings.Repeat("s", ntable.ReceiptValueBytes+1)
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("a"), "at", full, "over", over).Err())
	r, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "edge", Members: []ntable.BatchMemberEntry{
		{ID: "a", Expect: &ntable.MemberExpect{}, Unset: []string{"at", "over", "role", "none"}}}})
	require.NoError(t, err)
	f := r.BatchDelta.Members[0].Fields
	if f["at"].Before == nil || *f["at"].Before != full || f["at"].BeforeBytes != 0 {
		t.Errorf("a value at the bound: %+v", f["at"])
	}
	if f["over"].Before != nil || f["over"].BeforeBytes != len(over) || f["over"].BeforeSHA1 == "" {
		t.Errorf("a value one over the bound: %+v", f["over"])
	}
	if f["role"].Before == nil || *f["role"].Before != "x" {
		t.Errorf("a short value: %+v", f["role"])
	}
	if f["none"].Before != nil || f["none"].BeforeBytes != 0 || f["none"].After != nil {
		t.Errorf("an absent field is null with no length: %+v", f["none"])
	}
}
