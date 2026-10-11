//go:build functional

package ntable_test

// A caller tells a refusal from a transport failure from a manifest it cannot
// read with errors.Is and IsRefusal; a refusal has a code and a sentence; the
// texts say what to do next and never echo a long value.

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchRefusalHasACodeAndASentence(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	rev := probeRev(ctx, c)
	g := &ntable.MemberExpect{}
	cases := []struct {
		name string
		m    ntable.BatchManifest
		code string
		is   error
	}{
		{"not a member", refusalFixtureBatch(rev, "k1", ntable.BatchMemberEntry{ID: "zz", Expect: g}), "NOTMEMBER", ntable.ErrNotMember},
		{"duplicate (store)", refusalFixtureBatch(rev, "k2", ntable.BatchMemberEntry{ID: "a", Expect: g}, ntable.BatchMemberEntry{ID: "a", Expect: g}), "TWICE", ntable.ErrDuplicateMember},
		{"member revision", refusalFixtureBatch(rev, "k3", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Revision: "9"}}), "MEMBERREVISION", ntable.ErrMemberRevision},
		{"reserved (rule)", refusalFixtureBatch(rev, "k4", ntable.BatchMemberEntry{ID: "a", Expect: g, Set: map[string]string{"epoch": "1"}}), "RESERVEDFIELD", ntable.ErrReservedField},
		{"create and move (rule)", refusalFixtureBatch(rev, "k5", ntable.BatchMemberEntry{ID: "n", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}, Move: &ntable.MemberMoveOp{Row: "build", Col: "done"}}), "MUTATION", ntable.ErrMutation},
		{"empty (rule)", refusalFixtureBatch(rev, "k6"), "MANIFEST", ntable.ErrMalformedManifest},
		{"over a bound (rule)", refusalFixtureBatch(rev, "k7", ntable.BatchMemberEntry{ID: "a", Expect: g, Unset: manyNames(ntable.LimitUnsetFields + 1)}), "LIMIT", ntable.ErrLimit},
	}
	for _, tc := range cases {
		_, err := ntable.ApplyBatch(ctx, c, tc.m)
		var r *ntable.Refusal
		if !assert.Error(t, err, "%s: not a refusal", tc.name) || !assert.True(t, ntable.IsRefusal(err), "%s: %v is not a refusal", tc.name, err) || !assert.ErrorAs(t, err, &r, "%s: %v is not a refusal", tc.name, err) {
			continue
		}
		assert.Equal(t, tc.code, r.Code, "%s: %v; want %s wrapping %v", tc.name, err, tc.code, tc.is)
		assert.ErrorIs(t, err, tc.is, "%s: code %q, %v; want %s wrapping %v", tc.name, r.Code, err, tc.code, tc.is)
		assert.NotErrorIs(t, err, ntable.ErrUnknownOutcome, "%s: code %q, %v; want %s wrapping %v", tc.name, r.Code, err, tc.code, tc.is)
		assert.ErrorContains(t, err, "; code="+tc.code+"; changed=no; run: nova-table", "%s: the text has no code field: %v", tc.name, err)
		for _, leak := range []string{"NOTMEMBER:", "(TWICE)", "WRONGTYPE:"} {
			assert.NotContains(t, err.Error(), leak, "%s: a tag leaks into the sentence: %v", tc.name, err)
		}
	}
}

func TestBatchTransportFailureSaysWhatToDoAndIsNotARefusal(t *testing.T) {
	t.Parallel()
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
	defer dead.Close()
	m := refusalFixtureBatch("2", "lost-1", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}})
	_, err := ntable.ApplyBatch(t.Context(), dead, m)
	require.ErrorIs(t, err, ntable.ErrUnknownOutcome, "a transport failure: %v", err)
	require.False(t, ntable.IsRefusal(err), "a transport failure: %v", err)
	require.NotErrorIs(t, err, ntable.ErrMalformedManifest, "a transport failure: %v", err)
	for _, w := range []string{"changed=unknown", `same operation id "lost-1"`, "returns the original receipt if the batch was applied and applies it if it was not"} {
		assert.ErrorContains(t, err, w, "the message lacks %q: %v", w, err)
	}
	// a manifest that cannot be read is neither
	_, err = ntable.ApplyBatch(t.Context(), dead, ntable.BatchManifest{Table: "bad name", OperationID: "x"})
	assert.ErrorIs(t, err, ntable.ErrMalformedManifest, "an invalid table name: %v", err)
	assert.False(t, ntable.IsRefusal(err), "an invalid table name: %v", err)
	assert.NotErrorIs(t, err, ntable.ErrUnknownOutcome, "an invalid table name: %v", err)
}

func TestBatchOperationConflictAndLimitsSayHowToProceed(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	m := refusalFixtureBatch(probeRev(ctx, c), "seed", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}})
	_, err := ntable.ApplyBatch(ctx, c, m)
	assert.ErrorIs(t, err, ntable.ErrOpConflict, "an operation id that holds another request: %v", err)
	assert.ErrorContains(t, err, "use a new operation id", "an operation id that holds another request: %v", err)
	over := refusalFixtureBatch(probeRev(ctx, c), "narrow", ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{}, Set: manySet(ntable.LimitSetFields + 1)})
	_, err = ntable.ApplyBatch(ctx, c, over)
	assert.ErrorIs(t, err, ntable.ErrLimit, "a set over its bound: %v", err)
	assert.ErrorContains(t, err, "change at most 128 fields of a member in one manifest", "a set over its bound: %v", err)
	// the store's own LIMIT names the same way out
	raw := manifestWith(probeRev(ctx, c), "narrow2", `{"id":"a","expect":{},"set":{`+strings.TrimSuffix(strings.Repeat(`"f0":"v",`, 1), ",")+`}}`)
	_, err = ntable.ValidateBatchManifestRaw([]byte(raw))
	require.NoError(t, err)
}

func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "n" + strings.Repeat("u", i%9) + string(rune('a'+i%26)) + strings.Repeat("v", i/26)
	}
	return out
}

func manySet(n int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[strings.Repeat("f", 1)+string(rune('a'+i%26))+strings.Repeat("g", i/26)] = "v"
	}
	return m
}

func TestBatchRefusalsNameTheMemberOfAWrongTypeAndTheStateOfADrift(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	require.NoError(t, c.Set(ctx, ntable.MemberKey("junk"), "s", 0).Err())
	require.NoError(t, c.ZAdd(ctx, ntable.CellKey("demo", "test", "done"), redis.Z{Score: 1, Member: "ghost"}).Err())
	g := &ntable.MemberExpect{}
	_, err := ntable.ApplyBatch(ctx, c, refusalFixtureBatch(probeRev(ctx, c), "w1", ntable.BatchMemberEntry{ID: "junk", Expect: g}))
	assert.ErrorIs(t, err, ntable.ErrWrongType, "a wrong-type member: %v", err)
	assert.ErrorContains(t, err, `member "junk"`, "a wrong-type member: %v", err)
	assert.ErrorContains(t, err, "key table::member:junk is string, expected hash", "a wrong-type member: %v", err)
	_, err = ntable.ReadSetMembers(ctx, c, "demo", []string{"junk"})
	assert.ErrorIs(t, err, ntable.ErrWrongType, "read set of a wrong-type member: %v", err)
	assert.ErrorContains(t, err, `member "junk"`, "read set of a wrong-type member: %v", err)
	// a placement in an owned set that no record holds: there is no record to quote
	_, err = ntable.ApplyBatch(ctx, c, refusalFixtureBatch(probeRev(ctx, c), "d1", ntable.BatchMemberEntry{ID: "ghost", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 1}}))
	require.ErrorIs(t, err, ntable.ErrDrift, "a hidden placement")
	for _, w := range []string{`member "ghost"`, `owned set at row "test" column "done" holds member "ghost", and no record places it there`, "nova-table has no repair verb", "; run: nova-table cell members 'demo' 'test' 'done'"} {
		assert.ErrorContains(t, err, w, "the drift refusal lacks %q: %v", w, err)
	}
	assert.NotContains(t, err.Error(), "the record says", "the drift refusal quotes a record that does not exist: %v", err)
	// a record that names a place the set does not hold
	require.NoError(t, c.ZRem(ctx, ntable.CellKey("demo", "build", "ready"), "a").Err())
	_, err = ntable.ApplyBatch(ctx, c, refusalFixtureBatch(probeRev(ctx, c), "d2", ntable.BatchMemberEntry{ID: "a", Expect: g}))
	assert.ErrorIs(t, err, ntable.ErrDrift, "a record with no set entry: %v", err)
	assert.ErrorContains(t, err, `the record places member "a" at row "build" column "ready", and the owned set there does not hold it`, "a record with no set entry: %v", err)
}

func TestBatchRefusalsDoNotEchoALongValue(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	long := strings.Repeat("v", 50000)
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("a"), "big", long).Err())
	guard := func(g ntable.FieldGuard) ntable.BatchMemberEntry {
		return ntable.BatchMemberEntry{ID: "a", Expect: &ntable.MemberExpect{Fields: map[string]ntable.FieldGuard{"big": g}}}
	}
	eq := "x"
	absent := true
	for name, e := range map[string]ntable.BatchMemberEntry{
		"equals":   guard(ntable.FieldGuard{Equals: &eq}),
		"absent":   guard(ntable.FieldGuard{Absent: &absent}),
		"one_of":   guard(ntable.FieldGuard{OneOf: []string{"p", strings.Repeat("q", 4000)}}),
		"named":    {ID: "a", Expect: &ntable.MemberExpect{Fields: map[string]ntable.FieldGuard{strings.Repeat("n", 3000): {Equals: &eq}}}},
		"reserved": {ID: "a", Expect: &ntable.MemberExpect{}, Set: map[string]string{"place:" + strings.Repeat("r", 3000): "v"}},
	} {
		_, err := ntable.ApplyBatch(ctx, c, refusalFixtureBatch(probeRev(ctx, c), "long-"+name, e))
		if !assert.True(t, ntable.IsRefusal(err), "%s: %v", name, err) {
			continue
		}
		assert.LessOrEqual(t, len(err.Error()), 700, "%s: the refusal is %d bytes: it echoes a long value", name, len(err.Error()))
		if name == "equals" || name == "absent" {
			assert.ErrorContains(t, err, "(50000 bytes)", "%s: the refusal gives no length for the value: %.300s", name, err)
		}
	}
	require.Equal(t, storeImage(t, c), storeImage(t, c), "unstable image")
}

func TestBatchEpochWordingIsRequestedAndActiveEverywhere(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := t.Context()
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 2).Err())
	rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
	stale := ntable.BatchManifest{Schema: 1, Table: tb.Name, Epoch: "1", ExpectedTableRevision: rev, OperationID: "old-epoch", Members: []ntable.BatchMemberEntry{{ID: "a", Expect: &ntable.MemberExpect{}}}}
	_, err := ntable.ApplyBatch(ctx, c, stale)
	ahead := stale
	ahead.Epoch, ahead.OperationID = "5", "future"
	_, err2 := ntable.ApplyBatch(ctx, c, ahead)
	_, err3 := ntable.CellAdd(ctx, c, tb.Name, "x", "y", "m", 1, ntable.WriteOptions{Epoch: 1})
	for name, e := range map[string]error{"stale batch": err, "ahead batch": err2, "stale ordinary": err3} {
		require.Error(t, e, "%s: want requested and active, never observed", name)
		assert.NotContains(t, e.Error(), "observed", "%s: %v; want requested and active, never observed", name, e)
		assert.Contains(t, e.Error(), "requested", "%s: %v; want requested and active, never observed", name, e)
		assert.Contains(t, e.Error(), "active", "%s: %v; want requested and active, never observed", name, e)
	}
	assert.ErrorIs(t, err, ntable.ErrStale, "sentinels: %v %v", err, err2)
	assert.ErrorIs(t, err2, ntable.ErrEpochAhead, "sentinels: %v %v", err, err2)
}
