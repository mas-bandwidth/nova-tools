package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func runTaskCLI(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := runTask(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestTaskBatchHelpPerVerb: every batch verb answers --help (and -h) with the
// usage on stdout and exit 0.
func TestTaskBatchHelpPerVerb(t *testing.T) {
	for _, sub := range []string{"cancel", "block", "unblock", "move", "front", "sweep"} {
		for _, h := range []string{"--help", "-h"} {
			code, out, errOut := runTaskCLI(sub, h)
			if code != 0 || errOut != "" || !strings.Contains(out, "nova-sprint task "+sub) ||
				!strings.Contains(out, "TASK <verb> n=<k> stream=<s> ms=<n>") || !strings.Contains(out, "exit codes: 0 moved, 1 refused") {
				t.Errorf("task %s %s = %d %q %q", sub, h, code, out, errOut)
			}
		}
	}
}

// TestTaskBatchRefusesBadFlags: usage errors exit 2 before any dial.
func TestTaskBatchRefusesBadFlags(t *testing.T) {
	t.Setenv("NOVA_SPRINT_REDIS", "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	for _, args := range [][]string{
		{"block", "--ids", "a"},
		{"block", "--ids", "a", "--on", "task:x", "--reason", "r"},
		{"move", "--ids", "a"},
		{"move", "--ids", "a", "--to-stream", "s", "--to-friend", "f"},
		{"cancel", "--ids", "a", "--stream", "s"},
		{"unblock"},
		{"sweep"},
		{"sweep", "--friend", "f", "--ids", "a"},
		{"cancel", "--ids", "@/nonexistent/ids"},
		{"front", "--stream", "s", "extra"},
	} {
		code, out, errOut := runTaskCLI(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "run: nova-sprint help") {
			t.Errorf("%v = %d %q %q", args, code, out, errOut)
		}
	}
	// Without a batch source, cancel/move/front stay the one-id queue verbs.
	if isTaskBatch("cancel", []string{"--id", "x"}) || isTaskBatch("move", []string{"--id", "x", "--to", "b"}) {
		t.Fatal("one-id cancel/move routed to the batch verb")
	}
	if !isTaskBatch("cancel", []string{"--ids=@f"}) || !isTaskBatch("front", []string{"--set", "k"}) || !isTaskBatch("block", nil) {
		t.Fatal("batch forms not routed to the batch verb")
	}
}

var taskLine = regexp.MustCompile(`^TASK (\w+) n=(\d+) stream=(\S+|"[^"]*") ms=(\d+)`)

// TestTaskBatchCLI drives the verbs end to end on a loaded library: 1,000
// ids across 10 streams cancel from an @file in one call under a second, a
// refusal exits 1 naming the first bad id, move by --stream prints its
// destination, and sweep prints its counts.
func TestTaskBatchCLI(t *testing.T) {
	f := newSeat(t)
	f.as("a")
	t.Setenv("FRIEND_QUEUE_SPRINT", "")
	ctx := context.Background()
	c := f.client
	p := c.Pipeline()
	var ids []string
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("t%04d", i)
		stream := fmt.Sprintf("s%d", i%10)
		ids = append(ids, id)
		p.SAdd(ctx, "ws:names", stream)
		p.HSet(ctx, "task:"+id, "stream", stream, "state", "ready", "order", i, "owner", "a")
		p.ZAdd(ctx, "ws:"+stream+":ready", redis.Z{Score: float64(i), Member: id})
		p.SAdd(ctx, "sprint:"+seatSprint+":idx:a:open", id)
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("w%d", i)
		p.SAdd(ctx, "ws:names", "swarm: cards", "nova-sprint")
		p.HSet(ctx, "task:"+id, "stream", "swarm: cards", "state", "waiting", "order", i, "owner", "b")
		p.ZAdd(ctx, "ws:swarm: cards:waiting", redis.Z{Score: float64(i), Member: id})
	}
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "ids.txt")
	if err := os.WriteFile(file, []byte("# stale\n"+strings.Join(ids, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runTaskCLI("unblock", "--ids", "t0001,t0002")
	if code != 1 || errOut != "" || !strings.HasPrefix(out, "TASK unblock REFUSED id=t0001 why=unblock-from-ready ms=") {
		t.Fatalf("refusal = %d %q %q", code, out, errOut)
	}

	code, out, errOut = runTaskCLI("cancel", "--ids", "@"+file, "--why", "superseded")
	m := taskLine.FindStringSubmatch(strings.TrimSpace(out))
	if code != 0 || errOut != "" || m == nil || m[1] != "cancel" || m[2] != "1000" || m[3] != "*" {
		t.Fatalf("cancel = %d %q %q", code, out, errOut)
	}
	ms, _ := strconv.Atoi(m[4])
	t.Logf("CLI %s", strings.TrimSpace(out))
	if ms >= 1000 {
		t.Fatalf("1,000 cancels took %d ms", ms)
	}
	if n := c.SCard(ctx, "sprint:"+seatSprint+":idx:a:closed").Val(); n != 1000 {
		t.Fatalf("legacy closed %d", n)
	}
	for s := 0; s < 10; s++ {
		if n := c.ZCard(ctx, fmt.Sprintf("ws:s%d:ready", s)).Val(); n != 0 {
			t.Fatalf("ws:s%d:ready kept %d", s, n)
		}
	}

	code, out, _ = runTaskCLI("move", "--stream", "swarm: cards", "--to-stream", "nova-sprint")
	if code != 0 || !strings.HasPrefix(out, "TASK move n=3 stream=nova-sprint ms=") {
		t.Fatalf("move = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("block", "--stream", "nova-sprint", "--reason", "pit stop")
	if code != 0 || !strings.HasPrefix(out, "TASK block n=3 stream=nova-sprint ms=") {
		t.Fatalf("block waiting rows = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("sweep", "--friend", "a")
	if code != 0 || !regexp.MustCompile(`^TASK sweep n=0 stream=- ms=\d+ cancelled=0 waiting=0 skipped=0\n$`).MatchString(out) {
		t.Fatalf("sweep = %d %q", code, out)
	}
}
