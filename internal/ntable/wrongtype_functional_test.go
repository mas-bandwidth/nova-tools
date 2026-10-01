//go:build functional

package ntable_test

// A value of the wrong type at a key the table reads is a named refusal, WRONGTYPE
// with the key and the type found, from every function that reads it: never a
// raw Redis error from the script, and never a write.

import (
	"context"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plant(t *testing.T, ctx context.Context, c *redis.Client, key, kind string) {
	t.Helper()
	var err error
	switch kind {
	case "string":
		err = c.Set(ctx, key, "x", 0).Err()
	case "list":
		err = c.RPush(ctx, key, "x").Err()
	case "set":
		err = c.SAdd(ctx, key, "x").Err()
	case "zset":
		err = c.ZAdd(ctx, key, redis.Z{Score: 1, Member: "x"}).Err()
	}
	require.NoError(t, err)
}

func requireWrongType(t *testing.T, what string, err error, key, kind string) {
	t.Helper()
	if !assert.Error(t, err, "%s: accepted", what) || !assert.ErrorIs(t, err, ntable.ErrWrongType, "%s: not a WRONGTYPE refusal: %v", what, err) {
		return
	}
	want := fmt.Sprintf("key %s is %s, expected hash", key, kind)
	assert.ErrorContains(t, err, want, "%s: refusal does not say %q: %v", what, want, err)
	assert.NotContains(t, err.Error(), "ERR ", "%s: a raw script error: %v", what, err)
	assert.NotContains(t, err.Error(), "user_function", "%s: a raw script error: %v", what, err)
}

func TestWrongTypeAtAMemberKeyIsANamedRefusal(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"string", "list", "set", "zset"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			c, ctx := probeTable(t)
			seedTwo(t, ctx, c)
			key := ntable.MemberKey("junk")
			plant(t, ctx, c, key, kind)
			before := storeImage(t, c)

			_, err := ntable.Check(ctx, c, "demo")
			requireWrongType(t, "check", err, key, kind)

			_, err = ntable.Set(ctx, c, "demo", ntable.SetOpts{Rename: "demo2"})
			requireWrongType(t, "rename", err, key, kind)

			_, err = ntable.ReadSetMembers(ctx, c, "demo", []string{"junk"})
			requireWrongType(t, "read set", err, key, kind)

			raw := manifestWith(probeRev(ctx, c), "wt", `{"id":"junk","expect":{}}`)
			ans, rerr := rawApply(ctx, c, raw)
			assert.True(t, replyOpens(ans, rerr, "REFUSED", "WRONGTYPE", key, kind, "hash"), "apply: %v %v; want REFUSED WRONGTYPE %s %s hash", trunc(ans), rerr, key, kind)
			_, err = ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "wt2",
				Members: []ntable.BatchMemberEntry{{ID: "junk", Expect: &ntable.MemberExpect{}}}})
			requireWrongType(t, "apply (library)", err, key, kind)
			if err != nil {
				assert.Contains(t, err.Error(), "changed=no", "apply refusal does not say changed=no: %v", err)
			}

			assert.Equal(t, before, storeImage(t, c), "a refusal changed the store")
			// the members that are not at fault are still served
			_, err = ntable.ReadSetMembers(ctx, c, "demo", []string{"a"})
			assert.NoError(t, err, "read set of a sound member")
		})
	}
}

func TestWrongTypeAtARowKeyIsANamedRefusal(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	key := ntable.RowKey("demo", "build")
	plant(t, ctx, c, key, "string")
	before := storeImage(t, c)

	_, err := ntable.Read(ctx, c, "demo")
	requireWrongType(t, "read", err, key, "string")
	_, err = ntable.Check(ctx, c, "demo")
	requireWrongType(t, "check", err, key, "string")
	_, err = ntable.RowDel(ctx, c, "demo", "build")
	requireWrongType(t, "row del", err, key, "string")
	_, err = ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
	requireWrongType(t, "row add", err, key, "string")
	assert.Equal(t, before, storeImage(t, c), "a refusal changed the store")
}

// The owned cell is a sorted set; anything else there is named the same way.
func TestWrongTypeAtACellKeyIsANamedRefusal(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	key := ntable.CellKey("demo", "build", "working")
	plant(t, ctx, c, key, "string")
	before := storeImage(t, c)
	want := "key " + key + " is string, expected zset"

	_, cerr := ntable.Check(ctx, c, "demo")
	_, aerr := ntable.CellAdd(ctx, c, "demo", "build", "working", "q", 1)
	_, serr := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Selection: []ntable.CellSelection{{Row: "build", Col: "working"}}})
	_, berr := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "cell",
		Members: []ntable.BatchMemberEntry{{ID: "q", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "working", Score: 1}}}})
	for what, e := range map[string]error{"check": cerr, "cell add": aerr, "read set": serr, "apply": berr} {
		require.ErrorIs(t, e, ntable.ErrWrongType, "%s: %v; want a WRONGTYPE refusal saying %q", what, e, want)
		assert.Contains(t, e.Error(), want, "%s: %v; want a WRONGTYPE refusal saying %q", what, e, want)
		assert.NotContains(t, e.Error(), "user_function", "%s: %v; want a WRONGTYPE refusal saying %q", what, e, want)
	}
	assert.Equal(t, before, storeImage(t, c), "a refusal changed the store")
}
