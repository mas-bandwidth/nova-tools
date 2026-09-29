//go:build functional

package ntable_test

// A value of the wrong type at a key the table reads is a named refusal, WRONGTYPE
// with the key and the type found, from every function that reads it: never a
// raw Redis error from the script, and never a write.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
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
	if err != nil {
		t.Fatal(err)
	}
}

func requireWrongType(t *testing.T, what string, err error, key, kind string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: accepted", what)
		return
	}
	if !errors.Is(err, ntable.ErrWrongType) {
		t.Errorf("%s: not a WRONGTYPE refusal: %v", what, err)
		return
	}
	want := fmt.Sprintf("key %s is %s, expected hash", key, kind)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%s: refusal does not say %q: %v", what, want, err)
	}
	if strings.Contains(err.Error(), "ERR ") || strings.Contains(err.Error(), "user_function") {
		t.Errorf("%s: a raw script error: %v", what, err)
	}
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
			if rerr != nil || len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "WRONGTYPE" || ans[2] != key || ans[3] != kind || ans[4] != "hash" {
				t.Errorf("apply: %v %v; want REFUSED WRONGTYPE %s %s hash", trunc(ans), rerr, key, kind)
			}
			_, err = ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "wt2",
				Members: []ntable.BatchMemberEntry{{ID: "junk", Expect: &ntable.MemberExpect{}}}})
			requireWrongType(t, "apply (library)", err, key, kind)
			if err != nil && !strings.Contains(err.Error(), "changed=no") {
				t.Errorf("apply refusal does not say changed=no: %v", err)
			}

			if !reflect.DeepEqual(before, storeImage(t, c)) {
				t.Errorf("a refusal changed the store")
			}
			// the members that are not at fault are still served
			if _, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"a"}); err != nil {
				t.Errorf("read set of a sound member: %v", err)
			}
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
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refusal changed the store")
	}
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
		if e == nil || !errors.Is(e, ntable.ErrWrongType) || !strings.Contains(e.Error(), want) || strings.Contains(e.Error(), "user_function") {
			t.Errorf("%s: %v; want a WRONGTYPE refusal saying %q", what, e, want)
		}
	}
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refusal changed the store")
	}
}
