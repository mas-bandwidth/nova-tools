//go:build functional

package ntable_test

// Failures at the last checks a batch makes, and after a valid first entry: the
// destination cell, the change stream and the operation records of the wrong
// type, a full stream, a bound cell, a member of the wrong type. Each is a clean
// refusal and the store image is unchanged.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestBatchFailuresAtTheLastChecksWriteNothing(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	if _, err := ntable.RowAdd(ctx, c, "demo", "bnd", ntable.RowSpec{Binds: map[string]string{"ready": "ext:ready"}, Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, ntable.MemberKey("junk"), "s", 0).Err(); err != nil {
		t.Fatal(err)
	}
	first := `{"id":"a","expect":{"revision":"1"},"move":{"row":"test","col":"working"},"set":{"k":"v"}}`
	refusedWith := func(name, members, code string) {
		t.Helper()
		before := storeImage(t, c)
		ans, err := rawApply(ctx, c, manifestWith(probeRev(ctx, c), "late-"+strings.ReplaceAll(name, " ", "-"), members))
		if err != nil || len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != code {
			t.Errorf("%s: %v %v; want REFUSED %s", name, trunc(ans), err, code)
		}
		if !reflect.DeepEqual(before, storeImage(t, c)) {
			t.Errorf("%s: the store changed", name)
		}
	}
	refusedWith("late bound cell", first+`,{"id":"n9","expect":{"absent":true},"create":{"row":"bnd","col":"ready","score":1}}`, "BOUND")
	refusedWith("late wrong-type member", first+`,{"id":"junk","expect":{}}`, "WRONGTYPE")

	// a destination cell of the wrong type
	dst := ntable.CellKey("demo", "test", "done")
	if err := c.Set(ctx, dst, "s", 0).Err(); err != nil {
		t.Fatal(err)
	}
	refusedWith("wrong-type destination cell", first+`,{"id":"b","expect":{},"move":{"row":"test","col":"done"}}`, "WRONGTYPE")
	if err := c.Del(ctx, dst).Err(); err != nil {
		t.Fatal(err)
	}

	// the change stream of the wrong type, then full
	stream := ntable.DefKey("demo") + ":changes"
	saved := c.Dump(ctx, stream).Val()
	if err := c.Del(ctx, stream).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, stream, "s", 0).Err(); err != nil {
		t.Fatal(err)
	}
	refusedWith("change stream of the wrong type", first, "STREAMTYPE")
	if err := c.Del(ctx, stream).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Restore(ctx, stream, 0, saved).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.XAdd(ctx, &redis.XAddArgs{Stream: stream, ID: "18446744073709551615-18446744073709551615", Values: []string{"x", "y"}}).Err(); err != nil {
		t.Fatal(err)
	}
	refusedWith("full change stream", first, "STREAMFULL")

	// the operation records of the wrong type
	if err := c.Del(ctx, stream).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Restore(ctx, stream, 0, saved).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Del(ctx, ntable.DefKey("demo")+":ops").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, ntable.DefKey("demo")+":ops", "s", 0).Err(); err != nil {
		t.Fatal(err)
	}
	refusedWith("operation records of the wrong type", first, "WRONGTYPE")
}
