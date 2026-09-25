package life_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// TestHelperDispatch is the fake harness: serve execs this test binary with
// NOVA_SERVE_FAKE set and it behaves as that mode says. Without the variable
// it is an empty test.
func TestHelperDispatch(t *testing.T) {
	mode := os.Getenv("NOVA_SERVE_FAKE")
	if mode == "" {
		return
	}
	switch mode {
	case "score":
		fmt.Printf("reading the diff...\nSCORE who=%s head=%s score=9/10\ncost=$0.01\n",
			os.Getenv(life.ServeEnvFriend), os.Getenv(life.ServeEnvHead))
	case "done":
		fmt.Printf("DONE built %s brief=%s\n", os.Getenv(life.ServeEnvID), os.Getenv(life.ServeEnvBrief))
	case "gh":
		// A child that reaches for the GitHub CLI by name, as a model's shell would.
		out, err := exec.Command("gh", "api", "user").CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			code = -1
		}
		fmt.Printf("DONE gh exit=%d %s\n", code, strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	case "die":
		fmt.Println("boom: harness crashed before any line")
		os.Exit(3)
	case "slot":
		// A card child (#4095): it marks itself live in NOVA_SERVE_LIVE and
		// started in NOVA_SERVE_STARTED, holds its slot until it has seen a
		// second child live or every one of NOVA_SERVE_TOTAL has started (up
		// to NOVA_TEST_WAIT), records the most it saw live, then writes its
		// typed line with the model serve gave it.
		live, started := os.Getenv("NOVA_SERVE_LIVE"), os.Getenv("NOVA_SERVE_STARTED")
		total, _ := strconv.Atoi(os.Getenv("NOVA_SERVE_TOTAL"))
		id := os.Getenv(life.ServeEnvCard)
		mark := filepath.Join(live, id)
		if os.WriteFile(mark, nil, 0o644) != nil || os.WriteFile(filepath.Join(started, id), nil, 0o644) != nil {
			fmt.Println("cannot mark", id)
			os.Exit(5)
		}
		wait := 30 * time.Second
		if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				wait = d
			}
		}
		deadline, most := time.Now().Add(wait), 0
		for {
			nl, _ := os.ReadDir(live)
			ns, _ := os.ReadDir(started)
			most = max(most, len(nl))
			if most >= 2 || len(ns) >= total {
				break
			}
			if time.Now().After(deadline) {
				fmt.Println("never saw a second child")
				os.Exit(4)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if f, err := os.OpenFile(os.Getenv("NOVA_SERVE_SEEN"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%d\n", most)
			_ = f.Close()
		}
		_ = os.Remove(mark)
		fmt.Printf("DONE built %s model=%s\n", os.Getenv(life.ServeEnvCard), os.Getenv(life.ServeEnvModel))
	case "wait":
		// The child polls for its release file (the test's event) up to
		// NOVA_TEST_WAIT; a child never released is what a stop kills.
		wait := 30 * time.Second
		if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				wait = d
			}
		}
		deadline := time.Now().Add(wait)
		for {
			if _, err := os.Stat(os.Getenv("NOVA_SERVE_GO")); err == nil {
				fmt.Printf("DONE released %s\n", os.Getenv(life.ServeEnvID))
				return
			}
			if time.Now().After(deadline) {
				fmt.Println("never released")
				os.Exit(4)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// fakeDispatch is this binary in one fake mode; goFile is the release file a
// "wait" child polls for.
func fakeDispatch(mode, goFile string) (argv, env []string) {
	return []string{os.Args[0], "-test.run=^TestHelperDispatch$"},
		[]string{"NOVA_SERVE_FAKE=" + mode, "NOVA_SERVE_GO=" + goFile}
}

const serveHead = "0123456789abcdef0123456789abcdef01234567"

// seedSeat registers friend emma with slots desired, opens sprint s1 and
// returns the store and client. No beat: the seat is down until serve beats.
func seedSeat(t *testing.T, slots int) (*store.Store, *redis.Client) {
	t.Helper()
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	if err := client.HSet(ctx, "s:s1", "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "friends", "emma").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:emma:desired", "slots", slots, "machine", "studio", "paused", 0, "at", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "machine:studio:ceiling", "slots", slots).Err(); err != nil {
		t.Fatal(err)
	}
	return st, client
}

func pushWork(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if got, err := task.Push(context.Background(), st, task.PushRequest{
		Sprint: "s1", ID: id, Kind: task.KindWork, Title: "build " + id,
		Effects: task.EffectsNone, PayloadSHA: id, To: "emma",
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push %s: %s, %v", id, got, err)
	}
}

func serveConfig(t *testing.T, session string, width int, mode string) life.ServeConfig {
	t.Helper()
	dir := t.TempDir()
	argv, env := fakeDispatch(mode, goFile(dir))
	return life.ServeConfig{
		Friend: "emma", Session: session, Host: "studio", Harness: "fake", Sprint: "s1",
		Width: width, Dispatch: argv, Dir: dir, Env: env, Out: testWriter{t},
	}
}

// goFile is the release file the "wait" children of a serve dir poll for.
func goFile(dir string) string { return filepath.Join(dir, "go") }

func release(t *testing.T, cfg life.ServeConfig) {
	t.Helper()
	if err := os.WriteFile(goFile(cfg.Dir), []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func logKinds(t *testing.T, client *redis.Client) []string {
	t.Helper()
	entries, err := client.XRange(context.Background(), life.LogKey("emma"), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(entries))
	for _, e := range entries {
		kinds = append(kinds, fmt.Sprint(e.Values["kind"]))
	}
	return kinds
}

// TestServeFakeDispatchScoreLineClosesTask: a read in emma's queue is taken by
// her seat, dispatched to a fake harness that writes a SCORE line, and closed
// with that line as its evidence, APPROVE 9, at the task's head; the seat's
// receipts are on friend:emma:log and the seat is released after.
func TestServeFakeDispatchScoreLineClosesTask(t *testing.T) {
	st, client := seedSeat(t, 2)
	ctx := context.Background()
	// A read is pushed only against its PR record at the same head.
	if err := client.HSet(ctx, "s:s1:pr:nova-tools:7", "head", serveHead).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: "s1", ID: "r1", Kind: task.KindRead, Title: "read nova-tools#7",
		Effects: task.EffectsNone, PayloadSHA: "r1", To: "emma",
		Repo: "nova-tools", PR: 7, Head: serveHead,
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push read: %s, %v", got, err)
	}
	res, err := life.ServeOnce(ctx, st, serveConfig(t, "sess-1", 1, "score"))
	if err != nil {
		t.Fatalf("serve once: %v", err)
	}
	if res.Taken != 1 || res.Closed != 1 || res.Live != 0 {
		t.Fatalf("pass = %+v, want taken 1 closed 1 live 0", res)
	}
	h := client.HGetAll(ctx, task.Key("s1", "r1")).Val()
	if h["state"] != "closed" || h["verdict"] != "APPROVE" || h["score"] != "9" {
		t.Fatalf("task after serve: state=%s verdict=%s score=%s", h["state"], h["verdict"], h["score"])
	}
	if !strings.HasPrefix(h["evidence"], "SCORE who=emma head="+serveHead) {
		t.Fatalf("evidence %q is not the child's SCORE line", h["evidence"])
	}
	if n := client.ZCard(ctx, "friend:emma:starting").Val() + client.ZCard(ctx, "friend:emma:living").Val(); n != 0 {
		t.Fatalf("%d leases left after the close", n)
	}
	kinds := strings.Join(logKinds(t, client), " ")
	for _, want := range []string{"serve-up", "take", "start", "exit", "done", "serve-down"} {
		if !strings.Contains(kinds, want) {
			t.Fatalf("friend:emma:log lacks %s: %s", want, kinds)
		}
	}
	if client.Exists(ctx, life.LockKey("emma")).Val() != 0 {
		t.Fatalf("seat lock still held after release")
	}
	if client.Exists(ctx, "friend:emma:beat").Val() != 0 {
		t.Fatalf("beat still present after release")
	}
}

// TestServeDyingDispatchLeavesBlockedEvidence: a harness that exits 3 with no
// typed line closes its task with `blocked: exit=3 ...` naming the last line
// it wrote, so the task never sits in working without a live child.
func TestServeDyingDispatchLeavesBlockedEvidence(t *testing.T) {
	st, client := seedSeat(t, 2)
	ctx := context.Background()
	pushWork(t, st, "w1")
	res, err := life.ServeOnce(ctx, st, serveConfig(t, "sess-1", 1, "die"))
	if err != nil {
		t.Fatalf("serve once: %v", err)
	}
	if res.Taken != 1 || res.Closed != 1 {
		t.Fatalf("pass = %+v, want taken 1 closed 1", res)
	}
	h := client.HGetAll(ctx, task.Key("s1", "w1")).Val()
	if h["state"] != "closed" {
		t.Fatalf("task state %q, want closed", h["state"])
	}
	if !strings.HasPrefix(h["evidence"], "blocked: exit=3") || !strings.Contains(h["evidence"], "boom") {
		t.Fatalf("evidence %q, want blocked: exit=3 ... boom", h["evidence"])
	}
	if client.SIsMember(ctx, "s:s1:idx:task:working", "w1").Val() {
		t.Fatalf("w1 still in the working index")
	}
}

// TestServeWidthRespected: three ready tasks and a width of 1 run one child at
// a time; every pass holds at most one lease and all three close.
func TestServeWidthRespected(t *testing.T) {
	st, client := seedSeat(t, 4)
	ctx := context.Background()
	for _, id := range []string{"w1", "w2", "w3"} {
		pushWork(t, st, id)
	}
	cfg := serveConfig(t, "sess-1", 1, "wait")
	s, err := life.NewServer(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Pass(ctx)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if res.Taken != 1 || res.Live != 1 {
		t.Fatalf("first pass = %+v, want taken 1 live 1 at width 1", res)
	}
	// A second pass while the child lives takes nothing more at width 1.
	if res, err := s.Pass(ctx); err != nil || res.Taken != 0 || res.Live != 1 {
		t.Fatalf("second pass = %+v, %v; want taken 0 live 1", res, err)
	}
	release(t, cfg)
	closed := 0
	deadline := time.Now().Add(15 * time.Second)
	for closed < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("closed %d of 3 before the deadline", closed)
		}
		time.Sleep(100 * time.Millisecond)
		res, err := s.Pass(ctx)
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		closed += res.Closed
		if res.Live > 1 {
			t.Fatalf("live %d over width 1", res.Live)
		}
		if n := client.ZCard(ctx, "friend:emma:starting").Val() + client.ZCard(ctx, "friend:emma:living").Val(); n > 1 {
			t.Fatalf("%d leases held at width 1", n)
		}
	}
	for _, id := range []string{"w1", "w2", "w3"} {
		h := client.HGetAll(ctx, task.Key("s1", id)).Val()
		if h["state"] != "closed" || !strings.HasPrefix(h["evidence"], "DONE released "+id) {
			t.Fatalf("%s: state=%s evidence=%q", id, h["state"], h["evidence"])
		}
	}
	if err := s.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestServeBeatHasTTLAndSecondSeatRefuses: one beat leaves friend:emma (the
// #2673 hash with width) and friend:emma:beat under a TTL and friend:emma:last
// untimed; a second serve on the seat is refused with the holder named until
// the first releases.
func TestServeBeatHasTTLAndSecondSeatRefuses(t *testing.T) {
	st, client := seedSeat(t, 2)
	ctx := context.Background()
	a, err := life.NewServer(st, serveConfig(t, "sess-a", 2, "done"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Beat(ctx); err != nil {
		t.Fatalf("first beat: %v", err)
	}
	for _, key := range []string{"friend:emma", "friend:emma:beat", life.LockKey("emma")} {
		ttl := client.PTTL(ctx, key).Val()
		if ttl <= 0 || ttl > life.ServeLockTTL {
			t.Fatalf("%s PTTL %v, want within (0, %v]", key, ttl, life.ServeLockTTL)
		}
	}
	if ttl := client.PTTL(ctx, "friend:emma:last").Val(); ttl != -1 {
		t.Fatalf("friend:emma:last PTTL %v, want -1 (untimed)", ttl)
	}
	row := client.HGetAll(ctx, "friend:emma").Val()
	if row["width"] != "0" || row["cap"] != "2" || row["serve"] != "sess-a" || row["at"] == "" {
		t.Fatalf("friend:emma = %v", row)
	}
	if _, err := time.Parse(time.RFC3339, row["at"]); err != nil {
		t.Fatalf("at %q is not RFC 3339: %v", row["at"], err)
	}
	b, err := life.NewServer(st, serveConfig(t, "sess-b", 2, "done"))
	if err != nil {
		t.Fatal(err)
	}
	err = b.Beat(ctx)
	var held *life.SeatHeldError
	if !errors.Is(err, life.ErrSeatHeld) || !errors.As(err, &held) || held.Holder != "sess-a" {
		t.Fatalf("second seat: %v, want ErrSeatHeld by sess-a", err)
	}
	if err := b.Release(ctx); !errors.Is(err, life.ErrSeatHeld) {
		t.Fatalf("release by the other session: %v, want ErrSeatHeld", err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := b.Beat(ctx); err != nil {
		t.Fatalf("beat after release: %v", err)
	}
	if err := b.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestServeBeatFriendRowPreserved is #3813 on a throwaway server: when
// friend:<f> is the friend row (has up), ns_friend_serve_beat leaves the
// row's fields and PTTL (-1) unchanged, writing presence only to
// friend:<f>:beat and taking the seat lock. A serve release clears the lock
// and beat without deleting the friend row.
func TestServeBeatFriendRowPreserved(t *testing.T) {
	st, client := seedSeat(t, 2)
	ctx := context.Background()

	// Seed friend:emma as the friend row (#3447: has up, no TTL).
	rowFields := map[string]interface{}{
		"at":      "2026-09-25T08:00:00Z",
		"up":      "1",
		"ready":   "5",
		"working": "2",
		"width":   "4",
		"done":    "12",
		"slots":   "4",
	}
	if err := client.HSet(ctx, "friend:emma", rowFields).Err(); err != nil {
		t.Fatal(err)
	}

	srv, err := life.NewServer(st, serveConfig(t, "sess-row", 2, "done"))
	if err != nil {
		t.Fatal(err)
	}

	// Call ns_friend_serve_beat
	if err := srv.Beat(ctx); err != nil {
		t.Fatalf("beat on seat with friend row: %v", err)
	}

	// friend:emma fields must be completely unchanged.
	gotRow := client.HGetAll(ctx, "friend:emma").Val()
	for k, wantVal := range rowFields {
		if gotRow[k] != wantVal {
			t.Errorf("friend:emma field %s = %q, want %q", k, gotRow[k], wantVal)
		}
	}
	if gotRow["serve"] != "" {
		t.Errorf("friend:emma wrote serve = %q, want empty", gotRow["serve"])
	}
	if gotRow["cap"] != "" {
		t.Errorf("friend:emma wrote cap = %q, want empty", gotRow["cap"])
	}

	// PTTL of friend:emma must remain -1 (no expiry / unchanged).
	if ttl := client.PTTL(ctx, "friend:emma").Val(); ttl != -1 {
		t.Fatalf("friend:emma PTTL %v, want -1 (untimed row)", ttl)
	}

	// :last must not have been overwritten / written by serve beat.
	if client.Exists(ctx, "friend:emma:last").Val() != 0 {
		t.Fatal("ns_friend_serve_beat wrote friend:emma:last on a friend row")
	}

	// Presence must be only on friend:emma:beat.
	beatTTL := client.PTTL(ctx, "friend:emma:beat").Val()
	if beatTTL <= 0 || beatTTL > life.ServeLockTTL {
		t.Fatalf("friend:emma:beat PTTL %v, want within (0, %v]", beatTTL, life.ServeLockTTL)
	}
	beatHash := client.HGetAll(ctx, "friend:emma:beat").Val()
	if beatHash["session"] != "sess-row" || beatHash["harness"] != "fake" || beatHash["host"] != "studio" {
		t.Fatalf("friend:emma:beat = %v", beatHash)
	}

	// Direct call to ns_friend_serve_beat also leaves row untouched.
	reply, err := client.FCall(ctx, life.FunctionServeBeat, nil,
		"emma", "sess-row", "2026-09-25T08:30:00Z", "1", "2", "fake", "studio", "actor",
	).Slice()
	if err != nil || len(reply) == 0 || fmt.Sprint(reply[0]) != "OK" {
		t.Fatalf("direct FCall ns_friend_serve_beat: reply=%v, err=%v", reply, err)
	}
	if ttl := client.PTTL(ctx, "friend:emma").Val(); ttl != -1 {
		t.Fatalf("after direct FCall: friend:emma PTTL %v, want -1", ttl)
	}
	gotRowAfterFCall := client.HGetAll(ctx, "friend:emma").Val()
	for k, wantVal := range rowFields {
		if gotRowAfterFCall[k] != wantVal {
			t.Errorf("after direct FCall: friend:emma field %s = %q, want %q", k, gotRowAfterFCall[k], wantVal)
		}
	}

	// Seat lock held by sess-row.
	if lockHolder := client.Get(ctx, life.LockKey("emma")).Val(); lockHolder != "sess-row" {
		t.Fatalf("seat lock holder = %q, want sess-row", lockHolder)
	}

	// Release clears the lock and beat, leaving the friend row intact.
	if err := srv.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if client.Exists(ctx, life.LockKey("emma")).Val() != 0 {
		t.Fatal("seat lock still held after release")
	}
	if client.Exists(ctx, "friend:emma:beat").Val() != 0 {
		t.Fatal("friend:emma:beat still exists after release")
	}
	if client.Exists(ctx, "friend:emma").Val() != 1 {
		t.Fatal("release deleted friend:emma row; want row preserved")
	}
	afterReleaseRow := client.HGetAll(ctx, "friend:emma").Val()
	for k, wantVal := range rowFields {
		if afterReleaseRow[k] != wantVal {
			t.Errorf("after release: friend:emma field %s = %q, want %q", k, afterReleaseRow[k], wantVal)
		}
	}
	if ttl := client.PTTL(ctx, "friend:emma").Val(); ttl != -1 {
		t.Fatalf("after release: friend:emma PTTL %v, want -1", ttl)
	}
}

// TestServeStopGivesWorkBack: stopping a serve with a live child kills the
// child and gives its task back to the queue, so the next serve retakes it.
func TestServeStopGivesWorkBack(t *testing.T) {
	st, client := seedSeat(t, 2)
	ctx := context.Background()
	pushWork(t, st, "w1")
	s, err := life.NewServer(st, serveConfig(t, "sess-1", 1, "wait"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Pass(ctx)
	if err != nil || res.Live != 1 {
		t.Fatalf("pass = %+v, %v; want one live child", res, err)
	}
	if state := client.HGet(ctx, task.Key("s1", "w1"), "state").Val(); state != "working" {
		t.Fatalf("state %q while the child lives, want working (start ack)", state)
	}
	if err := s.Stop(ctx, "test"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state := client.HGet(ctx, task.Key("s1", "w1"), "state").Val(); state != "open" {
		t.Fatalf("state %q after stop, want open", state)
	}
	if n := client.ZCard(ctx, "friend:emma:living").Val(); n != 0 {
		t.Fatalf("%d living leases after stop", n)
	}
	if client.Exists(ctx, life.LockKey("emma")).Val() != 0 {
		t.Fatalf("seat lock held after stop")
	}
	if !strings.Contains(strings.Join(logKinds(t, client), " "), "cancel") {
		t.Fatalf("no cancel receipt on friend:emma:log")
	}
}

func TestServeTypedLineAndVerdict(t *testing.T) {
	out := "thinking\nSCORE who=emma head=abc score=8/10\nPASS\n"
	if got := life.TypedLine(out); got != "SCORE who=emma head=abc score=8/10" {
		t.Fatalf("TypedLine = %q", got)
	}
	if got := life.TypedLine("prose only\n"); got != "" {
		t.Fatalf("TypedLine of prose = %q", got)
	}
	cases := map[string][2]string{
		"SCORE who=e head=h score=9/10":                 {"APPROVE", "9"},
		"SCORE who=e head=h score=7":                    {"APPROVE", "7"},
		"DISPOSITION who=e head=h verdict=hold score=3": {"HOLD", "3"},
		"HOLD who=e head=h":                             {"HOLD", "0"},
		"DONE built it":                                 {"DONE", "0"},
		"BLOCKED why=no-mirror":                         {"BLOCKED", "0"},
	}
	for line, want := range cases {
		v, s := life.VerdictOf(line)
		if v != want[0] || s != want[1] {
			t.Errorf("VerdictOf(%q) = %s %s, want %s %s", line, v, s, want[0], want[1])
		}
	}
}

// TestServeChildReachesNoGh is #3600's harness half: a friend child that runs
// `gh api user` with a counting fake gh first on the seat's own PATH reaches
// the refusing gh serve puts ahead of it, exits 2 naming #3594, and the fake's
// counter (the token's call counter, standing in) is never written. The
// control is one edit: drop the shim from the child's PATH in start and the
// fake answers, exit=0 with one count.
func TestServeChildReachesNoGh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refusing gh is a /bin/sh script")
	}
	st, client := seedSeat(t, 1)
	ctx := context.Background()
	pushWork(t, st, "w1")
	fake := t.TempDir()
	counter := filepath.Join(fake, "calls")
	if err := os.WriteFile(filepath.Join(fake, "gh"), []byte("#!/bin/sh\necho call >> '"+counter+"'\necho fake gh answered\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := serveConfig(t, "sess-1", 1, "gh")
	cfg.Env = append(cfg.Env, "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	if res, err := life.ServeOnce(ctx, st, cfg); err != nil || res.Closed != 1 {
		t.Fatalf("serve once: %+v, %v", res, err)
	}
	ev := client.HGet(ctx, task.Key("s1", "w1"), "evidence").Val()
	if !strings.HasPrefix(ev, "DONE gh exit=2 ") || !strings.Contains(ev, "#3594") {
		t.Fatalf("evidence %q, want DONE gh exit=2 with the #3594 refusal", ev)
	}
	if b, err := os.ReadFile(counter); err == nil {
		t.Fatalf("the fake gh was called %d time(s): the child reached a real gh", strings.Count(string(b), "call"))
	}
}
