//go:build functional

package ntable_test

// The batch contract on a live store: a refusal writes nothing, whichever entry
// fails and however late; replay, stale writes and operation conflicts behave as
// specified; and the fixtures the batch tests share.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func probeTable(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	for _, r := range []string{"build", "test"} {
		_, err := ntable.RowAdd(ctx, c, "demo", r, ntable.RowSpec{})
		require.NoError(t, err)
	}
	return c, ctx
}

// operationRecord reads the record of an operation: a table's records are one
// hash, epoch:operation id to JSON.
func operationRecord(t *testing.T, c *redis.Client, table, epoch, opID string) map[string]string {
	t.Helper()
	raw, err := c.HGet(context.Background(), ntable.DefKey(table)+":ops", epoch+":"+opID).Result()
	require.NoError(t, err, "operation record %s:%s of %s: %v", epoch, opID, table, err)
	var rec map[string]string
	err = json.Unmarshal([]byte(raw), &rec)
	require.NoError(t, err, "operation record %s: %v", raw, err)
	return rec
}

func probeRev(ctx context.Context, c *redis.Client) string {
	return c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
}

func rawApply(ctx context.Context, c *redis.Client, raw string) ([]any, error) {
	return c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", raw).Slice()
}

func seedTwo(t *testing.T, ctx context.Context, c *redis.Client) {
	t.Helper()
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "seed", Actor: "seed-actor",
		Members: []ntable.BatchMemberEntry{
			{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}, Set: map[string]string{"role": "x"}},
			{ID: "b", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "test", Col: "ready", Score: 2}},
		}}
	_, err := ntable.ApplyBatch(ctx, c, m)
	require.NoError(t, err)
}

// Raw FCALL refusals: every one refuses and leaves the store bit-identical.
func TestBatchRawRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	if err := ntable.MemberCreate(ctx, c, "demo", "u"); err != nil { // existing, unplaced
		t.Fatal(err)
	}
	rev := probeRev(ctx, c)
	head := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"%s","actor":"p","members":[%s]}`
	firstOK := `{"id":"a","expect":{"revision":"1"},"move":{"row":"test","col":"working"},"set":{"k":"v"}}`
	cases := map[string]string{
		"set-unset overlap":          `{"id":"a","expect":{},"set":{"x":"1"},"unset":["x"]}`,
		"remove false":               `{"id":"a","expect":{},"remove":false}`,
		"remove string":              `{"id":"a","expect":{},"remove":"true"}`,
		"remove number":              `{"id":"a","expect":{},"remove":1}`,
		"remove null":                `{"id":"a","expect":{},"remove":null}`,
		"late member revision":       firstOK + `,{"id":"b","expect":{"revision":"7"}}`,
		"late field guard":           firstOK + `,{"id":"b","expect":{"fields":{"role":{"equals":"q"}}}}`,
		"late twice":                 firstOK + `,{"id":"a","expect":{}}`,
		"late reserved":              firstOK + `,{"id":"b","expect":{},"set":{"revision":"9"}}`,
		"late unknown col":           firstOK + `,{"id":"b","expect":{},"move":{"row":"test","col":"nope"}}`,
		"late unknown row":           firstOK + `,{"id":"b","expect":{},"move":{"row":"nope","col":"ready"}}`,
		"late create existing":       firstOK + `,{"id":"b","expect":{"absent":true},"create":{"row":"build","col":"done","score":1}}`,
		"late create existing unpl":  firstOK + `,{"id":"u","expect":{"absent":true},"create":{"row":"build","col":"done","score":1}}`,
		"late move unplaced":         firstOK + `,{"id":"u","expect":{},"move":{"row":"build","col":"done"}}`,
		"late remove unplaced":       firstOK + `,{"id":"u","expect":{},"remove":true}`,
		"late missing guard":         firstOK + `,{"id":"zz","expect":{}}`,
		"late bad score string":      firstOK + `,{"id":"n1","expect":{"absent":true},"create":{"row":"build","col":"done","score":"x"}}`,
		"late create no score":       firstOK + `,{"id":"n2","expect":{"absent":true},"create":{"row":"build","col":"done"}}`,
		"late create with revision":  firstOK + `,{"id":"n3","expect":{"absent":true,"revision":"0"},"create":{"row":"build","col":"done","score":1}}`,
		"late one_of empty":          firstOK + `,{"id":"b","expect":{"fields":{"role":{"one_of":[]}}}}`,
		"late guard two conds":       firstOK + `,{"id":"b","expect":{"fields":{"role":{"absent":true,"equals":"x"}}}}`,
		"late member epoch":          firstOK + `,{"id":"b","expect":{"place":{"row":"build","col":"ready"}}}`,
		"late huge unset":            firstOK + `,{"id":"b","expect":{},"unset":[` + strings.TrimSuffix(strings.Repeat(`"f",`, 9000), ",") + `]}`,
		"late huge guards failing":   firstOK + `,{"id":"b","expect":{"fields":{` + manyGuards(3000) + `,"role":{"equals":"q"}}}}`,
		"late unknown field":         firstOK + `,{"id":"b","expect":{},"bogus":1}`,
		"duplicate json key":         `{"id":"a","expect":{},"set":{"k":"1","k":"2"}}`,
		"late absent-not-bool guard": firstOK + `,{"id":"b","expect":{"absent":"no"}}`,
	}
	for name, members := range cases {
		got := probeRev(ctx, c)
		require.Equal(t, rev, got, "table revision moved to %s before %s", got, name)
		before := storeImage(t, c)
		raw := fmt.Sprintf(head, "op-"+strings.ReplaceAll(name, " ", "-"), members)
		ans, err := rawApply(ctx, c, raw)
		after := storeImage(t, c)
		refused := err == nil && len(ans) >= 2 && ans[0] == "REFUSED"
		changed := !reflect.DeepEqual(before, after)
		assert.False(t, changed, "%s: STORE CHANGED (partial or full write)", name)
		if err != nil {
			t.Errorf("%s: a raw error reply leaves the script: %v", name, err)
		} else if !refused {
			t.Errorf("%s: accepted: %v", name, trunc(ans))
		}
	}
}

func manyGuards(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"g%d":{"absent":true}`, i)
	}
	return b.String()
}

func trunc(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

// expect.absent given anything but true through raw FCALL on a
// missing member used as a create guard, and on a guard-only entry.
func TestBatchAbsentMustBeTrueOnTheServer(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	rev := probeRev(ctx, c)
	for i, v := range []string{`0`, `"false"`, `null`, `{}`} {
		raw := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"%s","operation_id":"abs-%d","actor":"p","members":[{"id":"n%d","expect":{"absent":%s},"create":{"row":"build","col":"ready","score":1}}]}`, rev, i, i, v)
		ans, err := rawApply(ctx, c, raw)
		if err != nil || len(ans) < 2 || ans[0] != "REFUSED" {
			t.Errorf("absent=%s: %v %v; want the server to refuse a create guard that is not true", v, trunc(ans), err)
			rev = probeRev(ctx, c)
		}
	}
	_, err := ntable.ValidateBatchManifestRaw([]byte(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"1","operation_id":"x","members":[{"id":"n","expect":{"absent":0}}]}`))
	assert.Error(t, err, "Go validator accepted absent:0")
}

// Temporal sequences.
func TestBatchSequencesReplayStaleAndConflict(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	rev := probeRev(ctx, c)
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-seq", Actor: "p",
		Members: []ntable.BatchMemberEntry{
			{ID: "a", Expect: &ntable.MemberExpect{Revision: "1", Place: &ntable.PlaceExpect{Row: "build", Col: "ready"}}, Move: &ntable.MemberMoveOp{Row: "test", Col: "working"}},
		}}
	r1, err := ntable.ApplyBatch(ctx, c, m)
	require.NoError(t, err)
	// 1. replay identical (lost reply then retry): same receipt, no writes
	img := storeImage(t, c)
	r2, err := ntable.ApplyBatch(ctx, c, m)
	if err != nil || r2.ID != r1.ID || r2.Before != r1.Before || r2.After != r1.After || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("replay: r1=%+v r2=%+v err=%v", r1, r2, err)
	}
	// 2. ordinary verb then stale batch (new op id, old revisions)
	_, err = ntable.CellMove(ctx, c, "demo", "test", "working", "done", "a")
	require.NoError(t, err)
	ra, _ := ntable.ReadSetMembers(ctx, c, "demo", []string{"a"})
	am, _ := ra.Member("a")
	assert.Equal(t, uint64(3), am.Revision, "ordinary writer did not advance member revision: %d, want 3", am.Revision)
	stale := m
	stale.OperationID = "op-seq-stale"
	stale.Members = []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{Revision: "2"}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}
	img = storeImage(t, c)
	_, err = ntable.ApplyBatch(ctx, c, stale)
	if err == nil || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("stale batch accepted or wrote")
	}
	stale.ExpectedTableRevision = probeRev(ctx, c)
	stale.OperationID = "op-seq-stale2"
	_, err = ntable.ApplyBatch(ctx, c, stale)
	if err == nil || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("stale member batch accepted or wrote")
	}
	// 3. replay of the first op after the world moved on: original receipt
	r3, err := ntable.ApplyBatch(ctx, c, m)
	if err != nil || r3.ID != r1.ID || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("late replay: %+v %v", r3, err)
	}
	// 4. changed request under the same op id refuses
	changed := m
	changed.Actor = "other"
	_, err = ntable.ApplyBatch(ctx, c, changed)
	assert.Error(t, err, "changed request with same op id accepted")
	// 5. remove then create the same id
	rev = probeRev(ctx, c)
	rm := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-rm", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "b", Expect: &ntable.MemberExpect{}, Remove: true}}}
	rr, err := ntable.ApplyBatch(ctx, c, rm)
	require.NoError(t, err)
	cr := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: strconv.FormatUint(rr.After, 10), OperationID: "op-cr", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "b", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 3}}}}
	_, err = ntable.ApplyBatch(ctx, c, cr)
	assert.Error(t, err, "create after remove accepted though the record is retained")
	// 5b. the removed (unplaced) member can be re-placed by a move? spec: move needs placement
	mv := cr
	mv.OperationID = "op-mv-unplaced"
	mv.Members = []ntable.BatchMemberEntry{{ID: "b", Expect: &ntable.MemberExpect{}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}
	_, err = ntable.ApplyBatch(ctx, c, mv)
	if !errors.Is(err, ntable.ErrNotMember) || !strings.Contains(err.Error(), "changed=no") {
		t.Errorf("a move of a removed (unplaced) member: %v; want NOTMEMBER and changed=no", err)
	}
	// 6. no-op batch: does the table revision advance?
	rev = probeRev(ctx, c)
	noop := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-noop", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{}}}}
	rn, err := ntable.ApplyBatch(ctx, c, noop)
	if err != nil || rn.Outcome != "noop" || rn.After != rn.Before+1 || rn.BatchDelta.ChangedCount != 0 {
		t.Errorf("a no-op batch: %+v %v; want outcome noop, one table revision step, changed 0", rn, err)
	}
	// 7. ONE PLACE: hidden duplicate placement then create refuses
	require.NoError(t, c.ZAdd(ctx, ntable.CellKey("demo", "test", "done"), redis.Z{Score: 1, Member: "ghost"}).Err())
	rev = probeRev(ctx, c)
	gh := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-ghost", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "ghost", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 3}}}}
	img = storeImage(t, c)
	_, err = ntable.ApplyBatch(ctx, c, gh)
	if err == nil || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("create over a hidden placement accepted: ONE PLACE broken")
	}
	// 8. read set is read-only and refuses as FCALL? it is flagged no-writes.
	img = storeImage(t, c)
	_, err = ntable.ReadSetMembers(ctx, c, "demo", []string{"a", "b", "missing"})
	assert.NoError(t, err, "read set")
	assert.Equal(t, img, storeImage(t, c), "read set wrote")
	rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"members":["a",7,{"x":1}]}`).Slice()
	if err != nil || len(rs) < 2 || rs[0] != "REFUSED" || rs[1] != "ARGS" {
		t.Errorf("a read set with non-string ids: %v %v; want REFUSED ARGS", trunc(rs), err)
	}
}

// The first write of an epoch, by a batch or by an ordinary verb, leaves that
// epoch's history readable after the epoch advances.
func TestBatchFirstWriteOfAnEpochKeepsItsHistory(t *testing.T) {
	t.Parallel()
	for _, viaBatch := range []bool{false, true} {
		c, tb := epochFixture(t)
		ctx := context.Background()
		require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
		if viaBatch {
			rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
			raw := `{"schema":1,"table":"epoch-test","epoch":"1","expected_table_revision":"` + rev + `","operation_id":"e1","actor":"p","members":[{"id":"f","expect":{"absent":true}}]}`
			ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice()
			if err != nil || len(ans) == 0 || ans[0] != "OK" {
				t.Fatalf("apply at epoch 1: %v %v", trunc(ans), err)
			}
		} else if err := ntable.MemberCreate(ctx, c, tb.Name, "f", ntable.WriteOptions{Epoch: 1}); err != nil {
			t.Fatalf("member create at epoch 1: %v", err)
		}
		def := c.HGetAll(ctx, "table:epoch-test:1:definition").Val()
		assert.NotEmpty(t, def["order"], "viaBatch=%v: the epoch-1 definition snapshot has no order: %v", viaBatch, def)
		require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 2).Err())
		_, err := ntable.ReadAt(ctx, c, tb.Name, 1)
		assert.NoError(t, err, "viaBatch=%v: the history of epoch 1 is unreadable: %v", viaBatch, err)
	}
}

// A field-guard or one_of count over the bound refuses by name; it never
// runs the guards and never echoes the options.
func TestBatchOverBoundGuardsRefuseByName(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	before := storeImage(t, c)
	cases := map[string]string{
		"3000 field guards":    `{"id":"b","expect":{"fields":{` + manyGuards(3000) + `}}}`,
		"20000 one_of options": `{"id":"b","expect":{"fields":{"role":{"one_of":[` + strings.TrimSuffix(strings.Repeat(`"z",`, 20000), ",") + `]}}}}`,
	}
	for name, member := range cases {
		raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"g-` + name[:4] + `","actor":"p","members":[` + member + `]}`
		ans, err := rawApply(ctx, c, raw)
		if err != nil || len(ans) < 5 || ans[0] != "REFUSED" || ans[1] != "LIMIT" {
			t.Errorf("%s: %v %v; want a LIMIT refusal", name, trunc(ans), err)
			continue
		}
		assert.LessOrEqual(t, len(fmt.Sprint(ans)), 200, "%s: the refusal is %d bytes; it echoes the input", name, len(fmt.Sprint(ans)))
	}
	assert.Equal(t, before, storeImage(t, c), "a refused over-bound manifest changed the store")
}

// A move score that is not a number is refused by name, never silently replaced
// by the current score.
func TestBatchMoveScoreMustBeANumber(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	for i, v := range []string{`"abc"`, `null`, `true`} {
		raw := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"%s","operation_id":"ms-%d","actor":"p","members":[{"id":"a","expect":{},"move":{"row":"test","col":"done","score":%s}}]}`, probeRev(ctx, c), i, v)
		ans, err := rawApply(ctx, c, raw)
		if err != nil || len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "SCORE" {
			t.Errorf("move with score %s: %v %v; want REFUSED SCORE", v, trunc(ans), err)
		}
	}
}

// A move to the member's current cell at its current score changes nothing.
func TestBatchMoveToTheCurrentCellIsANoop(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "same", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{Revision: "1"}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}}
	r, err := ntable.ApplyBatch(ctx, c, m)
	require.NoError(t, err)
	d := r.BatchDelta.Members[0]
	if d.AfterRev != "1" || r.Outcome != "noop" || r.BatchDelta.ChangedCount != 0 {
		t.Errorf("same-cell move is not a member no-op")
	}
}

// A create with no score is refused before the store is asked, and so is a
// manifest without its header.
func TestBatchCreateWithoutScoreIsRefusedBeforeTheStore(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	before := storeImage(t, c)
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"ns","members":[{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready"}}]}`
	_, err := ntable.ValidateBatchManifestRaw([]byte(raw))
	assert.ErrorContains(t, err, "create requires score", "a create without a score: %v; want the validator to refuse it", err)
	_, err = ntable.ValidateBatchManifestRaw([]byte(`{"table":"demo","operation_id":"ns2","members":[]}`))
	assert.Error(t, err, "a manifest without schema, epoch and expected_table_revision was accepted")
	assert.Equal(t, before, storeImage(t, c), "the store changed")
}
