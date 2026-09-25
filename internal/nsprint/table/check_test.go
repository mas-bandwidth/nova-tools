package table_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func checkStore(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestCheckSprintsEmpty(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	has, nc, err := table.CheckSprints(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if has || nc != "" {
		t.Fatalf("empty store: has=%v nonControl=%q", has, nc)
	}
}

func TestCheckSprintsControlOnly(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	if err := client.SAdd(ctx, "sprints", "control-a", "control-b").Err(); err != nil {
		t.Fatal(err)
	}
	has, nc, err := table.CheckSprints(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if !has || nc != "" {
		t.Fatalf("control-only: has=%v nonControl=%q", has, nc)
	}
}

func TestCheckSprintsNonControl(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	if err := client.SAdd(ctx, "sprints", "control-a", "live-sprint", "control-b").Err(); err != nil {
		t.Fatal(err)
	}
	has, nc, err := table.CheckSprints(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if !has || nc != "live-sprint" {
		t.Fatalf("non-control: has=%v nonControl=%q, want live-sprint", has, nc)
	}
}

func TestCheckLiveFreshFile(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	seedCommands(t, client, table.DefectFixture())

	dir := t.TempDir()
	out := filepath.Join(dir, "table.txt")
	snap, err := table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(snap.Render()), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := table.CheckLive(ctx, client, out, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, stderr.String())
	}
}

func TestCheckLiveStaleFile(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	seedCommands(t, client, table.DefectFixture())

	dir := t.TempDir()
	out := filepath.Join(dir, "table.txt")
	if err := os.WriteFile(out, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make the file 3 seconds old
	oldTime := time.Now().Add(-3 * time.Second)
	if err := os.Chtimes(out, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := table.CheckLive(ctx, client, out, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "old") {
		t.Fatalf("stderr does not mention age: %s", stderr.String())
	}
}

func TestCheckLiveCellMismatch(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	seedCommands(t, client, table.DefectFixture())

	dir := t.TempDir()
	out := filepath.Join(dir, "table.txt")
	// Write a file with a wrong cell
	wrong := "name | up | desired | starting | living | stale | leased | queue | done | why\nbench:b1 | up | 99 | 1 | 0 | 1 | 2 | 1 | 1 | stale: living >120s\n"
	if err := os.WriteFile(out, []byte(wrong), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := table.CheckLive(ctx, client, out, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit=%d, want 1; stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "cell mismatch") {
		t.Fatalf("stderr does not mention cell mismatch: %s", stderr.String())
	}
}

func TestCheckSeedsFixtureWhenEmpty(t *testing.T) {
	client := checkStore(t)
	ctx := context.Background()
	// Store is empty: no sprints set
	has, nc, err := table.CheckSprints(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if has || nc != "" {
		t.Fatalf("empty store: has=%v nonControl=%q", has, nc)
	}

	// Seed the fixture
	for _, cmd := range table.DefectFixture() {
		args := make([]any, len(cmd))
		for i, v := range cmd {
			args[i] = v
		}
		if err := client.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}

	// Now the store should have sprints
	has, nc, err = table.CheckSprints(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected sprints after seeding")
	}
	// Fixture has non-control sprints (s1, old)
	if nc == "" {
		t.Fatal("expected non-control sprint after seeding fixture")
	}

	// Render should match golden
	snap, err := table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Render() != table.DefectGolden() {
		t.Fatalf("rendered does not match golden:\n%s", snap.Render())
	}
}
