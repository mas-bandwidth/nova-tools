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
