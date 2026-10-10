//go:build functional

package ntable_test

// A batch's change events and receipts have the shape an ordinary verb's have
// for the same change, and carry scores as the exact decimal strings the store
// holds.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventShape returns the fields of every change event of the demo table, with the
// fields that name the verb and its arguments blanked.
func eventShape(t *testing.T, c *redis.Client) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, m := range c.XRange(context.Background(), ntable.DefKey("demo")+":changes", "-", "+").Val() {
		e := map[string]string{}
		for f, v := range m.Values {
			e[f] = fmt.Sprint(v)
		}
		for _, f := range []string{"verb", "args", "actor", "batch_delta"} {
			delete(e, f)
		}
		out = append(out, e)
	}
	return out
}

func TestBatchEventsMatchOrdinaryVerbsForTheSameChange(t *testing.T) {
	t.Parallel()
	steps := []struct {
		name  string
		plain func(ctx context.Context, c *redis.Client) error
		batch string
	}{
		{"place", func(ctx context.Context, c *redis.Client) error {
			_, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "q", 1)
			return err
		}, `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`},
		{"move", func(ctx context.Context, c *redis.Client) error {
			_, err := ntable.CellMove(ctx, c, "demo", "build", "ready", "working", "q")
			return err
		}, `{"id":"q","expect":{},"move":{"row":"build","col":"working"}}`},
		{"remove", func(ctx context.Context, c *redis.Client) error {
			_, err := ntable.CellRemove(ctx, c, "demo", "build", "working", "q")
			return err
		}, `{"id":"q","expect":{},"remove":true}`},
	}
	a, ctx := probeTable(t)
	b, _ := probeTable(t)
	for i, s := range steps {
		require.NoError(t, s.plain(ctx, a), "%s", s.name)
		ans, err := rawApply(ctx, b, manifestWith(probeRev(ctx, b), fmt.Sprintf("s%d", i), s.batch))
		require.True(t, replyOpens(ans, err, "OK"), "%s: %v %v", s.name, trunc(ans), err)
	}
	ea, eb := eventShape(t, a), eventShape(t, b)
	require.Len(t, ea, len(eb), "%d ordinary events, %d batch events", len(ea), len(eb))
	for i := range ea {
		for f, v := range ea[i] {
			assert.Equal(t, v, eb[i][f], "event %d field %s: ordinary %q, batch %q", i, f, v, eb[i][f])
		}
		for f := range eb[i] {
			_, ok := ea[i][f]
			assert.True(t, ok, "event %d: field %s only in the batch's event", i, f)
		}
		// the receipt checker reads members as strings to strings
		var members []map[string]string
		err := json.Unmarshal([]byte(eb[i]["members"]), &members)
		assert.NoError(t, err, "event %d members do not decode as string maps: %v", i, err)
	}
}

func TestBatchScoresAreTheExactDecimalStringsTheStoreHolds(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	scores := []string{"0.30000000000000004", "0.3", "123456789012345678", "-2.5e-7", "1e21", "5"}
	var members []string
	inputByID := map[string]string{}
	for i, sc := range scores {
		id := fmt.Sprintf("s%d", i)
		members = append(members, fmt.Sprintf(`{"id":%q,"expect":{"absent":true},"create":{"row":"build","col":"ready","score":%s}}`, id, sc))
		inputByID[id] = sc
	}
	r, err := ntable.ApplyBatch(ctx, c, mustManifest(t, manifestWith(probeRev(ctx, c), "exact", strings.Join(members, ","))))
	require.NoError(t, err)
	require.NotNil(t, r.BatchDelta, "receipt has no batch delta")
	require.Len(t, r.BatchDelta.Members, len(scores), "receipt has %d members, want %d: %+v", len(r.BatchDelta.Members), len(scores), r.BatchDelta)
	set, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"s0", "s1", "s2", "s3", "s4", "s5"})
	require.NoError(t, err)
	// a script sees the reply of the store's own protocol-2 formatting
	resp2 := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Protocol: 2})
	defer resp2.Close()
	seen := map[string]string{}
	events := eventShape(t, c)
	var eventMembers []map[string]string
	require.NoError(t, json.Unmarshal([]byte(events[len(events)-1]["members"]), &eventMembers))
	require.Len(t, eventMembers, len(scores), "event has %d members, want %d", len(eventMembers), len(scores))
	byID := map[string]string{}
	for _, m := range eventMembers {
		byID[m["id"]] = m["score"]
	}
	require.Len(t, byID, len(scores), "event has repeated member IDs: %v", eventMembers)
	seenIDs := map[string]bool{}
	for _, m := range r.BatchDelta.Members {
		input, expected := inputByID[m.ID]
		require.True(t, expected, "unexpected or repeated receipt member %q", m.ID)
		require.False(t, seenIDs[m.ID], "unexpected or repeated receipt member %q", m.ID)
		seenIDs[m.ID] = true
		text, err := resp2.Do(ctx, "ZSCORE", ntable.CellKey("demo", "build", "ready"), m.ID).Text()
		require.NoError(t, err)
		wantFloat, err := strconv.ParseFloat(input, 64)
		require.NoError(t, err)
		gotFloat, err := strconv.ParseFloat(text, 64)
		assert.NoError(t, err, "%s: store score %q does not round-trip input %q", m.ID, text, input)
		assert.Equal(t, math.Float64bits(wantFloat), math.Float64bits(gotFloat), "%s: store score %q does not round-trip input %q", m.ID, text, input)
		require.Equal(t, new(text), m.AfterScoreText, "%s: the receipt and the store disagree on the after score", m.ID)
		assert.Equal(t, text, byID[m.ID], "%s: the event says %q, the store holds %q", m.ID, byID[m.ID], text)
		sm, _ := set.Member(m.ID)
		assert.Equal(t, text, sm.ScoreText, "%s: read set says %q, the store holds %q", m.ID, sm.ScoreText, text)
		other, dup := seen[text]
		assert.False(t, dup, "scores %s and %s both render as %q", input, other, text)
		seen[text] = input
	}
	require.Len(t, seenIDs, len(inputByID), "receipt omitted members: got %v, want %v", seenIDs, inputByID)
}

func mustManifest(t *testing.T, raw string) ntable.BatchManifest {
	t.Helper()
	m, err := ntable.ValidateBatchManifestRaw([]byte(raw))
	require.NoError(t, err)
	return *m
}
