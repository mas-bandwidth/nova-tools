//go:build functional

package main

import (
	"bytes"
	"context"
	"regexp"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestBenchBeatWritesTheTableRow (#3440): `bench beat --once` writes the
// host row bench:<b> (host, load1, ncpu, at) beside its beat. (The friend
// row's verb left nova-sprint with the friend verbs; ns_friend_row and
// life.FriendRow stay for nova-friend.)
func TestBenchBeatWritesTheTableRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runBench(ctx, []string{"beat", "--redis", addr, "--bench", "b1", "--root", t.TempDir(), "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("bench beat exit %d: %s", code, errOut.String())
	}
	row := client.HGetAll(ctx, "bench:b1").Val()
	if row["host"] != "b1" || row["ncpu"] == "" || row["load1"] == "" || !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(row["at"]) {
		t.Fatalf("bench:b1 = %v", row)
	}
}
