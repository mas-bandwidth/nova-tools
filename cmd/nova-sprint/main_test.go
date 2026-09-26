package main

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runSprint(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint()
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout %q; a refusal belongs on stderr", stdout)
	}
	if !strings.Contains(stderr, "run: nova-sprint help") {
		t.Fatalf("stderr %q, want the help door", stderr)
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("a bare command printed more than one line:\n%s", stderr)
	}
}

func TestTableRefusalNamesEveryMissingPiece(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("table")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--redis <addr>", "--once", "--loop", "--check", "written nowhere", "run: nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestRefreshOwnSession(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		code, _, stderr := runSprint("refresh", "--", "true")
		if code != 2 || !strings.Contains(stderr, "setsid") {
			t.Fatalf("windows refresh exit %d stderr %q, want a setsid refusal", code, stderr)
		}
		return
	}
	code, stdout, stderr := runSprint("refresh", "--", "/usr/bin/true")
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "REFRESH SESSION pid=") {
		t.Fatalf("stdout %q, want REFRESH SESSION pid=", stdout)
	}
}

func TestRefreshRefusesAMissingCommand(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("refresh")
	if code != 2 || !strings.Contains(stderr, "--") {
		t.Fatalf("exit %d stderr %q, want a refusal that names --", code, stderr)
	}
}

// TestEndRefusesRelativeResults is the #3329 DONE-WHEN (writer half): the
// card hash field `results` is always absolute, so `card end --results
// rel/dir` exits 1 before it touches Redis, and ns_card_end, the field's
// single writer, refuses a relative dir itself and writes nothing.
func TestEndRefusesRelativeResults(t *testing.T) {
	const (
		sprint   = "s3329"
		label    = "rel"
		identity = sprint + "/" + label + "/0123abcd/b/1"
		token    = "tok-3329"
	)
	t.Run("card end", func(t *testing.T) {
		t.Setenv(store.UserEnv, "")
		mr := miniredis.RunT(t)
		mr.HSet("s:"+sprint+":card:"+label, "state", "running", "attempt", "1",
			"token", token, "identity", identity, "bench", "b")
		before := mr.Dump()
		code, stdout, stderr := runSprint("card", "end", "--redis", mr.Addr(), "--sprint", sprint, label,
			"--token", token, "--outcome", "DONE", "--reason", "done", "--results", identity)
		if code != 1 {
			t.Fatalf("exit %d, want 1 (USAGE); stdout %q stderr %q", code, stdout, stderr)
		}
		if !strings.Contains(stderr, "--results "+identity+" is relative") || !strings.Contains(stderr, "absolute") {
			t.Fatalf("stderr %q does not name the relative --results", stderr)
		}
		if n := mr.CommandCount(); n != 0 {
			t.Fatalf("card end sent %d Redis commands for a relative --results; want none", n)
		}
		if after := mr.Dump(); after != before {
			t.Fatalf("card end wrote to Redis:\nbefore %s\nafter %s", before, after)
		}
	})
	t.Run("ns_card_end", func(t *testing.T) {
		_, client := sprintRedis(t)
		ctx := context.Background()
		key := "s:" + sprint + ":card:" + label
		if err := client.HSet(ctx, key, "state", "running", "attempt", "1", "token", token,
			"token_sha", "sha", "identity", identity, "bench", "b", "base_sha", "0123abcd").Err(); err != nil {
			t.Fatal(err)
		}
		keys := []string{key, "s:" + sprint + ":log", "s:" + sprint + ":idem"}
		reply, err := client.FCall(ctx, "ns_card_end", keys, "token", sprint, label, token, identity,
			identity, "DONE", "done", "sha", "pushed", "0", "DONE", "done").Result()
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(reply); !strings.Contains(got, "USAGE") {
			t.Fatalf("ns_card_end with relative results = %s, want USAGE", got)
		}
		h, err := client.HGetAll(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if h["state"] != "running" || h["results"] != "" {
			t.Fatalf("ns_card_end wrote the card: %v", h)
		}
		if n, _ := client.XLen(ctx, "s:"+sprint+":log").Result(); n != 0 {
			t.Fatalf("ns_card_end wrote %d receipts", n)
		}
	})
}

// TestResultsPathUnixAbsolute is the #3336 repair of Stella's hold: the card
// hash field `results` is a Unix absolute path on the bench (leading '/', not
// '//', no '\', no '..' segment), and the Go gate (`card end`, card.End, the
// harvest pusher) and the Redis gate (ns_card_end) apply the same rule. A
// drive root (`C:\x`, `C:/x`), a network share (`//host/share`), a dot-dot
// path (`/a/../b`) and the empty string are refused by both before anything
// is written, including when the path ends in the card's canonical identity
// (so the refusal is the absolute rule, not the identity bound).
func TestResultsPathUnixAbsolute(t *testing.T) {
	const (
		sprint   = "s3336"
		label    = "abs"
		identity = sprint + "/" + label + "/0123abcd/b/1"
		token    = "tok-3336"
	)
	bad := []string{
		``,
		`C:\x`,
		`C:/x`,
		`//host/share`,
		`/a/../b`,
		`C:\srv\results\` + strings.ReplaceAll(identity, "/", `\`),
		`C:/srv/results/` + identity,
		`//host/share/` + identity,
		`/srv/../results/` + identity,
		`file:///srv/results/` + identity,
		`/srv/results\` + identity,
	}
	for _, results := range bad {
		results := results
		t.Run(fmt.Sprintf("card end %q", results), func(t *testing.T) {
			t.Setenv(store.UserEnv, "")
			mr := miniredis.RunT(t)
			mr.HSet("s:"+sprint+":card:"+label, "state", "running", "attempt", "1",
				"token", token, "identity", identity, "bench", "b")
			before := mr.Dump()
			code, stdout, stderr := runSprint("card", "end", "--redis", mr.Addr(), "--sprint", sprint, label,
				"--token", token, "--outcome", "DONE", "--reason", "done", "--results", results)
			if code != 1 {
				t.Fatalf("exit %d, want 1 (USAGE); stdout %q stderr %q", code, stdout, stderr)
			}
			want := "absolute"
			if results == "" {
				want = "missing value for --results" // the flag parser refuses it first
			}
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr %q lacks %q", stderr, want)
			}
			if n := mr.CommandCount(); n != 0 {
				t.Fatalf("card end sent %d Redis commands for --results %q; want none", n, results)
			}
			if after := mr.Dump(); after != before {
				t.Fatalf("card end wrote to Redis:\nbefore %s\nafter %s", before, after)
			}
		})
		t.Run(fmt.Sprintf("ns_card_end %q", results), func(t *testing.T) {
			_, client := sprintRedis(t)
			ctx := context.Background()
			key := "s:" + sprint + ":card:" + label
			if err := client.HSet(ctx, key, "state", "running", "attempt", "1", "token", token,
				"token_sha", "sha", "identity", identity, "bench", "b", "base_sha", "0123abcd").Err(); err != nil {
				t.Fatal(err)
			}
			keys := []string{key, "s:" + sprint + ":log", "s:" + sprint + ":idem"}
			reply, err := client.FCall(ctx, "ns_card_end", keys, "token", sprint, label, token, results,
				identity, "DONE", "done", "sha", "pushed", "0", "DONE", "done").Result()
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(reply); !strings.Contains(got, "USAGE") {
				t.Fatalf("ns_card_end with results %q = %s, want USAGE", results, got)
			}
			h, err := client.HGetAll(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			if h["state"] != "running" || h["results"] != "" {
				t.Fatalf("ns_card_end wrote the card: %v", h)
			}
			if n, _ := client.XLen(ctx, "s:"+sprint+":log").Result(); n != 0 {
				t.Fatalf("ns_card_end wrote %d receipts", n)
			}
		})
	}
	t.Run("ns_card_end accepts the Unix absolute attempt dir", func(t *testing.T) {
		_, client := sprintRedis(t)
		ctx := context.Background()
		key := "s:" + sprint + ":card:" + label
		if err := client.HSet(ctx, key, "state", "running", "attempt", "1", "token", token,
			"token_sha", "sha", "identity", identity, "bench", "b", "base_sha", "0123abcd").Err(); err != nil {
			t.Fatal(err)
		}
		keys := []string{key, "s:" + sprint + ":log", "s:" + sprint + ":idem"}
		reply, err := client.FCall(ctx, "ns_card_end", keys, "token", sprint, label, token, "/srv/results/"+identity,
			identity, "DONE", "done", "sha", "pushed", "0", "DONE", "done").Result()
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(reply); strings.Contains(got, "USAGE") {
			t.Fatalf("ns_card_end refused a Unix absolute results dir: %s", got)
		}
	})
}
