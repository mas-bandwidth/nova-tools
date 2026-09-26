//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFriendPullKeepsBeatAndNamesWorker (seat-keeps-beat) on a throwaway
// store: a real `friend pull --model --harness --child` records who works
// the copy on its record, writes the brief with its WORKER line and starts
// the friend's one beat loop (friend beat --loop --lease <the claimed
// token>); a second pull finds the loop's lease and starts none. `card
// render --id <copy>` prints the WORKER line; `friend beat --once` writes
// the beat's models; the live table's friend row reads up with the model
// beside the name.
func TestFriendPullKeepsBeatAndNamesWorker(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	me := os.Getenv(seatEnv)
	if me == "" {
		me = "rowan"
	}
	k, _ := taskcard.ParseConsumer("friend:" + me)
	c.HSet(ctx, k.DesiredKey(), "slots", "2")
	c.SAdd(ctx, "friends", me)
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "console-grammar", Where: "waiting", Stream: "friends", Kind: "build",
		Title: "console grammar", Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#4400", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", strings.Repeat("ab", 20), "paths", "cmd/nova-sprint/friend_copies.go",
			"done_when", "the friend row reads up", "body", "the issue"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, By: "reconciler"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal: %v %v", d, err)
	}
	cp := d[0].Copy
	run := func(verb func(context.Context, []string, *bytes.Buffer, *bytes.Buffer) int, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := verb(ctx, append(args, "--redis", addr), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	friend := func(ctx context.Context, a []string, o, e *bytes.Buffer) int { return runFriend(ctx, a, o, e) }
	cardMove := func(ctx context.Context, a []string, o, e *bytes.Buffer) int {
		return runCardMove(ctx, a[0], a[1:], o, e)
	}

	dir := t.TempDir()
	code, out, errOut := run(friend, "pull", "--as", k.String(), "--dir", dir, "--model", "opus-5.5", "--harness", "claude-code", "--child", "c7")
	if code != 0 || !strings.Contains(out, "\nBEATLOOP as="+k.String()+" started pid=4242 working=1\nFRIEND PULL ") {
		t.Fatalf("friend pull exit %d:\n%s%s", code, out, errOut)
	}
	starts := startsFor(addr)
	token := c.Get(ctx, life.BeatLoopKey(me)).Val()
	if len(starts) != 1 || token == "" || strings.Join(starts[0][1:], " ") != "friend beat --as "+k.String()+" --loop --lease "+token+" --redis "+addr {
		t.Fatalf("loops started %v, lease %q", starts, token)
	}
	rec := c.HGetAll(ctx, taskcard.Key(cp)).Val()
	if rec["where"] != "working" || rec["model"] != "opus-5.5" || rec["harness"] != "claude-code" || rec["child"] != "c7" {
		t.Fatalf("task:%s = %v", cp, rec)
	}
	const worker = "\nWORKER: model=opus-5.5 harness=claude-code child=c7\n"
	path := regexp.MustCompile(`(?m)^PULLED \S+ leg=work token=\S+ card=(\S+)$`).FindStringSubmatch(out)
	if path == nil {
		t.Fatalf("no PULLED line:\n%s", out)
	}
	if brief, err := os.ReadFile(path[1]); err != nil || !strings.Contains(string(brief), worker) {
		t.Fatalf("brief lacks %q (%v):\n%s", worker, err, brief)
	}
	if code, out, _ := run(friend, "pull", "--as", k.String(), "--dir", dir); code != 0 ||
		!strings.Contains(out, "BEATLOOP as="+k.String()+" running working=1\n") || len(startsFor(addr)) != 1 {
		t.Fatalf("second pull exit %d, %d starts:\n%s", code, len(startsFor(addr)), out)
	}

	code, out, errOut = run(cardMove, "render", "--id", cp)
	if code != 0 || !strings.Contains(out, worker) {
		t.Fatalf("card render exit %d lacks %q:\n%s%s", code, worker, out, errOut)
	}

	if code, out, errOut := run(friend, "beat", "--as", k.String(), "--host", "laptop", "--once"); code != 0 {
		t.Fatalf("friend beat exit %d %s%s", code, out, errOut)
	}
	if got := c.HGet(ctx, "friend:"+me+":beat", "models").Val(); got != "opus-5.5" {
		t.Fatalf("beat models = %q", got)
	}
	snap, err := table.NewSprintReader(c, table.SprintConfig{Friends: []string{me}}).Read(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(me) + ` opus-5.5 +\|     0 \|       1 \|     0 \|    - \| up     \| `).FindString(snap.Render(time.Now()))
	if row == "" {
		t.Fatalf("no up row with the model for %s:\n%s", me, snap.Render(time.Now()))
	}
}
