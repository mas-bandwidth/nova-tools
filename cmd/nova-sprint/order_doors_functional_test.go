//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// storeSnapshot is what a refused push must leave unchanged: DBSIZE, the
// length of ws:log and every sorted set with its scores (a throwaway store:
// KEYS is fine).
func storeSnapshot(t *testing.T, c *redis.Client) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	fmt.Fprintf(&b, "keys=%d ws:log=%d\n", c.DBSize(ctx).Val(), c.XLen(ctx, "ws:log").Val())
	keys := c.Keys(ctx, "*").Val()
	sort.Strings(keys)
	for _, k := range keys {
		if c.Type(ctx, k).Val() == "zset" {
			fmt.Fprintf(&b, "%s %v\n", k, c.ZRangeWithScores(ctx, k, 0, -1).Val())
		}
	}
	return b.String()
}

// TestOrderDoorsCLI is the #4322 fix round's push and move doors on a
// throwaway store. (1) A push that closes a DEPENDS-ON cycle is REFUSED and
// writes nothing (keys, ws:log length and every ZSET unchanged), in each
// form: task push --actor, friend-queue task push (no --actor), card push
// (a batch cycle, refused whole before any write, and a cycle through a
// live card, refused by ns_card_push itself).
// (5) A friend-queue task push onto a stream (its title's STREAM:) writes
// the order: one ORDER line. task move --to-stream orders both streams;
// scope park and unpark carry order= in their receipts.
func TestOrderDoorsCLI(t *testing.T) {
	f := newSeat(t)
	t.Setenv("FRIEND_QUEUE_SPRINT", seatSprint)
	t.Setenv("NOVA_FRIEND", "") // a coordinator shell for the --actor form
	ctx := context.Background()
	c := f.client
	const s, s2, s3 = "order: doors", "order: queue", "order: moved"
	stale := func(stream string) {
		t.Helper()
		if so, err := ws.ReadOrder(ctx, c, stream); err != nil || so.Stale() {
			t.Fatalf("%s: stale %v %v stored %v computed %v", stream, so.Stale(), err, so.Stored, so.Computed)
		}
	}

	// task push --actor: x on y (y is no card yet), then y on x.
	code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", "x", "--stream", s, "--kind", "build",
		"--title", "x", "--ref", "mas-bandwidth/nova-tools#1", "--on", "y")
	if code != 0 || !strings.Contains(out, " order=2 ") {
		t.Fatalf("push x: %d %q %q", code, out, errOut)
	}
	before := storeSnapshot(t, c)
	code, out, _ = runTaskCLI("push", "--actor", "rowan", "--id", "y", "--stream", s, "--kind", "build",
		"--title", "y", "--ref", "mas-bandwidth/nova-tools#2", "--on", "x")
	if code != 1 || !regexp.MustCompile(`^TASK push REFUSED id=y why="ORDER CYCLE stream=\\"order: doors\\" DEPENDS-ON cycle y -> x -> y" ms=\d+\n$`).MatchString(out) {
		t.Fatalf("push y on x: %d %q", code, out)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("task push --actor cycle: %s", strings.TrimSpace(out))

	// friend-queue task push (no --actor): the title names the stream.
	f.as("a")
	code, out, errOut = runTaskCLI("push", "--to", "a", "--id", "fq1", "--kind", "work", "--ref", "mas-bandwidth/nova-tools#3",
		"--title", "STREAM: "+s2+" | fq one")
	if code != 0 || !regexp.MustCompile(`^PUSH CREATED id=fq1\nORDER stream="order: queue" order=2 order_rt=3 order_ms=[0-9.]+\n$`).MatchString(out) {
		t.Fatalf("friend-queue push: %d %q %q", code, out, errOut)
	}
	stale(s2)
	t.Logf("friend-queue push: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	f.as("")
	t.Setenv("NOVA_FRIEND", "")
	if code, out, errOut = runTaskCLI("push", "--actor", "rowan", "--id", "z", "--stream", s2, "--kind", "build",
		"--title", "z", "--ref", "mas-bandwidth/nova-tools#4", "--on", "fq2"); code != 0 {
		t.Fatalf("push z: %d %q %q", code, out, errOut)
	}
	f.as("a")
	before = storeSnapshot(t, c)
	code, out, _ = runTaskCLI("push", "--to", "a", "--id", "fq2", "--kind", "work", "--ref", "mas-bandwidth/nova-tools#5",
		"--title", "STREAM: "+s2+" | fq two", "--on", "task:z")
	if code != 2 || out != `PUSH INVALID id=fq2 why="ORDER CYCLE stream=\"order: queue\" DEPENDS-ON cycle fq2 -> z -> fq2"`+"\n" {
		t.Fatalf("friend-queue push closing a cycle: %d %q", code, out)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused friend-queue push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("friend-queue cycle: %s", strings.TrimSpace(out))

	// friend-queue task push onto a stream that holds a cycle (the fix
	// round 3, probe (a)): z and fq1 name each other by a hand edit; fq3 on
	// z is outside the cycle and is written, fq4 on fq1 joins it (fq1 names
	// fq4) and is refused, nothing written.
	c.HSet(ctx, "task:z", "blocked_on", "fq1")
	c.HSet(ctx, "task:fq1", "depends_on", "task:z;task:fq4")
	code, out, errOut = runTaskCLI("push", "--to", "a", "--id", "fq3", "--kind", "work", "--ref", "mas-bandwidth/nova-tools#6",
		"--title", "STREAM: "+s2+" | fq three", "--on", "task:z")
	if code != 0 || !c.HExists(ctx, "task:fq3", "where").Val() ||
		!strings.Contains(out, "\nORDER CYCLE stream=\"order: queue\" DEPENDS-ON cycle fq1 -> z -> fq1\n") {
		t.Fatalf("friend-queue push beside a cycle: %d %q %q", code, out, errOut)
	}
	t.Logf("friend-queue, unrelated: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	before = storeSnapshot(t, c)
	code, out, _ = runTaskCLI("push", "--to", "a", "--id", "fq4", "--kind", "work", "--ref", "mas-bandwidth/nova-tools#7",
		"--title", "STREAM: "+s2+" | fq four", "--on", "task:fq1")
	if code != 2 || out != `PUSH INVALID id=fq4 why="ORDER CYCLE stream=\"order: queue\" DEPENDS-ON cycle fq4 -> fq1 -> fq4"`+"\n" {
		t.Fatalf("friend-queue push joining the cycle: %d %q", code, out)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused friend-queue push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("friend-queue, joining: exit %d, %s", code, strings.TrimSpace(out))

	// friend-queue task push by a seat without +fcall|ns_ws_reorder (probe
	// (c)): refused before any write, PUSH INVALID with ORDER GRANT.
	if err := c.Do(ctx, "ACL", "SETUSER", "fqbare", "on", ">fqbare-pass", "~*", "&*", "+@all", "-fcall", "+fcall|ns_task_push").Err(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.UserEnv, "fqbare")
	t.Setenv(redisauth.PasswordEnvEnv, "ORDER_DOORS_FQ_PASS")
	t.Setenv("ORDER_DOORS_FQ_PASS", "fqbare-pass")
	before = storeSnapshot(t, c)
	code, out, _ = runTaskCLI("push", "--to", "a", "--id", "fq5", "--kind", "work", "--ref", "mas-bandwidth/nova-tools#8",
		"--title", "STREAM: "+s2+" | fq five")
	if code != 2 || !strings.HasPrefix(out, `PUSH INVALID id=fq5 why="ORDER GRANT ns_ws_reorder: NOPERM User fqbare has no permissions to run the 'fcall' command;`) {
		t.Fatalf("friend-queue push without the grant: %d %q", code, out)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused friend-queue push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("friend-queue, no grant: exit %d, %s", code, strings.TrimSpace(out))
	t.Setenv(store.UserEnv, "")
	f.as("")
	t.Setenv("NOVA_FRIEND", "")

	// card push: one and two name each other.
	dir := t.TempDir()
	cardFile := func(name, origin, depends string) string {
		path := filepath.Join(dir, name+".md")
		body := "LABEL: " + name + "\nREPO: mas-bandwidth/nova-tools\nBASE: dev\nbase-sha: " + strings.Repeat("ab", 20) +
			"\nPATHS: internal/" + name + "\nDEPENDS-ON: " + depends + "\nDONE-WHEN: go test passes\nSTREAM: " + s +
			"\nORIGIN: mas-bandwidth/nova-tools#" + origin + "\n\nwhat and why\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	addr := os.Getenv("NOVA_SPRINT_REDIS")
	before = storeSnapshot(t, c)
	code, out, errOut = runSprint("card", "push", "--sprint", "sp1", "--redis", addr, cardFile("one", "8", "two"), cardFile("two", "7", "one"))
	if code == 0 || !strings.Contains(errOut, `push refused, nothing written: ORDER CYCLE stream="order: doors" DEPENDS-ON cycle s:sp1:card:two -> s:sp1:card:one -> s:sp1:card:two`) {
		t.Fatalf("card push cycle: %d %q %q", code, out, errOut)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused card push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("card push cycle: %s", strings.TrimSpace(errOut))

	// card push, a cycle through a live card (the fix round 3: ns_card_push
	// refuses it itself): three is live and, by a hand edit (card push
	// refuses a DEPENDS-ON on no card), on four; then four is pushed on three.
	if code, out, errOut = runSprint("card", "push", "--sprint", "sp1", "--redis", addr, cardFile("three", "9", "none")); code != 0 {
		t.Fatalf("card push three: %d %q %q", code, out, errOut)
	}
	c.HSet(ctx, "s:sp1:card:three", "depends_on", "four")
	before = storeSnapshot(t, c)
	code, out, errOut = runSprint("card", "push", "--sprint", "sp1", "--redis", addr, cardFile("four", "10", "three"))
	if code == 0 || !strings.Contains(errOut, `ns_card_push: ORDER CYCLE stream="order: doors" DEPENDS-ON cycle s:sp1:card:four -> s:sp1:card:three -> s:sp1:card:four`) {
		t.Fatalf("card push closing a cycle through a live card: %d %q %q", code, out, errOut)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused card push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("card push cycle through a live card: %s", strings.TrimSpace(errOut))

	// task move --to-stream: both streams ordered, one ORDER line each.
	code, out, _ = runTaskCLI("move", "--actor", "rowan", "--id", "x", "--to-stream", s3)
	if code != 0 || !regexp.MustCompile(`\nORDER stream="order: doors" order=2 order_rt=3 order_ms=[0-9.]+\nORDER stream="order: moved" order=2 order_rt=3 order_ms=[0-9.]+\n$`).MatchString(out) {
		t.Fatalf("move --to-stream: %d %q", code, out)
	}
	stale(s)
	stale(s3)
	t.Logf("move --to-stream: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))

	// scope park and unpark: order= in the receipt.
	// (everything parked: nothing is live, order=0)
	code, out, _ = runSprint("scope", "park", "--redis", addr, "--stream", s3, "--checkpoint", filepath.Join(dir, "cp.tsv"))
	if code != 0 || !regexp.MustCompile(`^PARKED stream="order: moved" parked=\d+ checkpoint=\S+ rows=\d+ order=0 order_rt=3 order_ms=[0-9.]+ ms=`).MatchString(out) {
		t.Fatalf("park: %d %q", code, out)
	}
	code, out, _ = runSprint("scope", "unpark", "--redis", addr, "--stream", s3)
	if code != 0 || !regexp.MustCompile(`^UNPARKED stream="order: moved" unparked=\d+ order=2 order_rt=3 order_ms=[0-9.]+ ms=`).MatchString(out) {
		t.Fatalf("unpark: %d %q", code, out)
	}
	stale(s3)
	t.Logf("unpark: %s", strings.TrimSpace(out))
}
