//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Named fields change only the application-field projection, never the pinned
// metadata or placement checks (SPEC-NOVA-TABLE, read-set contract).
func TestReadSetProjectedFieldsRetainMetadataAndDefaultFullRecords(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	tb := demo()
	tb.EpochKey = "projection-epoch"
	tb.EpochField = "n"
	newTable(t, c, tb).rows("build")
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	_, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: rev, OperationID: "projected-fields", Members: []ntable.BatchMemberEntry{{ID: "placed", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 4}, Set: map[string]string{"usage": "input=11 output=7 actual_usd=0.125", "brief": strings.Repeat("context", 1000), "empty": ""}}}})
	require.NoError(t, err)
	require.NoError(t, ntable.MemberCreate(ctx, c, "demo", "loose"))
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("loose"), "usage", "input=2").Err())
	ids := []string{"placed", "loose", "missing"}
	full, err := ntable.ReadSetMembers(ctx, c, "demo", ids)
	require.NoError(t, err)
	// Unlike the Go omitempty path, this exercises an explicitly transmitted [].
	ordinary, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"members":["placed","loose","missing"]}`).Slice()
	require.NoError(t, err)
	empty, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", `{"members":["placed","loose","missing"],"fields":[]}`).Slice()
	require.NoError(t, err)
	assert.Equal(t, ordinary, empty)
	// The wire projects in Redis: no full member HGETALL accompanies this read.
	before := projectionHashReads(t, c)
	_, err = ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Members: ids, Fields: []string{"usage"}})
	require.NoError(t, err)
	projectedCalls := projectionHashReads(t, c) - before
	before = projectionHashReads(t, c)
	_, err = ntable.ReadSetMembers(ctx, c, "demo", ids)
	require.NoError(t, err)
	fullCalls := projectionHashReads(t, c) - before
	assert.Equal(t, fullCalls-int64(len(ids)), projectedCalls, "every selected member avoids HGETALL, including missing hashes")
	for _, fields := range [][]string{{"usage", "empty", "absent"}, nil, {}} {
		projected, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Members: ids, Fields: fields})
		require.NoError(t, err)
		assert.Equal(t, full.Epoch, projected.Epoch)
		assert.Equal(t, full.Revision, projected.Revision)
		assert.Equal(t, full.Missing, projected.Missing)
		require.Len(t, projected.Members, 2)
		for i, member := range projected.Members {
			old := full.Members[i]
			oldFields := old.Fields
			if len(fields) > 0 {
				old.Fields = map[string]string{}
				for _, f := range fields {
					if value, ok := oldFields[f]; ok {
						old.Fields[f] = value
					}
				}
			}
			assert.Equal(t, old, member, "metadata and selected values are unchanged")
			if len(fields) > 0 {
				assert.NotContains(t, member.Fields, "brief")
			}
		}
	}
	selected, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Selection: []ntable.CellSelection{{Row: "build", Col: "ready"}}, Fields: []string{"usage"}})
	require.NoError(t, err)
	require.Len(t, selected.Members, 1)
	assert.Equal(t, "placed", selected.Members[0].ID)
	// Materialise the next epoch; the same projected read still verifies epoch zero.
	require.NoError(t, c.HSet(ctx, "projection-epoch", "n", "1").Err())
	_, err = ntable.RowAdd(ctx, c, "demo", "current", ntable.RowSpec{}, ntable.WriteOptions{Epoch: 1})
	require.NoError(t, err)
	old, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Members: ids, Fields: []string{"usage"}}, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 0, old.Epoch)
	require.Len(t, old.Members, 2)
	assert.Equal(t, full.Members[0].Revision, old.Members[0].Revision)
}

// Direct wire callers receive the same refusal as ordinary full reads for drift,
// wrong-type hashes and epoch mismatches; malformed projection scopes write nothing.
func TestReadSetProjectedFieldsRefuseMalformedRequestsAndDrift(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"epoch", "duplicate placement", "wrong type", "missing record in cell"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			c, _ := store(t)
			ctx := context.Background()
			newTable(t, c, demo()).rows("build")
			require.NoError(t, ntable.MemberCreate(ctx, c, "demo", "member"))
			_, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "member", 1)
			require.NoError(t, err)
			switch fault {
			case "epoch":
				require.NoError(t, c.HSet(ctx, ntable.MemberKey("member"), "epoch", "1").Err())
			case "duplicate placement":
				require.NoError(t, c.ZAdd(ctx, "table:demo:cell:build:working", redis.Z{Score: 1, Member: "member"}).Err())
			case "wrong type":
				require.NoError(t, c.Del(ctx, ntable.MemberKey("member")).Err())
				require.NoError(t, c.Set(ctx, ntable.MemberKey("member"), "invalid", 0).Err())
			case "missing record in cell":
				require.NoError(t, c.Del(ctx, ntable.MemberKey("member")).Err())
			}
			before := storeImage(t, c)
			_, fullErr := ntable.ReadSetMembers(ctx, c, "demo", []string{"member"})
			_, projectedErr := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Members: []string{"member"}, Fields: []string{"usage"}})
			require.Error(t, fullErr)
			require.Error(t, projectedErr)
			var fullRef, projectedRef *ntable.Refusal
			require.ErrorAs(t, fullErr, &fullRef)
			require.ErrorAs(t, projectedErr, &projectedRef)
			assert.Equal(t, fullRef.Code, projectedRef.Code)
			assert.Equal(t, before, storeImage(t, c))
		})
	}
	c, _ := store(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("build")
	before := storeImage(t, c)
	names := make([]string, ntable.LimitReadSetFields+1)
	for i := range names {
		names[i] = fmt.Sprintf("field-%d", i)
	}
	fieldBody, err := json.Marshal(names)
	require.NoError(t, err)
	overReply, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", fmt.Sprintf(`{"members":["member"],"fields":%s}`, fieldBody)).Slice()
	require.True(t, replyOpens(overReply, err, "REFUSED", "LIMIT", "read set fields"))
	require.Len(t, overReply, 6)
	assert.EqualValues(t, 128, overReply[3])
	assert.EqualValues(t, 129, overReply[4])
	assert.Equal(t, before, storeImage(t, c))
	reply, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", fmt.Sprintf(`{"members":["member"],"fields":["%s"]}`, strings.Repeat("f", ntable.LimitReadSetFieldNameBytes+1))).Slice()
	assert.True(t, replyOpens(reply, err, "REFUSED", "LIMIT"))
	assert.Equal(t, before, storeImage(t, c))
	for _, fields := range []string{string([]byte{'[', '"', 255, '"', ']'}), `{}`, `"usage"`, `[""]`, `["usage","usage"]`, `["epoch"]`, `["revision"]`, `["place:demo"]`, `["bad\nname"]`, `[null]`} {
		before := storeImage(t, c)
		reply, err := c.FCallRO(ctx, ntable.FnReadSet, []string{ntable.DefKey("demo")}, "demo", fmt.Sprintf(`{"members":["member"],"fields":%s}`, fields)).Slice()
		assert.True(t, replyOpens(reply, err, "REFUSED", "ARGS"), "fields=%s reply=%v error=%v", fields, reply, err)
		assert.Equal(t, before, storeImage(t, c))
	}
}

// The isolated Redis counter distinguishes true wire projection from client filtering.
func projectionHashReads(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	info, err := c.Info(context.Background(), "commandstats").Result()
	require.NoError(t, err)
	for _, line := range strings.Split(info, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "cmdstat_hgetall:"); ok {
			for _, kv := range strings.Split(rest, ",") {
				if value, ok := strings.CutPrefix(kv, "calls="); ok {
					n, err := strconv.ParseInt(value, 10, 64)
					require.NoError(t, err)
					return n
				}
			}
		}
	}
	return 0
}
