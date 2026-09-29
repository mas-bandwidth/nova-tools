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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, _, _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestMigrateOnAnEmptyDatabaseTwice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, err := st.Version(ctx); err != nil || v != 0 {
		t.Fatalf("version before migrate: %d %v", v, err)
	}
	all, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	from, to, applied, err := st.Migrate(ctx)
	if err != nil || from != 0 || to != len(all) || len(applied) != len(all) {
		t.Fatalf("first migrate: from %d to %d applied %v err %v", from, to, applied, err)
	}
	from, to, applied, err = st.Migrate(ctx)
	if err != nil || from != len(all) || to != len(all) || len(applied) != 0 {
		t.Fatalf("second migrate: from %d to %d applied %v err %v (idempotent: nothing applied twice)", from, to, applied, err)
	}
	if v, err := st.Version(ctx); err != nil || v != len(all) {
		t.Fatalf("version after: %d %v", v, err)
	}
	counts, err := st.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range Kinds {
		if k.Singleton {
			if _, found, err := st.Get(ctx, k.Name, k.Name); err != nil || !found {
				t.Errorf("fresh schema lacks the %s row migrate creates: %v %v", k.Name, found, err)
			}
			continue
		}
		if counts[k.Name] != 0 {
			t.Errorf("fresh schema counts %d %s rows", counts[k.Name], k.Name)
		}
	}
	if rev, err := st.Rev(ctx, KindFriend); err != nil || rev != 0 {
		t.Fatalf("rev of an empty history: %d %v", rev, err)
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
	if err == nil {
		t.Fatal("a closed port opened")
	}
	if got := err.Error(); !strings.Contains(got, "postgres at postgres@127.0.0.1:1/nova") {
		t.Fatalf("refusal %q does not name the store", got)
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
		if err != nil {
			t.Fatal(err)
		}
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
		t.Fatalf("no deadline: err %v, ctx %v; want the fallback's deadline error on an open context", err, ctx.Err())
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
		t.Fatalf("a deadline: err %v, ctx %v; want the caller's context to have governed", err, dctx.Err())
	}
}
