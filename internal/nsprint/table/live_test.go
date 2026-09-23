package table_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

func liveStore(t *testing.T, cmds [][]string) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	seedCommands(t, client, cmds)
	return client
}

// TestControl2674SprintLayout: the fixture keyspace renders the real
// SPRINT-TABLE.txt of 2026-09-23T19:26:17Z byte for byte.
func TestControl2674SprintLayout(t *testing.T) {
	client := liveStore(t, table.Fixture2674())
	snap, err := table.ReadLive(context.Background(), client, table.Fixture2674Config())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.RenderLive(table.Fixture2674Now()), table.Golden2674(); got != want {
		t.Fatalf("live layout differs from the bash golden\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestControl2674FailedTickNeverBlank: a failed read with no good read yet
// prints the bash's never-read shape; after a good read it keeps that read's
// friend rows and says how stale they are.
func TestControl2674FailedTickNeverBlank(t *testing.T) {
	cfg := table.Fixture2674Config()
	now := table.Fixture2674Now()
	got := table.FailedLive(cfg, nil).RenderLive(now)
	for _, want := range []string{"blocked: ?\n", "landed: ?\n", "rowan      |     - |       - |     - | ?         \n", "stale: never read (Redis did not answer since start)\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("never-read tick lacks %q:\n%s", want, got)
		}
	}
	client := liveStore(t, table.Fixture2674())
	good, err := table.ReadLive(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	good.LastGood = now.Add(-7e9)
	got = table.FailedLive(cfg, good).RenderLive(now)
	for _, want := range []string{"blocked: ?\n", "stella     |    87 |      21 |  1553 | up        \n", "stale: 7s (Redis did not answer; rows are the last good read)\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failed tick lacks %q:\n%s", want, got)
		}
	}
}

func TestFormatLandedAgesOut(t *testing.T) {
	now := table.Fixture2674Now()
	for raw, want := range map[string]string{
		"": "landed: ?",
		"193/666 landed 28% (a 1/2) eta=~21h at=2026-09-23T19:25:17Z": "landed: 193/666 28% -> ~21h",
		"193/666 landed 28% (a 1/2) eta=~21h at=2026-09-23T19:20:00Z": "landed: ?",
		"193/666 landed 28% (a 1/2) at=2026-09-23T19:25:17Z":          "landed: 193/666 28% -> ?",
		"garbage": "landed: ?",
	} {
		if got := table.FormatLanded(raw, now); got != want {
			t.Errorf("FormatLanded(%q) = %q, want %q", raw, got, want)
		}
	}
}
