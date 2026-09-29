//go:build functional

package ntable_test

// Cold-reader probes for #4610 at 50aa377c87. Each probe logs observed
// behaviour; t.Errorf marks a contract deviation.

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func probeTable(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"build", "test"} {
		if _, err := ntable.RowAdd(ctx, c, "demo", r, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	return c, ctx
}

func probeRev(ctx context.Context, c *redis.Client) string {
	return c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
}

func rawApply(ctx context.Context, c *redis.Client, raw string) ([]any, error) {
	return c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", raw).Slice()
}

func seedTwo(t *testing.T, ctx context.Context, c *redis.Client) {
	t.Helper()
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "seed", Actor: "probe",
		Members: []ntable.BatchMemberEntry{
			{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}, Set: map[string]string{"role": "x"}},
			{ID: "b", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "test", Col: "ready", Score: 2}},
		}}
	if _, err := ntable.ApplyBatch(ctx, c, m); err != nil {
		t.Fatal(err)
	}
}

// Raw FCALL refusals: every one must refuse and leave the store bit-identical.
func TestReaderProbeRawRefusalsWriteNothing(t *testing.T) {
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
		if got := probeRev(ctx, c); got != rev {
			t.Fatalf("table revision moved to %s before %s", got, name)
		}
		before := storeImage(t, c)
		raw := fmt.Sprintf(head, "op-"+strings.ReplaceAll(name, " ", "-"), members)
		ans, err := rawApply(ctx, c, raw)
		after := storeImage(t, c)
		refused := err == nil && len(ans) >= 2 && ans[0] == "REFUSED"
		changed := !reflect.DeepEqual(before, after)
		t.Logf("%-28s refused=%v changed=%v reply=%v err=%v", name, refused, changed, trunc(ans), err)
		if changed {
			t.Errorf("%s: STORE CHANGED (partial or full write)", name)
		}
		if !refused && err == nil {
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

// expect.absent given a non-boolean truthy value through raw FCALL on a
// missing member used as a create guard, and on a guard-only entry.
func TestReaderProbeAbsentNonBooleanServerSide(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	rev := probeRev(ctx, c)
	for i, v := range []string{`0`, `"false"`, `null`, `{}`} {
		raw := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"%s","operation_id":"abs-%d","actor":"p","members":[{"id":"n%d","expect":{"absent":%s},"create":{"row":"build","col":"ready","score":1}}]}`, rev, i, i, v)
		ans, err := rawApply(ctx, c, raw)
		t.Logf("absent=%s -> %v %v", v, trunc(ans), err)
		if err == nil && len(ans) > 0 && ans[0] == "OK" {
			t.Errorf("absent=%s accepted by the server as a create guard", v)
			rev = probeRev(ctx, c)
		}
	}
	if _, err := ntable.ValidateBatchManifestRaw([]byte(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"1","operation_id":"x","members":[{"id":"n","expect":{"absent":0}}]}`)); err == nil {
		t.Errorf("Go validator accepted absent:0")
	}
}

// Temporal sequences.
func TestReaderProbeSequences(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	rev := probeRev(ctx, c)
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-seq", Actor: "p",
		Members: []ntable.BatchMemberEntry{
			{ID: "a", Expect: &ntable.MemberExpect{Revision: "1", Place: &ntable.PlaceExpect{Row: "build", Col: "ready"}}, Move: &ntable.MemberMoveOp{Row: "test", Col: "working"}},
		}}
	r1, err := ntable.ApplyBatch(ctx, c, m)
	if err != nil {
		t.Fatal(err)
	}
	// 1. replay identical (lost reply then retry): same receipt, no writes
	img := storeImage(t, c)
	r2, err := ntable.ApplyBatch(ctx, c, m)
	if err != nil || r2.ID != r1.ID || r2.Before != r1.Before || r2.After != r1.After || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("replay: r1=%+v r2=%+v err=%v", r1, r2, err)
	}
	// 2. ordinary verb then stale batch (new op id, old revisions)
	if _, err := ntable.CellMove(ctx, c, "demo", "test", "working", "done", "a"); err != nil {
		t.Fatal(err)
	}
	ra, _ := ntable.ReadSetMembers(ctx, c, "demo", []string{"a"})
	am, _ := ra.Member("a")
	t.Logf("after ordinary cell move: member a rev=%d place=%s:%s", am.Revision, am.Row, am.Col)
	if am.Revision != 3 {
		t.Errorf("ordinary writer did not advance member revision: %d, want 3", am.Revision)
	}
	stale := m
	stale.OperationID = "op-seq-stale"
	stale.Members = []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{Revision: "2"}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}
	img = storeImage(t, c)
	_, err = ntable.ApplyBatch(ctx, c, stale)
	t.Logf("stale table revision: %v", err)
	if err == nil || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("stale batch accepted or wrote")
	}
	stale.ExpectedTableRevision = probeRev(ctx, c)
	stale.OperationID = "op-seq-stale2"
	_, err = ntable.ApplyBatch(ctx, c, stale)
	t.Logf("stale member revision: %v", err)
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
	t.Logf("op conflict: %v", err)
	if err == nil {
		t.Errorf("changed request with same op id accepted")
	}
	// 5. remove then create the same id
	rev = probeRev(ctx, c)
	rm := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-rm", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "b", Expect: &ntable.MemberExpect{}, Remove: true}}}
	rr, err := ntable.ApplyBatch(ctx, c, rm)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("remove receipt: %+v delta=%+v", rr, *rr.BatchDelta)
	cr := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: strconv.FormatUint(rr.After, 10), OperationID: "op-cr", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "b", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 3}}}}
	_, err = ntable.ApplyBatch(ctx, c, cr)
	t.Logf("create after remove: %v", err)
	if err == nil {
		t.Errorf("create after remove accepted though the record is retained")
	}
	// 5b. the removed (unplaced) member can be re-placed by a move? spec: move needs placement
	mv := cr
	mv.OperationID = "op-mv-unplaced"
	mv.Members = []ntable.BatchMemberEntry{{ID: "b", Expect: &ntable.MemberExpect{}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}
	_, err = ntable.ApplyBatch(ctx, c, mv)
	t.Logf("move of removed member: %v", err)
	// 6. no-op batch: does the table revision advance?
	rev = probeRev(ctx, c)
	noop := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-noop", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{}}}}
	rn, err := ntable.ApplyBatch(ctx, c, noop)
	t.Logf("noop batch: outcome=%s before=%d after=%d err=%v", rn.Outcome, rn.Before, rn.After, err)
	// 7. ONE PLACE: hidden duplicate placement then create refuses
	if err := c.ZAdd(ctx, ntable.CellKey("demo", "test", "done"), redis.Z{Score: 1, Member: "ghost"}).Err(); err != nil {
		t.Fatal(err)
	}
	rev = probeRev(ctx, c)
	gh := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "op-ghost", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "ghost", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 3}}}}
	img = storeImage(t, c)
	_, err = ntable.ApplyBatch(ctx, c, gh)
	t.Logf("create over hidden placement: %v", err)
	if err == nil || !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("create over a hidden placement accepted: ONE PLACE broken")
	}
	// 8. read set is read-only and refuses as FCALL? it is flagged no-writes.
	img = storeImage(t, c)
	if _, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"a", "b", "missing"}); err != nil {
		t.Errorf("read set: %v", err)
	}
	if !reflect.DeepEqual(storeImage(t, c), img) {
		t.Errorf("read set wrote")
	}
	rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"members":["a",7,{"x":1}]}`).Slice()
	t.Logf("read set with non-string ids: %v %v", trunc(rs), err)
}

// First write of a new epoch through apply, then the epoch advances: can the
// old epoch still be read historically (as for an ordinary first write)?
func TestReaderProbeEpochFirstWriteByApply(t *testing.T) {
	t.Parallel()
	for _, viaApply := range []bool{false, true} {
		c, tb := epochFixture(t)
		ctx := context.Background()
		if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
			t.Fatal(err)
		}
		if viaApply {
			// rows are per epoch: the new epoch needs a row first, but a row add is an
			// ordinary write that would snapshot the definition. Use member-only apply.
			rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
			raw := `{"schema":1,"table":"epoch-test","epoch":"1","expected_table_revision":"` + rev + `","operation_id":"e1","actor":"p","members":[{"id":"f","expect":{"absent":true}}]}`
			ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice()
			t.Logf("apply at epoch 1: %v %v", trunc(ans), err)
		} else {
			if err := ntable.MemberCreate(ctx, c, tb.Name, "f", ntable.WriteOptions{Epoch: 1}); err != nil {
				t.Logf("member create at epoch 1: %v", err)
			}
		}
		def := c.HGetAll(ctx, "table:epoch-test:1:definition").Val()
		t.Logf("viaApply=%v epoch-1 definition snapshot keys=%d order=%q", viaApply, len(def), def["order"])
		if err := c.HSet(ctx, tb.EpochKey, "n", 2).Err(); err != nil {
			t.Fatal(err)
		}
		_, err := ntable.ReadAt(ctx, c, tb.Name, 1)
		t.Logf("viaApply=%v ReadAt(1) after advancing to 2: %v", viaApply, err)
		if err != nil && viaApply {
			t.Errorf("history of an epoch whose first write was an apply is unreadable: %v", err)
		}
	}
}

// Refusal messages name operation, member/batch, expected/observed, changed=no, next command.
func TestReaderProbeRefusalText(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	rev := probeRev(ctx, c)
	base := func(op string, e ...ntable.BatchMemberEntry) ntable.BatchManifest {
		return ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: op, Actor: "p", Members: e}
	}
	cases := map[string]ntable.BatchManifest{
		"twice":     base("r-twice", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}}, ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}}),
		"memberrev": base("r-mrev", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Revision: "9"}}),
		"exists":    base("r-exists", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "done", Score: 1}}),
		"tablerev": func() ntable.BatchManifest {
			m := base("r-trev", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}})
			m.ExpectedTableRevision = "1"
			return m
		}(),
		"epoch": func() ntable.BatchManifest {
			m := base("r-ep", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}})
			m.Epoch = "3"
			return m
		}(),
		"fieldguard": base("r-fg", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Fields: map[string]ntable.FieldGuard{"role": {Absent: boolPtr(true)}}}}),
		"notmember":  base("r-nm", ntable.BatchMemberEntry{ID: "zz", Expect: &ntable.MemberExpect{}, Move: &ntable.MemberMoveOp{Row: "build", Col: "done"}}),
		"reserved":   base("r-res", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}, Set: map[string]string{"epoch": "5"}}),
		"placeguard": base("r-pg", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: "test", Col: "done"}}}),
	}
	for name, m := range cases {
		_, err := ntable.ApplyBatch(ctx, c, m)
		t.Logf("%-10s %v", name, err)
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		s := err.Error()
		if !strings.Contains(s, "changed=no") || !strings.Contains(s, "run: ") || !strings.Contains(s, m.OperationID) {
			t.Errorf("%s: refusal text misses changed=no / run: / operation id: %s", name, s)
		}
	}
}

// Field-guard count is not bounded: 3000 passing guards are accepted.
func TestReaderProbeUnboundedGuards(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"g","actor":"p","members":[{"id":"b","expect":{"fields":{` + manyGuards(3000) + `}}}]}`
	ans, err := rawApply(ctx, c, raw)
	t.Logf("3000 field guards on one entry: %v %v", trunc(ans), err)
	raw = `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"g2","actor":"p","members":[{"id":"b","expect":{"fields":{"role":{"one_of":[` + strings.TrimSuffix(strings.Repeat(`"z",`, 20000), ",") + `]}}}}]}`
	ans, err = rawApply(ctx, c, raw)
	t.Logf("20000 one_of options: %v %v", trunc(ans), err)
}

// A move score that is not a number, raw FCALL: refused, or silently ignored?
func TestReaderProbeMoveScoreNotANumber(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	for i, v := range []string{`"abc"`, `null`, `true`} {
		raw := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"%s","operation_id":"ms-%d","actor":"p","members":[{"id":"a","expect":{},"move":{"row":"test","col":"done","score":%s}}]}`, probeRev(ctx, c), i, v)
		ans, err := rawApply(ctx, c, raw)
		t.Logf("move score=%s -> %v %v", v, trunc(ans), err)
		if err == nil && len(ans) > 0 && ans[0] == "OK" {
			t.Errorf("move with score %s accepted (score silently kept)", v)
		}
	}
}

// read set: selection over a bound cell, and an empty/odd scope.
func TestReaderProbeReadSetScopes(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	for _, scope := range []string{`{}`, `[]`, `{"selection":[{"row":"build"}]}`, `{"selection":[{"row":"build","col":"ready"}]}`, `{"members":[]}`} {
		rs, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", scope).Slice()
		t.Logf("scope %s -> %v %v", scope, trunc(rs), err)
	}
	_, err := c.FCall(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"members":["a"]}`).Slice()
	t.Logf("read set via FCALL (not RO): %v", err)
}

// Prior hold (minor): a move to the member's current cell at its current score.
func TestReaderProbeMoveToSameCell(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: probeRev(ctx, c), OperationID: "same", Actor: "p",
		Members: []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{Revision: "1"}, Move: &ntable.MemberMoveOp{Row: "build", Col: "ready"}}}}
	r, err := ntable.ApplyBatch(ctx, c, m)
	if err != nil {
		t.Fatal(err)
	}
	d := r.BatchDelta.Members[0]
	t.Logf("same-cell move: outcome=%s changed=%d rev %s->%s table %d->%d", r.Outcome, r.BatchDelta.ChangedCount, d.BeforeRev, d.AfterRev, r.Before, r.After)
	if d.AfterRev != "1" || r.Outcome != "noop" || r.BatchDelta.ChangedCount != 0 {
		t.Errorf("same-cell move is not a member no-op")
	}
}

// Through the Go/CLI path, a create with no score: the server alone refuses
// SCORE; does the Go path silently place it at score 0?
func TestReaderProbeGoCreateWithoutScore(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + probeRev(ctx, c) + `","operation_id":"ns","members":[{"id":"n","expect":{"absent":true},"create":{"row":"build","col":"ready"}}]}`
	m, err := ntable.ValidateBatchManifestRaw([]byte(raw))
	if err != nil {
		t.Logf("validator refused: %v", err)
		return
	}
	r, err := ntable.ApplyBatch(ctx, c, *m)
	score, _ := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "n").Result()
	t.Logf("apply: outcome=%s err=%v score=%v", r.Outcome, err, score)
	if err == nil {
		t.Errorf("create without score accepted through the Go path at score %v", score)
	}
	noSchema := `{"table":"demo","operation_id":"ns2","members":[]}`
	m2, err := ntable.ValidateBatchManifestRaw([]byte(noSchema))
	if err == nil {
		_, err = ntable.ApplyBatch(ctx, c, *m2)
	}
	t.Logf("manifest without schema/epoch/expected_table_revision: %v", err)
}
