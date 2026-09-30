//go:build functional

package ntable_test

// Operation identity is table, epoch and operation id. A drop removes the
// operation records of every epoch of the table, in the same call; a table
// created again under the name is a new table. A clear (the epoch advance)
// removes none: an operation recorded in an earlier epoch replays with its
// original receipt.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func asReplayed(ans []any) []any {
	got, _ := asReplay(ans)
	return got
}

func TestBatchDropRemovesTheOperationRecordsAndTheNameStartsAgain(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"drop", "drop --definition"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			c, ctx := probeTable(t)
			member := `{"id":"a","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`
			old := manifestWith(probeRev(ctx, c), "op-1", member)
			first, err := rawApply(ctx, c, old)
			require.True(t, replyOpens(first, err, "OK"), "first apply: %v %v", trunc(first), err)
			// within the table's life the replay returns the original receipt
			if again, err := rawApply(ctx, c, old); err != nil || !reflect.DeepEqual(asReplayed(again), first) {
				t.Fatalf("replay: %v %v", trunc(again), err)
			}

			var derr error
			if verb == "drop" {
				_, derr = ntable.Drop(ctx, c, "demo")
			} else {
				_, derr = ntable.DropDefinition(ctx, c, "demo")
			}
			require.NoError(t, derr)
			for _, k := range c.Keys(ctx, "*").Val() {
				if strings.Contains(k, ":op:") || strings.HasSuffix(k, ":ops") {
					t.Errorf("after %s the store holds the operation key %s", verb, k)
				}
			}
			newTable(t, c, demo()).rows("build")

			// the old bytes are a new request of a new table: judged on their merits, never answered from the old receipt
			ans, err := rawApply(ctx, c, old)
			require.NoError(t, err)
			if reflect.DeepEqual(asReplayed(ans), first) {
				t.Fatalf("a request after drop and create returned the old receipt: %v", trunc(ans))
			}
			if _, marked := asReplay(ans); marked {
				t.Errorf("a request after drop and create is marked a replay: %v", trunc(ans))
			}
			assert.True(t, replyOpens(ans, nil, "REFUSED", "REVISION"), "the old bytes against the new table: %v; want a refusal on the table revision", trunc(ans))

			// the same operation id with a request that fits applies freshly
			fresh := manifestWith(probeRev(ctx, c), "op-1", `{"id":"b","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
			ans, err = rawApply(ctx, c, fresh)
			if err != nil || ans[0] != "OK" || reflect.DeepEqual(asReplayed(ans), first) {
				t.Fatalf("a fresh request under a reused operation id: %v %v", trunc(ans), err)
			}
			_, marked := asReplay(ans)
			assert.False(t, marked, "a fresh application is marked a replay")
			if again, err := rawApply(ctx, c, fresh); err != nil || !reflect.DeepEqual(asReplayed(again), ans) {
				t.Errorf("replay of the fresh request: %v %v", trunc(again), err)
			}
		})
	}
}

// A clear, and the epoch advance it belongs to, keep the operation records: an
// operation of an earlier epoch replays with its original epoch and revisions;
// a drop then removes the records of every epoch in one call.
func TestBatchOperationsOfEarlierEpochsReplayAndAllGoWithTheDrop(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := t.Context()
	raw := func(epoch, op string) string {
		rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
		return `{"schema":1,"table":"epoch-test","epoch":"` + epoch + `","expected_table_revision":"` + rev + `","operation_id":"` + op + `","actor":"p","members":[{"id":"g-` + op + `","expect":{"absent":true}}]}`
	}
	call := func(r string) []any {
		ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, r).Slice()
		require.NoError(t, err)
		return ans
	}
	zero := raw("0", "op-e0")
	first := call(zero)
	require.True(t, replyOpens(first, nil, "OK"), "epoch 0: %v", trunc(first))
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil { // the epoch advances
		t.Fatal(err)
	}
	_, err := ntable.Clear(ctx, c, tb.Name, ntable.WriteOptions{Epoch: 1})
	require.NoError(t, err)
	one := raw("1", "op-e1")
	if second := call(one); second[0] != "OK" {
		t.Fatalf("epoch 1: %v", trunc(second))
	}

	// the operation of epoch 0 replays: its epoch and revisions are unchanged
	again := call(zero)
	if got, marked := asReplay(again); !marked || !reflect.DeepEqual(got, first) {
		t.Errorf("replay of an earlier epoch's operation: %v; want the original receipt %v", trunc(again), trunc(first))
	}
	if receipt := first[1].([]any); receipt[2] != "0" {
		t.Errorf("the original receipt names epoch %v, want 0", receipt[2])
	}

	// every epoch's records are one key, and the drop removes it
	n := c.HLen(ctx, ntable.DefKey(tb.Name)+":ops").Val()
	assert.Equal(t, int64(2), n, "the table's operation records: %d, want 2 (epochs 0 and 1)", n)
	_, err = ntable.Drop(ctx, c, tb.Name, ntable.WriteOptions{Epoch: 1})
	require.NoError(t, err)
	for _, k := range c.Keys(ctx, "*").Val() {
		if strings.Contains(k, ":op:") || strings.HasSuffix(k, ":ops") {
			t.Errorf("after drop the store holds the operation key %s", k)
		}
	}
	// the epoch snapshots stay readable
	if !(c.Exists(ctx, "table:"+tb.Name+":1:definition").Val() == 1) && !(c.Exists(ctx, "table:"+tb.Name+":definition").Val() == 1) {
		t.Errorf("a drop removed the epoch snapshots")
	}
}
