//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestReadPostVerbMovesThePrimary is the verb-level half of nova-tools
// #4094 and #4097: through nova-sprint read post (--no-github), a SCORE 9
// at the record head moves the PRIMARY review -> merging in the same call
// (tasks_moved=1), and a SCORE 6 leaves it in review and cuts one fix copy
// on the author's queue carrying the line (copies_cut=1); nova-sprint pr
// record --head (the fix's new head) then cuts one fresh read copy.
func TestReadPostVerbMovesThePrimary(t *testing.T) {
	t.Parallel()
	if os.Getenv(store.UserEnv) != "" {
		t.Skip(store.UserEnv + " is set; this parallel test cannot clear it")
	}
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const s = "swarm: cards"
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	author, _ := taskcard.ParseConsumer("friend:f")
	emma, _ := taskcard.ParseConsumer("friend:emma")
	for _, k := range []taskcard.Consumer{author, emma} {
		c.SAdd(ctx, "friends", k.Name)
		c.HSet(ctx, k.DesiredKey(), "slots", "2")
		c.HSet(ctx, k.BeatKey(), "at", now)
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
	}
	c.HSet(ctx, "friend:emma:roles", "roles", "reader")
	head := func(i int) string { return fmt.Sprintf("%040x", 0xb0000+i) }
	review := func(id string, pr int) string {
		t.Helper()
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: s, Kind: "build",
			Repo: "mas-bandwidth/nova-tools", Title: "t " + id, By: "rowan",
			Fields: []string{"base", "dev", "base_sha", strings.Repeat("1", 40), "done_when", "go test ./x passes"}}); err != nil {
			t.Fatal(err)
		}
		d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: author, IDs: []string{id}, By: "rowan"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := taskcard.Work(ctx, c, author, "f", 0, false, d[0].Copy); err != nil {
			t.Fatal(err)
		}
		if code, out, errOut := runCLI("pr", "record", "--repo", "mas-bandwidth/nova-tools", "--n", strconv.Itoa(pr),
			"--head", head(pr), "--base", "dev", "--stream", s, "--redis", addr); code != 0 {
			t.Fatalf("pr record = %d %q %q", code, out, errOut)
		}
		e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, OK: true, Repo: "nova-tools",
			PR: strconv.Itoa(pr), Head: head(pr), By: "f"})
		if err != nil || e[0].To != "review" || e[0].Next == "" || strings.Contains(e[0].Next, ",") {
			t.Fatalf("end ok %v %v (want reading and one friend read copy)", e, err)
		}
		return e[0].Next
	}
	where := func(id string) string { return c.HGet(ctx, "task:"+id, "where").Val() }
	post := func(pr int, line string) string {
		t.Helper()
		code, out, errOut := runCLI("read", "post", "--repo", "nova-tools", "--n", strconv.Itoa(pr), "--line", line,
			"--no-github", "--redis", addr)
		if code != 0 {
			t.Fatalf("read post = %d %q %q", code, out, errOut)
		}
		return out
	}

	// SCORE 9 at the record head: the primary advances in the same call
	r1 := review("v1", 1)
	c.HSet(ctx, "ci:nova-tools:"+head(1), "final", "OK")
	out := post(1, "SCORE who=emma head="+head(1)+" score=9/10 gates=ci:ok,base:ok,scope:ok")
	if !strings.Contains(out, "kind=SCORE") || !strings.Contains(out, "tasks_moved=1") || where("v1") != "merging" ||
		where(r1) != "ok" {
		t.Fatalf("SCORE 9: %q primary=%s copy=%s", out, where("v1"), where(r1))
	}

	// SCORE 6: the primary stays; one fix copy on the author's queue
	r2 := review("v2", 2)
	line := "SCORE who=emma head=" + head(2) + " score=6/10 gates=ci:ok,base:ok,scope:ok: no failing test"
	out = post(2, line)
	fix := c.HGet(ctx, "task:v2", "copy").Val()
	if !strings.Contains(out, "tasks_moved=0 copies_cut=1") || where("v2") != "review" || where(r2) != "ok" || fix == "" {
		t.Fatalf("SCORE 6: %q primary=%s copy=%s fix=%q", out, where("v2"), where(r2), fix)
	}
	if f := c.HGetAll(ctx, "task:"+fix).Val(); f["kind"] != "fix" || f["consumer"] != "friend:f" || f["finding"] != line {
		t.Fatalf("fix copy %v", f)
	}

	// the fix's new head through pr record --head: one fresh read copy
	code, out, errOut := runCLI("pr", "record", "--repo", "nova-tools", "--n", "2", "--head", head(20), "--redis", addr)
	if code != 0 || !strings.Contains(out, "PR REHEAD task=v2 ") {
		t.Fatalf("pr record --head = %d %q %q", code, out, errOut)
	}
	fresh := strings.Fields(c.HGet(ctx, "task:v2", "reads").Val())
	if len(fresh) != 1 || c.HGet(ctx, "task:"+fresh[0], "head").Val() != head(20) || where(fix) != "ok" || where("v2") != "review" {
		t.Fatalf("after the new head: reads=%v fix=%s primary=%s", fresh, where(fix), where("v2"))
	}
	r, err := taskcard.FsckMoves(ctx, c, false)
	if err != nil || r.Drift != 0 {
		t.Fatalf("card fsck %+v %v", r, err)
	}
}
