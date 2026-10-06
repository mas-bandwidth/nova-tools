//go:build functional

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil/pg"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// restoreBudget is the restore time docs/DATA.md states for configuration,
// messages, receipts and runtime state.
const restoreBudget = 30 * time.Minute

// TestRestoreDrillOntoAFreshHost is docs/DATA.md's acceptance: the fleet's
// configuration (PostgreSQL) and its bus store (Redis) are backed up the way
// DATA.md says, the old host is lost (its Redis shut down unsaved, its
// database dropped), and both are restored onto a fresh PostgreSQL cluster
// and an empty Redis directory, bound to loopback, in DATA.md's restore order;
// then apply runs. The retained messages, the receipts and the pending
// deliveries are back as they were at the snapshot, the configuration is back
// as it was at the dump, apply has replaced the snapshot's older copy of it,
// a message sent after the snapshot is gone (the acceptable loss), and the
// whole restore ran inside the budget.
func TestRestoreDrillOntoAFreshHost(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), restoreBudget)
	defer cancel()

	// --- the old host: configuration applied, the bus in use -------------
	old := newReal(t, true)
	old.run(t, 0, "migrate")
	old.run(t, 0, "machine", "add", "m1", "--user", "nova", "--seat", "s1", "--slots", "16")
	old.run(t, 0, "friend", "add", "ada", "--slots", "4", "--tiers", "flash")
	old.run(t, 0, "friend", "add", "bob", "--slots", "4", "--tiers", "flash")
	old.run(t, 0, "fleet", "set", "--store", "m1", "--coordinator", "m1", "--redis_port", "6380", "--pg_dsn", "postgres://nova_config@127.0.0.1:5432/nova")
	old.run(t, 0, "apply")
	b := &bus.Bus{Store: bus.Redis{C: old.client}}
	var toBob []bus.Message
	for i := range 5 {
		m, err := b.Send(ctx, bus.Message{From: "ada", To: []string{"bob"}, CC: []string{"m1"}, Subject: fmt.Sprintf("to bob %d", i), Body: "body\n"})
		require.NoError(t, err)
		toBob = append(toBob, m)
	}
	// bob is handed three: one acked, two pending (delivered, not acked);
	// two are never delivered.
	for i := range 3 {
		e, ok, err := b.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok, "recv %d", i)
		if i == 0 {
			acked, err := b.AckEntry(ctx, "bob", e.Entry)
			require.NoError(t, err)
			require.True(t, acked)
		}
	}
	// bob's reply to the first is his receipt of it; four stay owed to him.
	_, err := b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Subject: "re", Body: "read\n", Re: toBob[0].ID})
	require.NoError(t, err)
	before := busFactsOf(ctx, t, old.client)
	require.EqualValues(t, 6, before.LogLen)
	require.Len(t, before.Pending["bob"], 2, "two delivered and not acked")
	require.Len(t, before.Fresh["bob"], 2, "two never delivered")
	require.Len(t, before.Owed["bob"], 4, "four owed bob's receipt")
	require.Len(t, before.Owed["ada"], 1, "bob's reply is owed ada's receipt")

	// --- the backups, as DATA.md schedules them --------------------------
	backups := t.TempDir()
	snap := &store.Snapshotter{Dir: filepath.Join(backups, "redis-bus"), Keep: 3, Source: saved{old.client}, Twin: store.RDBTwin{}, Now: time.Now}
	taken, err := snap.Take(ctx)
	require.NoError(t, err)
	// After the snapshot: a configuration change (its dump is newer than the
	// snapshot's copy of the configuration, so apply must replace that copy)
	// and a message (sent in the window the snapshot does not cover).
	old.run(t, 0, "friend", "add", "cy", "--slots", "2", "--tiers", "flash")
	lost, err := b.Send(ctx, bus.Message{From: "ada", To: []string{"bob"}, Subject: "after the snapshot", Body: "lost\n"})
	require.NoError(t, err)
	out, _ := old.run(t, 0, "backup", "--dir", filepath.Join(backups, "postgres"), "--keep", "3")
	require.True(t, strings.HasPrefix(out, "CONFIG BACKUP pg="), "backup: %q", out)
	dumps, err := backupFiles(filepath.Join(backups, "postgres"))
	require.NoError(t, err)
	require.Len(t, dumps, 1)
	dump := filepath.Join(backups, "postgres", dumps[0])
	friendsBefore, _ := old.run(t, 0, "friend", "list")
	fleetBefore, _ := old.run(t, 0, "fleet", "show")

	// --- the old host is lost ---------------------------------------------
	_ = old.client.ShutdownNoSave(ctx).Err() // ignored: the server hangs up as it exits, which is the error
	dropDatabase(ctx, t, old.env["NOVA_PG_DSN"])

	// The trap DATA.md's restore order steps around: a store started with
	// its AOF on, in a directory holding only the snapshot's dump.rdb, loads
	// nothing (no AOF is there to load) and would write an empty AOF over it.
	trap := t.TempDir()
	body, err := os.ReadFile(taken.File)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(trap, "dump.rdb"), body, 0o600))
	trapped := redis.NewClient(&redis.Options{Addr: testutil.Start(t, "--dir", trap, "--dbfilename", "dump.rdb", "--appendonly", "yes")})
	t.Cleanup(func() { _ = trapped.Close() })
	n, err := trapped.DBSize(ctx).Result()
	require.NoError(t, err)
	require.Zero(t, n, "a store started AOF-on beside only an RDB loaded it; DATA.md's two-step load is no longer needed")

	// --- the fresh host, in DATA.md's restore order -----------------------
	start := time.Now()
	// 3. PostgreSQL: an empty database, the newest dump, migrate, status.
	fresh := pg.Start(t)
	createDatabase(ctx, t, fresh, "nova")
	freshDSN := fresh.DSN("nova")
	env, err := pgEnv(freshDSN)
	require.NoError(t, err)
	msg, err := runPGTool(ctx, env, "pg_restore", "--no-owner", "--no-password", "--exit-on-error", "--dbname", "nova", dump)
	require.NoError(t, err, "pg_restore: %s", msg)
	// 4. Redis: the snapshot's sum checked, placed as dump.rdb in an empty
	// --dir, the server started there with its AOF on, the functions loaded.
	_, sum, err := store.RestoreDrill(taken.File, store.RDBTwin{})
	require.NoError(t, err)
	require.Equal(t, taken.SHA256, sum)
	rdb, err := os.ReadFile(taken.File)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dump.rdb"), rdb, 0o600))
	// Loaded with the AOF off; the AOF turned on in the running server, which
	// rewrites it from memory; then the server the store runs as (AOF on)
	// started on that directory, and it loads the AOF.
	loader := redis.NewClient(&redis.Options{Addr: testutil.Start(t, "--dir", dir, "--dbfilename", "dump.rdb", "--appendonly", "no")})
	t.Cleanup(func() { _ = loader.Close() })
	require.NoError(t, loader.ConfigSet(ctx, "appendonly", "yes").Err())
	waitAOF(ctx, t, loader)
	loaded, err := loader.DBSize(ctx).Result()
	require.NoError(t, err)
	_ = loader.Shutdown(ctx).Err() // ignored: the server hangs up as it exits, which is the error
	addr := testutil.Start(t, "--dir", dir, "--dbfilename", "dump.rdb", "--appendonly", "yes", "--appendfsync", "everysec")
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	served, err := client.DBSize(ctx).Result()
	require.NoError(t, err)
	require.Equal(t, loaded, served, "the store, AOF on, loads what the RDB held")
	require.Positive(t, served)
	require.NoError(t, fn.Load(ctx, client))
	restored := &real{env: map[string]string{"NOVA_PG_DSN": freshDSN, "NOVA_FRIEND": "rowan", "NOVA_SPRINT_REDIS": addr}, client: client}
	out, _ = restored.run(t, 0, "migrate")
	require.Contains(t, out, " applied=0\n", "the dump is at this binary's schema: %q", out)
	// status says the Redis copy is behind PostgreSQL until apply runs.
	_, errs := restored.run(t, 1, "status")
	require.Contains(t, errs, "run: nova-config apply", "status before apply: %q", errs)
	// Before apply the store holds the snapshot's copy: cy is not on it.
	nb := &bus.Bus{Store: bus.Redis{C: client}}
	names, err := nb.Names(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ada", "bob", "m1"}, names, "the snapshot's copy of the roster")
	// 5. Apply config: PostgreSQL over the snapshot's older copy.
	restored.run(t, 0, "apply")
	// 6. Check.
	after := busFactsOf(ctx, t, client)
	elapsed := time.Since(start)

	assert.Equal(t, before, after, "the bus store is back as it was at the snapshot")
	names, err = nb.Names(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ada", "bob", "cy", "m1"}, names, "apply replaced the snapshot's roster with PostgreSQL's")
	friendsAfter, _ := restored.run(t, 0, "friend", "list")
	assert.Equal(t, friendsBefore, friendsAfter, "the configuration is back as it was at the dump")
	fleetAfter, _ := restored.run(t, 0, "fleet", "show")
	assert.Equal(t, fleetBefore, fleetAfter)
	log, err := nb.Log(ctx, "-")
	require.NoError(t, err)
	for _, e := range log {
		assert.NotEqual(t, lost.ID, e.Message().ID, "the message sent after the snapshot is in the window DATA.md accepts losing")
	}
	// The deliveries are deliverable again: bob is handed the two never
	// delivered, in order (the two he holds stay pending until he acks them
	// or the claim wait passes, as on the old host).
	for i := range 2 {
		e, ok, err := nb.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok, "recv %d after the restore", i)
		assert.Equal(t, before.Fresh["bob"][i], e.Entry, "the undelivered come in order")
	}
	backlog, err := nb.Undelivered(ctx, "ada", "bob")
	require.NoError(t, err)
	assert.Equal(t, 1, backlog[0].Count)
	assert.Equal(t, 4, backlog[1].Count)
	_, err = os.Stat(filepath.Join(dir, "appendonlydir"))
	assert.NoError(t, err, "the restored store writes its AOF from the RDB it loaded")

	assert.Less(t, elapsed, restoreBudget)
	t.Logf("RESTORE DRILL OK elapsed=%s budget=%s log=%d newest=%s pending_bob=%d undelivered_bob=%d owed_bob=%d owed_ada=%d dump=%s snapshot=%s lost_after_snapshot=1",
		elapsed.Round(time.Millisecond), restoreBudget, after.LogLen, after.LogLast, len(after.Pending["bob"]), len(after.Fresh["bob"]),
		len(after.Owed["bob"]), len(after.Owed["ada"]), filepath.Base(dump), filepath.Base(taken.File))
}

// waitAOF waits for the AOF that CONFIG SET appendonly yes starts to be
// written whole.
func waitAOF(ctx context.Context, t *testing.T, c *redis.Client) {
	t.Helper()
	for {
		info, err := c.Info(ctx, "persistence").Result()
		require.NoError(t, err)
		if strings.Contains(info, "aof_enabled:1") && strings.Contains(info, "aof_rewrite_in_progress:0") &&
			!strings.Contains(info, "aof_rewrite_scheduled:1") && strings.Contains(info, "aof_last_bgrewrite_status:ok") {
			return
		}
		require.NoError(t, ctx.Err(), "the AOF rewrite did not finish")
		time.Sleep(50 * time.Millisecond)
	}
}

// busFacts is what DATA.md's check compares: the log (the retained
// messages), each stream's length and its group's last delivered entry,
// each group's pending list, what was never delivered, and the receipts
// owed.
type busFacts struct {
	LogLen  int64
	LogLast string
	Streams map[string]int64
	Groups  map[string]string
	Pending map[string][]string
	Fresh   map[string][]string
	Owed    map[string]map[string]string
}

func busFactsOf(ctx context.Context, t *testing.T, c *redis.Client) busFacts {
	t.Helper()
	f := busFacts{Streams: map[string]int64{}, Groups: map[string]string{}, Pending: map[string][]string{}, Fresh: map[string][]string{}, Owed: map[string]map[string]string{}}
	var err error
	f.LogLen, err = c.XLen(ctx, bus.LogKey).Result()
	require.NoError(t, err)
	last, err := c.XRevRangeN(ctx, bus.LogKey, "+", "-", 1).Result()
	require.NoError(t, err)
	if len(last) == 1 {
		f.LogLast = last[0].ID
	}
	b := &bus.Bus{Store: bus.Redis{C: c}}
	for _, n := range []string{"ada", "bob", "m1"} {
		s := bus.StreamOf(n)
		f.Streams[n], err = c.XLen(ctx, s).Result()
		require.NoError(t, err)
		if groups, err := c.XInfoGroups(ctx, s).Result(); err == nil {
			for _, g := range groups {
				f.Groups[n+"/"+g.Name] = g.LastDeliveredID
			}
		}
		pending, fresh, err := b.Peek(ctx, n)
		require.NoError(t, err)
		for _, e := range pending {
			f.Pending[n] = append(f.Pending[n], e.Entry)
		}
		for _, e := range fresh {
			f.Fresh[n] = append(f.Fresh[n], e.Entry)
		}
		sort.Strings(f.Pending[n])
		owed, err := c.HGetAll(ctx, bus.OwedOf(n)).Result()
		require.NoError(t, err)
		if len(owed) > 0 {
			f.Owed[n] = owed
		}
	}
	return f
}

// saved is the bus store as a snapshot source: it saves (SAVE, which
// answers when the RDB is written, where nova-sprint snapshot asks for a
// BGSAVE and waits on LASTSAVE) and returns the RDB the server wrote, read on
// its own host, with the keys it held.
type saved struct{ c *redis.Client }

func (s saved) Save(ctx context.Context) ([]byte, store.SnapshotCounts, error) {
	none := store.SnapshotCounts{Keys: -1, Cards: -1}
	if err := s.c.Save(ctx).Err(); err != nil {
		return nil, none, err
	}
	dir, err := s.c.ConfigGet(ctx, "dir").Result()
	if err != nil {
		return nil, none, err
	}
	name, err := s.c.ConfigGet(ctx, "dbfilename").Result()
	if err != nil {
		return nil, none, err
	}
	keys, err := s.c.DBSize(ctx).Result()
	if err != nil {
		return nil, none, err
	}
	body, err := os.ReadFile(filepath.Join(dir["dir"], name["dbfilename"]))
	return body, store.SnapshotCounts{Keys: int(keys), Cards: -1}, err
}

func dropDatabase(ctx context.Context, t *testing.T, dsn string) {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	name := strings.TrimPrefix(u.Path, "/")
	u.Path = "/postgres"
	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
	require.NoError(t, err)
}

func createDatabase(ctx context.Context, t *testing.T, s *pg.Server, name string) {
	t.Helper()
	db, err := sql.Open("pgx", s.DSN("postgres"))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
}
