//go:build functional

package ntable_test

// Failures at the last checks a batch makes, and after a valid first entry: the
// destination cell, the change stream and the operation records of the wrong
// type, a full stream, a bound cell, a member of the wrong type. Each is a clean
// refusal and the store image is unchanged.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchFailuresAtTheLastChecksWriteNothing(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	_, err := ntable.RowAdd(ctx, c, "demo", "bnd", ntable.RowSpec{Binds: map[string]string{"ready": "ext:ready"}, Owner: "o"})
	require.NoError(t, err)
	require.NoError(t, c.Set(ctx, ntable.MemberKey("junk"), "s", 0).Err())
	first := `{"id":"a","expect":{"revision":"1"},"move":{"row":"test","col":"working"},"set":{"k":"v"}}`
	refusedWith := func(name, members, code string) {
		t.Helper()
		before := storeImage(t, c)
		ans, err := rawApply(ctx, c, manifestWith(probeRev(ctx, c), "late-"+strings.ReplaceAll(name, " ", "-"), members))
		if err != nil || len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != code {
			t.Errorf("%s: %v %v; want REFUSED %s", name, trunc(ans), err, code)
		}
		assert.Equal(t, before, storeImage(t, c), "%s: the store changed", name)
	}
	refusedWith("late bound cell", first+`,{"id":"n9","expect":{"absent":true},"create":{"row":"bnd","col":"ready","score":1}}`, "BOUND")
	refusedWith("late wrong-type member", first+`,{"id":"junk","expect":{}}`, "WRONGTYPE")

	// a destination cell of the wrong type
	dst := ntable.CellKey("demo", "test", "done")
	require.NoError(t, c.Set(ctx, dst, "s", 0).Err())
	refusedWith("wrong-type destination cell", first+`,{"id":"b","expect":{},"move":{"row":"test","col":"done"}}`, "WRONGTYPE")
	require.NoError(t, c.Del(ctx, dst).Err())

	// the change stream of the wrong type, then full
	stream := ntable.DefKey("demo") + ":changes"
	saved := c.Dump(ctx, stream).Val()
	require.NoError(t, c.Del(ctx, stream).Err())
	require.NoError(t, c.Set(ctx, stream, "s", 0).Err())
	refusedWith("change stream of the wrong type", first, "STREAMTYPE")
	require.NoError(t, c.Del(ctx, stream).Err())
	require.NoError(t, c.Restore(ctx, stream, 0, saved).Err())
	require.NoError(t, c.XAdd(ctx, &redis.XAddArgs{Stream: stream, ID: "18446744073709551615-18446744073709551615", Values: []string{"x", "y"}}).Err())
	refusedWith("full change stream", first, "STREAMFULL")

	// the operation records of the wrong type
	require.NoError(t, c.Del(ctx, stream).Err())
	require.NoError(t, c.Restore(ctx, stream, 0, saved).Err())
	require.NoError(t, c.Del(ctx, ntable.DefKey("demo")+":ops").Err())
	require.NoError(t, c.Set(ctx, ntable.DefKey("demo")+":ops", "s", 0).Err())
	refusedWith("operation records of the wrong type", first, "WRONGTYPE")
}
