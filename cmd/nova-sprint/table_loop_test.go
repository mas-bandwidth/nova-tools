package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func wholeTableRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	for _, cmd := range table.SprintFixture() {
		args := make([]any, len(cmd))
		for i, v := range cmd {
			args[i] = v
		}
		if err := client.Do(context.Background(), args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}
	return mr.Addr(), client
}

// TestControl3530LoopPublishesOneWriter: `table --layout live --loop 1 --out
// <file>` publishes the whole table by rename (no temp file left beside it),
// a second writer on the same lock refuses with exit 3 while the first runs,
// and the first releases the lock on its way out.
func TestControl3530LoopPublishesOneWriter(t *testing.T) {
	addr, client := wholeTableRedis(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "TABLE.txt")
	cfg := table.SprintConfig{Sprint: "fix", Friends: []string{"rowan", "johnny", "emma", "stella"}, RowStale: 10 * time.Second}
	opts := tableOpts{layout: "live", loop: true, every: 100 * time.Millisecond, out: out}
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- loopTable(ctx, addr, cfg, opts, &stdout, &stderr) }()

	var body []byte
	waitFor(t, "the first publish", func() bool {
		b, err := os.ReadFile(out)
		body = b
		return err == nil && len(b) > 0
	})
	if !strings.HasPrefix(string(body), "SPRINT TABLE *** PIT STOP ***\n\n585/591 left, 1% done -> ~") {
		t.Fatalf("published table:\n%s\nstderr: %s", body, stderr.String())
	}
	if !strings.Contains(stdout.String(), "TABLE loop out="+out) {
		t.Fatalf("stdout %q, want one TABLE loop line", stdout.String())
	}

	var out2, err2 lockedBuffer
	if code := loopTable(context.Background(), addr, cfg, opts, &out2, &err2); code != 3 || !strings.Contains(err2.String(), "REFUSED: lock:nova-sprint-table is held by") {
		t.Fatalf("second writer: exit %d stderr %q, want 3 and REFUSED", code, err2.String())
	}

	// Two more publishes: each is a new file renamed over the old one.
	for i := 0; i < 2; i++ {
		before, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the next publish", func() bool {
			now, err := os.Stat(out)
			return err == nil && !os.SameFile(before, now)
		})
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("first writer exit %d; stderr %s", code, stderr.String())
		}
	case <-time.After(holdWait()):
		t.Fatal("first writer did not stop on cancel")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "TABLE.txt" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the out directory holds %v, want only TABLE.txt", names)
	}
	if n, _ := client.Exists(context.Background(), "lock:nova-sprint-table").Result(); n != 0 {
		t.Fatal("the writer left its lock behind")
	}
}

// waitFor polls cond until it holds or NOVA_TEST_WAIT (30 s) passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(holdWait())
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s", what, holdWait())
		}
		time.Sleep(5 * time.Millisecond) // wall-ok: polling a condition in a test
	}
}

// TestTableLoopTakesSeconds: `--loop 1` is a loop at one tick a second, not
// a positional argument; the refusal it meets here is the --once conflict.
func TestTableLoopTakesSeconds(t *testing.T) {
	addr, _ := wholeTableRedis(t)
	code, _, stderr := runSprint("table", "--layout", "live", "--redis", addr, "--loop", "1", "--once")
	if code != 2 || !strings.Contains(stderr, "--loop takes no --once") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	code, _, stderr = runSprint("table", "--layout", "live", "--redis", addr, "--loop", "0")
	if code != 2 || !strings.Contains(stderr, "--loop wants seconds") {
		t.Fatalf("--loop 0: exit %d stderr %q", code, stderr)
	}
}

// TestTableLiveOnceOut: one render to --out, by rename, is the whole table.
func TestTableLiveOnceOut(t *testing.T) {
	addr, _ := wholeTableRedis(t)
	out := filepath.Join(t.TempDir(), "TABLE.txt")
	code, stdout, stderr := runSprint("table", "--layout", "live", "--redis", addr, "--sprint", "fix", "--friends", "rowan,johnny,emma,stella", "--once", "--out", out)
	if code != 0 || stdout != "" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(b), "\nrowan      |     0 |      12 |     4 | ") || !strings.Contains(string(b), "\nstream                         | waiting | working | merging | landed\n") {
		t.Fatalf("published table:\n%s (%v)", b, err)
	}
}

// TestControl3637ClearVerb: `table clear` prints the checkpoint line first,
// writes the checkpoint, then clears and says how long it took.
func TestControl3637ClearVerb(t *testing.T) {
	addr, client := wholeTableRedis(t)
	cp := filepath.Join(t.TempDir(), "clear.tsv")
	code, stdout, stderr := runSprint("table", "clear", "--redis", addr, "--checkpoint", cp, "--by", "rowan")
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// friends=5: every member of the friends SET has a done set, ghost's
	// empty (a ZCARD of 0), so each gets a base.
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "CHECKPOINT at=") || !strings.Contains(lines[0], " landed=6 friends=5") ||
		!strings.HasPrefix(lines[1], "CLEARED landed=6 streams=3 friends=5 by=rowan ms=") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	b, err := os.ReadFile(cp)
	if err != nil || strings.Count(string(b), "\ntask\t") != 6 {
		t.Fatalf("checkpoint:\n%s (%v)", b, err)
	}
	if n, _ := client.ZCard(context.Background(), "ws:rowan-tools:landed").Result(); n != 0 {
		t.Fatalf("ws:rowan-tools:landed has %d after clear", n)
	}
	if n, _ := client.ZCard(context.Background(), "ws:swarm: cards:waiting").Result(); n != 150 {
		t.Fatalf("waiting moved: %d", n)
	}
	if code, _, stderr := runSprint("table", "clear", "--redis", addr); code != 2 || !strings.Contains(stderr, "--checkpoint <file>") {
		t.Fatalf("no checkpoint: exit %d %s", code, stderr)
	}
}
