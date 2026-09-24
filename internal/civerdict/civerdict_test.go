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

	if got := Key("o/r", " abc "); got != "ci:o/r:abc" {
		t.Fatalf("Key = %q", got)
	}
	f, err := Read(ctx, c, "o/r", "abc")
	if err != nil || len(f) != 0 || Of(f) != "" {
		t.Fatalf("absent: %v %v, want empty and nil", f, err)
	}
	mr.HSet("ci:o/r:abc", Field, " OK ")
	f, err = Read(ctx, c, "o/r", "abc")
	if err != nil || Of(f) != OK || !Green(Of(f)) {
		t.Fatalf("present: %v %v, want verdict OK", f, err)
	}
	mr.Del("ci:o/r:abc")
	_ = mr.Set("ci:o/r:abc", "OK")
	if _, err := Read(ctx, c, "o/r", "abc"); err == nil {
		t.Fatal("a string at the key read as a record; want WRONGTYPE")
	}
	if Of(nil) != "" || Green("ok") || Green("") {
		t.Fatal("only exactly OK is green")
	}
}
