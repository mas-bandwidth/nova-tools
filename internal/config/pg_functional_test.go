//go:build functional

package config

import (
	"context"
	"os"
	"strings"
	"testing"

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
