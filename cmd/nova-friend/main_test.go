package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// Every store client this test binary opens refuses a closed port at once
// rather than after go-redis's retry waits: a test asserts the refusal,
// never the library's backoff.
func init() { store.NoRetryWaits() }

// runFriend is the tool with its own environment: nothing here reads or
// sets the process's (never t.Setenv).
func runFriend(env map[string]string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, func(k string) string { return env[k] }, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{}, {"fly"}, {"help", "x"}, {"version", "x"}, {"wake", "rowan"}} {
		code, stdout, stderr := runFriend(nil, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
		if stdout != "" {
			t.Fatalf("%v: stdout %q; a refusal belongs on stderr", args, stdout)
		}
		if !strings.Contains(stderr, "run: nova-friend help") || strings.Count(stderr, "\n") != 1 {
			t.Fatalf("%v: stderr %q, want one line naming the door", args, stderr)
		}
	}
}

func TestHelpEndsInExamplesAStrangerCanPaste(t *testing.T) {
	t.Parallel()

	code, banner, stderr := runFriend(nil, "help")
	if code != 0 || stderr != "" {
		t.Fatalf("help exit %d stderr %q", code, stderr)
	}
	examples, err := onboarding.ExampleLines(banner, "nova-friend")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 3 {
		t.Fatalf("example block holds %d commands, want 3:\n%s", len(examples), strings.Join(examples, "\n"))
	}
	for _, ex := range examples {
		if strings.ContainsAny(ex, "<>") {
			t.Errorf("example %q carries a placeholder", ex)
		}
	}
	for _, gone := range []string{"nova-friend wake", "--machine"} {
		if strings.Contains(banner, gone) {
			t.Errorf("the banner still says %q; wake comes later and machine is runtime", gone)
		}
	}
	code, out, _ := runFriend(nil, "version")
	if code != 0 || !strings.HasPrefix(out, "nova-friend ") {
		t.Fatalf("version exit %d %q", code, out)
	}
}

// helpFlagSet runs the verb with -h and returns the flag set whose Parse
// raised verbflag.Help.
func helpFlagSet(verb string) (fs *flag.FlagSet) {
	defer func() {
		if r := recover(); r != nil {
			h, ok := r.(verbflag.Help)
			if !ok {
				panic(r)
			}
			fs = h.FS
		}
	}()
	e := env{getenv: func(string) string { return "" }}
	args := []string{"-h"}
	ctx := context.Background()
	switch verb {
	case "here":
		runHere(ctx, e, args, io.Discard, io.Discard)
	case "bye":
		runBye(ctx, e, args, io.Discard, io.Discard)
	case "pull":
		runPull(ctx, e, args, io.Discard, io.Discard)
	case "done":
		runDone(ctx, e, args, io.Discard, io.Discard)
	case "list":
		runList(ctx, e, args, io.Discard, io.Discard)
	case "show":
		runShow(ctx, e, args, io.Discard, io.Discard)
	case "away":
		runAway(ctx, e, true, args, io.Discard, io.Discard)
	case "back":
		runAway(ctx, e, false, args, io.Discard, io.Discard)
	}
	return nil
}

// everyVerb is the tool's whole grammar; a verb added to the dispatcher
// fails TestEveryVerbAnswersDashHNamingEveryFlag until it is here.
var everyVerb = []string{"here", "bye", "pull", "done", "list", "show", "away", "back"}

// TestEveryVerbAnswersDashHNamingEveryFlag: -h on every verb prints its
// usage line and each flag with a help line on stdout, exit 2, and dials
// nothing; every flag carries a help line, and every verb takes --redis.
func TestEveryVerbAnswersDashHNamingEveryFlag(t *testing.T) {
	t.Parallel()

	for _, verb := range everyVerb {
		fs := helpFlagSet(verb)
		if fs == nil {
			t.Fatalf("%s -h did not raise verbflag.Help", verb)
		}
		if fs.Lookup("redis") == nil {
			t.Errorf("%s takes no --redis", verb)
		}
		fs.VisitAll(func(f *flag.Flag) {
			if strings.TrimSpace(f.Usage) == "" {
				t.Errorf("%s --%s has no help line", verb, f.Name)
			}
		})
		code, out, errOut := runFriend(nil, verb, "-h")
		if code != 2 || errOut != "" || !strings.HasPrefix(out, "usage: nova-friend "+verb+" [flags]\n") || !strings.Contains(out, "exit codes: 0 done, 1 refused, 2 usage") {
			t.Errorf("%s -h: exit %d stdout %q stderr %q", verb, code, out, errOut)
		}
	}
	code, _, errOut := runFriend(nil, "list", "--fixture", "x")
	if code != 2 || !strings.Contains(errOut, "flag provided but not defined: -fixture") {
		t.Fatalf("a mistyped flag: exit %d %q", code, errOut)
	}
}

// TestARefusalSaysWhatTheInputWants: every usage refusal is one stderr
// line naming what the flag wants and the door, exit 2, nothing dialled.
func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	t.Parallel()

	seat := map[string]string{"NOVA_FRIEND": "rowan"}
	me := strconv.Itoa(os.Getpid())
	rows := []struct {
		env  map[string]string
		args []string
		want string
	}{
		{nil, []string{"here"}, "nova-friend here: --as wants your friend name (NOVA_FRIEND is empty); run: nova-friend help\n"},
		{seat, []string{"here", "--as", "stella"}, "nova-friend here: --as stella is not the seat (NOVA_FRIEND=rowan); run: nova-friend help\n"},
		{nil, []string{"here", "--as", "Rowan"}, "nova-friend here: friend name \"Rowan\" wants lower case; run: nova-friend help\n"},
		{nil, []string{"here", "--as", "ro wan"}, "nova-friend here: friend name \"ro wan\" wants letters, digits, dots and dashes; run: nova-friend help\n"},
		{seat, []string{"here", "rowan"}, "nova-friend here: takes flags, not positional arguments; run: nova-friend help\n"},
		{seat, []string{"here", "--host", "a:b"}, "nova-friend here: --host wants one word without a colon; run: nova-friend help\n"},
		{seat, []string{"here", "--pid", me}, "nova-friend here: --pid wants your harness's live pid, not this process; run: nova-friend help\n"},
		{nil, []string{"bye"}, "nova-friend bye: --as wants your friend name (NOVA_FRIEND is empty); run: nova-friend help\n"},
		{seat, []string{"bye", "rowan"}, "nova-friend bye: takes flags, not positional arguments; run: nova-friend help\n"},
		{seat, []string{"pull", "--n", "-1"}, "nova-friend pull: --n wants a positive count; omit it to fill every free slot; run: nova-friend help\n"},
		{seat, []string{"pull", "--model", "opus 5.5"}, "nova-friend pull: --model wants one word, got \"opus 5.5\"; run: nova-friend help\n"},
		{seat, []string{"done", "--ok"}, "nova-friend done: --id wants a copy id <primary>~<n>; run: nova-friend help\n"},
		{seat, []string{"done", "--id", "q1~1"}, "nova-friend done: wants exactly one of --ok, --score <N>/10 and --fail <why>; run: nova-friend help\n"},
		{seat, []string{"done", "--id", "q1~1", "--ok", "--fail", "x"}, "nova-friend done: wants exactly one of --ok, --score <N>/10 and --fail <why>; run: nova-friend help\n"},
		{seat, []string{"done", "--id", "q1~1", "--ok", "--pr", "nova-tools#1"}, "nova-friend done: --pr wants --head <sha>; run: nova-friend help\n"},
		{seat, []string{"done", "--id", "q1~1", "--fail", "x", "--pr", "nova-tools#1", "--head", "abc"}, "nova-friend done: --pr goes with --ok; run: nova-friend help\n"},
		{seat, []string{"done", "--id", "q1~1", "--ok", "--pr", "nova-tools", "--head", "abc"}, "nova-friend done: --pr wants <repo>#<n>; run: nova-friend help\n"},
		{seat, []string{"done", "--id", "q1~1", "--score", "11/10"}, "nova-friend done: --score wants N/10, N 1-10; run: nova-friend help\n"},
		{seat, []string{"away", "stella"}, "nova-friend away: --reason wants why stella is away; run: nova-friend help\n"},
		{seat, []string{"away", "--reason", "x"}, "nova-friend away: want one friend name; flags may come before or after it; run: nova-friend help\n"},
		{seat, []string{"back", "stella", "emma"}, "nova-friend back: want one friend name; flags may come before or after it; run: nova-friend help\n"},
		{seat, []string{"show"}, "nova-friend show: want one friend name; flags may come before or after it; run: nova-friend help\n"},
		{seat, []string{"list", "rowan"}, "nova-friend list: takes flags, not positional arguments; run: nova-friend help\n"},
		{nil, []string{"list"}, "nova-friend list: want --redis <addr> (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-friend help\n"},
	}
	for _, r := range rows {
		code, out, errOut := runFriend(r.env, r.args...)
		if code != 2 || out != "" || errOut != r.want {
			t.Errorf("%v (env %v): exit %d stdout %q stderr %q\nwant exit 2 and %q", r.args, r.env, code, out, errOut, r.want)
		}
	}
	// a store that does not answer is could-not-run, one line, at once
	code, out, errOut := runFriend(nil, "list", "--redis", "127.0.0.1:1")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-friend list: ") {
		t.Fatalf("a closed port: exit %d %q %q", code, out, errOut)
	}
}

// TestEnvNamesTheSeatAndTheStore: --as defaults to NOVA_FRIEND and --redis
// to NOVA_SPRINT_REDIS then NOVA_REDIS_ADDR, read through the seam.
func TestEnvNamesTheSeatAndTheStore(t *testing.T) {
	t.Parallel()

	e := env{getenv: func(k string) string {
		return map[string]string{"NOVA_FRIEND": "rowan", "NOVA_REDIS_ADDR": "b:1"}[k]
	}}
	if got, err := e.actor(""); err != nil || got != "rowan" {
		t.Fatalf("actor from the seat: %q %v", got, err)
	}
	if got, err := e.actor("rowan"); err != nil || got != "rowan" {
		t.Fatalf("actor equal to the seat: %q %v", got, err)
	}
	if e.redis("") != "b:1" || e.redis("c:2") != "c:2" {
		t.Fatalf("redis: %q %q", e.redis(""), e.redis("c:2"))
	}
	e.getenv = func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": "a:1", "NOVA_REDIS_ADDR": "b:1"}[k]
	}
	if e.redis("") != "a:1" {
		t.Fatalf("NOVA_SPRINT_REDIS first: %q", e.redis(""))
	}
	if _, err := e.actor(""); err == nil {
		t.Fatal("no seat and no --as was not refused")
	}
}
