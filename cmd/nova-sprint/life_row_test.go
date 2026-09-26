//go:build functional

package main

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFriendRowAndBenchBeatWriteTheTableRows (#3440): `friend row --once`
// writes friend:<f> and prints one receipt; `bench beat --once` writes the
// host row bench:<b> (host, load1, ncpu, at) beside its beat; a missing flag
// is refused.
func TestFriendRowAndBenchBeatWriteTheTableRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.SAdd(ctx, "sprint:s1:idx:walter:working", "a")
	client.HSet(ctx, "friend:walter:desired", "slots", "8")
	// up is the beat's at under a minute old (#4233), not the key's existence
	client.HSet(ctx, "friend:walter:beat", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))

	var out, errOut bytes.Buffer
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--sprint", "s1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("friend row exit %d: %s", code, errOut.String())
	}
	if !regexp.MustCompile(`^walter row up=1 ready=0 working=1 waiting=0 done=0 slots=8 at=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ\n$`).MatchString(out.String()) {
		t.Fatalf("receipt %q", out.String())
	}
	if got := client.HGet(ctx, "friend:walter", "width").Val(); got != "1" {
		t.Fatalf("friend:walter width = %q", got)
	}
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--once"}, &out, &errOut); code == 0 {
		t.Fatal("friend row with no --sprint was not refused")
	}
	// a beat two minutes old, with no TTL to expire it, reads down
	client.HSet(ctx, "friend:walter:beat", "at", strconv.FormatInt(time.Now().Add(-2*time.Minute).UnixMilli(), 10))
	out.Reset()
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--sprint", "s1", "--once"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "walter row up=0 ") {
		t.Fatalf("stale beat: exit %d receipt %q %s", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runBench(ctx, []string{"beat", "--redis", addr, "--bench", "b1", "--root", t.TempDir(), "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("bench beat exit %d: %s", code, errOut.String())
	}
	row := client.HGetAll(ctx, "bench:b1").Val()
	if row["host"] != "b1" || row["ncpu"] == "" || row["load1"] == "" || !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(row["at"]) {
		t.Fatalf("bench:b1 = %v", row)
	}
}
