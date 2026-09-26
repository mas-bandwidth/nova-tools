//go:build functional

package table_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// publish writes the renderer's --out file from a direct read, as the wide
// table's tick does, and returns its path and mtime.
func publish(t *testing.T, client *redis.Client) (string, time.Time) {
	t.Helper()
	snap, err := table.Read(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "table.txt")
	if err := os.WriteFile(out, []byte(snap.Render()), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, info.ModTime()
}

// TestCheckLiveSameSecond is #3253's DONE-WHEN, library half: a file younger
// than 2 s whose every cell equals the direct read passes; a renderer stopped
// for 3 s fails; a second-writer key fails and is named; a changed cell fails
// and is named.
func TestCheckLiveSameSecond(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := controlStore(t)
	seedCommands(t, client, table.DefectFixture())
	out, at := publish(t, client)

	receipt, err := table.CheckLive(ctx, client, out, "", at.Add(500*time.Millisecond))
	if err != nil {
		t.Fatalf("fresh equal file refused: %v", err)
	}
	if !strings.HasPrefix(receipt, "CHECK LIVE OK file="+out+" age=500ms cells=") {
		t.Fatalf("receipt %q", receipt)
	}

	_, err = table.CheckLive(ctx, client, out, "", at.Add(3*time.Second))
	if err == nil || !strings.Contains(err.Error(), "3s old") || !strings.Contains(err.Error(), "not ticking") {
		t.Fatalf("renderer stopped 3 s: got %v", err)
	}

	if err := client.Set(ctx, "bench:b1:width", "99", 0).Err(); err != nil {
		t.Fatal(err)
	}
	_, err = table.CheckLive(ctx, client, out, "", at.Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), "two writers: bench:b1:width") {
		t.Fatalf("second-writer key: got %v", err)
	}
	if err := client.Del(ctx, "bench:b1:width").Err(); err != nil {
		t.Fatal(err)
	}

	if err := client.HSet(ctx, "bench:b1:desired", "slots", "5").Err(); err != nil {
		t.Fatal(err)
	}
	_, err = table.CheckLive(ctx, client, out, "", at.Add(time.Second))
	if err == nil || !strings.Contains(err.Error(), `cell bench:b1 desired: file "4", direct read "5"`) {
		t.Fatalf("changed cell: got %v", err)
	}
}

func TestCheckLiveMissingFile(t *testing.T) {
	t.Parallel()

	client := controlStore(t)
	_, err := table.CheckLive(context.Background(), client, filepath.Join(t.TempDir(), "none.txt"), "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "--check --live") {
		t.Fatalf("missing file: got %v", err)
	}
}

// TestPrepareCheck is #3253's plain --check half: an empty store is seeded
// with the fixture (and renders the golden), a re-run on it is accepted, and
// a store holding a non-control sprint is refused by name.
func TestPrepareCheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := controlStore(t)
	// fn.Load writes the function library, not keys: the store is empty.
	seeded, err := table.PrepareCheck(ctx, client)
	if err != nil || !seeded {
		t.Fatalf("empty store: seeded=%v err=%v", seeded, err)
	}
	snap, err := table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Render(); got != table.DefectGolden() {
		t.Fatalf("seeded store renders\n%s\nwant\n%s", got, table.DefectGolden())
	}
	seeded, err = table.PrepareCheck(ctx, client)
	if err != nil || seeded {
		t.Fatalf("re-run on the fixture: seeded=%v err=%v", seeded, err)
	}

	if err := client.SAdd(ctx, "sprints", "control-extra").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := table.PrepareCheck(ctx, client); err != nil {
		t.Fatalf("control sprint refused: %v", err)
	}

	if err := client.SAdd(ctx, "sprints", "nova-sprint-0925j").Err(); err != nil {
		t.Fatal(err)
	}
	seeded, err = table.PrepareCheck(ctx, client)
	if err == nil || seeded || !strings.Contains(err.Error(), `"nova-sprint-0925j", a non-control sprint`) {
		t.Fatalf("live store: seeded=%v err=%v", seeded, err)
	}
}
