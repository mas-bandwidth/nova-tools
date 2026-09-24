package civerdict

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestReadIsTheHashShape(t *testing.T) {
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
	mr.HSet(key, Field, " OK ")
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
	if _, err := Expected(ctx, c, "o/r", "dev", "sha-base"); err == nil {
		t.Fatal("expected ErrNoPolicy when policy record absent")
	}
	mr.HSet(PolicyKey("o/r", "dev"), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
	expGid, err := Expected(ctx, c, "o/r", "dev", "sha-base")
	if err != nil || expGid != gid {
		t.Fatalf("Expected = %q, %v; want %q", expGid, err, gid)
	}
}
