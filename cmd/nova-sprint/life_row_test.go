package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestFriendRowAndBenchBeatWriteTheTableRows (#3440): `friend row --once`
// writes friend:<f> and prints one receipt; `bench beat --once` writes the
// host row bench:<b> (host, load1, ncpu, at) beside its beat; a missing flag
// is refused.
func TestFriendRowAndBenchBeatWriteTheTableRows(t *testing.T) {
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	client.SAdd(ctx, "sprint:s1:idx:walter:working", "a")
	client.HSet(ctx, "friend:walter:desired", "slots", "8")
	client.HSet(ctx, "friend:walter:beat", "at", "1")

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

// TestFriendRowClearDownOnNewerSeatBeat (#3824): friend:<f>:down is cleared
// when friend:<f>:beat at is newer than the down stamp; older beat stays down;
// keeper evidence alone never clears.
func TestFriendRowClearDownOnNewerSeatBeat(t *testing.T) {
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}

	// 1. Older beat stays down, no UP line printed.
	client.Set(ctx, "friend:walter:down", "glenn-not-up@2026-09-25T12:00:00Z", 0)
	client.HSet(ctx, "friend:walter:beat", "at", "1790337599000") // T - 1s (2026-09-25T11:59:59Z)
	var out, errOut bytes.Buffer
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--sprint", "s1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if client.Exists(ctx, "friend:walter:down").Val() != 1 {
		t.Fatal("down key should stay when beat is older")
	}
	if strings.Contains(out.String(), "UP ") {
		t.Fatalf("no UP line expected with older beat, got %q", out.String())
	}

	// 2. Newer beat clears down, prints one UP line.
	out.Reset()
	errOut.Reset()
	client.HSet(ctx, "friend:walter:beat", "at", "1790337601000") // T + 1s (2026-09-25T12:00:01Z)
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--sprint", "s1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if client.Exists(ctx, "friend:walter:down").Val() != 0 {
		t.Fatal("down key should be deleted when beat is newer")
	}
	wantUP := "UP walter 2026-09-25T12:00:01Z (down since glenn-not-up@2026-09-25T12:00:00Z cleared by seat beat)\n"
	if !strings.HasPrefix(out.String(), wantUP) {
		t.Fatalf("expected UP line prefix %q, got output %q", wantUP, out.String())
	}
	if strings.Count(out.String(), "UP ") != 1 {
		t.Fatalf("expected exactly one UP line, got %q", out.String())
	}

	// 3. Minute-precision stamp clears too.
	out.Reset()
	errOut.Reset()
	client.Set(ctx, "friend:walter:down", "out-of-credits@2026-09-25T12:00Z", 0)
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--sprint", "s1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if client.Exists(ctx, "friend:walter:down").Val() != 0 {
		t.Fatal("down key with minute precision should be deleted when beat is newer")
	}
	wantMinUP := "UP walter 2026-09-25T12:00:01Z (down since out-of-credits@2026-09-25T12:00Z cleared by seat beat)\n"
	if !strings.HasPrefix(out.String(), wantMinUP) {
		t.Fatalf("expected UP line prefix %q, got output %q", wantMinUP, out.String())
	}

	// 4. Keeper evidence alone (no seat beat) never clears.
	out.Reset()
	errOut.Reset()
	client.Set(ctx, "friend:walter:down", "glenn-not-up@2026-09-25T12:00:00Z", 0)
	client.Del(ctx, "friend:walter:beat")
	if code := runFriend(ctx, []string{"row", "--redis", addr, "--as", "walter", "--sprint", "s1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if client.Exists(ctx, "friend:walter:down").Val() != 1 {
		t.Fatal("down key should stay when there is no seat beat")
	}
	if strings.Contains(out.String(), "UP ") {
		t.Fatalf("no UP line expected with keeper evidence alone, got %q", out.String())
	}
}
