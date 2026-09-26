//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFriendPullDoneBeat is #4233's friend path from the friend's own
// session: `friend pull --as friend:rowan` takes the dealt copy into
// working (card work) and writes the person's brief to --dir, one PULLED
// line naming the path and the token; `friend beat --once` writes the
// friend's beat (host, at, load) and row and renews the copy's lease;
// `friend done --ok --pr --head` ends the copy (card end) and the primary
// moves to review. A copy that is not the friend's is NOTMINE; a bench or
// bare --as is refused; a stale token is FENCED, exit 3.
func TestFriendPullDoneBeat(t *testing.T) {
	t.Parallel() // every call names --redis; the friend is the seat's name when one is set
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
	rowan, _ := taskcard.ParseConsumer("friend:" + me)
	emma, _ := taskcard.ParseConsumer("friend:emma")
	c.HSet(ctx, rowan.DesiredKey(), "slots", "2")
	c.HSet(ctx, emma.DesiredKey(), "slots", "1")
	base := strings.Repeat("ab", 20)
	for _, id := range []string{"q1", "q2"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: "swarm: cards", Kind: "build",
			Title: "quack " + id, Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#4233", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", base, "paths", "cmd/nova-sprint/friend_copies.go",
				"done_when", "go test ./cmd/nova-sprint -run TestFriendPullDoneBeat passes", "body", "the issue"}}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: rowan, N: 1, By: "reconciler"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal to rowan: %v %v", d, err)
	}
	other, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: emma, N: 1, By: "reconciler"})
	if err != nil || len(other) != 1 {
		t.Fatalf("deal to emma: %v %v", other, err)
	}
	mine, theirs := d[0].Copy, other[0].Copy
	dir := t.TempDir()
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runFriend(ctx, append(args, "--redis", addr), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	for _, bad := range [][]string{{"pull", "--as", "rowan"}, {"pull", "--as", "bench:b"}, {"beat", "--as", "rowan", "--once"},
		{"done", "--as", "rowan", "--id", mine, "--ok"}, {"done", "--as", "friend:" + me, "--id", "q1", "--ok"},
		{"done", "--as", "friend:" + me, "--id", mine}} {
		if code, out, errOut := run(bad...); code == 0 || !strings.HasPrefix(errOut, "nova-sprint friend ") {
			t.Fatalf("%v: exit %d %q %q; want a refusal that prints", bad, code, out, errOut)
		}
	}

	code, out, errOut := run("pull", "--as", "friend:"+me, "--dir", dir)
	if code != 0 {
		t.Fatalf("friend pull exit %d: %s%s", code, out, errOut)
	}
	pulled := regexp.MustCompile(`(?m)^PULLED (\S+) leg=work token=(\S+) card=(\S+)$`).FindStringSubmatch(out)
	if pulled == nil || pulled[1] != mine || !strings.HasSuffix(out, "FRIEND PULL as=friend:"+me+" n=1 free=1 dir="+dir+" ms="+lastNumber(out)+"\n") {
		t.Fatalf("friend pull receipt:\n%s", out)
	}
	token, path := pulled[2], pulled[3]
	if filepath.Dir(path) != dir {
		t.Fatalf("card written outside --dir: %s", path)
	}
	brief, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\nCOPY: " + mine + "\n", "FRIEND: friend:" + me + " owns this copy end to end",
		"nova-sprint friend done --as friend:" + me + " --id " + mine + " --ok --pr nova-tools#<n> --head <sha>", "\n> the issue\n"} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("brief lacks %q:\n%s", want, brief)
		}
	}
	rec := c.HGetAll(ctx, taskcard.Key(mine)).Val()
	if rec["where"] != "working" || rec["token"] != token {
		t.Fatalf("after pull task:%s = %v", mine, rec)
	}
	if code, out, _ := run("pull", "--as", "friend:"+me, "--dir", dir); code != 0 || !strings.Contains(out, "FRIEND PULL as=friend:"+me+" n=0 free=1") {
		t.Fatalf("a second pull with nothing ready: exit %d %s", code, out)
	}

	before, _ := strconv.ParseInt(rec["lease_until"], 10, 64)
	c.HSet(ctx, taskcard.Key(mine), "lease_until", strconv.FormatInt(before-1000, 10))
	code, out, errOut = run("beat", "--as", "friend:"+me, "--host", "laptop", "--once")
	if code != 0 || !regexp.MustCompile(`^FRIEND BEAT as=friend:`+me+` host=laptop working=1 lease_until=\d+ at=\d+\n$`).MatchString(out) {
		t.Fatalf("friend beat exit %d %q %s", code, out, errOut)
	}
	if n := c.Exists(ctx, "friend:"+me).Val(); n != 0 {
		t.Fatalf("friend beat wrote the friend:%s row; ns_friend_row is its one writer", me)
	}
	beat := c.HGetAll(ctx, "friend:"+me+":beat").Val()
	if beat["host"] != "laptop" || beat["at"] == "" || beat["ncpu"] == "" || beat["harness"] != "friend beat" {
		t.Fatalf("%s:beat = %v", rowan, beat)
	}
	if ttl := c.TTL(ctx, "friend:"+me+":beat").Val(); ttl > 0 {
		t.Fatalf("%s:beat expires in %s; keys do not expire", rowan, ttl)
	}
	after, _ := strconv.ParseInt(c.HGet(ctx, taskcard.Key(mine), "lease_until").Val(), 10, 64)
	if after < before {
		t.Fatalf("lease not renewed: %d -> %d", before, after)
	}

	if code, out, _ := run("done", "--as", "friend:"+me, "--id", theirs, "--ok"); code != 1 || !strings.Contains(out, "FRIEND DONE REFUSED id="+theirs+" why=\"NOTMINE ") {
		t.Fatalf("ending emma's copy: exit %d %s", code, out)
	}
	if code, out, _ := run("done", "--as", "friend:"+me, "--id", mine, "--ok", "--token", "stale@1"); code != 3 || !strings.Contains(out, "why=\"FENCED ") {
		t.Fatalf("a stale token: exit %d %s", code, out)
	}
	// nothing pre-records the PR: friend done --ok --pr records it (as the
	// wrapper's harvest does) before the end, which card end would refuse
	// NOPR without the record
	head := strings.Repeat("cd", 20)
	code, out, errOut = run("done", "--as", "friend:"+me, "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", head, "--token", token)
	if code != 0 || !strings.HasPrefix(out, "RECORDED pr=nova-tools#4400 head="+head+" branch=nova/copies/q1-c1-a1\nENDED "+mine+" primary=q1 from=working to=review next=-\n") ||
		!strings.Contains(out, "FRIEND DONE as=friend:"+me+" n=1 ") {
		t.Fatalf("friend done exit %d %q %s", code, out, errOut)
	}
	pr := c.HGetAll(ctx, "pr:nova-tools:4400").Val()
	if pr["head"] != head || pr["base"] != "dev" || pr["branch"] != "nova/copies/q1-c1-a1" || pr["task"] != "q1" || pr["state"] != "open" || pr["ci"] != "pending" {
		t.Fatalf("pr:nova-tools:4400 = %v", pr)
	}
	if n := c.ZCard(ctx, rowan.Key("ok")).Val(); n != 1 {
		t.Fatalf("%s ok = %d", rowan.Key("ok"), n)
	}
	if got := c.HGet(ctx, taskcard.Key("q1"), "where").Val(); got != "review" {
		t.Fatalf("q1 is %s, want review", got)
	}
	if code, out, _ := run("done", "--as", "friend:"+me, "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", head); code != 0 || !strings.Contains(out, "ALREADY "+mine+" ") {
		t.Fatalf("the same end again: exit %d %s", code, out)
	}
}

// lastNumber is the ms= value of the receipt line that ends out.
func lastNumber(out string) string {
	m := regexp.MustCompile(`ms=(\d+)\n$`).FindStringSubmatch(out)
	if m == nil {
		return "?"
	}
	return m[1]
}
