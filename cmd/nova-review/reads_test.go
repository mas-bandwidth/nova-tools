package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

const (
	oneReadH1 = "1111111111111111111111111111111111111111"
	oneReadH2 = "2222222222222222222222222222222222222222"
)

// oneReadStore is a throwaway redis-server with the nova_sprint library and
// pr:nova-tools:3874 at H2 on dev, CI green: the author pushed H2 after emma
// read H1. The reads are posted through the one writer (ns_line_post): emma at
// H1 and again at H2, stella once at H2, jev at H2.
func oneReadStore(t *testing.T) (*redis.Client, string) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint: %v", err)
	}
	if err := c.HSet(ctx, "pr:nova-tools:3874", "repo", "nova-tools", "n", "3874", "head", oneReadH2, "base", "dev",
		"state", "open", "ci", "green", "mergeable", "true").Err(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"SCORE who=emma head=" + oneReadH1 + " score=9/10",
		"SCORE who=emma head=" + oneReadH2 + " score=8/10\nre-read after the push",
		"SCORE who=stella head=" + oneReadH2[:8] + " score=10/10",
		"SCORE who=jev head=" + oneReadH2 + " score=10/10",
	} {
		p, err := line.Post(ctx, c, "mas-bandwidth/nova-tools", "3874", text, line.Scope{})
		if err != nil || p.Status != "POSTED" {
			t.Fatalf("post %q: %+v %v", text, p, err)
		}
	}
	return c, addr
}

// TestOneReadRecord is the DONE-WHEN of nova-tools#3874: reads posted for one
// PR (two heads by emma, one by stella, one by jev) are seen by the lander
// (stream.LoadPRs, the records Members and lander --shadow read, and ReadAt,
// its verdict) and by nova-review reads as exactly one current read per
// non-Jev reader at the head, while pr:nova-tools:3874 has no reads field and
// no cards:done:read:* key exists.
func TestOneReadRecord(t *testing.T) {
	c, addr := oneReadStore(t)
	ctx := context.Background()

	// The lander: one current read per reader at H2, jev never.
	recs, err := stream.LoadPRs(ctx, c, "mas-bandwidth/nova-tools", []int{3874})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(recs[0].Reads, "|"); got != "SCORE who=emma head="+oneReadH2+" score=8/10|SCORE who=stella head="+oneReadH2[:8]+" score=10/10" {
		t.Fatalf("lander reads %q, want emma's H2 read and stella's, one each", got)
	}
	if r := stream.ReadAt(recs[0].Reads, recs[0].Head); r.Who != "stella" || r.Score != 10 || r.Held != "" {
		t.Fatalf("lander verdict %+v, want stella=10", r)
	}

	// nova-review reads: the same two, and no stale reader (emma re-read).
	var out, errOut bytes.Buffer
	if code := run([]string{"reads", "--redis", addr, "--repo", "nova-tools", "--pr", "3874"}, &out, &errOut); code != 0 {
		t.Fatalf("reads exit %d: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || lines[0] != "READS PR repo=nova-tools n=3874 head="+oneReadH2+" current=2 stale=0" {
		t.Fatalf("reads:\n%s", out.String())
	}
	for i, who := range []string{"emma", "stella"} {
		if !strings.HasPrefix(lines[i+1], "READ repo=nova-tools n=3874 who="+who+" kind=SCORE ") || !strings.Contains(lines[i+1], " head=22222222 ") {
			t.Fatalf("read %d: %q, want %s at 22222222", i, lines[i+1], who)
		}
	}
	if strings.Contains(out.String(), "who=jev") {
		t.Fatalf("jev counted as a read:\n%s", out.String())
	}

	// One record: no reads field, no claim keys.
	if c.HExists(ctx, "pr:nova-tools:3874", "reads").Val() {
		t.Fatal("pr:nova-tools:3874 has a reads field")
	}
	if keys := c.Keys(ctx, "cards:done:read:*").Val(); len(keys) != 0 {
		t.Fatalf("claim keys exist: %v", keys)
	}
}

// TestReadsNamesAStaleReader: a push after the only read leaves that reader
// with no current read and one READS STALE line naming both heads; a PR with
// no record is one line, not a refusal.
func TestReadsNamesAStaleReader(t *testing.T) {
	c, addr := oneReadStore(t)
	ctx := context.Background()
	h3 := strings.Repeat("3", 40)
	if err := c.HSet(ctx, "pr:nova-tools:3874", "head", h3).Err(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"reads", "--redis", addr, "--repo", "mas-bandwidth/nova-tools", "--pr", "3874", "--pr", "#9"}, &out, &errOut); code != 0 {
		t.Fatalf("reads exit %d: %s", code, errOut.String())
	}
	want := strings.Join([]string{
		"READS PR repo=nova-tools n=3874 head=" + h3 + " current=0 stale=2",
		"READS STALE repo=nova-tools n=3874 who=emma kind=SCORE read=22222222 live=33333333",
		"READS STALE repo=nova-tools n=3874 who=stella kind=SCORE read=22222222 live=33333333",
		"READS PR repo=nova-tools n=9 head=- current=0 stale=0 why=no-record",
	}, "\n")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("reads:\n%s\nwant\n%s", got, want)
	}
	for _, args := range [][]string{
		{"reads", "--repo", "nova-tools", "--pr", "1"},
		{"reads", "--redis", addr, "--pr", "1"},
		{"reads", "--redis", addr, "--repo", "nova-tools"},
		{"reads", "--lane", "x"},
	} {
		out.Reset()
		errOut.Reset()
		if code := run(args, &out, &errOut); code != 2 || !strings.HasPrefix(errOut.String(), "READS REFUSED: ") {
			t.Errorf("%v: exit %d stderr %q, want READS REFUSED exit 2", args, code, errOut.String())
		}
	}
}
