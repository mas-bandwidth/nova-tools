//go:build functional

package config

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
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
	{
		_, _, _, err := st.Migrate(ctx)
		require.NoError(t, err)
	}
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
		require.False(t, err != nil || v != 0, "version before migrate: %d %v", v, err)
	}
	all, err := Migrations()
	require.NoError(t, err)
	from, to, applied, err := st.Migrate(ctx)
	require.False(t, err != nil || from != 0 || to != len(all) || len(applied) != len(all), "first migrate: from %d to %d applied %v err %v", from, to, applied, err)
	from, to, applied, err = st.Migrate(ctx)
	require.False(t, err != nil || from != len(all) || to != len(all) || len(applied) != 0, "second migrate: from %d to %d applied %v err %v (idempotent: nothing applied twice)", from, to, applied, err)
	{
		v, err := st.Version(ctx)
		require.False(t, err != nil || v != len(all), "version after: %d %v", v, err)
	}
	counts, err := st.Counts(ctx)
	require.NoError(t, err)
	for _, k := range Kinds {
		if k.Singleton {
			{
				_, found, err := st.Get(ctx, k.Name, k.Name)
				assert.False(t, err != nil || !found, "fresh schema lacks the %s row migrate creates: %v %v", k.Name, found, err)
			}
			continue
		}
		assert.False(t, counts[k.Name] != 0, "fresh schema counts %d %s rows", counts[k.Name], k.Name)
	}
	{
		rev, err := st.Rev(ctx, KindFriend)
		require.False(t, err != nil || rev != 0, "rev of an empty history: %d %v", rev, err)
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
	{
		got := err.Error()
		require.False(t, !strings.Contains(got, "postgres at postgres@127.0.0.1:1/nova"), "refusal %q does not name the store", got)
	}
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
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		require.Failf(t, "unexpected result", "no deadline: err %v, ctx %v; want the fallback's deadline error on an open context", err, ctx.Err())
	}

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
	if err == nil || dctx.Err() == nil {
		require.Failf(t, "unexpected result", "a deadline: err %v, ctx %v; want the caller's context to have governed", err, dctx.Err())
	}
}
