//go:build functional

package config

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil/pg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One throwaway Postgres for the package, one database per test
// (docs/TESTING.md: one server per package, not one per test).
var server *pg.Server

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nova-config-pg-")
	if err != nil {
		panic(err)
	}
	server, err = pg.StartServer(dir)
	if err != nil {
		os.Stderr.WriteString("throwaway postgres: " + err.Error() + "\n")
		os.Exit(2)
	}
	code := m.Run()
	_ = server.Stop()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// migrated opens a fresh database and migrates it.
func migrated(t *testing.T) *PG {
	t.Helper()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	_, _, _, setupErr952 := st.Migrate(ctx)
	require.NoError(t, setupErr952)
	return st
}

func TestMigrateOnAnEmptyDatabaseTwice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	{
		v, err := st.Version(ctx)
		assertionMsg62 := []any{"version before migrate: %d %v", v, err}
		require.NoError(t, err, assertionMsg62...)
		require.Equal(t, 0, v, assertionMsg62...)
	}
	all, err := Migrations()
	require.NoError(t, err)
	from, to, applied, err := st.Migrate(ctx)
	assertionMsg67 := []any{"first migrate: from %d to %d applied %v err %v", from, to, applied, err}
	require.NoError(t, err, assertionMsg67...)
	require.Equal(t, 0, from, assertionMsg67...)
	require.Equal(t, len(all), to, assertionMsg67...)
	require.Len(t, applied, len(all), assertionMsg67...)
	from, to, applied, err = st.Migrate(ctx)
	assertionMsg69 := []any{"second migrate: from %d to %d applied %v err %v (idempotent: nothing applied twice)", from, to, applied, err}
	require.NoError(t, err, assertionMsg69...)
	require.Equal(t, len(all), from, assertionMsg69...)
	require.Equal(t, len(all), to, assertionMsg69...)
	require.Empty(t, applied, assertionMsg69...)
	{
		v, err := st.Version(ctx)
		assertionMsg72 := []any{"version after: %d %v", v, err}
		require.NoError(t, err, assertionMsg72...)
		require.Equal(t, len(all), v, assertionMsg72...)
	}
	counts, err := st.Counts(ctx)
	require.NoError(t, err)
	fleet, found, err := st.Get(ctx, KindFleet, KindFleet)
	require.NoError(t, err)
	require.True(t, found)
	assert.Empty(t, fleet.Fields["redis_port"], "migration leaves the Redis port unset")
	assert.Empty(t, fleet.Fields["pg_dsn"])
	var noPortDefault bool
	require.NoError(t, st.db.QueryRowContext(ctx, `SELECT column_default IS NULL FROM information_schema.columns WHERE table_schema = 'config' AND table_name = 'fleet' AND column_name = 'redis_port'`).Scan(&noPortDefault))
	assert.True(t, noPortDefault, "migration must not guess a Redis port")
	for _, k := range Kinds {
		if k.Singleton {
			{
				_, found, err := st.Get(ctx, k.Name, k.Name)
				assertionMsg92 := []any{"fresh schema lacks the %s row migrate creates: %v %v", k.Name, found, err}
				func() {
					if !assert.NoError(t, err, assertionMsg92...) {
						return
					}
					assert.True(t, found, assertionMsg92...)
				}()
			}
			continue
		}
		assert.Equal(t, len(k.Seed), counts[k.Name], "fresh schema counts %d %s rows (migrate makes the kind's Seed)", counts[k.Name], k.Name)
	}
	{
		rev, err := st.Rev(ctx, KindFriend)
		assertionMsg88 := []any{"rev of an empty history: %d %v", rev, err}
		require.NoError(t, err, assertionMsg88...)
		require.Equal(t, int64(0), rev, assertionMsg88...)
	}
}

// TestMigrationTwelveFillsTheOldWidth: 0012 adds the machine's width and
// fills it with the old derived width's beat-free rule (the slots of every
// friend charged to the fleet row's coordinator machine; every other machine
// width = slots; never below 0); a width set after it survives the file run
// again, and migrate after it applies nothing.
func TestMigrationTwelveFillsTheOldWidth(t *testing.T) {
	t.Parallel()
	machines := `INSERT INTO config.machines (name, "user", seat, slots, runners) VALUES ('m1', 'u', 's', 144, 0), ('m2', 'u', 's', 8, 1), ('m3', 'u', 's', 0, 0)`
	cases := []struct {
		name  string
		setup []string
		want  map[string]string
	}{
		{name: "friends charged to the coordinator",
			setup: []string{machines, `INSERT INTO config.friends (name, slots) VALUES ('f1', 64), ('f2', 64), ('f3', 0)`, `UPDATE config.fleet SET coordinator = 'm1'`},
			want:  map[string]string{"m1": "16", "m2": "8", "m3": "0"}},
		{name: "friends over the coordinator's slots leave it 0",
			setup: []string{machines, `INSERT INTO config.friends (name, slots) VALUES ('f1', 4)`, `UPDATE config.fleet SET coordinator = 'm2'`},
			want:  map[string]string{"m1": "144", "m2": "4", "m3": "0"}},
		{name: "a coordinator with no slots stays 0",
			setup: []string{machines, `INSERT INTO config.friends (name, slots) VALUES ('f1', 4)`, `UPDATE config.fleet SET coordinator = 'm3'`},
			want:  map[string]string{"m1": "144", "m2": "8", "m3": "0"}},
		{name: "no coordinator charges nothing",
			setup: []string{machines, `INSERT INTO config.friends (name, slots) VALUES ('f1', 64)`},
			want:  map[string]string{"m1": "144", "m2": "8", "m3": "0"}},
		{name: "no friends charges nothing",
			setup: []string{machines, `UPDATE config.fleet SET coordinator = 'm1'`},
			want:  map[string]string{"m1": "144", "m2": "8", "m3": "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			st, err := OpenPG(ctx, server.Database(t))
			require.NoError(t, err)
			defer st.Close()
			all, err := Migrations()
			require.NoError(t, err)
			var twelve Migration
			for _, m := range all {
				if m.Version == 12 {
					twelve = m
					break
				}
				require.NoError(t, st.applyOne(ctx, m), "migration %s", m.Name)
			}
			require.Equal(t, "0012_machine_width.sql", twelve.Name)
			for _, q := range tc.setup {
				_, err = st.db.ExecContext(ctx, q)
				require.NoError(t, err, q)
			}
			require.NoError(t, st.applyOne(ctx, twelve))
			widths := func() map[string]string {
				t.Helper()
				// by SQL: the store at version 12 has no note column, which List reads
				rows, err := st.db.QueryContext(ctx, `SELECT name, width::text FROM config.machines`)
				require.NoError(t, err)
				defer rows.Close()
				out := map[string]string{}
				for rows.Next() {
					var name, width string
					require.NoError(t, rows.Scan(&name, &width))
					out[name] = width
				}
				require.NoError(t, rows.Err())
				return out
			}
			assert.Equal(t, tc.want, widths(), "the fill")
			_, err = st.db.ExecContext(ctx, `UPDATE config.machines SET width = 3 WHERE name = 'm2'`) // by SQL: no note column yet
			require.NoError(t, err)
			_, err = st.db.ExecContext(ctx, twelve.SQL)
			require.NoError(t, err, "the file run again")
			assert.Equal(t, "3", widths()["m2"], "the file run again overwrote a width set since")
			_, to, applied, err := st.Migrate(ctx)
			require.NoError(t, err)
			assert.Equal(t, len(all), to)
			assert.NotContains(t, applied, 12, "migrate after the fill applied 0012 again")
		})
	}
}

// TestMigrationTwelveChecksAWidthColumnAlreadyThere: a width column that is
// there before 0012 runs is kept, and its fill skipped, only when it is the
// column 0012 makes; any other shape fails the migration naming every
// difference, and 0012 is not recorded as applied.
func TestMigrationTwelveChecksAWidthColumnAlreadyThere(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		column string
		want   []string // the phrases the failure names; none when it passes
	}{
		{name: "the shape 0012 makes passes and keeps the widths", column: `integer NOT NULL DEFAULT 0 CHECK (width >= 0)`},
		{name: "text, nullable, no default, no check", column: `text`, want: []string{"its type is text, want integer", "it is nullable, want NOT NULL", "its default is none, want 0", "it has no CHECK (width >= 0)"}},
		{name: "a default of 1", column: `integer NOT NULL DEFAULT 1 CHECK (width >= 0)`, want: []string{"its default is 1, want 0"}},
		{name: "no check", column: `integer NOT NULL DEFAULT 0`, want: []string{"it has no CHECK (width >= 0)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			st, err := OpenPG(ctx, server.Database(t))
			require.NoError(t, err)
			defer st.Close()
			all, err := Migrations()
			require.NoError(t, err)
			for _, m := range all[:11] {
				require.NoError(t, st.applyOne(ctx, m), "migration %s", m.Name)
			}
			_, err = st.db.ExecContext(ctx, `INSERT INTO config.machines (name, "user", seat, slots, runners) VALUES ('m1', 'u', 's', 144, 0)`)
			require.NoError(t, err)
			_, err = st.db.ExecContext(ctx, `ALTER TABLE config.machines ADD COLUMN width `+tc.column)
			require.NoError(t, err)
			if tc.want == nil {
				_, err = st.db.ExecContext(ctx, `UPDATE config.machines SET width = 7`)
				require.NoError(t, err)
			}
			err = st.applyOne(ctx, all[11])
			v, verr := st.Version(ctx)
			require.NoError(t, verr)
			if tc.want == nil {
				require.NoError(t, err)
				assert.Equal(t, 12, v)
				var width string
				require.NoError(t, st.db.QueryRowContext(ctx, `SELECT width::text FROM config.machines WHERE name = 'm1'`).Scan(&width), "by SQL: the store at version 12 has no note column, which Get reads")
				assert.Equal(t, "7", width, "a width column already there is kept, not refilled")
				return
			}
			require.Error(t, err)
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w)
			}
			assert.Contains(t, err.Error(), "already has a width column that is not the one 0012 makes")
			assert.Equal(t, 11, v, "a failed 0012 is not recorded")
		})
	}
}

// TestAppliedIsTheLedger: Applied reads every version recorded, so a gap
// below the greatest is seen (migrate --dry-run prints it as missing); none
// before the first migrate.
func TestAppliedIsTheLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	got, err := st.Applied(ctx)
	require.NoError(t, err)
	assert.Empty(t, got, "no ledger before the first migrate")
	_, _, _, err = st.Migrate(ctx)
	require.NoError(t, err)
	_, err = st.db.ExecContext(ctx, `DELETE FROM config.schema_migrations WHERE version = 11`)
	require.NoError(t, err)
	got, err = st.Applied(ctx)
	require.NoError(t, err)
	all, err := Migrations()
	require.NoError(t, err)
	assert.NotContains(t, got, 11)
	assert.Len(t, got, len(all)-1)
	assert.Equal(t, len(all), got[len(got)-1])
}

// 0017 makes a loop's width the machine's: the second reader rows of
// 2026-10-02 (reader-<m>-2) are removed, every nova-swarm member argv that
// spells a width loses it (before any --; another program's is its own; the
// rest of the argv kept word for word, canonical), and the loops' width column
// goes. A row's argv after it is one the loop kind's Check accepts.
func TestMigrationSeventeenMovesTheLoopWidthToTheMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	all, err := Migrations()
	require.NoError(t, err)
	var seventeen Migration
	for _, m := range all {
		if m.Version == 17 {
			seventeen = m
			break
		}
		require.NoError(t, st.applyOne(ctx, m), "migration %s", m.Name)
	}
	require.Equal(t, "0017_loop_width_is_the_machines.sql", seventeen.Name)
	_, err = st.db.ExecContext(ctx, `INSERT INTO config.machines (name, "user", seat, slots, runners) VALUES ('m1', 'u', 's', 4, 0)`)
	require.NoError(t, err)
	_, err = st.db.ExecContext(ctx, `INSERT INTO config.machines (name, "user", seat, slots, runners) VALUES ('bench-2', 'u', 's', 4, 0)`)
	require.NoError(t, err)
	loops := []struct {
		name, argv, machine string
		width               int
		want                string // the argv after; "" when the row is removed
	}{
		{"reader-m1", `["nova-swarm","member","--as","reader-m1","--reader","--width","8"]`, "m1", 8, `["nova-swarm","member","--as","reader-m1","--reader"]`},
		{"reader-m1-2", `["nova-swarm","member","--as","reader-m1-2","--reader","--width","8"]`, "m1", 8, ""},
		{"member-m1", `["/opt/bin/nova-swarm","member","--as","m1","--width=4","--root","r"]`, "m1", 0, `["/opt/bin/nova-swarm","member","--as","m1","--root","r"]`},
		{"single-dash", `["nova-swarm","member","-width","3","--reader"]`, "m1", 3, `["nova-swarm","member","--reader"]`},
		{"blank-word", `["nova-swarm","member","--as","a b","--width","2","--x","&<>"]`, "m1", 0, `["nova-swarm","member","--as","a b","--x","&<>"]`},
		{"after-terminator", `["nova-swarm","member","--","--width","9"]`, "m1", 0, `["nova-swarm","member","--","--width","9"]`},
		{"another-program", `["p","--width","12"]`, "m1", 12, `["p","--width","12"]`},
		// the fleet's member rows: env, the endpoint names, then the member
		{"member-env", `["/usr/bin/env","NOVA_SPRINT_REDIS_USER=bench","NOVA_SPRINT_REDIS_PASSWORD_ENV=P","~/.local/bin/nova-swarm","member","--as","m1","--width","4"]`, "m1", 4, `["/usr/bin/env","NOVA_SPRINT_REDIS_USER=bench","NOVA_SPRINT_REDIS_PASSWORD_ENV=P","~/.local/bin/nova-swarm","member","--as","m1"]`},
		{"other-env", `["/usr/bin/env","A=b","p","--width","5"]`, "m1", 5, `["/usr/bin/env","A=b","p","--width","5"]`},
		{"reader-2", `["p","--reader"]`, "m1", 1, `["p","--reader"]`}, // its name ends in -2 and names no machine: kept
		// a machine whose own name ends in -2: its one reader reader-bench-2 is
		// kept, not mistaken for a second reader of a machine named bench
		{"reader-bench-2", `["nova-swarm","member","--as","reader-bench-2","--reader"]`, "bench-2", 0, `["nova-swarm","member","--as","reader-bench-2","--reader"]`},
	}
	for _, l := range loops {
		_, err = st.db.ExecContext(ctx, `INSERT INTO config.loops (name, machine, argv, keepalive, width) VALUES ($1, $2, $3, true, $4)`, l.name, l.machine, l.argv, l.width)
		require.NoError(t, err, l.name)
	}
	require.NoError(t, st.applyOne(ctx, seventeen))
	rows, err := st.List(ctx, KindLoop)
	require.NoError(t, err)
	got := map[string]Row{}
	for _, r := range rows {
		got[r.Name] = r
	}
	loop, _ := Lookup(KindLoop)
	for _, l := range loops {
		r, there := got[l.name]
		if l.want == "" {
			assert.False(t, there, "%s: a second reader row is removed", l.name)
			continue
		}
		require.True(t, there, "%s: the row is kept", l.name)
		assert.Equal(t, l.want, r.Fields["argv"], "%s: the argv after 0017", l.name)
		assert.NotContains(t, r.Fields, "width", "%s: no width field", l.name)
		canonical, err := marshalArgv(Argv(r.Fields["argv"]))
		require.NoError(t, err)
		assert.Equal(t, string(canonical), r.Fields["argv"], "%s: the argv is canonical", l.name)
		assert.NoError(t, loop.Check(r), "%s: the row after 0017 passes the loop kind's Check", l.name)
	}
	var column bool
	require.NoError(t, st.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'config' AND table_name = 'loops' AND column_name = 'width')`).Scan(&column))
	assert.False(t, column, "config.loops still has a width column")
}

// 0018 gives every friend a width (the owner, 2026-10-02: "please update
// friends in nova-config so each friend has a width of 8"): each friend row
// there before it is set to 8, a row added after takes 8 by default, a width
// below 1 is refused by the column's CHECK, and the file run again keeps a
// width set since.
func TestMigrationEighteenGivesEveryFriendAWidthOfEight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	all, err := Migrations()
	require.NoError(t, err)
	var eighteen Migration
	for _, m := range all {
		if m.Version == 18 {
			eighteen = m
			break
		}
		require.NoError(t, st.applyOne(ctx, m), "migration %s", m.Name)
	}
	require.Equal(t, "0018_friend_width.sql", eighteen.Name)
	six := []string{"f1", "f2", "f3", "f4", "f5", "f6"}
	for _, f := range six {
		_, err = st.db.ExecContext(ctx, `INSERT INTO config.friends (name, slots, tiers, roles) VALUES ($1, 2, 'flash', '')`, f)
		require.NoError(t, err, f)
	}
	require.NoError(t, st.applyOne(ctx, eighteen))
	widths := func() map[string]string {
		rows, err := st.List(ctx, KindFriend)
		require.NoError(t, err)
		out := map[string]string{}
		for _, r := range rows {
			out[r.Name] = r.Fields["width"]
		}
		return out
	}
	assert.Equal(t, map[string]string{"f1": "8", "f2": "8", "f3": "8", "f4": "8", "f5": "8", "f6": "8"}, widths(), "every friend there before 0018 has width 8")

	_, err = st.db.ExecContext(ctx, `INSERT INTO config.friends (name, slots, tiers, roles) VALUES ('f7', 2, 'flash', '')`)
	require.NoError(t, err)
	_, err = st.db.ExecContext(ctx, `UPDATE config.friends SET width = 3 WHERE name = 'f6'`)
	require.NoError(t, err)
	_, err = st.db.ExecContext(ctx, `UPDATE config.friends SET width = 0 WHERE name = 'f2'`)
	require.Error(t, err, "a width below 1 is refused by the CHECK")
	_, err = st.db.ExecContext(ctx, eighteen.SQL)
	require.NoError(t, err, "the file runs again")
	got := widths()
	assert.Equal(t, "8", got["f7"], "a row added later takes the default")
	assert.Equal(t, "3", got["f6"], "a width set since is kept when the file runs again")
	assert.Equal(t, "8", got["f2"])
}

// TestPostgresStoreKeepsTheContract runs the one store contract the Mem
// fake is held to (store_test.go) against Postgres.
func TestPostgresStoreKeepsTheContract(t *testing.T) {
	t.Parallel()
	storeTests(t, func(t *testing.T) Store { return migrated(t) })
}

func TestOpenPGRefusesAClosedPort(t *testing.T) {
	t.Parallel()

	_, err := OpenPG(context.Background(), "postgres://postgres@127.0.0.1:1/nova?sslmode=disable&connect_timeout=2")
	require.Error(t, err, "a closed port opened")
	scopedGot120 := err.Error()
	require.Contains(t, scopedGot120, "postgres at postgres@127.0.0.1:1/nova", "refusal %q does not name the store", scopedGot120)
}

// Without a deadline of its own the connection check is bounded by the
// fallback it is given (ConnectTimeout in OpenPG); with one, the caller's
// deadline governs and the fallback is not applied. Both are read from the
// context, not the clock: after the fallback fires the caller's context is
// still open, and after the caller's deadline fires it is done.
func TestOpenPGBoundsByTheFallbackOnlyWithoutADeadline(t *testing.T) {
	t.Parallel()

	// stall is a store that accepts connections and never answers; accepted
	// gets one value per connection.
	stall := func() (dsn string, accepted chan struct{}) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		accepted = make(chan struct{}, 8)
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				accepted <- struct{}{}
			}
		}()
		return "postgres://nova_config@" + l.Addr().String() + "/nova", accepted
	}

	// No deadline: the 100ms fallback ends the wait, the context stays open.
	dsn, _ := stall()
	ctx := context.Background()
	_, err := openPGWithin(ctx, dsn, 100*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded, "the fallback deadline should govern an open context")
	require.NoError(t, ctx.Err(), "the caller's context should remain open")

	// A deadline (a distant one, so nothing here waits on it): it governs, so
	// a 1ns fallback is not applied and the call is still waiting when the
	// store has accepted the connection. The test then cancels the context
	// and the call returns with it done.
	dsn, accepted := stall()
	dctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	go func() {
		<-accepted
		cancel()
	}()
	_, err = openPGWithin(dctx, dsn, time.Nanosecond)
	require.Error(t, err, "the caller's deadline should govern")
	require.Error(t, dctx.Err(), "the caller's context should be done")
}
