package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// TestHelperServeDispatch is the fake harness the serve tests declare as the
// seat's dispatch: this test binary, which writes one DONE line when
// NOVA_SERVE_FAKE is set and is otherwise an empty test.
func TestHelperServeDispatch(t *testing.T) {
	t.Parallel()

	if os.Getenv("NOVA_SERVE_FAKE") == "" {
		return
	}
	fmt.Printf("DONE served %s\n", os.Getenv(life.ServeEnvID))
}

// TestFriendServeUsageRefusalsOpenNoStore: every usage refusal exits 2 before
// any dial (the --redis address is unreachable on purpose).
func TestFriendServeUsageRefusalsOpenNoStore(t *testing.T) {
	const dead = "127.0.0.1:1"
	refused := func(env string, args ...string) string {
		t.Helper()
		if env != "" {
			t.Setenv(seatEnv, env)
		} else {
			t.Setenv(seatEnv, "")
		}
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Fatalf("%v: exit %d, want 2 (%s%s)", args, code, out.String(), errOut.String())
		}
		return errOut.String()
	}
	if got := refused("emma", "friend", "serve", "--redis", dead); !strings.Contains(got, "--as is required") {
		t.Fatalf("no --as: %s", got)
	}
	if got := refused("", "friend", "serve", "--as", "emma", "--redis", dead); !strings.Contains(got, "NOVA_FRIEND") {
		t.Fatalf("no seat: %s", got)
	}
	if got := refused("rowan", "friend", "wake", "--as", "emma", "--redis", dead); !strings.Contains(got, "equal to NOVA_FRIEND") {
		t.Fatalf("seat mismatch: %s", got)
	}
	if got := refused("emma", "friend", "serve", "--as", "emma", "--dispatch", "x", "--redis", dead, "--", "y"); !strings.Contains(got, "not both") {
		t.Fatalf("two harnesses: %s", got)
	}
	if got := refused("emma", "friend", "serve", "--as", "emma", "--width", "-1", "--redis", dead); !strings.Contains(got, "--width") {
		t.Fatalf("bad width: %s", got)
	}
	if got := refused("emma", "friend", "serve", "--as", "emma", "--model", "build", "--redis", dead); !strings.Contains(got, "<kind>=<model>") {
		t.Fatalf("bad --model: %s", got)
	}
}

// TestFriendServeVerb drives the seat through the CLI against a throwaway
// Redis: no declared dispatch refuses (exit 1, naming cfg:friend:<f>), a held
// seat refuses (exit 1, naming the holder), and `friend wake --as` with the
// declared dispatch takes the one ready task, runs the fake harness and
// closes the task with its DONE line.
func TestFriendServeVerb(t *testing.T) {
	addr := startThrowawayRedis(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	st := store.New(client)
	if err := client.HSet(ctx, "s:s1", "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "friends", "emma").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:emma:desired", "slots", "2", "machine", "studio").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "machine:studio:ceiling", "slots", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: "s1", ID: "w1", Kind: task.KindWork, Title: "build w1",
		Effects: task.EffectsNone, PayloadSHA: "w1", To: "emma",
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push: %s, %v", got, err)
	}
	t.Setenv(seatEnv, "emma")
	t.Setenv("NOVA_SERVE_FAKE", "done")
	dir := t.TempDir()
	base := []string{"friend", "wake", "--as", "emma", "--redis", addr, "--sprint", "s1", "--dir", dir, "--host", "studio"}

	var out, errOut bytes.Buffer
	if code := run(base, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "cfg:friend:emma") {
		t.Fatalf("no dispatch: exit %d (%s%s), want 1 naming cfg:friend:emma", code, out.String(), errOut.String())
	}
	if state := client.HGet(ctx, task.Key("s1", "w1"), "state").Val(); state != "open" {
		t.Fatalf("a refused serve touched the task: state %s", state)
	}

	if err := client.HSet(ctx, cfgFriendKey("emma"), "dispatch", os.Args[0]+" -test.run=^TestHelperServeDispatch$").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, life.LockKey("emma"), "other-session", life.ServeLockTTL).Err(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := run(base, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "seat-held holder=other-session") {
		t.Fatalf("held seat: exit %d (%s%s), want 1 naming the holder", code, out.String(), errOut.String())
	}
	if err := client.Del(ctx, life.LockKey("emma")).Err(); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errOut.Reset()
	if code := run(base, &out, &errOut); code != 0 {
		t.Fatalf("wake --as: exit %d: %s%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "SERVE emma once taken=1 closed=1 width=2") {
		t.Fatalf("wake --as line: %s", out.String())
	}
	h := client.HGetAll(ctx, task.Key("s1", "w1")).Val()
	if h["state"] != "closed" || !strings.HasPrefix(h["evidence"], "DONE served w1") {
		t.Fatalf("task after wake: state=%s evidence=%q", h["state"], h["evidence"])
	}
	if client.Exists(ctx, life.LockKey("emma")).Val() != 0 {
		t.Fatalf("seat lock held after the one-shot pass")
	}
	if n := client.XLen(ctx, life.LogKey("emma")).Val(); n == 0 {
		t.Fatalf("no receipts on friend:emma:log")
	}
}

// TestFriendServeLogin is #3797's DONE-WHEN on a throwaway server: before
// the seat starts, typed hold and read lines signed who=rowan-claude are
// REFUSED unknown-who; `friend serve --as rowan --login rowan-claude` writes
// friends:login through ns_friend_hello on start (one friend-login receipt)
// and the same lines then record against rowan. A clashing alias refuses
// with exit 1, writes nothing and leaves the seat free.
func TestFriendServeLogin(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s-serve-login")
	e.friends("stella")
	e.register("rowan", 2)
	e.policy("rowan", "stella")
	if err := e.c.HSet(ctx, cfgFriendKey("rowan"), "dispatch", os.Args[0]+" -test.run=^TestHelperServeDispatch$").Err(); err != nil {
		t.Fatal(err)
	}
	e.unit(3286, "b2d830d36bef4d7d907b461e7a176c6009996d9d", "johnny")
	e.unit(2804, "5c22281227787713632a1cd01f90ba9a2be61277", "johnny")
	hold := strings.Replace(fixture(t, "5802800953.md"), "who=stella", "who=rowan-claude", 1)
	read := strings.Replace(fixture(t, "5802733875.md"), "who=stella", "who=rowan-claude", 1)
	if hold == fixture(t, "5802800953.md") || read == fixture(t, "5802733875.md") {
		t.Fatal("fixtures no longer carry who=stella")
	}
	for pr, b := range map[int]string{3286: hold, 2804: read} {
		if code, out, _ := e.ingest(pr, b); code != 2 || !strings.HasPrefix(out, "REFUSED unknown-who") {
			t.Fatalf("#%d before serve: exit %d %q, want REFUSED unknown-who", pr, code, out)
		}
	}

	serve := func(extra ...string) (int, string, string) {
		t.Helper()
		t.Setenv(seatEnv, "rowan")
		args := append([]string{"friend", "serve", "--as", "rowan", "--once", "--host", "ctl",
			"--dir", t.TempDir()}, extra...)
		return e.run(args...)
	}
	if code, out, errOut := serve("--login", "stella"); code != 1 || !strings.Contains(errOut, "REFUSED login LOGIN-IS-FRIEND stella") {
		t.Fatalf("serve --login stella: exit %d %s%s, want 1 LOGIN-IS-FRIEND", code, out, errOut)
	}
	if n := e.c.Exists(ctx, "friends:login", life.LockKey("rowan"), "friend:rowan:beat").Val(); n != 0 {
		t.Fatalf("a refused login left %d of friends:login, the seat lock, the beat", n)
	}

	if code, out, errOut := serve("--login", "rowan-claude"); code != 0 || !strings.Contains(out, "SERVE rowan once taken=0") {
		t.Fatalf("serve --login rowan-claude: exit %d %s%s", code, out, errOut)
	}
	if got := e.c.HGetAll(ctx, "friends:login").Val(); len(got) != 1 || got["rowan-claude"] != "rowan" {
		t.Fatalf("friends:login = %v, want rowan-claude=rowan", got)
	}
	logins := 0
	for _, m := range e.c.XRange(ctx, "cap:log", "-", "+").Val() {
		if m.Values["kind"] == "friend-login" && m.Values["subject"] == "rowan" {
			logins++
		}
	}
	if logins != 1 {
		t.Fatalf("cap:log friend-login receipts = %d, want 1", logins)
	}
	if e.c.Exists(ctx, life.LockKey("rowan")).Val() != 0 {
		t.Fatalf("seat lock held after the one-shot pass")
	}

	e.mustIngest(3286, hold, "RECORD hold")
	e.mustIngest(2804, read, "RECORD read")
	if r := e.c.HGetAll(ctx, "s:"+e.S+":read:"+unitOf(2804)+":rowan").Val(); r["verdict"] != "APPROVE" {
		t.Fatalf("read by rowan-claude = %v, want APPROVE on rowan's key", r)
	}

	// A second start with the same alias is a no-op renewal: exit 0, no new receipt.
	if code, _, errOut := serve("--login", "rowan-claude"); code != 0 {
		t.Fatalf("second serve --login rowan-claude: exit %d %s", code, errOut)
	}
	n := 0
	for _, m := range e.c.XRange(ctx, "cap:log", "-", "+").Val() {
		if m.Values["kind"] == "friend-login" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("friend-login receipts after a repeat start = %d, want 1", n)
	}
}
