//go:build functional

package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
)

// saveRDB has the server write its RDB (SAVE, in the foreground) and returns
// the file's bytes, read from where CONFIG GET says the server wrote it.
func saveRDB(t *testing.T, c *redis.Client) []byte {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, c.Save(ctx).Err())
	dir, err := c.ConfigGet(ctx, "dir").Result()
	require.NoError(t, err)
	name, err := c.ConfigGet(ctx, "dbfilename").Result()
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(dir["dir"], name["dbfilename"]))
	require.NoError(t, err)
	return b
}

// isolatedRestore loads the RDB into a Redis of the test's own (its own
// directory, port and process, which nothing else writes to), loads this
// build's function library into it, and reads the sprint it holds under names.
func isolatedRestore(t *testing.T, rdb []byte, names sprint.Names) (SprintState, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "restore.rdb"), rdb, 0o600))
	addr := testutil.Start(t, "--dir", dir, "--dbfilename", "restore.rdb")
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() }) // ignored: a test client closed at the end
	n, err := c.DBSize(ctx).Result()
	require.NoError(t, err)
	require.Positive(t, n, "the isolated Redis loaded the dump at its start")
	require.NoError(t, fn.Load(ctx, c), "this build's function library loads over the dump's")
	got, err := ReadState(ctx, &Redis{C: c, Names: names, Now: time.Now}, names)
	require.NoError(t, err)
	return got, c
}

// isolatedTwin is the semantic twin of an RDB on the bench: the integrity check,
// then the load into an isolated Redis (isolatedRestore).
type isolatedTwin struct {
	t     *testing.T
	names sprint.Names
}

func (x isolatedTwin) Load(rdb []byte) (SnapshotCounts, error) { return RDBTwin{}.Load(rdb) }

func (x isolatedTwin) LoadState(_ context.Context, rdb []byte) (SprintState, error) {
	s, _ := isolatedRestore(x.t, rdb, x.names)
	return s, nil
}

// THE SEMANTIC RESTORE OF A REDIS DUMP, on the bench's redis-server. A sprint
// with heads, attempts, costs, a read, an open judgment and callers' recorded
// results is written on a Redis of the test's own; its RDB is saved and passes
// the integrity check (RDBTwin); it is loaded into an isolated Redis with this
// build's function library, and the sprint state read there is the source's,
// part for part. Then a dump that lost a primary's head and the callers'
// recorded results, saved by Redis itself so its CRC-64 is its own, passes the
// integrity check and fails the semantic one, naming those parts.
func TestARedisDumpRestoresTheSprintOnAnIsolatedRedis(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, c := liveStore(t)
	h := &harness{t: t, st: st, ctx: ctx, now: time.Now(), live: []string{"m1", "m2"}}
	fillRestoreSprint(h)
	want, err := ReadState(ctx, st.B, st.Names)
	require.NoError(t, err)
	for _, part := range []string{"epoch", "record work s1-1 field head", "record work s1-1 field attempt", "done finish-s1-1", "fence"} {
		require.Contains(t, want.Parts, part, "the source's sprint holds %s", part)
	}
	rdb := saveRDB(t, c)
	_, err = RDBTwin{}.Load(rdb)
	require.NoError(t, err, "the dump passes the integrity check")

	got, restored := isolatedRestore(t, rdb, st.Names)
	require.Empty(t, want.Diff(got), "the restored sprint differs from the source's in: %v", want.Diff(got))
	require.NoError(t, SemanticRestore(ctx, want, isolatedTwin{t, st.Names}, rdb))

	// a dump that lost the primary's head and the callers' results: taken out of
	// the restored copy, which Redis saves again with a checksum of its own
	es, err := st.B.Epoch(ctx)
	require.NoError(t, err)
	at := (&Redis{C: restored, Names: st.Names}).AtEpoch(es.N, false).(*Redis)
	require.NoError(t, restored.Del(ctx, at.key(keyDone)).Err())
	keys, err := restored.Keys(ctx, "*s1-1*").Result()
	require.NoError(t, err)
	dropped := ""
	for _, k := range keys {
		if typ, _ := restored.Type(ctx, k).Result(); typ == "hash" && strings.HasPrefix(k, st.Names.MemberPrefix(sprint.Work)) {
			if ok, _ := restored.HExists(ctx, k, "head").Result(); ok {
				require.NoError(t, restored.HDel(ctx, k, "head").Err())
				dropped = k
			}
		}
	}
	require.NotEmpty(t, dropped, "the primary's work record holds its head (keys %v)", keys)
	incomplete := saveRDB(t, restored)
	_, err = RDBTwin{}.Load(incomplete)
	require.NoError(t, err, "the incomplete dump's checksum is its own")
	lost, _ := isolatedRestore(t, incomplete, st.Names)
	d := want.Diff(lost)
	require.Contains(t, d, "record work s1-1 field head", "the restore names the lost head: %v", d)
	require.Contains(t, d, "done finish-s1-1", "the restore names the lost result: %v", d)
	require.ErrorContains(t, SemanticRestore(ctx, want, isolatedTwin{t, st.Names}, incomplete), "the restored sprint is not the store's")
}
