package consume

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// TestControl52 is #2756 control 52 (#3093, spec 3.1, 10.12).
// A review task for a PENDING head is in waiting-ci and in no open queue.
// The OK end moves it to the reader in the same call, and the read-enqueued
// clock is that call's time. A FAIL cancels the reviews and cuts exactly one
// fix task. A head change does the same for the old head.
func TestControl52(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	const (
		sprint = "control-c52abcd"
		repo   = "nova-tools"
		pr     = 3093
		reader = "ctl-read"
		other  = "ctl-readb"
		author = "ctl-author"
	)
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, reader, 4)
	seedFriend(t, client, other, 4)
	seedFriend(t, client, author, 4)

	headOK := strings.Repeat("a", 40)
	headFail := strings.Repeat("b", 40)
	headOld := strings.Repeat("c", 40)
	headNew := strings.Repeat("d", 40)

	t.Run("pending then OK", func(t *testing.T) {
		setHead(t, client, sprint, repo, pr, headOK)
		setVerdict(t, client, repo, headOK, "PENDING")
		id := task.ReviewID(repo, pr, headOK, reader)
		mode, err := PlaceReviews(ctx, st, PlaceRequest{
			Sprint: sprint, Repo: repo, PR: pr, Head: headOK,
			Readers: []string{reader}, Priority: 1,
		})
		if err != nil {
			t.Fatalf("place: %v", err)
		}
		if mode != "waiting-ci" {
			t.Fatalf("mode = %s; want waiting-ci", mode)
		}
		if got := hget(t, client, taskKey(sprint, id), "state"); got != "waiting-ci" {
			t.Fatalf("state = %q; want waiting-ci", got)
		}
		if got := hget(t, client, taskKey(sprint, id), "enqueued_at"); got != "" {
			t.Fatalf("enqueued_at = %q before OK; the read clock must not have started", got)
		}
		if !member(t, client, "s:"+sprint+":waiting-ci", id) {
			t.Fatal("review is not in waiting-ci")
		}
		assertNotOpen(t, client, sprint, id)
		claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: id, As: reader})
		if err != nil {
			t.Fatalf("take while waiting: %v", err)
		}
		if ok {
			t.Fatalf("waiting review was claimable: %+v", claim)
		}

		if err := CIEnd(ctx, st, EndRequest{
			Sprint: sprint, Repo: repo, PR: pr, Head: headOK, Verdict: "OK", Author: author,
		}); err != nil {
			t.Fatalf("ci end OK: %v", err)
		}
		if got := hget(t, client, "ci:"+repo+":"+headOK, "verdict"); got != "OK" {
			t.Fatalf("verdict = %q; want OK", got)
		}
		if got := hget(t, client, taskKey(sprint, id), "state"); got != "open" {
			t.Fatalf("state after OK = %q; want open", got)
		}
		if member(t, client, "s:"+sprint+":waiting-ci", id) {
			t.Fatal("OK left the review in waiting-ci")
		}
		if _, err := client.ZScore(ctx, "s:"+sprint+":open:"+reader, id).Result(); err != nil {
			t.Fatalf("OK did not open the review to %s: %v", reader, err)
		}
		assertNotOpen(t, client, sprint, id, reader)
		enqueued := hget(t, client, taskKey(sprint, id), "enqueued_at")
		ended := hget(t, client, "ci:"+repo+":"+headOK, "end_at")
		openedAt := receiptAt(t, client, sprint, "task", id, "open")
		ciAt := receiptAt(t, client, sprint, "ci end", repo+":"+headOK, "OK")
		if enqueued == "" || enqueued != ended || enqueued != openedAt || enqueued != ciAt {
			t.Fatalf("read clock = enqueued %q end_at %q open receipt %q ci receipt %q; want one TIME from the OK call",
				enqueued, ended, openedAt, ciAt)
		}
		if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: id, As: reader}); err != nil || !ok {
			t.Fatalf("reader take after OK: ok=%v err=%v", ok, err)
		}
		if n := kindCount(t, client, sprint, "fix"); n != 0 {
			t.Fatalf("OK cut %d fix tasks; want none", n)
		}
	})

	t.Run("FAIL cuts one fix", func(t *testing.T) {
		setHead(t, client, sprint, repo, pr, headFail)
		setVerdict(t, client, repo, headFail, "PENDING")
		ids := []string{task.ReviewID(repo, pr, headFail, reader), task.ReviewID(repo, pr, headFail, other)}
		if _, err := PlaceReviews(ctx, st, PlaceRequest{
			Sprint: sprint, Repo: repo, PR: pr, Head: headFail,
			Readers: []string{reader, other}, Priority: 1,
		}); err != nil {
			t.Fatalf("place: %v", err)
		}
		if err := CIEnd(ctx, st, EndRequest{
			Sprint: sprint, Repo: repo, PR: pr, Head: headFail, Verdict: "FAIL",
			Pkg: "internal/nsprint", Author: author,
		}); err != nil {
			t.Fatalf("ci end FAIL: %v", err)
		}
		for _, id := range ids {
			if got := hget(t, client, taskKey(sprint, id), "state"); got != "cancelled" {
				t.Fatalf("%s state = %q; want cancelled", id, got)
			}
			if member(t, client, "s:"+sprint+":waiting-ci", id) {
				t.Fatalf("%s still in waiting-ci", id)
			}
			assertNotOpen(t, client, sprint, id)
		}
		want := fixID(repo, pr, headFail, "internal/nsprint")
		if got := hget(t, client, taskKey(sprint, want), "kind"); got != "fix" {
			t.Fatalf("fix kind = %q; want fix task %s", got, want)
		}
		if _, err := client.ZScore(ctx, "s:"+sprint+":open:"+author, want).Result(); err != nil {
			t.Fatalf("fix task is not on the author's queue: %v", err)
		}
		if n := kindCount(t, client, sprint, "fix"); n != 1 {
			t.Fatalf("fix tasks = %d; want exactly one", n)
		}
		if n := unresolvedCount(t, client, sprint, "ci-fail:"); n != 1 {
			t.Fatalf("ci-fail dedup keys = %d; want one", n)
		}
		if err := CIEnd(ctx, st, EndRequest{
			Sprint: sprint, Repo: repo, PR: pr, Head: headFail, Verdict: "FAIL",
			Pkg: "internal/nsprint", Author: author,
		}); err != nil {
			t.Fatalf("second FAIL: %v", err)
		}
		if n := kindCount(t, client, sprint, "fix"); n != 1 {
			t.Fatalf("second FAIL cut another fix; count = %d", n)
		}
	})

	t.Run("head change cuts one fix", func(t *testing.T) {
		setHead(t, client, sprint, repo, pr, headOld)
		setVerdict(t, client, repo, headOld, "PENDING")
		id := task.ReviewID(repo, pr, headOld, reader)
		if _, err := PlaceReviews(ctx, st, PlaceRequest{
			Sprint: sprint, Repo: repo, PR: pr, Head: headOld,
			Readers: []string{reader}, Priority: 1,
		}); err != nil {
			t.Fatalf("place: %v", err)
		}
		before := kindCount(t, client, sprint, "fix")
		if err := HeadChange(ctx, st, HeadChangeRequest{
			Sprint: sprint, Repo: repo, PR: pr, OldHead: headOld, NewHead: headNew, Author: author,
		}); err != nil {
			t.Fatalf("head change: %v", err)
		}
		if got := hget(t, client, taskKey(sprint, id), "state"); got != "cancelled" {
			t.Fatalf("state = %q; want cancelled", got)
		}
		if got := hget(t, client, taskKey(sprint, id), "reason"); got != "head-changed" {
			t.Fatalf("reason = %q; want head-changed", got)
		}
		assertNotOpen(t, client, sprint, id)
		want := fixID(repo, pr, headOld, HeadChangedPkg)
		if _, err := client.ZScore(ctx, "s:"+sprint+":open:"+author, want).Result(); err != nil {
			t.Fatalf("head-change fix is not on the author's queue: %v", err)
		}
		if n := kindCount(t, client, sprint, "fix"); n != before+1 {
			t.Fatalf("fix tasks = %d; want %d", n, before+1)
		}
		if hget(t, client, "ci:"+repo+":"+headOld, "verdict") != "PENDING" {
			t.Fatal("head change wrote the ci verdict")
		}
		if err := HeadChange(ctx, st, HeadChangeRequest{
			Sprint: sprint, Repo: repo, PR: pr, OldHead: headOld, NewHead: headNew, Author: author,
		}); err != nil {
			t.Fatalf("second head change: %v", err)
		}
		if n := kindCount(t, client, sprint, "fix"); n != before+1 {
			t.Fatalf("second head change cut another fix; count = %d", n)
		}
	})
}

func fixID(repo string, pr int, head, pkg string) string {
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	return "fix-" + repo + "-" + strconv.Itoa(pr) + "-" + short + "-" + pkg
}

func taskKey(sprint, id string) string { return "s:" + sprint + ":task:" + id }

func setHead(t *testing.T, client *redis.Client, sprint, repo string, pr int, head string) {
	t.Helper()
	if err := client.HSet(context.Background(), "s:"+sprint+":pr:"+repo+":"+strconv.Itoa(pr), "head", head).Err(); err != nil {
		t.Fatal(err)
	}
}

func setVerdict(t *testing.T, client *redis.Client, repo, head, verdict string) {
	t.Helper()
	if err := client.HSet(context.Background(), "ci:"+repo+":"+head, "verdict", verdict, "head", head).Err(); err != nil {
		t.Fatal(err)
	}
}

func hget(t *testing.T, client *redis.Client, key, field string) string {
	t.Helper()
	v, err := client.HGet(context.Background(), key, field).Result()
	if err == redis.Nil {
		return ""
	}
	if err != nil {
		t.Fatalf("HGET %s %s: %v", key, field, err)
	}
	return v
}

func member(t *testing.T, client *redis.Client, key, id string) bool {
	t.Helper()
	ok, err := client.SIsMember(context.Background(), key, id).Result()
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// assertNotOpen fails if id sits on ready or on any open queue. skip queues
// are the ones a successful open is allowed to use.
func assertNotOpen(t *testing.T, client *redis.Client, sprint, id string, skip ...string) {
	t.Helper()
	ctx := context.Background()
	if err := client.ZScore(ctx, "s:"+sprint+":ready", id).Err(); err != redis.Nil {
		t.Fatalf("task %s is on ready (%v); want no ready place", id, err)
	}
	allowed := map[string]bool{}
	for _, name := range skip {
		allowed["s:"+sprint+":open:"+name] = true
	}
	keys, err := client.Keys(ctx, "s:"+sprint+":open:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if allowed[key] {
			continue
		}
		err := client.ZScore(ctx, key, id).Err()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("task %s is in open queue %s", id, key)
	}
}

func kindCount(t *testing.T, client *redis.Client, sprint, kind string) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	for _, state := range []string{"open", "waiting-ci", "claimed", "working", "closed", "cancelled"} {
		ids, err := client.SMembers(ctx, "s:"+sprint+":idx:task:"+state).Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if hget(t, client, taskKey(sprint, id), "kind") == kind {
				n++
			}
		}
	}
	return n
}

func unresolvedCount(t *testing.T, client *redis.Client, sprint, prefix string) int {
	t.Helper()
	fields, err := client.HKeys(context.Background(), "s:"+sprint+":unresolved").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, field := range fields {
		if strings.Contains(field, prefix) {
			n++
		}
	}
	return n
}

func receiptAt(t *testing.T, client *redis.Client, sprint, kind, id, to string) string {
	t.Helper()
	messages, err := client.XRange(context.Background(), "s:"+sprint+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, message := range messages {
		if message.Values["kind"] == kind && message.Values["id"] == id && message.Values["to"] == to {
			at, _ := message.Values["at"].(string)
			found = at
		}
	}
	if found == "" {
		t.Fatalf("no %s receipt for %s to %s", kind, id, to)
	}
	return found
}

func seedFriend(t *testing.T, client *redis.Client, friend string, slots int) {
	t.Helper()
	ctx := context.Background()
	if err := client.SAdd(ctx, "friends", friend).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":desired", "slots", slots, "paused", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":beat", "host", "fixture").Err(); err != nil {
		t.Fatal(err)
	}
}

func controlRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

func startRedis(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	port := strings.TrimPrefix(addr, "127.0.0.1:")
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("throwaway redis did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
