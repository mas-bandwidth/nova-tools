//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func refreshMultiRevisions(ctx context.Context, c *redis.Client, tables []ntable.MultiBatchTable) {
	for i := range tables {
		tables[i].ExpectedTableRevision = c.HGet(ctx, ntable.DefKey(tables[i].Name)+":revision", "n").Val()
	}
}

// A large application field outside the manifest's guards and changes does not
// enter the batch's value budget. The same physical record is read for two
// placements, but its application fields are read only by name.
func TestMultiBatchIgnoresUnmentionedHugeFieldOnSharedRecord(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "hold_a", "hold_b")
	seed := ntable.MultiBatchManifest{Schema: 2, Scope: "test.hold", OperationID: "seed", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "hold_a", ID: "m", Expect: &ntable.MultiMemberExpect{Absent: true,
			Places: []ntable.MultiPlaceExpect{{Table: "hold_a", Absent: true}, {Table: "hold_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "hold_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}},
				{Table: "hold_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 2}}}}}}
	if _, err := ntable.ApplyMultiBatch(ctx, c, seed); err != nil {
		t.Fatal(err)
	}
	const huge = ntable.LimitBatchValueBytes + 1
	if err := c.HSet(ctx, ntable.MemberKey("m"), "ignored", strings.Repeat("x", huge), "small", "old").Err(); err != nil {
		t.Fatal(err)
	}
	refreshMultiRevisions(ctx, c, tables)
	old := "old"
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.hold", OperationID: "small-change", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "hold_a", ID: "m", Expect: &ntable.MultiMemberExpect{Revision: "1",
			Fields: map[string]ntable.FieldGuard{"small": {Equals: &old}},
			Places: []ntable.MultiPlaceExpect{{Table: "hold_a", Row: "work", Col: "ready"}, {Table: "hold_b", Row: "work", Col: "ready"}}},
			Set: map[string]string{"small": "new"}}}}
	r, err := ntable.ApplyMultiBatch(ctx, c, m)
	if err != nil || r.Delta == nil || r.Delta.ChangedCount != 1 {
		t.Fatalf("small change beside an ignored %d-byte field: %+v %v", huge, r, err)
	}
	if n := c.HStrLen(ctx, ntable.MemberKey("m"), "ignored").Val(); n != huge {
		t.Fatalf("ignored field size = %d, want %d", n, huge)
	}
	if got := c.HGet(ctx, ntable.MemberKey("m"), "small").Val(); got != "new" {
		t.Fatalf("small field = %q", got)
	}
}

// Two touched before-values are individually below the bound, but together
// exceed it by one byte. The late member must refuse the entire transaction.
func TestMultiBatchAggregateTouchedValuesRefuseBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "value_a", "value_b")
	seed := ntable.MultiBatchManifest{Schema: 2, Scope: "test.values", OperationID: "seed", Tables: tables}
	for _, id := range []string{"first", "last"} {
		seed.Members = append(seed.Members, ntable.MultiBatchMember{RecordTable: "value_a", ID: id,
			Expect:     &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "value_a", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "value_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}})
	}
	if _, err := ntable.ApplyMultiBatch(ctx, c, seed); err != nil {
		t.Fatal(err)
	}
	const half = ntable.LimitBatchValueBytes / 2
	if err := c.HSet(ctx, ntable.MemberKey("first"), "large", strings.Repeat("a", half)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, ntable.MemberKey("last"), "large", strings.Repeat("b", half+1)).Err(); err != nil {
		t.Fatal(err)
	}
	refreshMultiRevisions(ctx, c, tables)
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.values", OperationID: "over", Tables: tables,
		Members: []ntable.MultiBatchMember{
			{RecordTable: "value_a", ID: "first", Expect: &ntable.MultiMemberExpect{Revision: "1"}, Unset: []string{"large"}},
			{RecordTable: "value_a", ID: "last", Expect: &ntable.MultiMemberExpect{Revision: "1"}, Unset: []string{"large"}},
		}}
	before := storeImage(t, c)
	_, err := ntable.ApplyMultiBatch(ctx, c, m)
	var limit *ntable.LimitError
	if !errors.Is(err, ntable.ErrLimit) || !errors.As(err, &limit) || !limit.AtLeast || limit.Observed != ntable.LimitBatchValueBytes+1 || !strings.Contains(err.Error(), "observed at least") {
		t.Fatalf("aggregate value bound must report its lower-bound count: %v", err)
	}
	assertMultiStoreUnchanged(t, c, before)
	if c.HExists(ctx, "table::batch:test.values:ops", "over").Val() {
		t.Fatal("refused operation left a replay record")
	}
}

func TestMultiBatchNamedFieldOnWrongTypeMemberRefusesWholeStore(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "type_a", "type_b")
	if err := c.Set(ctx, ntable.MemberKey("bad"), "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.type", OperationID: "bad-member", Tables: tables,
		Members: []ntable.MultiBatchMember{
			{RecordTable: "type_a", ID: "good", Expect: &ntable.MultiMemberExpect{Absent: true,
				Places: []ntable.MultiPlaceExpect{{Table: "type_a", Absent: true}}},
				Placements: []ntable.MultiPlacement{{Table: "type_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}},
			{RecordTable: "type_b", ID: "bad", Expect: &ntable.MultiMemberExpect{Revision: "0"},
				Set: map[string]string{"named": "after"}},
		}}
	before := storeImage(t, c)
	_, err := ntable.ApplyMultiBatch(ctx, c, m)
	var refusal *ntable.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "WRONGTYPE" || !errors.Is(err, ntable.ErrWrongType) {
		t.Fatalf("wrong-type named member field: %v", err)
	}
	assertMultiStoreUnchanged(t, c, before)
}

func TestMultiBatchDeniedMemberLengthReadsRefuseBeforeWrite(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, command string
		fields        bool
	}{
		{"hstrlen", "hstrlen", true},
		{"hlen", "hlen", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			tables := multiTableFixture(t, c, "denied_a", "denied_b")
			seed := ntable.MultiBatchManifest{Schema: 2, Scope: "test.denied", OperationID: "seed", Tables: tables,
				Members: []ntable.MultiBatchMember{{RecordTable: "denied_a", ID: "m", Expect: &ntable.MultiMemberExpect{Absent: true,
					Places: []ntable.MultiPlaceExpect{{Table: "denied_a", Absent: true}}},
					Placements: []ntable.MultiPlacement{{Table: "denied_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}}}}
			if _, err := ntable.ApplyMultiBatch(ctx, c, seed); err != nil {
				t.Fatal(err)
			}
			refreshMultiRevisions(ctx, c, tables)
			user := "multi_no_" + tc.command
			if err := c.Do(ctx, "ACL", "SETUSER", user, "on", ">multi-test-password", "~*", "+@all", "-"+tc.command).Err(); err != nil {
				t.Fatal(err)
			}
			limited := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: user, Password: "multi-test-password"})
			defer limited.Close()
			m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.denied", OperationID: "denied", Tables: tables,
				Members: []ntable.MultiBatchMember{{RecordTable: "denied_a", ID: "m", Expect: &ntable.MultiMemberExpect{Revision: "1"}}}}
			if tc.fields {
				m.Members[0].Set = map[string]string{"small": "new"}
			}
			before := storeImage(t, c)
			_, err := ntable.ApplyMultiBatch(ctx, limited, m)
			var refusal *ntable.Refusal
			if !errors.As(err, &refusal) || refusal.Code != "NOPERM" || !strings.Contains(err.Error(), strings.ToUpper(tc.command)) {
				t.Fatalf("denied %s: %v", tc.command, err)
			}
			assertMultiStoreUnchanged(t, c, before)
		})
	}
}
