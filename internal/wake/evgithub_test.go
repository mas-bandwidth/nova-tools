package wake

import (
	"context"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Write the established key independently of the reader's constant. This
// catches a split producer/consumer key while checking the existing cursor,
// count and whitespace behavior against the Redis protocol fake.
func TestEvGithubReadsTheEstablishedStream(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	e := OpenEvGithub(mr.Addr(), "", "")
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	if tip, err := e.Tip(ctx); err != nil || tip != "0-0" {
		t.Fatalf("empty tip = %q, %v", tip, err)
	}
	for _, id := range []string{"1-0", "2-0"} {
		_, err := e.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: "ev:github", ID: id,
			Values: map[string]any{
				"repo": " o/r ", "number": " 17 ", "kind": " check_run ",
				"action": " completed ", "head": " abc ", "sender": " reader ",
				"at": " timestamp ",
			},
		}).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	if tip, err := e.Tip(ctx); err != nil || tip != "2-0" {
		t.Fatalf("populated tip = %q, %v", tip, err)
	}
	cursor := "0-0"
	for _, id := range []string{"1-0", "2-0"} {
		got, err := e.Read(ctx, cursor, 1, -1) // no BLOCK and no wall-clock wait
		want := []Event{{ID: id, Repo: "o/r", Number: "17", Kind: "check_run", Action: "completed", Head: "abc", Sender: "reader", At: "timestamp"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("after %s = %#v, %v; want %#v", cursor, got, err, want)
		}
		cursor = id
	}
	if got, err := e.Read(ctx, cursor, 1, -1); err != nil || len(got) != 0 {
		t.Fatalf("drained = %#v, %v", got, err)
	}
}
