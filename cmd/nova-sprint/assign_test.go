package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

var seatFCalls = regexp.MustCompile(`(?m)^cmdstat_fcall:calls=(\d+),`)

func fcallCount(t *testing.T, client *redis.Client) int {
	t.Helper()
	info, err := client.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	m := seatFCalls.FindStringSubmatch(info)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func assignSeatFixture(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := startThrowawayRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	seed(t, addr, [][]string{
		{"SADD", "friends", "a", "b", "c"}, {"SADD", "sprints", "s"}, {"HSET", "s:s", "status", "open"},
		{"HSET", "friend:a:roles", "roles", "coordinator"}, {"HSET", "friend:b:roles", "roles", "may-hold,builder"},
		{"HSET", "friend:c:roles", "roles", "may-hold,builder"},
		{"HSET", "friend:a:desired", "slots", "8", "paused", "0"}, {"HSET", "friend:b:desired", "slots", "8", "paused", "0"}, {"HSET", "friend:c:desired", "slots", "8", "paused", "0"},
		{"HSET", "friend:a:beat", "at", "1"}, {"HSET", "friend:b:beat", "at", "1"}, {"HSET", "friend:c:beat", "at", "1"},
		{"HSET", "task:r1", "state", "open", "kind", "read", "priority", "1", "repo", "r", "pr", "1", "head", strings.Repeat("1", 40), "author", "a"},
		{"ZADD", "s:s:open:c", "1", "r1"}, {"SADD", "s:s:idx:task:open", "r1"},
	})
	return addr, client
}

func TestSeatNeutralActorFromSeat(t *testing.T) {
	addr, client := assignSeatFixture(t)
	t.Setenv("NOVA_FRIEND", "a")
	assignStdin = strings.NewReader("r1 b\n")
	var out, errOut bytes.Buffer
	if code := run([]string{"assign", "--redis", addr, "--sprint", "s", "--stdin"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d %s", code, errOut.String())
	}
	entries := client.XRange(context.Background(), "s:s:log", "-", "+").Val()
	if got := entries[len(entries)-1].Values["actor"]; got != "a" {
		t.Fatalf("actor %v", got)
	}
}

func TestSeatNeutralRosterFromRedis(t *testing.T) {
	addr, client := assignSeatFixture(t)
	t.Setenv("NOVA_FRIEND", "a")
	assignStdin = strings.NewReader("r1 b\n")
	var out, errOut bytes.Buffer
	if code := run([]string{"assign", "--redis", addr, "--sprint", "s", "--stdin"}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if client.ZScore(context.Background(), "s:s:open:b", "r1").Err() != nil {
		t.Fatal("role-bearing target did not receive read")
	}
}

func TestSeatNeutralEmptyActorRefused(t *testing.T) {
	addr, client := assignSeatFixture(t)
	_ = os.Unsetenv("NOVA_FRIEND")
	before := fcallCount(t, client)
	assignStdin = strings.NewReader("r1 b\n")
	var out, errOut bytes.Buffer
	code := run([]string{"assign", "--redis", addr, "--sprint", "s", "--stdin"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "NOVA_FRIEND is empty") || fcallCount(t, client) != before {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
}

func TestSeatNeutralMismatchedActorRefused(t *testing.T) {
	addr, client := assignSeatFixture(t)
	t.Setenv("NOVA_FRIEND", "a")
	before := fcallCount(t, client)
	assignStdin = strings.NewReader("r1 b\n")
	var out, errOut bytes.Buffer
	code := run([]string{"assign", "--redis", addr, "--sprint", "s", "--stdin", "--actor", "b"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "actor") || fcallCount(t, client) != before {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
}

func TestSeatNeutralActorMustBeAFriend(t *testing.T) {
	t.Parallel()

	addr, client := assignSeatFixture(t)
	_ = addr
	ctx := context.Background()
	calls := [][]any{
		{"ns_assign_batch", "s", "why", "zz", "i", "r1", "b"},
		{"ns_redistribute_from", "c", "why", "", "", "zz", "i"},
		{"ns_redistribute_floor", "s", 1, 1, "zz", "i"},
		{"ns_friend_roles", "b", "may-hold", "zz", "i"},
	}
	for _, call := range calls {
		name := call[0].(string)
		if _, err := client.FCall(ctx, name, nil, call[1:]...).Result(); err == nil || !strings.Contains(err.Error(), "ACTOR zz") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestSeatNeutralRoleFlagsRefused(t *testing.T) {
	addr, client := assignSeatFixture(t)
	t.Setenv("NOVA_FRIEND", "a")
	for _, flag := range []string{"--may-hold", "--builders", "--coordinator"} {
		before := fcallCount(t, client)
		var out, errOut bytes.Buffer
		code := run([]string{"redistribute", "--redis", addr, "--from", "c", "--reason", "x", flag, "b"}, &out, &errOut)
		if code != 2 || !strings.Contains(errOut.String(), "REFUSED roster flags") || fcallCount(t, client) != before {
			t.Fatalf("%s exit %d stderr %q", flag, code, errOut.String())
		}
	}
}

func TestSeatNeutralNoFriendLiterals(t *testing.T) {
	t.Parallel()

	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	for _, path := range []string{"assign.go", "friend_roles.go", filepath.Join("..", "..", "internal", "nsprint", "fn", "lua", "friend_roles.lua"), filepath.Join("..", "..", "internal", "nsprint", "fn", "lua", "redistribute_floor.lua")} {
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"rowan", "stella", "johnny", "emma"} {
			if strings.Contains(strings.ToLower(string(body)), name) {
				t.Fatalf("%s contains friend literal %s", path, name)
			}
		}
	}
}

func TestFloorIsOneBatch(t *testing.T) {
	addr, client := assignSeatFixture(t)
	seed(t, addr, [][]string{
		{"HSET", "task:r2", "state", "open", "kind", "read", "priority", "2", "repo", "r", "pr", "2", "head", strings.Repeat("2", 40), "author", "a"},
		{"ZADD", "s:s:open:c", "2", "r2"}, {"SADD", "s:s:idx:task:open", "r2"},
	})
	t.Setenv("NOVA_FRIEND", "a")
	before := fcallCount(t, client)
	var out, errOut bytes.Buffer
	code := run([]string{"redistribute", "--redis", addr, "--sprint", "s", "--floor", "1", "--max-move", "1"}, &out, &errOut)
	if code != 0 || fcallCount(t, client) != before+1 || !strings.Contains(out.String(), "moved=1") {
		t.Fatalf("exit %d out %q err %q calls %d", code, out.String(), errOut.String(), fcallCount(t, client)-before)
	}
}

// TestAssignStdinVerbPrintsOneLinePerInputLine drives `nova-sprint assign
// --stdin` and `nova-sprint redistribute --from` end to end on a throwaway
// Redis: one line per input line in order, DEDUP spelled as the spec prints
// it, exit 1 when a line is refused, and the moved title marker.
func TestAssignStdinVerbPrintsOneLinePerInputLine(t *testing.T) {
	addr := startThrowawayRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	t.Setenv("NOVA_FRIEND", "emma") // #2929: task push runs from a seat
	seed(t, addr, [][]string{
		{"SADD", "sprints", "s1"},
		{"HSET", "s:s1", "status", "open"},
		{"SADD", "friends", "emma", "stella", "fran"},
		{"HSET", "friend:emma:beat", "at", "1"},
		{"HSET", "friend:stella:beat", "at", "1"},
		{"HSET", "friend:fran:beat", "at", "1"},
		{"HSET", "friend:stella:desired", "slots", "8"},
		{"HSET", "friend:fran:desired", "slots", "8"},
		{"HSET", "friend:emma:roles", "roles", "may-hold,builder"},
		{"HSET", "friend:stella:roles", "roles", "may-hold,builder"},
		{"HSET", "friend:fran:roles", "roles", "may-hold"},
		{"HSET", "s:s1:pr:nova-tools:7", "head", head, "author", "rowan"},
		{"HSET", "s:s1:disp:nova-tools:7", "fran@" + head, "HOLD 6 at head"},
	})
	for _, id := range []string{"read-7a", "build-1"} {
		kind, pr := "read", []string{"--repo", "nova-tools", "--pr", "7", "--head", head}
		if id == "build-1" {
			kind, pr = "work", nil
		}
		var out, errOut bytes.Buffer
		args := append([]string{"task", "push", "--redis", addr, "--sprint", "s1", "--id", id, "--kind", kind, "--title", id, "--to", "emma"}, pr...)
		if code := run(args, &out, &errOut); code != 0 {
			t.Fatalf("push %s: %d %s %s", id, code, out.String(), errOut.String())
		}
	}

	assignStdin = strings.NewReader("# task friend\nread-7a fran\nread-7a stella\n\nread-nope stella\n")
	t.Cleanup(func() { assignStdin = strings.NewReader("") })
	var out, errOut bytes.Buffer
	code := run([]string{"assign", "--redis", addr, "--sprint", "s1", "--stdin"}, &out, &errOut)
	want := "DEDUP read-7a: fran posted a typed line at " + head + "\n" +
		"ASSIGN MOVED read-7a -> stella from=emma\n" +
		"ASSIGN NOTFOUND read-nope -> stella\n" +
		"ASSIGNED 1/3 sprint=s1 calls=1\n"
	if code != 1 || out.String() != want {
		t.Fatalf("assign exit %d, stdout:\n%s\nwant:\n%s\nstderr: %s", code, out.String(), want, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = run([]string{"redistribute", "--redis", addr, "--from", "emma", "--reason", "underfull", "--kind", "work", "--to", "stella"}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "MOVED build-1 -> stella [moved from emma: underfull]") ||
		!strings.Contains(out.String(), "REDISTRIBUTE friend=emma state=- moved=1 leases=0 released=0 unrouted=0 kept=0") {
		t.Fatalf("redistribute exit %d:\n%s%s", code, out.String(), errOut.String())
	}
	if title := client.HGet(ctx, "task:build-1", "title").Val(); title != "build-1 [moved from emma: underfull]" {
		t.Fatalf("title %q", title)
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"assign", "--redis", addr, "--sprint", "s1"}, &out, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), "needs --sprint <S> and --stdin") {
		t.Fatalf("assign without --stdin: %d %s", code, errOut.String())
	}
}
