//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func multiTableFixture(t *testing.T, c *redis.Client, names ...string) []ntable.MultiBatchTable {
	t.Helper()
	ctx := context.Background()
	participants := make([]ntable.MultiBatchTable, 0, len(names))
	for _, name := range names {
		tb := demo()
		tb.Name = name
		if err := ntable.Create(ctx, c, tb, now); err != nil {
			t.Fatal(err)
		}
		if _, err := ntable.RowAdd(ctx, c, name, "work", ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
		participants = append(participants, ntable.MultiBatchTable{Name: name, Epoch: "0", ExpectedTableRevision: c.HGet(ctx, ntable.DefKey(name)+":revision", "n").Val()})
	}
	return participants
}

func assertMultiStoreUnchanged(t *testing.T, c *redis.Client, before map[string]string) {
	t.Helper()
	if after := storeImage(t, c); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused multi batch changed the Redis store: before=%d keys after=%d keys", len(before), len(after))
	}
}

func TestMultiBatchSharedRecordAcrossThreeTablesAndReplay(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "multi_a", "multi_b", "multi_c")
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.shared", OperationID: "create-1", Actor: "test", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "multi_a", ID: "card", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "multi_a", Absent: true}, {Table: "multi_b", Absent: true}, {Table: "multi_c", Absent: true}}}, Set: map[string]string{"status": "new"},
			Placements: []ntable.MultiPlacement{
				{Table: "multi_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}},
				{Table: "multi_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 2}},
				{Table: "multi_c", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 3}},
			}}}}
	r, err := ntable.ApplyMultiBatch(ctx, c, m)
	if err != nil {
		t.Fatal(err)
	}
	if r.Replay || r.ID == "" || r.Delta == nil || len(r.Delta.Members) != 1 || r.Delta.ChangedCount != 1 {
		t.Fatalf("receipt %+v", r)
	}
	d := r.Delta.Members[0]
	if d.BeforeRev != "0" || d.AfterRev != "1" || len(d.Placements) != 3 {
		t.Fatalf("one shared member revision expected, delta %+v", d)
	}
	for i, name := range []string{"multi_a", "multi_b", "multi_c"} {
		v, err := c.ZScore(ctx, ntable.CellKey(name, "work", "ready"), "card").Result()
		if err != nil || v != float64(i+1) {
			t.Fatalf("%s score %v: %v", name, v, err)
		}
	}
	if rev := c.HGet(ctx, ntable.MemberKey("card"), "revision").Val(); rev != "1" {
		t.Fatalf("shared revision %q", rev)
	}
	aggregate := c.XRevRangeN(ctx, "table::batch:test.shared:changes", "+", "-", 1).Val()
	if len(aggregate) != 1 || aggregate[0].ID != r.ID || aggregate[0].Values["batch_delta"] == nil {
		t.Fatalf("aggregate stream event: %+v", aggregate)
	}
	for _, name := range []string{"multi_a", "multi_b", "multi_c"} {
		ref := c.XRevRangeN(ctx, ntable.DefKey(name)+":changes", "+", "-", 1).Val()
		if len(ref) != 1 || ref[0].Values["verb"] != "multi_ref" || ref[0].Values["aggregate_id"] != r.ID || ref[0].Values["batch_delta"] != nil || len(fmt.Sprint(ref[0].Values)) > 2048 {
			t.Fatalf("%s bounded receipt reference: %+v", name, ref)
		}
	}
	replay, err := ntable.ApplyMultiBatch(ctx, c, m)
	if err != nil || !replay.Replay || replay.ID != r.ID {
		t.Fatalf("replay %+v: %v", replay, err)
	}
	conflict := m
	conflict.Members = append([]ntable.MultiBatchMember(nil), m.Members...)
	conflict.Members[0].Set = map[string]string{"status": "different"}
	if _, err := ntable.ApplyMultiBatch(ctx, c, conflict); !errors.Is(err, ntable.ErrOpConflict) {
		t.Fatalf("op conflict: %v", err)
	}
	if v := c.HGet(ctx, ntable.MemberKey("card"), "status").Val(); v != "new" {
		t.Fatalf("conflict wrote status %q", v)
	}
	if _, err := ntable.Drop(ctx, c, "multi_c"); err != nil {
		t.Fatal(err)
	}
	historical, err := ntable.ApplyMultiBatch(ctx, c, m)
	if err != nil || !historical.Replay || historical.ID != r.ID {
		t.Fatalf("historical replay after participant drop: %+v %v", historical, err)
	}
}

func TestMultiBatchLastParticipantStaleIsAtomic(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "stale_a", "stale_b", "stale_c")
	// Advance only the last table after capturing the three revision guards.
	if _, err := ntable.RowAdd(ctx, c, "stale_c", "later", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, tb := range tables {
		before[tb.Name] = c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
	}
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.stale", OperationID: "last-stale", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "stale_a", ID: "card", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "stale_a", Absent: true}, {Table: "stale_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "stale_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}, {Table: "stale_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}}}}
	image := storeImage(t, c)
	if _, err := ntable.ApplyMultiBatch(ctx, c, m); !errors.Is(err, ntable.ErrRevisionMismatch) {
		t.Fatalf("last-table revision refusal: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
	for _, tb := range tables {
		if rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val(); rev != before[tb.Name] {
			t.Fatalf("%s changed revision %q from %q", tb.Name, rev, before[tb.Name])
		}
		if n := c.ZCard(ctx, ntable.CellKey(tb.Name, "work", "ready")).Val(); n != 0 {
			t.Fatalf("%s has %d uncommitted members", tb.Name, n)
		}
	}
	if c.Exists(ctx, ntable.MemberKey("card")).Val() != 0 {
		t.Fatal("refused batch created physical member")
	}
}

func TestMultiBatchAttachExistingSharedMemberAtIndependentScore(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "attach_a", "attach_b")
	seed := ntable.BatchManifest{Schema: 1, Table: "attach_a", Epoch: "0", ExpectedTableRevision: tables[0].ExpectedTableRevision, OperationID: "seed", Members: []ntable.BatchMemberEntry{{
		ID: "shared", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 7}, Set: map[string]string{"status": "seeded"},
	}}}
	if _, err := ntable.ApplyBatch(ctx, c, seed); err != nil {
		t.Fatal(err)
	}
	for i := range tables {
		tables[i].ExpectedTableRevision = c.HGet(ctx, ntable.DefKey(tables[i].Name)+":revision", "n").Val()
	}
	beforeRev := c.HGet(ctx, ntable.MemberKey("shared"), "revision").Val()
	if beforeRev != "1" {
		t.Fatalf("seed revision %q", beforeRev)
	}
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.attach", OperationID: "attach-second", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "attach_a", ID: "shared", Expect: &ntable.MultiMemberExpect{Revision: beforeRev,
			Places: []ntable.MultiPlaceExpect{{Table: "attach_a", Row: "work", Col: "ready"}, {Table: "attach_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "attach_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 23}}}}}}
	r, err := ntable.ApplyMultiBatch(ctx, c, m)
	if err != nil || r.Delta == nil || r.Delta.ChangedCount != 1 || len(r.Delta.Members) != 1 {
		t.Fatalf("attach receipt %+v %v", r, err)
	}
	delta := r.Delta.Members[0]
	if delta.BeforeRev != "1" || delta.AfterRev != "2" || len(delta.Placements) != 1 || delta.Placements[0].Table != "attach_b" {
		t.Fatalf("shared record advanced once: %+v", delta)
	}
	for _, tc := range []struct {
		table string
		score float64
	}{{"attach_a", 7}, {"attach_b", 23}} {
		score, err := c.ZScore(ctx, ntable.CellKey(tc.table, "work", "ready"), "shared").Result()
		if err != nil || score != tc.score {
			t.Fatalf("%s score %v: %v; want %v", tc.table, score, err, tc.score)
		}
	}
	if rev := c.HGet(ctx, ntable.MemberKey("shared"), "revision").Val(); rev != "2" {
		t.Fatalf("shared record revision %q", rev)
	}
	if got := c.HGet(ctx, ntable.MemberKey("shared"), "place:attach_a").Val(); got != "work:ready" {
		t.Fatalf("first placement %q", got)
	}
	if got := c.HGet(ctx, ntable.MemberKey("shared"), "place:attach_b").Val(); got != "work:ready" {
		t.Fatalf("second placement %q", got)
	}
}

func TestMultiBatchDuplicatePhysicalAliasRefused(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "alias_a", "alias_b")
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.alias", OperationID: "dup", Tables: tables,
		Members: []ntable.MultiBatchMember{
			{RecordTable: "alias_a", ID: "same", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "alias_a", Absent: true}}}, Set: map[string]string{"x": "a"}, Placements: []ntable.MultiPlacement{{Table: "alias_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}},
			{RecordTable: "alias_b", ID: "same", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "alias_b", Absent: true}}}, Set: map[string]string{"x": "b"}, Placements: []ntable.MultiPlacement{{Table: "alias_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 2}}}},
		}}
	image := storeImage(t, c)
	if _, err := ntable.ApplyMultiBatch(ctx, c, m); !errors.Is(err, ntable.ErrDuplicateMember) {
		t.Fatalf("alias duplicate: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
	if c.Exists(ctx, ntable.MemberKey("same")).Val() != 0 {
		t.Fatal("duplicate alias created member")
	}
}

func TestMultiBatchSameIDInDistinctMemberNamespaces(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := make([]ntable.MultiBatchTable, 0, 2)
	for i, name := range []string{"prefix_a", "prefix_b"} {
		tb := demo()
		tb.Name = name
		tb.MemberPrefix = fmt.Sprintf("multi::member%d:", i)
		if err := ntable.Create(ctx, c, tb, now); err != nil {
			t.Fatal(err)
		}
		if _, err := ntable.RowAdd(ctx, c, name, "work", ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, ntable.MultiBatchTable{Name: name, Epoch: "0", ExpectedTableRevision: c.HGet(ctx, ntable.DefKey(name)+":revision", "n").Val()})
	}
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.distinct", OperationID: "same-id", Tables: tables}
	for i, name := range []string{"prefix_a", "prefix_b"} {
		m.Members = append(m.Members, ntable.MultiBatchMember{RecordTable: name, ID: "same", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: name, Absent: true}}},
			Set: map[string]string{"owner": name}, Placements: []ntable.MultiPlacement{{Table: name, Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: float64(i + 1)}}}})
	}
	r, err := ntable.ApplyMultiBatch(ctx, c, m)
	if err != nil || r.Delta == nil || r.Delta.ChangedCount != 2 {
		t.Fatalf("distinct physical members: %+v %v", r, err)
	}
	for i, name := range []string{"prefix_a", "prefix_b"} {
		if owner := c.HGet(ctx, fmt.Sprintf("multi::member%d:same", i), "owner").Val(); owner != name {
			t.Fatalf("%s owner %q", name, owner)
		}
	}
}

func TestMultiBatchAggregateChangedBound(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "bound_a", "bound_b")
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.bound", OperationID: "too-many", Tables: tables}
	for i := 0; i < ntable.LimitChangedEntries+1; i++ {
		m.Members = append(m.Members, ntable.MultiBatchMember{RecordTable: "bound_a", ID: fmt.Sprintf("m%d", i), Expect: &ntable.MultiMemberExpect{Absent: true}, Set: map[string]string{"v": "x"}})
	}
	image := storeImage(t, c)
	if _, err := ntable.ApplyMultiBatch(ctx, c, m); !errors.Is(err, ntable.ErrLimit) {
		t.Fatalf("aggregate bound: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := c.FCall(ctx, ntable.FnApplyMulti, nil, m.Scope, string(raw)).Slice()
	if err != nil || len(reply) < 5 || reply[0] != "REFUSED" || reply[1] != "LIMIT" || reply[2] != "entries with changes" {
		t.Fatalf("raw Lua aggregate changed bound: %v %v", reply, err)
	}
	assertMultiStoreUnchanged(t, c, image)
	if len(c.Keys(ctx, "table::member:m*").Val()) != 0 {
		t.Fatal("bounded request wrote member")
	}
}

func TestMultiBatchRawAbsentWithEmptyFieldsParity(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "empty_fields_a", "empty_fields_b")
	raw := fmt.Sprintf(`{"schema":2,"scope":"test.empty-fields","operation_id":"bad","tables":[{"name":"empty_fields_a","epoch":"0","expected_table_revision":%q},{"name":"empty_fields_b","epoch":"0","expected_table_revision":%q}],"members":[{"record_table":"empty_fields_a","id":"card","expect":{"absent":true,"fields":{}}}]}`, tables[0].ExpectedTableRevision, tables[1].ExpectedTableRevision)
	if _, err := ntable.ValidateMultiBatchManifestRaw([]byte(raw)); !errors.Is(err, ntable.ErrMutation) {
		t.Fatalf("Go absent/fields parity: %v", err)
	}
	image := storeImage(t, c)
	reply, err := c.FCall(ctx, ntable.FnApplyMulti, nil, "test.empty-fields", raw).Slice()
	if err != nil || len(reply) < 2 || reply[0] != "REFUSED" || reply[1] != "MUTATION" {
		t.Fatalf("Lua absent/fields parity: %v %v", reply, err)
	}
	assertMultiStoreUnchanged(t, c, image)
}

func TestMultiBatchLateWrongTypeRefusesWholeTransaction(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "wrong_a", "wrong_b")
	// The second participant's owned cell must be a sorted set.
	key := ntable.CellKey("wrong_b", "work", "ready")
	if err := c.Set(ctx, key, "wrong", 0).Err(); err != nil {
		t.Fatal(err)
	}
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.wrong", OperationID: "late", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "wrong_a", ID: "card", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "wrong_a", Absent: true}, {Table: "wrong_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "wrong_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}, {Table: "wrong_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}}}}
	image := storeImage(t, c)
	if _, err := ntable.ApplyMultiBatch(ctx, c, m); err == nil || (!errors.Is(err, ntable.ErrWrongType) && !strings.Contains(err.Error(), "WRONGTYPE")) {
		t.Fatalf("late wrong type: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
	if c.Exists(ctx, ntable.MemberKey("card")).Val() != 0 {
		t.Fatal("late refusal wrote member")
	}
	if c.ZCard(ctx, ntable.CellKey("wrong_a", "work", "ready")).Val() != 0 {
		t.Fatal("late refusal wrote first table")
	}
}

func TestMultiBatchAggregateReceiptBoundRefusesBeforeWrite(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "receipt_a", "receipt_b")
	seed := ntable.MultiBatchManifest{Schema: 2, Scope: "test.receipt", OperationID: "seed", Tables: tables}
	const members, fields = 32, 500
	for i := 0; i < members; i++ {
		seed.Members = append(seed.Members, ntable.MultiBatchMember{RecordTable: "receipt_a", ID: fmt.Sprintf("m%d", i),
			Expect:     &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "receipt_a", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "receipt_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: float64(i)}}}})
	}
	if _, err := ntable.ApplyMultiBatch(ctx, c, seed); err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("v", 256)
	pipe := c.Pipeline()
	for i := 0; i < members; i++ {
		values := make(map[string]any, fields)
		for j := 0; j < fields; j++ {
			values[fmt.Sprintf("f%d", j)] = value
		}
		pipe.HSet(ctx, ntable.MemberKey(fmt.Sprintf("m%d", i)), values)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for i := range tables {
		tables[i].ExpectedTableRevision = c.HGet(ctx, ntable.DefKey(tables[i].Name)+":revision", "n").Val()
	}
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.receipt", OperationID: "large-unset", Tables: tables}
	for i := 0; i < members; i++ {
		unset := make([]string, fields)
		for j := range unset {
			unset[j] = fmt.Sprintf("f%d", j)
		}
		m.Members = append(m.Members, ntable.MultiBatchMember{RecordTable: "receipt_a", ID: fmt.Sprintf("m%d", i), Expect: &ntable.MultiMemberExpect{Revision: "1"}, Unset: unset})
	}
	image := storeImage(t, c)
	_, err := ntable.ApplyMultiBatch(ctx, c, m)
	if !errors.Is(err, ntable.ErrLimit) || !strings.Contains(err.Error(), "receipt bytes") {
		t.Fatalf("aggregate receipt bound: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
	for _, tb := range tables {
		if rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val(); rev != tb.ExpectedTableRevision {
			t.Fatalf("%s changed revision on receipt refusal", tb.Name)
		}
	}
	if v := c.HGet(ctx, ntable.MemberKey("m0"), "f0").Val(); v != value {
		t.Fatal("receipt refusal changed fields")
	}
}

func TestMultiBatchACLRefusalBeforeWrite(t *testing.T) {
	t.Parallel()
	addr, c := live(t)
	ctx := context.Background()
	tables := multiTableFixture(t, c, "acl_a", "acl_b")
	if err := c.Do(ctx, "ACL", "SETUSER", "multi_nozadd", "on", ">multi-test-password", "~*", "+@all", "-zadd").Err(); err != nil {
		t.Fatal(err)
	}
	limited := redis.NewClient(&redis.Options{Addr: addr, Username: "multi_nozadd", Password: "multi-test-password"})
	defer limited.Close()
	m := ntable.MultiBatchManifest{Schema: 2, Scope: "test.acl", OperationID: "deny-zadd", Tables: tables,
		Members: []ntable.MultiBatchMember{{RecordTable: "acl_a", ID: "card", Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "acl_a", Absent: true}, {Table: "acl_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{{Table: "acl_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}, {Table: "acl_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}}}}}}
	image := storeImage(t, c)
	_, err := ntable.ApplyMultiBatch(ctx, limited, m)
	var refusal *ntable.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "NOPERM" {
		t.Fatalf("ACL refusal: %v", err)
	}
	assertMultiStoreUnchanged(t, c, image)
	for _, tb := range tables {
		if rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val(); rev != tb.ExpectedTableRevision {
			t.Fatalf("%s changed revision after ACL refusal", tb.Name)
		}
	}
	if c.Exists(ctx, ntable.MemberKey("card")).Val() != 0 {
		t.Fatal("ACL refusal wrote member")
	}
}
