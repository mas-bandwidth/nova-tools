package store

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// redisKV is a Redis KV over a throwaway miniredis, so the probe runs under
// NOVA_TEST_NO_HOST=1 and reaches no host: the real client, the real EVAL.
func redisKV(t *testing.T) (*Redis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	names := sprint.Names{Prefix: "t-"}
	return &Redis{C: rdb, Names: names, Now: time.Now}, rdb
}

// A write key that holds a non-string type refuses the whole exchange and
// applies nothing: the roster, the job records and the removals all stay as
// they were. MULTI/EXEC could not do this — a WRONGTYPE on one SET still
// committed the other SETs and DELs.
func TestRedisSetKeysWRONGTYPEAppliesNothing(t *testing.T) {
	t.Parallel()
	r, rdb := redisKV(t)
	ctx := context.Background()

	// The state before the sync: the roster (read back as a list, the wrong
	// type for a SET), a job record to overwrite, and a beat and another
	// friend's jobs to delete.
	require.NoError(t, rdb.Set(ctx, r.Names.Key(friendJobsKey("a")), `[{"id":"j1","state":"ready"}]`, 0).Err())
	require.NoError(t, rdb.Set(ctx, r.Names.Key(friendBeatKey("a")), `{"at":"2026-10-02T00:00:00Z"}`, 0).Err())
	require.NoError(t, rdb.Set(ctx, r.Names.Key(friendJobsKey("b")), `[{"id":"j1","state":"done","ok":true}]`, 0).Err())
	require.NoError(t, rdb.RPush(ctx, r.Names.Key(keyFriends), "not-a-roster").Err())

	err := r.SetKeys(ctx,
		map[string]string{
			keyFriends:         `{"a":{"width":2}}`,
			friendJobsKey("a"): `[{"id":"j1","state":"working"}]`,
		},
		[]string{friendBeatKey("a"), friendJobsKey("b")},
	)
	require.Error(t, err, "a write over the list must refuse the exchange")
	require.Contains(t, err.Error(), "WRONGTYPE")

	// Nothing applied: the roster is still the list, the job record is as it
	// was, and neither delete ran.
	typ, err := rdb.Type(ctx, r.Names.Key(keyFriends)).Result()
	require.NoError(t, err)
	require.Equal(t, "list", typ, "the roster write must not have run over the list")
	got, err := rdb.Get(ctx, r.Names.Key(friendJobsKey("a"))).Result()
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":"j1","state":"ready"}]`, got, "the job write must not have run")
	require.Equal(t, int64(1), rdb.Exists(ctx, r.Names.Key(friendBeatKey("a"))).Val(), "the beat delete must not have run")
	require.Equal(t, int64(1), rdb.Exists(ctx, r.Names.Key(friendJobsKey("b"))).Val(), "the jobs delete must not have run")
}

// The happy path still applies every write and delete in the one exchange.
func TestRedisSetKeysAppliesAllWrites(t *testing.T) {
	t.Parallel()
	r, rdb := redisKV(t)
	ctx := context.Background()

	require.NoError(t, rdb.Set(ctx, r.Names.Key(friendJobsKey("a")), `[{"id":"j1","state":"ready"}]`, 0).Err())
	require.NoError(t, rdb.Set(ctx, r.Names.Key(friendBeatKey("a")), `{"at":"2026-10-02T00:00:00Z"}`, 0).Err())
	require.NoError(t, rdb.Set(ctx, r.Names.Key(friendJobsKey("b")), `[{"id":"j1","state":"done","ok":true}]`, 0).Err())

	require.NoError(t, r.SetKeys(ctx,
		map[string]string{
			keyFriends:         `{"a":{"width":2}}`,
			friendJobsKey("a"): `[{"id":"j1","state":"working"}]`,
		},
		[]string{friendBeatKey("a"), friendJobsKey("b")},
	))

	got, err := rdb.Get(ctx, r.Names.Key(keyFriends)).Result()
	require.NoError(t, err)
	require.JSONEq(t, `{"a":{"width":2}}`, got, "the roster write must land")
	got, err = rdb.Get(ctx, r.Names.Key(friendJobsKey("a"))).Result()
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":"j1","state":"working"}]`, got, "the job write must land")
	require.Equal(t, int64(0), rdb.Exists(ctx, r.Names.Key(friendBeatKey("a"))).Val(), "the beat delete must land")
	require.Equal(t, int64(0), rdb.Exists(ctx, r.Names.Key(friendJobsKey("b"))).Val(), "the jobs delete must land")
}
