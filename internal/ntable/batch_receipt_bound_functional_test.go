//go:build functional

package ntable_test

// A receipt is bounded twice: a value over ReceiptValueBytes is recorded as its
// length and SHA-1 wherever a receipt is kept, and the receipt as a whole is at
// most LimitReceiptBytes, checked before the first write.

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchReceiptOverTheBoundIsRefusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	const members, fields, size = 128, 1000, 256
	var creates []string
	for i := 0; i < members; i++ {
		creates = append(creates, fmt.Sprintf(`{"id":"m%d","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`, i))
	}
	seed, err := rawApply(ctx, c, manifestWith(probeRev(ctx, c), "seed", strings.Join(creates, ",")))
	require.True(t, replyOpens(seed, err, "OK"), "seed: %.200v: %v", seed, err)
	value := strings.Repeat("v", size)
	pipe := c.Pipeline()
	for i := 0; i < members; i++ {
		f := map[string]any{}
		for j := 0; j < fields; j++ {
			f[fmt.Sprintf("f%d", j)] = value
		}
		pipe.HSet(ctx, ntable.MemberKey(fmt.Sprintf("m%d", i)), f)
	}
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)
	names := make([]string, fields)
	for j := range names {
		names[j] = fmt.Sprintf(`"f%d"`, j)
	}
	unset := func(from, to int) string {
		var ents []string
		for i := from; i < to; i++ {
			ents = append(ents, fmt.Sprintf(`{"id":"m%d","expect":{},"unset":[%s]}`, i, strings.Join(names, ",")))
		}
		return strings.Join(ents, ",")
	}

	// the manifest is under its own bound, the receipt it would make is not
	raw := manifestWith(probeRev(ctx, c), "big-unset", unset(0, members))
	require.LessOrEqual(t, len(raw), int(ntable.LimitManifestBytes), "the manifest is %d bytes, over its bound", len(raw))
	before := storeImage(t, c)
	ans, err := rawApply(ctx, c, raw)
	why := fmt.Sprintf("a receipt over its bound: %.200v", ans)
	require.True(t, replyOpens(ans, err, "REFUSED", "LIMIT"), "%s: %v", why, err)
	require.GreaterOrEqual(t, len(ans), 5, why)
	require.Equal(t, "receipt bytes", ans[2], why)
	require.Equal(t, fmt.Sprint(ntable.LimitReceiptBytes), fmt.Sprint(ans[3]), why)
	var computed int
	_, err = fmt.Sscan(fmt.Sprint(ans[4]), &computed)
	assert.NoError(t, err, "the refusal names the computed size %v, want more than %d", ans[4], ntable.LimitReceiptBytes)
	assert.Greater(t, computed, ntable.LimitReceiptBytes, "the refusal names the computed size %v, want more than %d", ans[4], ntable.LimitReceiptBytes)
	assert.Equal(t, before, storeImage(t, c), "a batch refused for its receipt changed the store")

	// through the library: the same refusal, says changed=no
	_, err = ntable.ApplyBatch(ctx, c, mustManifest(t, raw))
	require.ErrorContains(t, err, "receipt bytes", "ApplyBatch of a receipt over its bound")
	assert.ErrorContains(t, err, "changed=no", "ApplyBatch of a receipt over its bound")
	assert.Equal(t, before, storeImage(t, c), "ApplyBatch refused for its receipt changed the store")

	// fewer members in one manifest is a batch the store takes, and its receipt is
	// within the bound
	small := manifestWith(probeRev(ctx, c), "small-unset", unset(0, 4))
	ans, err = rawApply(ctx, c, small)
	require.True(t, replyOpens(ans, err, "OK"), "four members: %.200v %v", ans, err)
	ev := c.XRevRangeN(ctx, ntable.DefKey("demo")+":changes", "+", "-", 1).Val()
	got := len(fmt.Sprint(ev[0].Values["batch_delta"]))
	assert.LessOrEqual(t, got, int(ntable.LimitReceiptBytes), "an accepted batch left a delta of %d bytes, over %d", got, ntable.LimitReceiptBytes)
}

func TestBatchReceiptDigestsALongValueInEveryRecordOfIt(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	at := strings.Repeat("A", ntable.ReceiptValueBytes)
	over := strings.Repeat("O", ntable.ReceiptValueBytes+1)
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("a"), "at", at, "over", over).Err())
	raw := manifestWith(probeRev(ctx, c), "digests", `{"id":"a","expect":{},"unset":["at","over"]}`)
	ans, err := rawApply(ctx, c, raw)
	require.True(t, replyOpens(ans, err, "OK"), "apply: %.200v %v", ans, err)
	sum := sha1.Sum([]byte(over))
	digest := hex.EncodeToString(sum[:])
	event := fmt.Sprint(c.XRevRangeN(ctx, ntable.DefKey("demo")+":changes", "+", "-", 1).Val()[0].Values["batch_delta"])
	record := c.HGet(ctx, ntable.DefKey("demo")+":ops", "0:digests").Val()
	for what, text := range map[string]string{"the receipt": fmt.Sprint(ans), "the change event": event, "the operation record": record} {
		assert.NotContains(t, text, over, "%s holds a %d-byte value in full", what, len(over))
		assert.Contains(t, text, digest, "%s does not hold the SHA-1 of the long value", what)
		assert.Contains(t, text, at, "%s does not hold a %d-byte value in full", what, len(at))
	}
}
