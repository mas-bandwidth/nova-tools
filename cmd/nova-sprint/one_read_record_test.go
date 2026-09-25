package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestOneReadRecordLandWhy is the lander half of nova-tools#3874's DONE-WHEN
// (cmd/nova-review TestOneReadRecord is the nova-review half): reads posted
// with read post and pr lines for one PR (emma at two heads, stella once, jev
// once) are one current read per non-Jev reader at the head to the stream
// lander (Members) and to its why (lander --shadow); the record has no reads
// field and no cards:done:read:* key exists.
func TestOneReadRecordLandWhy(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	h1, h2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	const strm = "redis: store + bus"
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", "mas-bandwidth/nova-tools", "--n", "3874",
		"--head", h1, "--base", "dev", "--stream", strm, "--task", "t3874"); code != 0 {
		t.Fatalf("pr record: %d %q %q", code, out, errOut)
	}
	post := func(text string) {
		t.Helper()
		if code, out, errOut := runSprint("read", "post", "--repo", "nova-tools", "--n", "3874", "--line", text, "--no-github", "--redis", addr); code != 0 {
			t.Fatalf("read post %q: %d %q %q", text, code, out, errOut)
		}
	}
	post("SCORE who=emma head=" + h1 + " score=9/10")
	// The author pushes H2: the record moves, ci green there.
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", "nova-tools", "--n", "3874",
		"--head", h2, "--ci", "green", "--mergeable", "true"); code != 0 {
		t.Fatalf("pr record h2: %d %q %q", code, out, errOut)
	}
	post("SCORE who=emma head=" + h2 + " score=8/10")
	if code, out, errOut := runSprint("pr", "lines", "--redis", addr, "--repo", "nova-tools", "--n", "3874",
		"--add", "SCORE who=stella head="+h2[:8]+" score=10/10"); code != 0 {
		t.Fatalf("pr lines: %d %q %q", code, out, errOut)
	}
	post("SCORE who=jev head=" + h2 + " score=10/10")

	// The lander: t3874 in merging is a member on stella's read.
	if err := c.ZAdd(ctx, stream.WSKey(strm, "merging"), redis.Z{Score: 1, Member: "t3874"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:t3874", "stream", strm, "state", "merging", "pr", "nova-tools#3874").Err(); err != nil {
		t.Fatal(err)
	}
	recs, err := stream.LoadPRs(ctx, c, "nova-tools", []int{3874})
	if err != nil || len(recs[0].Reads) != 2 {
		t.Fatalf("lander reads %q %v, want one per reader (emma, stella)", recs[0].Reads, err)
	}
	ms, skips, err := stream.Members(ctx, c, "nova-tools", []string{strm}, 8)
	if err != nil || len(ms) != 1 || len(skips) != 0 || ms[0].Who != "stella" || ms[0].Score != 10 {
		t.Fatalf("members %+v skips %+v %v, want #3874 on stella=10", ms, skips, err)
	}

	// Its why: lander --shadow names the read that carries the member.
	var out, errOut bytes.Buffer
	if code := runLander(ctx, []string{"--shadow", "--redis", addr, "--repo", "nova-tools", "3874"}, &out, &errOut); code != 0 {
		t.Fatalf("lander --shadow %d %q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "SHADOW nova-tools#3874 head=22222222 verdict=LAND why=read:stella=10,ci:green\n") {
		t.Fatalf("shadow %q", out.String())
	}

	// A hold at head by emma replaces nothing of stella's and holds the member.
	post("HOLD who=emma head=" + h2)
	out.Reset()
	if code := runLander(ctx, []string{"--shadow", "--redis", addr, "--repo", "nova-tools", "3874"}, &out, &errOut); code != 0 ||
		!strings.HasPrefix(out.String(), "SHADOW nova-tools#3874 head=22222222 verdict=HOLD why=hold:emma\n") {
		t.Fatalf("shadow after the hold %d %q", code, out.String())
	}
	if recs, _ = stream.LoadPRs(ctx, c, "nova-tools", []int{3874}); len(recs[0].Reads) != 2 {
		t.Fatalf("after emma's hold the lander sees %q, want still one read each", recs[0].Reads)
	}

	if c.HExists(ctx, "pr:nova-tools:3874", "reads").Val() {
		t.Fatal("pr:nova-tools:3874 has a reads field")
	}
	if keys := c.Keys(ctx, "cards:done:read:*").Val(); len(keys) != 0 {
		t.Fatalf("claim keys exist: %v", keys)
	}
}
