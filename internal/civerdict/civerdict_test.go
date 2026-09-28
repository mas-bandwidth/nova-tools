package civerdict

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestReadIsTheHashShape(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	gid := GID("single", "dev", "sha-base", "req1", "pol1", "run1")
	key := Key("o/r", "abc", gid)
	if key != "ci:o/r:abc:"+gid {
		t.Fatalf("Key = %q", key)
	}
	f, err := Read(ctx, c, "o/r", "abc", gid)
	if err != nil || len(f) != 0 || Of(f) != "" {
		t.Fatalf("absent: %v %v, want empty and nil", f, err)
	}
	mr.HSet(PolicyKey("o/r", "dev"), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
	mr.HSet(TipKey("o/r", "dev"), "sha", "sha-base")
	mr.HSet(key, Field, " OK ", "base", "dev", "base_sha", "sha-base", "gid", gid)
	mr.SAdd(GIDsKey("o/r", "abc"), gid)
	f, err = Read(ctx, c, "o/r", "abc", gid)
	if err != nil || Of(f) != OK || !Green(Of(f)) {
		t.Fatalf("present: %v %v, want verdict OK", f, err)
	}
	headRec, err := ReadHead(ctx, c, "o/r", "abc")
	if err != nil || Of(headRec) != OK || !Green(Of(headRec)) {
		t.Fatalf("ReadHead: %v %v, want verdict OK", headRec, err)
	}
	mr.Del(key)
	_ = mr.Set(key, "OK")
	if _, err := Read(ctx, c, "o/r", "abc", gid); err == nil {
		t.Fatal("a string at the key read as a record; want WRONGTYPE")
	}
	if Of(nil) != "" || Green("ok") || Green("") {
		t.Fatal("only exactly OK is green")
	}

	// Test Expected reading policy record
	mr.Del(PolicyKey("o/r", "dev"))
	if _, err := Expected(ctx, c, "o/r", "dev", "sha-base"); err == nil {
		t.Fatal("expected ErrNoPolicy when policy record absent")
	}
	mr.HSet(PolicyKey("o/r", "dev"), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
	expGid, err := Expected(ctx, c, "o/r", "dev", "sha-base")
	if err != nil || expGid != gid {
		t.Fatalf("Expected = %q, %v; want %q", expGid, err, gid)
	}
}

func TestReadHeadRequiresMatchingBaseAndPolicy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	const (
		repo     = "mas-bandwidth/nova-tools"
		head     = "commit-head"
		base     = "dev"
		tip1     = "tip-sha-1"
		tip2     = "tip-sha-2"
		pol1     = "pol1"
		pol2     = "pol2"
		reqSet   = "req1"
		runnerID = "run1"
	)

	gid1 := GID("single", base, tip1, reqSet, pol1, runnerID)
	rec1 := map[string]string{
		"verdict":         "OK",
		"gid":             gid1,
		"head":            head,
		"base":            base,
		"base_sha":        tip1,
		"required_set_id": reqSet,
		"policy_id":       pol1,
		"runner_id":       runnerID,
	}

	mr.HSet(PolicyKey(repo, base), "policy_id", pol1, "required_set_id", reqSet, "runner_id", runnerID)
	mr.HSet(TipKey(repo, base), "sha", tip1)
	mr.SAdd(GIDsKey(repo, head), gid1)
	for k, v := range rec1 {
		mr.HSet(Key(repo, head, gid1), k, v)
	}

	// 1. Valid matching receipt returns OK
	got, err := ReadHead(ctx, c, repo, head)
	if err != nil {
		t.Fatalf("ReadHead: %v", err)
	}
	if Of(got) != OK {
		t.Fatalf("ReadHead = %v, want OK", got)
	}

	// 2. Base tip moves to tip2: receipt for tip1 is now stale and must NOT be accepted as green
	mr.HSet(TipKey(repo, base), "sha", tip2)
	got, err = ReadHead(ctx, c, repo, head)
	if err != nil {
		t.Fatalf("ReadHead with moved tip: %v", err)
	}
	if Of(got) == OK {
		t.Fatalf("ReadHead accepted stale receipt on moved base tip: got %v", got)
	}

	// 3. Reset tip to tip1, but change policy to pol2: receipt for pol1 is stale and must NOT be accepted
	mr.HSet(TipKey(repo, base), "sha", tip1)
	mr.HSet(PolicyKey(repo, base), "policy_id", pol2)
	got, err = ReadHead(ctx, c, repo, head)
	if err != nil {
		t.Fatalf("ReadHead with changed policy: %v", err)
	}
	if Of(got) == OK {
		t.Fatalf("ReadHead accepted stale receipt on changed policy: got %v", got)
	}

	// 4. Multiple receipts in set: old gid1 and new gid2
	gid2 := GID("single", base, tip2, reqSet, pol2, runnerID)
	rec2 := map[string]string{
		"verdict":         "OK",
		"gid":             gid2,
		"head":            head,
		"base":            base,
		"base_sha":        tip2,
		"required_set_id": reqSet,
		"policy_id":       pol2,
		"runner_id":       runnerID,
	}
	for k, v := range rec2 {
		mr.HSet(Key(repo, head, gid2), k, v)
	}
	mr.SAdd(GIDsKey(repo, head), gid2)
	mr.HSet(TipKey(repo, base), "sha", tip2) // current tip is tip2, policy is pol2
	got, err = ReadHead(ctx, c, repo, head)
	if err != nil {
		t.Fatalf("ReadHead with multiple receipts: %v", err)
	}
	if Of(got) != OK || got["gid"] != gid2 {
		t.Fatalf("ReadHead = %v; want gid2 (%s) with OK", got, gid2)
	}

	// 5. Policy absent: must return empty (MISSING)
	mr.Del(PolicyKey(repo, base))
	got, err = ReadHead(ctx, c, repo, head)
	if err != nil {
		t.Fatalf("ReadHead without policy: %v", err)
	}
	if Of(got) != "" {
		t.Fatalf("ReadHead without policy returned %v, want empty", got)
	}
}
