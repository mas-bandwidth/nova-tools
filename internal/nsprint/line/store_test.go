//go:build functional

package line_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2"
)

// storeRedis is a throwaway redis-server with the nova_sprint library and one
// PR record pr:nova-tools:7 at headA on dev.
func storeRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint: %v", err)
	}
	if err := c.HSet(ctx, "pr:nova-tools:7", "head", headA, "base", "dev", "paths", "internal/x").Err(); err != nil {
		t.Fatal(err)
	}
	return c
}

func mustPost(t *testing.T, c *redis.Client, text string, scope line.Scope) line.Posted {
	t.Helper()
	p, err := line.Post(context.Background(), c, "nova-tools", "7", text, scope)
	if err != nil || p.Status != "POSTED" {
		t.Fatalf("post %q: %+v %v", text, p, err)
	}
	return p
}

// TestLinePostRefusesMalformed: a line that is not a typed line, or whose
// typed gate disagrees with the measured one, or whose score is over the gate
// cap with a measured gate red, writes nothing.
func TestLinePostRefusesMalformed(t *testing.T) {
	t.Parallel()

	c := storeRedis(t)
	ctx := context.Background()
	for _, text := range []string{
		"",
		"looks good to me",
		"APPROVE who=emma head=" + headA + " score=10/10",
		"SCORE head=" + headA + " score=9/10",
		"SCORE who=emma score=9/10",
		"SCORE who=emma head=xyz1234 score=9/10",
		"SCORE who=emma head=" + headA,
		"SCORE who=emma head=" + headA + " score=11/10",
		"SCORE who=emma head=" + headA + " score=nine",
		"SCORE who=emma head=" + headA + " score=9/10 gates=ci",
		"SCORE who=Emma:x head=" + headA + " score=9/10",
	} {
		p, err := line.Post(ctx, c, "nova-tools", "7", text, line.Scope{})
		if !errors.Is(err, line.ErrMalformed) || p.Status != "REFUSED" {
			t.Errorf("%q: want a malformed refusal, got %+v %v", text, p, err)
		}
	}
	// Measured gates: CI red at head, the base stacked, scope measured no.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.HSet(ctx, "ci:nova-tools:"+headA, "ci", "red").Err())
	for _, tc := range []struct {
		text  string
		scope line.Scope
		want  string
	}{
		{"SCORE who=emma head=" + headA + " score=10/10 gates=ci:ok,base:ok,scope:ok", line.Scope{}, "gate ci typed ok but measured red"},
		{"SCORE who=emma head=" + headA + " score=9/10", line.Scope{}, "over the gate cap 7: ci measured red"},
		{"HOLD who=emma head=" + headA + " gates=ci:red,scope:ok", line.Scope{Word: "no", Outside: []string{"cmd/y.go"}}, "gate scope typed ok but measured no (files outside PATHS: cmd/y.go)"},
	} {
		p, err := line.Post(ctx, c, "nova-tools", "7", tc.text, tc.scope)
		if err != nil || p.Status != "REFUSED" || !strings.Contains(p.Why, tc.want) {
			t.Errorf("%q: want REFUSED %q, got %+v %v", tc.text, tc.want, p, err)
		}
	}
	must(c.HSet(ctx, "ci:nova-tools:"+headA, "ci", "green").Err())
	must(c.HSet(ctx, "pr:nova-tools:7", "base", "rowan/stacked").Err())
	p, err := line.Post(ctx, c, "nova-tools", "7", "SCORE who=emma head="+headA+" score=10/10 gates=ci:ok,base:ok,scope:ok", line.Scope{})
	if err != nil || p.Status != "REFUSED" || !strings.Contains(p.Why, "gate base typed ok but measured no (the PR base is rowan/stacked)") {
		t.Errorf("stacked base: %+v %v", p, err)
	}
	if keys := c.Keys(ctx, "pr:nova-tools:7:*").Val(); len(keys) != 0 {
		t.Fatalf("a refused post wrote %v", keys)
	}
	if got := c.HGet(ctx, "pr:nova-tools:7", "reads").Val(); got != "" {
		t.Fatalf("a refused post wrote reads %q", got)
	}
	p, err = line.Post(ctx, c, "nova-tools", "8", "SCORE who=emma head="+headA+" score=9/10", line.Scope{})
	if err != nil || p.Status != "MISSING" || p.Key != "pr:nova-tools:8" {
		t.Errorf("no record: %+v %v", p, err)
	}
}

// TestLineListByHead: List returns the lines at one head only, oldest
// first; "" is the record's head and a typed prefix expands to it.
func TestLineListByHead(t *testing.T) {
	t.Parallel()

	c := storeRedis(t)
	ctx := context.Background()
	if err := c.HSet(ctx, "ci:nova-tools:"+headA, "ci", "green").Err(); err != nil {
		t.Fatal(err)
	}
	p := mustPost(t, c, "SCORE who=emma head="+headA[:8]+" score=10/10 gates=ci:ok,base:ok,scope:ok: all three pass", line.Scope{Word: "ok"})
	if p.Head != headA || p.Gates != "ci:ok,base:ok,scope:ok" || p.Measured != "ci,base,scope" || p.Lines != 1 {
		t.Fatalf("posted %+v", p)
	}
	mustPost(t, c, "HOLD who=stella head="+headA+" gates=ci:ok: item 1", line.Scope{})
	mustPost(t, c, "SCORE who=johnny head="+headB+" score=8/10", line.Scope{})
	for _, head := range []string{"", headA, headA[:7]} {
		full, recs, err := line.List(ctx, c, "mas-bandwidth/nova-tools", "7", head)
		if err != nil || full != headA || len(recs) != 2 {
			t.Fatalf("list %q: %s %v %v", head, full, recs, err)
		}
		if recs[0]["who"] != "emma" || recs[0]["kind"] != "SCORE" || recs[0]["score"] != "10" || recs[0]["gates"] != "ci:ok,base:ok,scope:ok" {
			t.Fatalf("list %q first: %v", head, recs[0])
		}
		if recs[1]["who"] != "stella" || recs[1]["kind"] != "HOLD" || recs[1]["gates"] != "ci:ok,base:ok" || recs[1]["measured"] != "ci,base" {
			t.Fatalf("list %q second: %v", head, recs[1])
		}
	}
	full, recs, err := line.List(ctx, c, "nova-tools", "7", headB)
	if err != nil || full != headB || len(recs) != 1 || recs[0]["who"] != "johnny" {
		t.Fatalf("list headB: %s %v %v", full, recs, err)
	}
	if _, _, err := line.List(ctx, c, "nova-tools", "9", ""); err == nil || !strings.Contains(err.Error(), "MISSING pr:nova-tools:9") {
		t.Fatalf("list of no record: %v", err)
	}
}

// TestLineKeyedByWho: the record is pr:<name>:<n>:line:<head>:<who>:<kind>;
// a second line of a kind by the same reader at the same head replaces the
// first, another reader's is its own record, and the log keeps both.
func TestLineKeyedByWho(t *testing.T) {
	t.Parallel()

	c := storeRedis(t)
	ctx := context.Background()
	p := mustPost(t, c, "SCORE who=Emma head="+headA+" score=8/10", line.Scope{})
	if want := "pr:nova-tools:7:line:" + headA + ":emma:SCORE"; p.Key != want || line.LineKey("nova-tools", "7", headA, "emma", "SCORE") != want {
		t.Fatalf("key %q, want %q", p.Key, want)
	}
	mustPost(t, c, "SCORE who=emma head="+headA+" score=9/10", line.Scope{})
	mustPost(t, c, "SCORE who=stella head="+headA+" score=10/10", line.Scope{})
	_, recs, err := line.List(ctx, c, "nova-tools", "7", headA)
	if err != nil || len(recs) != 2 {
		t.Fatalf("list: %v %v", recs, err)
	}
	if recs[0]["who"] != "emma" || recs[0]["score"] != "9" || recs[1]["who"] != "stella" {
		t.Fatalf("keyed by who: %v", recs)
	}
	if n := c.LLen(ctx, "pr:nova-tools:7:lines").Val(); n != 3 {
		t.Fatalf("log has %d lines, want 3", n)
	}
}

// TestImportFromComments: typed comment lines become records with source
// comment:<id>, prose is skipped, a second import writes nothing new, and an
// imported line keeps its typed gate even when the measure now disagrees.
func TestImportFromComments(t *testing.T) {
	t.Parallel()

	c := storeRedis(t)
	ctx := context.Background()
	if err := c.HSet(ctx, "ci:nova-tools:"+headA, "ci", "red").Err(); err != nil {
		t.Fatal(err)
	}
	raw := `[{"id": 101, "body": "Looks fine, reading now."},
{"id": 102, "body": "SCORE who=emma head=` + headA + ` score=10/10 gates=ci:ok,base:ok,scope:ok: all three pass\n- no items"}]
[{"id": 103, "body": "HOLD who=stella head=` + headA + ` score=5/10 gates=ci:red: item 1"}]`
	comments, err := line.ParseComments(strings.NewReader(raw))
	if err != nil || len(comments) != 3 {
		t.Fatalf("parse: %v %v", comments, err)
	}
	res, err := line.Import(ctx, c, "nova-tools", "7", comments)
	if err != nil || res != (line.Imported{Comments: 3, Typed: 2, Imported: 2}) {
		t.Fatalf("import: %+v %v", res, err)
	}
	res, err = line.Import(ctx, c, "nova-tools", "7", comments)
	if err != nil || res != (line.Imported{Comments: 3, Typed: 2, Existed: 2}) {
		t.Fatalf("second import: %+v %v", res, err)
	}
	_, recs, err := line.List(ctx, c, "nova-tools", "7", "")
	if err != nil || len(recs) != 2 {
		t.Fatalf("list: %v %v", recs, err)
	}
	if recs[0]["source"] != "comment:102" || recs[0]["gates_typed"] != "ci:ok,base:ok,scope:ok" || recs[0]["gates"] != "ci:red,base:ok,scope:ok" {
		t.Fatalf("imported SCORE: %v", recs[0])
	}
	if recs[1]["source"] != "comment:103" || recs[1]["kind"] != "HOLD" {
		t.Fatalf("imported HOLD: %v", recs[1])
	}
	if n := c.LLen(ctx, "pr:nova-tools:7:lines").Val(); n != 2 {
		t.Fatalf("log has %d lines after two imports, want 2", n)
	}
}

// TestLanderReadsPostedLine: the stream lander lands a PR whose only line is
// in Redis: before the post the member is skipped no-read-at-head, after a
// SCORE posted through the store it is a member at that score.
func TestLanderReadsPostedLine(t *testing.T) {
	t.Parallel()

	c := storeRedis(t)
	ctx := context.Background()
	if err := c.HSet(ctx, "pr:nova-tools:7", "state", "open", "stream", "s1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:t7", "pr", "7").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, stream.WSKeyAt(0, "s1", "merging"), redis.Z{Score: 1, Member: "t7"}).Err(); err != nil {
		t.Fatal(err)
	}
	members, skips, err := stream.Members(ctx, c, "nova-tools", []string{"s1"}, 8)
	if err != nil || len(members) != 0 || len(skips) != 1 || skips[0].Why != "no-read-at-head" {
		t.Fatalf("before: %v %v %v", members, skips, err)
	}
	mustPost(t, c, "SCORE who=emma head="+headA+" score=10/10 gates=ci:ok,base:ok,scope:ok", line.Scope{Word: "ok"})
	members, skips, err = stream.Members(ctx, c, "nova-tools", []string{"s1"}, 8)
	if err != nil || len(skips) != 0 || len(members) != 1 || members[0].N != 7 || members[0].Who != "emma" || members[0].Score != 10 {
		t.Fatalf("after: %v %v %v", members, skips, err)
	}
}
