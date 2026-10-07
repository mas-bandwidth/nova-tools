//go:build functional

package ntable_test

// The size of the change event and of the operation record, at the largest
// batches the bounds allow. The specification states these figures.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventBound and recordBound are the bounds the specification states for the
// change event and for one operation record, in bytes.
const (
	eventBound  = 2 << 20
	recordBound = 5 << 20
)

// A long actor is in the manifest once, in the delta once, and in the change
// event twice; a character JSON escapes doubles it in the delta, and the record
// holds the delta a second time inside its own encoding.
func TestBatchEventAndRecordStayWithinTheirBoundsAtTheLargestActor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		char  string
		grows int // bytes of delta for each byte of actor
	}{{"plain", "a", 1}, {"escaped", "/", 2}} {
		c, ctx := probeTable(t)
		seedTwo(t, ctx, c)
		size := func(op string, n int) (delta, event, record int) {
			ans, err := rawApply(ctx, c, manifestWithActor(probeRev(ctx, c), op, strings.Repeat(tc.char, n), `{"id":"a","expect":{}}`))
			require.True(t, replyOpens(ans, err, "OK"), "%s: %.200v %v", tc.name, ans, err)
			delta = len(fmt.Sprint(ans[1].([]any)[6]))
			for _, v := range c.XRevRangeN(ctx, ntable.DefKey("demo")+":changes", "+", "-", 1).Val()[0].Values {
				event += len(fmt.Sprint(v))
			}
			return delta, event, int(c.HStrLen(ctx, ntable.DefKey("demo")+":ops", "0:"+op).Val())
		}
		d0, _, _ := size("op-p", 1000)
		n := 1000 + (ntable.LimitReceiptBytes-d0)/tc.grows
		delta, event, record := size("op-l", n)
		t.Logf("%s actor of %d bytes: delta %d, event %d, record %d", tc.name, n, delta, event, record)
		assert.LessOrEqual(t, delta, ntable.LimitReceiptBytes, "%s: the delta is %d, want the bound %d", tc.name, delta, ntable.LimitReceiptBytes)
		assert.GreaterOrEqual(t, delta, ntable.LimitReceiptBytes-tc.grows, "%s: the delta is %d, want the bound %d", tc.name, delta, ntable.LimitReceiptBytes)
		assert.LessOrEqual(t, event, eventBound, "%s: event %d (bound %d)", tc.name, event, eventBound)
		assert.LessOrEqual(t, record, recordBound, "%s: record %d (bound %d)", tc.name, record, recordBound)
	}
}

// Short values of a character JSON escapes, on every member: the delta is nearly
// at its bound and the record holds it escaped twice more.
func TestBatchRecordStaysWithinItsBoundAtTheLargestEscapedDelta(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	holdMembers(t, ctx, c, "m", 128)
	value := strings.Repeat("/", ntable.ReceiptValueBytes)
	entries := func(per int) string {
		var ents []string
		for i := 0; i < 128; i++ {
			var f []string
			for j := 0; j < per; j++ {
				f = append(f, fmt.Sprintf(`"g%d":"%s"`, j, value))
			}
			ents = append(ents, fmt.Sprintf(`{"id":"m%d","expect":{},"set":{%s}}`, i, strings.Join(f, ",")))
		}
		return strings.Join(ents, ",")
	}
	ans, err := rawApply(ctx, c, manifestWithActor(probeRev(ctx, c), "largest", "p", entries(26)))
	require.True(t, replyOpens(ans, err, "OK"), "26 fields on 128 members: %.200v %v", ans, err)
	event := 0
	for _, v := range c.XRevRangeN(ctx, ntable.DefKey("demo")+":changes", "+", "-", 1).Val()[0].Values {
		event += len(fmt.Sprint(v))
	}
	record := int(c.HStrLen(ctx, ntable.DefKey("demo")+":ops", "0:largest").Val())
	t.Logf("delta %d, event %d, record %d", len(fmt.Sprint(ans[1].([]any)[6])), event, record)
	assert.LessOrEqual(t, event, eventBound, "event %d (bound %d)", event, eventBound)
	assert.LessOrEqual(t, record, recordBound, "record %d (bound %d)", record, recordBound)
	over, err := rawApply(ctx, c, manifestWithActor(probeRev(ctx, c), "over", "p", entries(27)))
	require.True(t, replyOpens(over, err, "REFUSED"), "27 fields on 128 members: %.200v; want a receipt refusal: %v", over, err)
	assert.Equal(t, "receipt bytes", over[2], "27 fields on 128 members: %.200v; want a receipt refusal", over)
}
