package read_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// eventTask is task:<id> in ws:<stream>:<state>, scored by its age, indexed
// under the refs its fields name (ns_task_refs), with the stream registered.
func eventTask(t *testing.T, c *redis.Client, id, stream, state string, age int64, fields ...string) {
	t.Helper()
	ctx := context.Background()
	h := append([]string{"stream", stream, "state", state, "created_at", fmt.Sprint(age)}, fields...)
	pipe := c.TxPipeline()
	pipe.HSet(ctx, "task:"+id, h)
	pipe.ZAdd(ctx, ws.Key(stream, state), redis.Z{Score: float64(age), Member: id})
	pipe.SAdd(ctx, "ws:names", stream)
	pipe.ZAddNX(ctx, "ws:order", redis.Z{Score: 1, Member: stream})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.FCall(ctx, "ns_task_refs", nil, id).Err(); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, c *redis.Client, id string) string {
	t.Helper()
	return c.HGet(context.Background(), "task:"+id, "state").Val()
}

// TestReadPostEventsMoveTasks (nova-tools#3779): a reader's SCORE line moves
// the PR's read task working -> merging; a person's CLOSE line (a hand
// landing) lands every task naming the PR or an issue its record closes,
// why the line's "with <repo>#<n> (<sha>)"; the ws sets agree with every
// record after each post (ws.Check), and a jev CLOSE moves nothing.
func TestReadPostEventsMoveTasks(t *testing.T) {
	t.Parallel()

	_, base, head := mirrorFixture(t, false)
	c := client(t)
	ctx := context.Background()
	seedRecord(t, c, base, head)
	if err := c.HSet(ctx, read.Key("nova-tools", "7"), "closes", "44").Err(); err != nil {
		t.Fatal(err)
	}
	const s = "landing: streams + lander"
	read8 := "read-7-" + head[:8]
	eventTask(t, c, "build-7-thing", s, "merging", 100, "pr", "7", "repo", "mas-bandwidth/nova-tools")
	// the read task is the reader's (a friend take): a SCORE moves a
	// friend-held task; a primary no friend holds enters merging only as a
	// read copy's card end (#3929)
	eventTask(t, c, read8, s, "working", 200, "ref", "nova-tools#7", "friend", "emma", "owner", "emma")
	eventTask(t, c, "build-44-issue", "swarm: cards", "working", 300, "ref", "https://forge.invalid/mas-bandwidth/nova-tools/issues/44")
	eventTask(t, c, "build-45-other", "swarm: cards", "working", 400, "ref", "nova-tools#45")
	ids := []string{"build-7-thing", read8, "build-44-issue", "build-45-other"}
	check := func(step string) {
		t.Helper()
		if err := ws.Check(ctx, c, ids); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	check("seed")
	post := func(l string) string {
		t.Helper()
		var stdout, stderr strings.Builder
		if code := read.Post(ctx, c, "nova-tools", "7", l, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("post %q: exit %d stderr %q", l, code, stderr.String())
		}
		return stdout.String()
	}

	out := post("SCORE who=emma head=" + head + " score=9/10 gates=ci:ok,base:ok,scope:ok")
	if !strings.Contains(out, "kind=SCORE lines=2 github_calls=0 tasks_moved=1") || stateOf(t, c, read8) != "merging" {
		t.Fatalf("SCORE: %q, read task %s", out, stateOf(t, c, read8))
	}
	check("score")

	out = post("CLOSE who=jev head=" + head[:8] + " landed: in dev at " + head[:8])
	if !strings.Contains(out, "kind=CLOSE lines=3 github_calls=0 tasks_moved=0") || stateOf(t, c, "build-7-thing") != "merging" {
		t.Fatalf("jev CLOSE moved: %q", out)
	}

	merge := strings.Repeat("e", 8)
	out = post("CLOSE who=rowan head=" + head[:8] + " landed: in dev at " + head[:8] + " with nova-tools#7 (" + merge + ")")
	if !strings.Contains(out, "kind=CLOSE lines=4 github_calls=0 tasks_moved=3") {
		t.Fatalf("CLOSE: %q", out)
	}
	for _, id := range []string{"build-7-thing", read8, "build-44-issue"} {
		if st := stateOf(t, c, id); st != "landed" {
			t.Fatalf("%s is %s after the CLOSE, want landed", id, st)
		}
		if why := c.HGet(ctx, "task:"+id, "why").Val(); why != "landed with nova-tools#7 ("+merge+")" {
			t.Fatalf("%s why %q", id, why)
		}
	}
	if st := stateOf(t, c, "build-45-other"); st != "working" {
		t.Fatalf("a task naming another issue moved: %s", st)
	}
	if n := c.ZCard(ctx, ws.Key(s, "landed")).Val(); n != 2 {
		t.Fatalf("ws:%s:landed = %d, want 2", s, n)
	}
	check("close")

	// A repeat CLOSE moves nothing twice.
	out = post("CLOSE who=rowan head=" + head[:8] + " landed: in dev at " + head[:8] + " with nova-tools#7 (" + merge + ")")
	if !strings.Contains(out, "tasks_moved=0") {
		t.Fatalf("repeat CLOSE: %q", out)
	}
	check("repeat")
}
