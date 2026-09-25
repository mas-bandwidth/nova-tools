package consume

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// firstReadFixture is one sprint with a harvested card PR whose first head
// event (prev empty, source harvest) is on s:<S>:log, as ns_card_harvested
// writes it (#3739).
type firstReadFixture struct {
	t      *testing.T
	ctx    context.Context
	client *redis.Client
	pr     *PRRead
	out    *bytes.Buffer
	S      string
}

func newFirstReadFixture(t *testing.T, S string, policy ...string) *firstReadFixture {
	t.Helper()
	st, client := initTestRedis(t)
	ctx := context.Background()
	pipe := client.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", append([]string{"readers", "1", "readers_security", "2"}, policy...))
	pipe.SAdd(ctx, "friends", "rowan", "emma", "stella", "johnny", "jev")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return &firstReadFixture{t: t, ctx: ctx, client: client, out: out, S: S,
		pr: &PRRead{Store: st, Sprint: S, Consumer: "t-" + S, Instance: "t-" + S, Actor: "pr-to-read", Out: out}}
}

// up sets friend:<f> as the friend row writer does.
func (f *firstReadFixture) up(name, up string, ready, working int) {
	f.t.Helper()
	if err := f.client.HSet(f.ctx, "friend:"+name, "up", up, "ready", strconv.Itoa(ready),
		"working", strconv.Itoa(working), "at", "20260925T050600Z").Err(); err != nil {
		f.t.Fatal(err)
	}
}

// cardPR records a harvested card PR and its first `pr head` log entry.
func (f *firstReadFixture) cardPR(label, repo string, pr int, head, paths string) {
	f.t.Helper()
	n := strconv.Itoa(pr)
	pipe := f.client.TxPipeline()
	pipe.HSet(f.ctx, "s:"+f.S+":card:"+label, "state", "harvested", "repo", repo, "pr", n,
		"head", head, "stream", "swarm", "paths", paths)
	pipe.HSet(f.ctx, "s:"+f.S+":prcard", repo+"#"+n, label)
	pipe.XAdd(f.ctx, &redis.XAddArgs{Stream: "s:" + f.S + ":log", Values: []any{
		"kind", "pr head", "repo", repo, "pr", n, "head", head, "prev", "", "source", "harvest", "at", "1758776760000"}})
	if _, err := pipe.Exec(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

func (f *firstReadFixture) once() {
	f.t.Helper()
	if err := f.pr.Once(f.ctx); err != nil {
		f.t.Fatalf("pr-to-read once: %v", err)
	}
}

// queue is the task ids on q:<friend>.
func (f *firstReadFixture) queue(name string) []string {
	f.t.Helper()
	msgs, err := f.client.XRange(f.ctx, "q:"+name, "-", "+").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.Values["id"].(string))
	}
	return ids
}

func (f *firstReadFixture) logKinds(kind string) int {
	f.t.Helper()
	msgs, err := f.client.XRange(f.ctx, "s:"+f.S+":log", "-", "+").Result()
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Values["kind"] == kind {
			n++
		}
	}
	return n
}

// TestFirstReadQueuesLeastLoadedNonAuthor: the first head of a card PR with
// two UP friends and readers=1 queues exactly one read, to the least-loaded
// friend that is not the author (rowan, who opened it, and jev never read).
func TestFirstReadQueuesLeastLoadedNonAuthor(t *testing.T) {
	f := newFirstReadFixture(t, "fr-one")
	head := "0123456789abcdef0123456789abcdef01234567"
	f.up("rowan", "1", 0, 0) // author: least loaded, never a reader
	f.up("jev", "1", 0, 0)
	f.up("emma", "1", 3, 1)
	f.up("stella", "1", 0, 1)
	f.up("johnny", "0", 0, 0) // down
	f.cardPR("c-3726", "mas-bandwidth/nova-tools", 3726, head, "internal/x.go")
	f.once()

	id := "read-3726-01234567"
	if got := f.queue("stella"); len(got) != 1 || got[0] != id {
		t.Fatalf("q:stella = %v, want [%s]", got, id)
	}
	for _, other := range []string{"rowan", "jev", "emma", "johnny"} {
		if got := f.queue(other); len(got) != 0 {
			t.Fatalf("q:%s = %v, want empty", other, got)
		}
	}
	task, _ := f.client.HGetAll(f.ctx, "task:"+id).Result()
	want := map[string]string{
		"kind": "read", "owner": "stella", "state": "open", "head": head, "pr": "3726",
		"ref":   prURL(nil, "mas-bandwidth/nova-tools", "3726"),
		"title": "STREAM: swarm | read nova-tools#3726 at 01234567 (c-3726)",
	}
	for k, v := range want {
		if task[k] != v {
			t.Fatalf("task:%s %s = %q, want %q (%v)", id, k, task[k], v, task)
		}
	}
	if !strings.HasSuffix(task["ref"], "/mas-bandwidth/nova-tools/pull/3726") {
		t.Fatalf("task:%s ref = %q, want the PR's web URL", id, task["ref"])
	}
	if ok, _ := f.client.SIsMember(f.ctx, "sprint:fr-one:idx:stella:open", id).Result(); !ok {
		t.Fatalf("%s not in sprint:fr-one:idx:stella:open", id)
	}
	if !strings.Contains(f.out.String(), "READ QUEUED n=3726 head=01234567 to=stella\n") {
		t.Fatalf("receipt missing: %q", f.out.String())
	}
	if n := f.logKinds("read queued"); n != 1 {
		t.Fatalf("read queued log entries = %d, want 1", n)
	}
	if n, _ := f.client.ZCard(f.ctx, "s:fr-one:reads:pending").Result(); n != 0 {
		t.Fatalf("reads pending = %d, want 0", n)
	}

	// A second pass (and a redelivery of the same event) adds nothing.
	f.out.Reset()
	f.once()
	if got := f.queue("stella"); len(got) != 1 {
		t.Fatalf("second pass: q:stella = %v, want one entry", got)
	}
	if n := f.logKinds("read queued"); n != 1 {
		t.Fatalf("second pass: read queued log entries = %d, want 1", n)
	}
	if f.out.Len() != 0 {
		t.Fatalf("second pass printed %q", f.out.String())
	}
}

// TestFirstReadSecurityPathTwoFriends: a card touching a security path needs
// readers_security distinct friends.
func TestFirstReadSecurityPathTwoFriends(t *testing.T) {
	f := newFirstReadFixture(t, "fr-sec", "security_paths", "internal/secrets")
	head := "abcdef0123456789abcdef0123456789abcdef01"
	f.up("rowan", "1", 0, 0)
	f.up("emma", "1", 2, 0)
	f.up("stella", "1", 1, 0)
	f.up("johnny", "1", 5, 5)
	f.cardPR("c-3727", "nova-tools", 3727, head, "internal/secrets/store.go")
	f.once()

	stella, emma, johnny := f.queue("stella"), f.queue("emma"), f.queue("johnny")
	if len(stella) != 1 || stella[0] != "read-3727-abcdef01" {
		t.Fatalf("q:stella = %v, want [read-3727-abcdef01]", stella)
	}
	if len(emma) != 1 || emma[0] != "read-3727-abcdef01-emma" {
		t.Fatalf("q:emma = %v, want [read-3727-abcdef01-emma]", emma)
	}
	if len(johnny) != 0 || len(f.queue("rowan")) != 0 {
		t.Fatalf("q:johnny = %v, q:rowan = %v, want both empty", johnny, f.queue("rowan"))
	}
	if n := f.logKinds("read queued"); n != 2 {
		t.Fatalf("read queued log entries = %d, want 2", n)
	}
}

// TestFirstReadPendingUntilAFriendIsUp: with nobody up the PR stays in
// s:<S>:reads:pending with one READ-PENDING record; a later pass with a
// friend up drains it. With only Emma up (Rowan the author) Emma reads.
func TestFirstReadPendingUntilAFriendIsUp(t *testing.T) {
	f := newFirstReadFixture(t, "fr-pend")
	head := "fedcba9876543210fedcba9876543210fedcba98"
	f.up("rowan", "1", 0, 0) // the author, up: still no reader
	f.up("emma", "0", 0, 0)
	f.cardPR("c-3728", "mas-bandwidth/nova-tools", 3728, head, "internal/x.go")
	f.once()

	if n, _ := f.client.ZCard(f.ctx, "s:fr-pend:reads:pending").Result(); n != 1 {
		t.Fatalf("reads pending = %d, want 1", n)
	}
	if score, err := f.client.ZScore(f.ctx, "s:fr-pend:reads:pending",
		"mas-bandwidth/nova-tools#3728@"+head).Result(); err != nil || score != 1758776760000 {
		t.Fatalf("pending score = %v (%v), want the event time", score, err)
	}
	if got := f.out.String(); got != "READ-PENDING n=3728 need=1 have=0\n" {
		t.Fatalf("output = %q, want one READ-PENDING line", got)
	}
	f.out.Reset()
	f.once()
	if f.out.Len() != 0 || f.logKinds("read pending") != 1 {
		t.Fatalf("second pass printed %q; read pending log entries %d, want none and 1", f.out.String(), f.logKinds("read pending"))
	}

	f.up("emma", "1", 4, 2)
	f.once()
	if got := f.queue("emma"); len(got) != 1 || got[0] != "read-3728-fedcba98" {
		t.Fatalf("q:emma = %v, want [read-3728-fedcba98]", got)
	}
	if got := f.queue("rowan"); len(got) != 0 {
		t.Fatalf("q:rowan = %v, want empty (the author)", got)
	}
	if n, _ := f.client.ZCard(f.ctx, "s:fr-pend:reads:pending").Result(); n != 0 {
		t.Fatalf("reads pending = %d after emma came up, want 0", n)
	}
	if !strings.Contains(f.out.String(), "READ QUEUED n=3728 head=fedcba98 to=emma\n") {
		t.Fatalf("receipt missing: %q", f.out.String())
	}
}

// TestFirstReadNotACardPR: a first head of a PR no card produced stays a NOOP.
func TestFirstReadNotACardPR(t *testing.T) {
	f := newFirstReadFixture(t, "fr-none")
	f.up("emma", "1", 0, 0)
	if err := f.client.XAdd(f.ctx, &redis.XAddArgs{Stream: "s:fr-none:log", Values: []any{
		"kind", "pr head", "repo", "nova-tools", "pr", "9", "head", strings.Repeat("9", 40),
		"prev", "", "source", "ls-remote", "at", "1"}}).Err(); err != nil {
		t.Fatal(err)
	}
	f.once()
	if got := f.queue("emma"); len(got) != 0 {
		t.Fatalf("q:emma = %v, want empty", got)
	}
	if n, _ := f.client.ZCard(f.ctx, "s:fr-none:reads:pending").Result(); n != 0 {
		t.Fatalf("reads pending = %d, want 0", n)
	}
	pending, err := f.client.XPending(f.ctx, "s:fr-none:log", RulePRToRead).Result()
	if err != nil || pending.Count != 0 {
		t.Fatalf("pr-to-read pending = %+v (%v), want acked", pending, err)
	}
}
