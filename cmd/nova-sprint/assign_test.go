package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

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
	seed(t, addr, [][]string{
		{"SADD", "sprints", "s1"},
		{"HSET", "s:s1", "status", "open"},
		{"SADD", "friends", "emma", "stella", "fran"},
		{"HSET", "friend:emma:beat", "at", "1"},
		{"HSET", "friend:stella:beat", "at", "1"},
		{"HSET", "friend:fran:beat", "at", "1"},
		{"HSET", "friend:stella:desired", "slots", "8"},
		{"HSET", "friend:fran:desired", "slots", "8"},
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
	code := run([]string{"assign", "--redis", addr, "--sprint", "s1", "--stdin", "--actor", "rowan"}, &out, &errOut)
	want := "DEDUP read-7a: fran posted a typed line at " + head + "\n" +
		"ASSIGN MOVED read-7a -> stella from=emma\n" +
		"ASSIGN NOTFOUND read-nope -> stella\n" +
		"ASSIGNED 1/3 sprint=s1 calls=1\n"
	if code != 1 || out.String() != want {
		t.Fatalf("assign exit %d, stdout:\n%s\nwant:\n%s\nstderr: %s", code, out.String(), want, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code = run([]string{"redistribute", "--redis", addr, "--from", "emma", "--reason", "underfull", "--kind", "work", "--to", "stella", "--actor", "rowan"}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "MOVED build-1 -> stella [moved from emma: underfull]") ||
		!strings.Contains(out.String(), "REDISTRIBUTE friend=emma state=- moved=1 leases=0 released=0 unrouted=0 kept=0") {
		t.Fatalf("redistribute exit %d:\n%s%s", code, out.String(), errOut.String())
	}
	if title := client.HGet(ctx, "s:s1:task:build-1", "title").Val(); title != "build-1 [moved from emma: underfull]" {
		t.Fatalf("title %q", title)
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"assign", "--redis", addr, "--sprint", "s1"}, &out, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), "needs --sprint <S> and --stdin") {
		t.Fatalf("assign without --stdin: %d %s", code, errOut.String())
	}
}
