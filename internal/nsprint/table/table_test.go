package table_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

func controlStore(t *testing.T) *redis.Client {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := client.Ping(ctx).Err(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("throwaway redis did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	return client
}

func seedCommands(t *testing.T, client *redis.Client, cmds [][]string) {
	t.Helper()
	for _, cmd := range cmds {
		args := make([]any, len(cmd))
		for i, value := range cmd {
			args[i] = value
		}
		if err := client.Do(context.Background(), args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}
}

func TestControl21TableCheckFixture(t *testing.T) {
	ctx := context.Background()
	client := controlStore(t)
	seedCommands(t, client, table.DefectFixture())
	snap, err := table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.Render(), table.DefectGolden(); got != want {
		t.Fatalf("fixture output differs\ngot:\n%s\nwant:\n%s", got, want)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("clean fixture errors: %v", snap.Errors)
	}
	for _, key := range []string{
		"bench:b1:width", "bench:b1:queue", "bench:b1:done",
		"friend:fran:width", "friend:fran:queue", "friend:fran:done", "friend:fran:slots",
	} {
		if err := client.Set(ctx, key, "99", 0).Err(); err != nil {
			t.Fatal(err)
		}
		snap, err := table.Read(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(snap.Render(), "two writers: "+key) {
			t.Fatalf("%s accepted:\n%s", key, snap.Render())
		}
		if err := client.Del(ctx, key).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestControl24BenchCellsFromCardIndexes(t *testing.T) {
	ctx := context.Background()
	client := controlStore(t)
	seedCommands(t, client, [][]string{
		{"SADD", "sprints", "control-a"},
		{"HSET", "s:control-a", "status", "open"},
		{"SADD", "benches", "ctl-b"},
		{"HSET", "bench:ctl-b:desired", "slots", "4"},
		{"HSET", "bench:ctl-b:beat", "at", "1"},
		{"ZADD", "s:control-a:bench:ctl-b:queue", "0", "c1", "0", "c2", "0", "c3"},
		{"SADD", "s:control-a:bench:ctl-b:ended", "e1", "e2"},
		{"ZADD", "s:control-a:open:ghost", "0", "x1", "0", "x2", "0", "x3", "0", "x4", "0", "x5"},
		{"SADD", "s:control-a:done:ghost", "y1", "y2", "y3", "y4", "y5", "y6", "y7"},
	})
	snap, err := table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Benches) != 1 {
		t.Fatalf("bench rows=%d", len(snap.Benches))
	}
	row := snap.Benches[0]
	if row.Queue != 3 || row.Done != 2 {
		t.Fatalf("bench queue=%d done=%d, want 3/2; friend task indexes must not feed bench", row.Queue, row.Done)
	}
	if strings.Contains(snap.Render(), " | 5 | 7 |") {
		t.Fatalf("friend task cells leaked into bench:\n%s", snap.Render())
	}
}
