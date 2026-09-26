//go:build functional

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// beatSeat is the friend these tests act as: the seat when NOVA_FRIEND is
// set (a verb refuses any other --as), else rowan.
func beatSeat() string {
	if me := os.Getenv(seatEnv); me != "" {
		return me
	}
	return "rowan"
}

// beatStore is a throwaway store with the library loaded, friend me
// enrolled with slots, and n primaries dealt to it as ready copies.
func beatStore(t *testing.T, c *redis.Client, me string, slots, n int) []string {
	t.Helper()
	ctx := context.Background()
	k, _ := taskcard.ParseConsumer("friend:" + me)
	c.HSet(ctx, k.DesiredKey(), "slots", strconv.Itoa(slots))
	c.SAdd(ctx, "friends", me)
	var copies []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("console-grammar-%d", i)
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: "friends", Kind: "build",
			Title: "console grammar", Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#4400", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", strings.Repeat("ab", 20), "paths", "cmd/nova-sprint/friend_copies.go",
				"done_when", "the friend row reads up", "body", "the issue"}}); err != nil {
			t.Fatal(err)
		}
		d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, By: "reconciler"})
		if err != nil || len(d) != 1 {
			t.Fatalf("deal: %v %v", d, err)
		}
		copies = append(copies, d[0].Copy)
	}
	return copies
}

// bindTestOwner uses this test process as the independently live harness
// stand-in. It binds through the real atomic store boundary, never raw fields.
func bindTestOwner(t *testing.T, c *redis.Client, k taskcard.Consumer, ids ...string) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	sample := life.ProbeProcess(os.Getpid())
	if sample.Err != nil || sample.Absent {
		t.Fatalf("test owner: %+v", sample)
	}
	for _, id := range ids {
		token, err := c.HGet(context.Background(), taskcard.Key(id), "token").Result()
		if err != nil {
			t.Fatal(err)
		}
		if err := taskcard.BindOwner(context.Background(), c, k, id, token, taskcard.ProcessOwner{Host: host, PID: os.Getpid(), Start: sample.Start}); err != nil {
			t.Fatal(err)
		}
	}
}

// loadedStore starts a throwaway store (extra: redis-server arguments) and
// loads the library as the default user.
func loadedStore(t *testing.T, extra ...string) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t, extra...)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr, c
}

// TestFriendPullKeepsBeatAndNamesWorker (seat-keeps-beat) on a throwaway
// store: a real `friend pull --model --harness --child` has ns_cm_work
// record who works the copy on its record, writes the brief with its
// WORKER line and starts the friend's one beat loop (friend beat --loop
// --lease <the claimed token>, its pid on the lease, its log named); a
// second pull finds the live loop and starts none. `card render --id
// <copy>` prints the WORKER line; `friend beat --once` writes the beat's
// models; the live table's friend row reads up with the model beside the
// name (the row's 25-wide cell carries the model only; card render carries
// model, harness and child).
func TestFriendPullKeepsBeatAndNamesWorker(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	ctx := context.Background()
	me := beatSeat()
	k, _ := taskcard.ParseConsumer("friend:" + me)
	cp := beatStore(t, c, me, 2, 1)[0]
	host, _ := os.Hostname()
	log, _ := testBeatLog(me)

	dir := t.TempDir()
	code, out, errOut := runSprint("friend", "pull", "--as", k.String(), "--dir", dir, "--model", "opus-5.5", "--harness", "claude-code", "--child", "c7", "--redis", addr)
	if code != 0 || !strings.Contains(out, "\nBEATLOOP as="+k.String()+" started pid=4242 working=1 log="+log+"\nFRIEND PULL ") {
		t.Fatalf("friend pull exit %d:\n%s%s", code, out, errOut)
	}
	starts := startsFor(addr)
	lease := c.HGetAll(ctx, life.BeatLoopKey(me)).Val()
	if len(starts) != 1 || lease["token"] == "" || lease["pid"] != "4242" || lease["host"] != host ||
		strings.Join(starts[0][1:], " ") != "friend beat --as "+k.String()+" --loop --lease "+lease["token"]+" --redis "+addr {
		t.Fatalf("loops started %v, lease %v", starts, lease)
	}
	rec := c.HGetAll(ctx, taskcard.Key(cp)).Val()
	if rec["where"] != "working" || rec["model"] != "opus-5.5" || rec["harness"] != "claude-code" || rec["child"] != "c7" {
		t.Fatalf("task:%s = %v", cp, rec)
	}
	const worker = "\nWORKER: model=opus-5.5 harness=claude-code child=c7\n"
	path := regexp.MustCompile(`(?m)^PULLED \S+ leg=work token=\S+ card=(\S+)$`).FindStringSubmatch(out)
	if path == nil {
		t.Fatalf("no PULLED line:\n%s", out)
	}
	if brief, err := os.ReadFile(path[1]); err != nil || !strings.Contains(string(brief), worker) {
		t.Fatalf("brief lacks %q (%v):\n%s", worker, err, brief)
	}
	running := "BEATLOOP as=" + k.String() + " running pid=4242 host=" + host + " working=1\n"
	if code, out, _ := runSprint("friend", "pull", "--as", k.String(), "--dir", dir, "--redis", addr); code != 0 ||
		!strings.Contains(out, running) || len(startsFor(addr)) != 1 {
		t.Fatalf("second pull exit %d, %d starts:\n%s", code, len(startsFor(addr)), out)
	}

	code, out, errOut = runSprint("card", "render", "--id", cp, "--redis", addr)
	if code != 0 || !strings.Contains(out, worker) {
		t.Fatalf("card render exit %d lacks %q:\n%s%s", code, worker, out, errOut)
	}
	bindTestOwner(t, c, k, cp)
	if code, out, errOut := runSprint("friend", "beat", "--as", k.String(), "--host", "laptop", "--once", "--redis", addr); code != 0 {
		t.Fatalf("friend beat exit %d %s%s", code, out, errOut)
	}
	if got := c.HGet(ctx, "friend:"+me+":beat", "models").Val(); got != "opus-5.5" {
		t.Fatalf("beat models = %q", got)
	}
	snap, err := table.NewSprintReader(c, table.SprintConfig{Friends: []string{me}}).Read(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(me) + ` opus-5.5 +\|     0 \|       1 \|     0 \|    - \| up     \| `).FindString(snap.Render(time.Now()))
	if row == "" {
		t.Fatalf("no up row with the model for %s:\n%s", me, snap.Render(time.Now()))
	}
}

// TestEnsureFriendBeatStartsOneLoop (seat-keeps-beat, invariant A): a
// friend holding a working copy with no loop's lease gets exactly one loop
// started (friend beat --loop --lease <the token the verb claimed>, the
// started pid written onto the lease); the next verb finds the live holder
// and starts none; a holder on this host whose pid is gone is taken over
// (restarted, dead=<pid>); a holder on another host is left running; a friend holding
// nothing starts none; a start that fails releases the claim; a bench
// starts none.
func TestEnsureFriendBeatStartsOneLoop(t *testing.T) {
	t.Parallel()
	_, c := loadedStore(t)
	ctx := context.Background()
	k, _ := taskcard.ParseConsumer("friend:rowan")
	host, _ := os.Hostname()
	now := time.Date(2026, 9, 26, 17, 32, 0, 0, time.UTC)
	var calls [][]string
	live := map[int]bool{99: true}
	s := loopStarter{
		start: func(argv []string, _ string) (int, error) {
			calls = append(calls, argv)
			return 99 + len(calls) - 1, nil
		},
		alive: func(pid int) bool { return live[pid] },
		log:   func(f string) (string, error) { return "/state/" + f + "/beatloop.log", nil },
	}

	if line, err := ensureFriendBeat(ctx, c, k, "", s, now); err != nil || line != "" || len(calls) != 0 {
		t.Fatalf("no working copy: %q %v, %d starts", line, err, len(calls))
	}
	c.ZAdd(ctx, k.Key("working"), redis.Z{Score: 1, Member: "console-grammar~1"})
	line, err := ensureFriendBeat(ctx, c, k, "127.0.0.1:1", s, now)
	if err != nil || line != "BEATLOOP as=friend:rowan started pid=99 working=1 log=/state/rowan/beatloop.log" || len(calls) != 1 {
		t.Fatalf("first verb: %q %v, %d starts", line, err, len(calls))
	}
	lease := c.HGetAll(ctx, life.BeatLoopKey("rowan")).Val()
	want := []string{"friend", "beat", "--as", "friend:rowan", "--loop", "--lease", lease["token"], "--redis", "127.0.0.1:1"}
	if lease["token"] == "" || lease["pid"] != "99" || lease["host"] != host || !slices.Equal(calls[0][1:], want) {
		t.Fatalf("started %v with lease %v; want <exe> %v", calls[0], lease, want)
	}
	if ttl := c.PTTL(ctx, life.BeatLoopKey("rowan")).Val(); ttl <= 0 || ttl > life.BeatLoopLease {
		t.Fatalf("lease pttl %s, want (0, %s]", ttl, life.BeatLoopLease)
	}
	line, err = ensureFriendBeat(ctx, c, k, "", s, now.Add(time.Second))
	if err != nil || line != "BEATLOOP as=friend:rowan running pid=99 host="+host+" working=1" || len(calls) != 1 {
		t.Fatalf("second verb: %q %v, %d starts", line, err, len(calls))
	}

	live[99] = false // kill -9: the lease outlives the loop
	line, err = ensureFriendBeat(ctx, c, k, "", s, now.Add(2*time.Second))
	if err != nil || line != "BEATLOOP as=friend:rowan restarted pid=100 working=1 log=/state/rowan/beatloop.log dead=99" || len(calls) != 2 {
		t.Fatalf("after the loop died: %q %v, %d starts", line, err, len(calls))
	}
	c.HSet(ctx, life.BeatLoopKey("rowan"), "host", "elsewhere")
	line, err = ensureFriendBeat(ctx, c, k, "", s, now.Add(3*time.Second))
	if err != nil || line != "BEATLOOP as=friend:rowan running pid=100 host=elsewhere working=1" || len(calls) != 2 {
		t.Fatalf("another host's loop: %q %v, %d starts", line, err, len(calls))
	}

	c.Del(ctx, life.BeatLoopKey("rowan"))
	failing := s
	failing.start = func([]string, string) (int, error) { return 0, errors.New("no fork") }
	if _, err := ensureFriendBeat(ctx, c, k, "", failing, now); err == nil || !strings.Contains(err.Error(), "no fork") {
		t.Fatalf("a failed start: %v", err)
	}
	if c.Exists(ctx, life.BeatLoopKey("rowan")).Val() != 0 {
		t.Fatal("a failed start left its claim on the lease")
	}
	b, _ := taskcard.ParseConsumer("bench:studio")
	c.ZAdd(ctx, b.Key("working"), redis.Z{Score: 1, Member: "x~1"})
	if line, err := ensureFriendBeat(ctx, c, b, "", s, now); err != nil || line != "" || len(calls) != 2 {
		t.Fatalf("a bench: %q %v, %d starts", line, err, len(calls))
	}
}

// TestEnsureFriendBeatRaceStartsOne (seat-keeps-beat PROBE: two writers at
// once): 50 trials, each 8 verbs at once for one friend holding a working
// copy, first with a free lease and then with a dead holder's lease on this
// host: in every trial exactly one verb starts a loop and the other seven
// say running.
func TestEnsureFriendBeatRaceStartsOne(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	ctx := context.Background()
	k, _ := taskcard.ParseConsumer("friend:rowan")
	c.ZAdd(ctx, k.Key("working"), redis.Z{Score: 1, Member: "console-grammar~1"})
	host, _ := os.Hostname()
	s := loopStarter{
		start: func([]string, string) (int, error) { return 4242, nil },
		alive: func(pid int) bool { return pid != 13 },
		log:   testBeatLog,
	}
	for trial := 0; trial < 50; trial++ {
		for _, dead := range []bool{false, true} {
			c.Del(ctx, life.BeatLoopKey("rowan"))
			if dead {
				c.HSet(ctx, life.BeatLoopKey("rowan"), "token", "gone", "host", host, "pid", "13")
			}
			lines := make([]string, 8)
			var wg sync.WaitGroup
			for i := range lines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					cl := redis.NewClient(&redis.Options{Addr: addr})
					defer func() { _ = cl.Close() }()
					line, err := ensureFriendBeat(ctx, cl, k, "", s, time.Unix(int64(trial), int64(i)))
					if err != nil {
						line = err.Error()
					}
					lines[i] = line
				}()
			}
			wg.Wait()
			started := 0
			for _, l := range lines {
				switch {
				case strings.Contains(l, " started pid=4242 "), strings.Contains(l, " restarted pid=4242 "):
					started++
				case strings.Contains(l, " running pid="):
				default:
					t.Fatalf("trial %d dead=%v: %q", trial, dead, l)
				}
			}
			if started != 1 {
				t.Fatalf("trial %d dead=%v: %d loops started, want 1:\n%s", trial, dead, started, strings.Join(lines, "\n"))
			}
		}
	}
}

// TestFriendBeatLoopLogsAndDeadPid (seat-keeps-beat) with a real process
// through startOwnSessionLog: the started process's stdout and stderr land
// in the log the BEATLOOP line names (a loop that backs off is not silent);
// after kill -9 of it the lease still names its pid, and the next verb finds
// the pid gone on this host (pidAlive) and restarts one at once (restarted, dead=<pid>),
// instead of saying running for the rest of the lease.
func TestFriendBeatLoopLogsAndDeadPid(t *testing.T) {
	t.Parallel()
	_, c := loadedStore(t)
	ctx := context.Background()
	k, _ := taskcard.ParseConsumer("friend:rowan")
	c.ZAdd(ctx, k.Key("working"), redis.Z{Score: 1, Member: "console-grammar~1"})
	log := filepath.Join(t.TempDir(), "friend", "rowan", "beatloop.log")
	var pids []int
	s := loopStarter{
		// the loop's stand-in: says it started and why it backs off, then
		// lives until killed
		start: func(argv []string, log string) (int, error) {
			pid, err := startOwnSessionLog([]string{"/bin/sh", "-c", `echo "FRIEND BEAT LOOP $1 $2"; echo "friend rowan beat loop: refused; retry in 2s" >&2; exec sleep 30`,
				"sh", argv[3], argv[4]}, log)
			pids = append(pids, pid)
			return pid, err
		},
		alive: pidAlive,
		log:   func(string) (string, error) { return log, nil },
	}
	t.Cleanup(func() {
		for _, p := range pids {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	})
	line, err := ensureFriendBeat(ctx, c, k, "", s, time.Now())
	if err != nil || len(pids) != 1 || line != fmt.Sprintf("BEATLOOP as=friend:rowan started pid=%d working=1 log=%s", pids[0], log) {
		t.Fatalf("start: %q %v", line, err)
	}
	want := "FRIEND BEAT LOOP --as friend:rowan\nfriend rowan beat loop: refused; retry in 2s\n"
	var got []byte
	for i := 0; i < 200 && string(got) != want; i++ {
		time.Sleep(10 * time.Millisecond)
		got, _ = os.ReadFile(log)
	}
	if string(got) != want {
		t.Fatalf("log %s = %q, want %q", log, got, want)
	}
	if line, _ := ensureFriendBeat(ctx, c, k, "", s, time.Now()); !strings.Contains(line, fmt.Sprintf(" running pid=%d ", pids[0])) {
		t.Fatalf("with the loop alive: %q", line)
	}
	if err := syscall.Kill(pids[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200 && pidAlive(pids[0]); i++ {
		time.Sleep(10 * time.Millisecond) // startOwnSession's Wait reaps it
	}
	if pidAlive(pids[0]) {
		t.Fatalf("pid %d still alive after kill -9", pids[0])
	}
	if pid := c.HGet(ctx, life.BeatLoopKey("rowan"), "pid").Val(); pid != strconv.Itoa(pids[0]) {
		t.Fatalf("lease pid %q after kill -9, want the dead %d", pid, pids[0])
	}
	line, err = ensureFriendBeat(ctx, c, k, "", s, time.Now())
	if err != nil || len(pids) != 2 || line != fmt.Sprintf("BEATLOOP as=friend:rowan restarted pid=%d working=1 log=%s dead=%d", pids[1], log, pids[0]) {
		t.Fatalf("after kill -9: %q %v", line, err)
	}
}

// TestTaskTakeRecordsWhoAndKeepsBeat (seat-keeps-beat): `task take --actor
// <f> --model --harness --child` works the friend's ready copy through
// ns_cm_work with who on its record, and keeps the friend's one beat loop
// (one BEATLOOP line; a second take finds it running).
func TestTaskTakeRecordsWhoAndKeepsBeat(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	ctx := context.Background()
	me := beatSeat()
	cps := beatStore(t, c, me, 2, 2)
	log, _ := testBeatLog(me)
	code, out, errOut := runSprint("task", "take", "--actor", me, "--model", "sonnet-5", "--harness", "codex", "--child", "c9", "--redis", addr)
	if code != 0 || !strings.Contains(out, "BEATLOOP as=friend:"+me+" started pid=4242 working=1 log="+log+"\nTASK take n=1 ids="+cps[0]+" ") {
		t.Fatalf("task take exit %d:\n%s%s", code, out, errOut)
	}
	rec := c.HGetAll(ctx, taskcard.Key(cps[0])).Val()
	if rec["where"] != "working" || rec["model"] != "sonnet-5" || rec["harness"] != "codex" || rec["child"] != "c9" {
		t.Fatalf("task:%s = %v", cps[0], rec)
	}
	code, out, errOut = runSprint("task", "take", "--actor", me, "--redis", addr)
	if code != 0 || !strings.Contains(out, " running pid=4242 ") || len(startsFor(addr)) != 1 {
		t.Fatalf("second take exit %d, %d starts:\n%s%s", code, len(startsFor(addr)), out, errOut)
	}
	if rec := c.HGetAll(ctx, taskcard.Key(cps[1])).Val(); rec["where"] != "working" || rec["model"] != "" {
		t.Fatalf("task:%s taken with no who = %v", cps[1], rec)
	}
}

// TestFriendSeatKeepsBeatUnderACL (seat-keeps-beat) on a throwaway store
// whose users are store/acl.go's rows, with a built nova-sprint logged in as
// ns-friend through its environment (the row's own grants, nothing more):
// `friend pull` works a copy with its model and claims the loop's lease
// (ns_friend_loop_claim) and starts the REAL loop, which steps under the
// same login (ns_friend_loop_renew, the friend beat's HSET and PERSIST,
// ns_cm_beat, ns_friend_models) and writes its log; after kill -9 of it the
// next pull says restarted (dead=<its pid>), and SIGTERM ends the new one
// with ns_friend_loop_release. `friend beat --once` then writes the beat and its
// models. The lease functions also run on an ns-friend client directly. The
// control: the same seat without the ns_friend_* grants is refused (NOPERM)
// the claim and the models, and ns-friend is refused a SET on the lease key.
func TestFriendSeatKeepsBeatUnderACL(t *testing.T) {
	t.Parallel()
	rules := map[string]string{}
	for _, rule := range store.ACLRules {
		name, body, _ := strings.Cut(rule, " ")
		rules[name] = body
	}
	var bare []string
	for _, tok := range strings.Fields(rules["ns-friend"]) {
		if !strings.HasPrefix(tok, "+fcall|ns_friend_") && tok != "+fcall|ns_cm_owner" {
			bare = append(bare, tok)
		}
	}
	extra := append([]string{"--user", "ns-friend", "on", ">ns-friend-pass"}, strings.Fields(rules["ns-friend"])...)
	extra = append(append(extra, "--user", "ns-friend-bare", "on", ">ns-friend-bare-pass"), bare...)
	addr, admin := loadedStore(t, extra...)
	ctx := context.Background()
	const me = "aclfriend"
	k, _ := taskcard.ParseConsumer("friend:" + me)
	cp := beatStore(t, admin, me, 2, 2)[0]
	bin := buildRoutesBinary(t)
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), seatEnv + "=" + me, store.UserEnv + "=ns-friend",
		store.PasswordEnvEnv + "=NS_FRIEND_TEST_PASSWORD", "NS_FRIEND_TEST_PASSWORD=ns-friend-pass"}
	sprint := func(args ...string) (int, string) {
		cmd := exec.Command(bin, append(args, "--redis", addr)...)
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), string(b)
		}
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return 0, string(b)
	}

	code, out := sprint("friend", "pull", "--as", k.String(), "--n", "1", "--model", "opus-5.5", "--harness", "claude-code", "--child", "c7")
	m := regexp.MustCompile(`(?m)^BEATLOOP as=` + k.String() + ` started pid=(\d+) working=1 log=(\S+)$`).FindStringSubmatch(out)
	if code != 0 || m == nil || m[2] != filepath.Join(home, ".nova-sprint", "friend", me, "beatloop.log") {
		t.Fatalf("ns-friend friend pull exit %d:\n%s", code, out)
	}
	pid, _ := strconv.Atoi(m[1])
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if got := admin.HGetAll(ctx, taskcard.Key(cp)).Val(); got["model"] != "opus-5.5" || got["harness"] != "claude-code" || got["child"] != "c7" {
		t.Fatalf("task:%s = %v", cp, got)
	}
	// Bind the actual owner through the CLI under the same restricted seat.
	claim := admin.HGet(ctx, taskcard.Key(cp), "token").Val()
	if code, o := sprint("card", "owner", "--as", k.String(), "--id", cp, "--token", claim, "--pid", strconv.Itoa(os.Getpid())); code != 0 {
		t.Fatalf("owner bind: %d %s", code, o)
	}
	// the loop's next step observes the bound test process under ns-friend
	for i := 0; i < 300 && admin.HGet(ctx, "friend:"+me+":beat", "models").Val() != "opus-5.5"; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := admin.HGetAll(ctx, "friend:"+me+":beat").Val(); got["models"] != "opus-5.5" || got["harness"] != life.FriendBeatHarness {
		log, _ := os.ReadFile(m[2])
		t.Fatalf("the loop's beat under ns-friend: %v\nlog:\n%s", got, log)
	}
	if got := admin.HGet(ctx, life.BeatLoopKey(me), "pid").Val(); got != m[1] {
		t.Fatalf("lease pid %q, want the loop's %s", got, m[1])
	}
	// kill -9: the lease outlives the loop, and the next pull restarts it at
	// once (the holder's pid is gone on this host), not "running"
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300 && pidAlive(pid); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	code, out = sprint("friend", "pull", "--as", k.String(), "--n", "1", "--model", "opus-5.5")
	m2 := regexp.MustCompile(`(?m)^BEATLOOP as=` + k.String() + ` restarted pid=(\d+) working=2 log=(\S+) dead=` + m[1] + `$`).FindStringSubmatch(out)
	if code != 0 || m2 == nil {
		t.Fatalf("ns-friend friend pull after kill -9 of pid %d exit %d:\n%s", pid, code, out)
	}
	bindTestOwner(t, admin, k, admin.ZRange(ctx, k.Key("working"), 0, -1).Val()...)
	pid, _ = strconv.Atoi(m2[1])
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	for i := 0; i < 300 && !strings.Contains(readFile(m[2]), "pid="+m2[1]); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300 && admin.Exists(ctx, life.BeatLoopKey(me)).Val() != 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	log, _ := os.ReadFile(m[2])
	if admin.Exists(ctx, life.BeatLoopKey(me)).Val() != 0 || !strings.Contains(string(log), "FRIEND BEAT LOOP as="+k.String()+" host=") ||
		strings.Contains(string(log), "NOPERM") {
		t.Fatalf("after SIGTERM the lease is %v; log:\n%s", admin.HGetAll(ctx, life.BeatLoopKey(me)).Val(), log)
	}

	admin.PExpire(ctx, "friend:"+me+":beat", time.Hour) // a hello loop's TTL: the beat's PERSIST removes it
	code, out = sprint("friend", "beat", "--as", k.String(), "--host", "laptop", "--once")
	if code != 0 || !strings.HasPrefix(out, "FRIEND BEAT as="+k.String()+" host=laptop working=2 ") {
		t.Fatalf("ns-friend friend beat --once exit %d:\n%s", code, out)
	}
	if got := admin.HGet(ctx, "friend:"+me+":beat", "models").Val(); got != "opus-5.5" || admin.TTL(ctx, "friend:"+me+":beat").Val() > 0 {
		t.Fatalf("beat models %q ttl %s", got, admin.TTL(ctx, "friend:"+me+":beat").Val())
	}

	as := func(user string) *redis.Client {
		cl := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: user + "-pass"})
		t.Cleanup(func() { _ = cl.Close() })
		return cl
	}
	fc := as("ns-friend")
	loop := life.LoopHolder{Token: "loop", Host: "laptop", PID: 7}
	if took, _, err := life.ClaimBeatLoop(ctx, fc, me, loop, ""); !took || err != nil {
		t.Fatalf("ns-friend claim: %v %v", took, err)
	}
	if ok, err := life.RenewBeatLoop(ctx, fc, me, loop); !ok || err != nil {
		t.Fatalf("ns-friend renew: %v %v", ok, err)
	}
	if err := life.ReleaseBeatLoop(ctx, fc, me, "loop"); err != nil || admin.Exists(ctx, life.BeatLoopKey(me)).Val() != 0 {
		t.Fatalf("ns-friend release: %v", err)
	}
	if err := fc.Set(ctx, life.BeatLoopKey(me), "x", 0).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("ns-friend SET on the lease: %v, want NOPERM", err)
	}
	if _, _, err := life.ClaimBeatLoop(ctx, as("ns-friend-bare"), me, loop, ""); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("ns-friend without the loop grants, claim: %v, want NOPERM", err)
	}
	if _, err := life.FriendBeat(ctx, store.New(as("ns-friend-bare")), life.FriendBeatRequest{Friend: me, Host: "laptop"}); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("ns-friend without the models grant, friend beat: %v, want NOPERM", err)
	}
}

// TestFriendPullRaceStartsOneLoop (seat-keeps-beat PROBE: two pulls at the
// same instant): 20 trials, each two `friend pull --n 1` at once for a
// friend with ready copies and no loop: in every trial exactly one pull
// prints started and the other running, and one loop is started.
func TestFriendPullRaceStartsOneLoop(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	ctx := context.Background()
	me := beatSeat()
	k, _ := taskcard.ParseConsumer("friend:" + me)
	beatStore(t, c, me, 40, 40)
	for trial := 0; trial < 20; trial++ {
		c.Del(ctx, life.BeatLoopKey(me))
		before := len(startsFor(addr))
		outs := make([]string, 2)
		var wg sync.WaitGroup
		for i := range outs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, out, errOut := runSprint("friend", "pull", "--as", k.String(), "--n", "1", "--dir", t.TempDir(), "--redis", addr)
				outs[i] = fmt.Sprintf("exit %d\n%s%s", code, out, errOut)
			}()
		}
		wg.Wait()
		started, running := 0, 0
		for _, o := range outs {
			if strings.Contains(o, "\nBEATLOOP as="+k.String()+" started pid=4242 ") {
				started++
			}
			if strings.Contains(o, "\nBEATLOOP as="+k.String()+" running pid=") {
				running++
			}
		}
		if started != 1 || running != 1 || len(startsFor(addr))-before != 1 {
			t.Fatalf("trial %d: %d started, %d running, %d loops:\n%s", trial, started, running, len(startsFor(addr))-before, strings.Join(outs, "\n"))
		}
	}
}

// TestEndedCopyNotRenewedByLoop (seat-keeps-beat PROBE: a copy ended while
// the loop runs): a friend pulls four copies and its loop steps with the
// real friend beat; each copy is then ended through one door (friend done
// --fail, card end --fail, task done, card cancel), and the loop's next
// step counts one fewer and leaves the ended copy's record as its end left
// it (no lease_until, no beat_at after the end): ns_cm_beat renews only
// the working set. With none left, the next step is idle and the one after
// releases the lease. Mid-tick: ns_friend_models given a copy the first
// trip read but that has ended since writes no model for it.
func TestEndedCopyNotRenewedByLoop(t *testing.T) {
	t.Parallel()
	addr, c := loadedStore(t)
	ctx := context.Background()
	me := beatSeat()
	k, _ := taskcard.ParseConsumer("friend:" + me)
	cps := beatStore(t, c, me, 4, 4)
	code, out, errOut := runSprint("friend", "pull", "--as", k.String(), "--dir", t.TempDir(), "--model", "opus-5.5", "--redis", addr)
	if code != 0 || !strings.Contains(out, " started pid=4242 working=4 ") {
		t.Fatalf("pull exit %d:\n%s%s", code, out, errOut)
	}
	bindTestOwner(t, c, k, cps...)
	token := c.HGet(ctx, life.BeatLoopKey(me), "token").Val()
	st := store.New(c)
	l := &life.BeatLoop{Lease: life.StoreLease{Client: c, Friend: me, Me: life.LoopHolder{Token: token, Host: "h", PID: 4242}}, Friend: me,
		Tick: func(ctx context.Context, now time.Time) (int, error) {
			res, err := friendBeatOnce(ctx, st, k, "laptop", now)
			if err == nil && int64(res.Working) != c.ZCard(ctx, k.Key("working")).Val() {
				t.Errorf("tick counted %d, the set holds %d", res.Working, c.ZCard(ctx, k.Key("working")).Val())
			}
			return res.Working, err
		}}
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	if done, why, err := l.Step(ctx, now); done || err != nil {
		t.Fatalf("step with four copies: %v %q %v", done, why, err)
	}
	// mid-tick: the models trip names a copy that ended after the first trip read it
	c.ZRem(ctx, k.Key("working"), cps[0])
	if m, err := c.FCall(ctx, life.FnFriendModels, []string{"friend:" + me + ":beat", k.Key("working"), taskcard.Key(cps[0])}).Text(); err != nil || m != "" ||
		c.HExists(ctx, "friend:"+me+":beat", "models").Val() {
		t.Fatalf("models of an ended copy: %q %v", m, err)
	}
	c.ZAdd(ctx, k.Key("working"), redis.Z{Score: 1, Member: cps[0]})

	tok := func(id string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "PULLED "+id+" ") {
				return regexp.MustCompile(`token=(\S+)`).FindStringSubmatch(line)[1]
			}
		}
		return ""
	}
	doors := [][]string{
		{"friend", "done", "--as", k.String(), "--id", cps[0], "--fail", "gave up", "--token", tok(cps[0])},
		{"card", "end", "--id", cps[1], "--fail", "gave up"},
		{"task", "done", "--actor", me, "--id", cps[2], "--evidence", "done already"},
		{"card", "cancel", "--id", cps[3], "--why", "given back"},
	}
	for i, door := range doors {
		code, o, e := runSprint(append(door, "--redis", addr)...)
		if code != 0 || c.ZScore(ctx, k.Key("working"), cps[i]).Err() == nil {
			t.Fatalf("%v: exit %d, still working:\n%s%s", door[:2], code, o, e)
		}
		ended := c.HGetAll(ctx, taskcard.Key(cps[i])).Val()
		now = now.Add(time.Second)
		if done, why, err := l.Step(ctx, now); err != nil || done != false {
			t.Fatalf("step after %v: %v %q %v", door[:2], done, why, err)
		}
		after := c.HGetAll(ctx, taskcard.Key(cps[i])).Val()
		if after["lease_until"] != ended["lease_until"] || after["beat_at"] != ended["beat_at"] || after["lease_until"] != "" && after["where"] == "working" {
			t.Fatalf("%v: the loop touched the ended copy: %v -> %v", door[:2], ended, after)
		}
	}
	done, why, err := l.Step(ctx, now.Add(time.Second))
	if err != nil || !done || !strings.HasPrefix(why, "IDLE ") || c.Exists(ctx, life.BeatLoopKey(me)).Val() != 0 {
		t.Fatalf("second idle step: %v %q %v, lease %v", done, why, err, c.HGetAll(ctx, life.BeatLoopKey(me)).Val())
	}
}

// readFile is a file's text, "" when it cannot be read.
func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// takeCopiesAndTask is the reviewer's shape on a throwaway store: friend me
// holds two copies and one friend-queue task (fq-probe-1) in working, all
// three by one `task take --n 3`, which starts the friend's beat loop.
func takeCopiesAndTask(t *testing.T) (addr string, c *redis.Client, me string, cps []string) {
	t.Helper()
	addr, c = loadedStore(t)
	ctx := context.Background()
	me = beatSeat()
	cps = beatStore(t, c, me, 2, 2)
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "fq-probe-1", Stream: "friends", Friend: me, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("task", "take", "--actor", me, "--n", "3", "--redis", addr)
	if code != 0 || !strings.Contains(out, "BEATLOOP as=friend:"+me+" started pid=4242 working=2 ") {
		t.Fatalf("task take exit %d:\n%s%s", code, out, errOut)
	}
	working := c.ZRange(ctx, "friend:"+me+":cards:working", 0, -1).Val()
	if len(working) != 3 || !slices.Contains(working, "fq-probe-1") {
		t.Fatalf("working %v", working)
	}
	return addr, c, me, cps
}

// TestFriendBeatOnceSkipsFriendQueueTask (seat-beat-fix3, probe a): with
// two copies and a friend-queue task in working, `friend beat --once`
// renews both copies' leases, prints one SKIPPED line naming the task and
// exits 0, and leaves the task's lease to task beat. The loop's tick (the
// same life.FriendBeat through life.BeatLoop.Step) renews them too. Named
// ids stay all or nothing: `card beat --ids <copy>,fq-probe-1` is still
// refused NOTWORKING.
func TestFriendBeatOnceSkipsFriendQueueTask(t *testing.T) {
	t.Parallel()
	addr, c, me, cps := takeCopiesAndTask(t)
	ctx := context.Background()
	for _, id := range append([]string{"fq-probe-1"}, cps...) {
		c.HSet(ctx, taskcard.Key(id), "lease_until", "1")
	}
	code, out, errOut := runSprint("friend", "beat", "--as", "friend:"+me, "--once", "--redis", addr)
	want := "FRIEND BEAT SKIPPED as=friend:" + me + " id=fq-probe-1 why=\"not a copy: task beat renews a friend-queue task\"\n" +
		"FRIEND BEAT as=friend:" + me + " "
	if code != 0 || !strings.HasPrefix(out, want) || !strings.Contains(out, " working=2 ") || strings.Count(out, "SKIPPED") != 1 {
		t.Fatalf("friend beat --once exit %d:\n%s%s", code, out, errOut)
	}
	for _, id := range cps {
		if v := c.HGet(ctx, taskcard.Key(id), "lease_until").Val(); v == "1" {
			t.Fatalf("copy %s not renewed", id)
		}
	}
	if v := c.HGet(ctx, taskcard.Key("fq-probe-1"), "lease_until").Val(); v != "1" {
		t.Fatalf("the friend beat wrote the task's lease: %s", v)
	}
	st, err := store.OpenSingle(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	k, _ := taskcard.ParseConsumer("friend:" + me)
	token := c.HGet(ctx, life.BeatLoopKey(me), "token").Val()
	host, _ := os.Hostname()
	l := &life.BeatLoop{Lease: life.StoreLease{Client: c, Friend: me, Me: life.LoopHolder{Token: token, Host: host, PID: 4242}}, Friend: me,
		Tick: func(ctx context.Context, now time.Time) (int, error) {
			res, err := friendBeatOnce(ctx, st, k, host, now)
			return res.Working, err
		}}
	if done, why, err := l.Step(ctx, time.Now()); done || err != nil || l.RetryIn() != 0 {
		t.Fatalf("loop step: done=%v why=%q err=%v retry=%s", done, why, err, l.RetryIn())
	}
	code, out, _ = runSprint("card", "beat", "--as", "friend:"+me, "--ids", cps[0]+",fq-probe-1", "--redis", addr)
	if code == 0 || !strings.Contains(out, "NOTWORKING fq-probe-1 ") {
		t.Fatalf("named non-copy: exit %d %s", code, out)
	}
}

// countingLease is the store's lease (ns_friend_loop_renew), counting the
// renewals that held.
type countingLease struct {
	life.StoreLease
	held atomic.Int32
}

func (l *countingLease) Renew(ctx context.Context) (bool, error) {
	ok, err := l.StoreLease.Renew(ctx)
	if ok {
		l.held.Add(1)
	}
	return ok, err
}

// TestBeatLoopInErrorsKeepsLeaseAndPullSaysRunning (seat-beat-fix3, probe
// b) on a throwaway store: friend beat --loop's own stepping
// (stepBeatLoop, the loop the verb started: its claimed token, pid 4242) is
// fed one tick a second for 25 s of its clock while its tick is refused for
// the first 20 s. Every one of the 26 steps renews the lease
// (ns_friend_loop_renew) and its PTTL is a fresh BeatLoopLease after each
// (it never lapses while the loop is alive), the log names
// seven errors (1, 2, 4, 4 ... s apart), and a `friend pull` at 20 s says
// running pid=4242 and starts nothing. When the refusals stop the copies'
// leases are renewed again; the end releases the lease.
func TestBeatLoopInErrorsKeepsLeaseAndPullSaysRunning(t *testing.T) {
	t.Parallel()
	addr, c, me, cps := takeCopiesAndTask(t)
	ctx := context.Background()
	st, err := store.OpenSingle(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	k, _ := taskcard.ParseConsumer("friend:" + me)
	token := c.HGet(ctx, life.BeatLoopKey(me), "token").Val()
	host, _ := os.Hostname()
	t0 := time.Now()
	lease := &countingLease{StoreLease: life.StoreLease{Client: c, Friend: me, Me: life.LoopHolder{Token: token, Host: host, PID: 4242}}}
	l := &life.BeatLoop{Lease: lease, Friend: me,
		Tick: func(ctx context.Context, now time.Time) (int, error) {
			if now.Sub(t0) < 20*time.Second {
				return 0, errors.New("friend beat: leases: REFUSED (forced)")
			}
			res, err := friendBeatOnce(ctx, st, k, host, now)
			return res.Working, err
		}}
	c.HSet(ctx, taskcard.Key(cps[0]), "lease_until", "1")
	lctx, stop := context.WithCancel(ctx)
	ticks := make(chan time.Time)
	var out, errOut strings.Builder
	ended := make(chan int)
	go func() {
		ended <- stepBeatLoop(lctx, l, t0, ticks, func() { _ = life.ReleaseBeatLoop(ctx, c, me, token) }, k, &out, &errOut)
	}()
	for i := 1; i <= 25; i++ {
		ticks <- t0.Add(time.Duration(i) * time.Second) // step i-1 is done
		if ttl := c.PTTL(ctx, life.BeatLoopKey(me)).Val(); ttl < life.BeatLoopLease-time.Second {
			t.Fatalf("after step %d: lease PTTL %s, want a fresh %s", i-1, ttl, life.BeatLoopLease)
		}
		if i == 21 {
			code, pout, perr := runSprint("friend", "pull", "--as", "friend:"+me, "--redis", addr, "--dir", t.TempDir())
			if !strings.Contains(pout, "BEATLOOP as=friend:"+me+" running pid=4242 ") || len(startsFor(addr)) != 1 {
				t.Fatalf("pull at 20 s exit %d, %d starts:\n%s%s", code, len(startsFor(addr)), pout, perr)
			}
		}
	}
	stop()
	if code := <-ended; code != 0 || !strings.HasSuffix(out.String(), "FRIEND BEAT LOOP END as=friend:"+me+" why=signal\n") {
		t.Fatalf("loop exit %d: %s", code, out.String())
	}
	if n := strings.Count(errOut.String(), "retry in "); n != 7 || !strings.Contains(errOut.String(), "; retry in 4s\n") || strings.Contains(errOut.String(), "retry in 8s") {
		t.Fatalf("%d errors logged:\n%s", n, errOut.String())
	}
	if n := lease.held.Load(); n != 26 {
		t.Fatalf("the lease was renewed on %d of 26 steps", n)
	}
	if v := c.HGet(ctx, taskcard.Key(cps[0]), "lease_until").Val(); v == "1" {
		t.Fatalf("copy %s not renewed after the refusals stopped", cps[0])
	}
	if c.Exists(ctx, life.BeatLoopKey(me)).Val() != 0 {
		t.Fatal("the ended loop kept its lease")
	}
}
