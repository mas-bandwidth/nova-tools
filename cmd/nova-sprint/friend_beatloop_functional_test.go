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
// ns_cm_beat, ns_friend_models) and writes its log; SIGTERM ends it with
// ns_friend_loop_release. `friend beat --once` then writes the beat and its
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
		if !strings.HasPrefix(tok, "+fcall|ns_friend_") {
			bare = append(bare, tok)
		}
	}
	extra := append([]string{"--user", "ns-friend", "on", ">ns-friend-pass"}, strings.Fields(rules["ns-friend"])...)
	extra = append(append(extra, "--user", "ns-friend-bare", "on", ">ns-friend-bare-pass"), bare...)
	addr, admin := loadedStore(t, extra...)
	ctx := context.Background()
	const me = "aclfriend"
	k, _ := taskcard.ParseConsumer("friend:" + me)
	cp := beatStore(t, admin, me, 2, 1)[0]
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

	code, out := sprint("friend", "pull", "--as", k.String(), "--model", "opus-5.5", "--harness", "claude-code", "--child", "c7")
	m := regexp.MustCompile(`(?m)^BEATLOOP as=` + k.String() + ` started pid=(\d+) working=1 log=(\S+)$`).FindStringSubmatch(out)
	if code != 0 || m == nil || m[2] != filepath.Join(home, ".nova-sprint", "friend", me, "beatloop.log") {
		t.Fatalf("ns-friend friend pull exit %d:\n%s", code, out)
	}
	pid, _ := strconv.Atoi(m[1])
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if got := admin.HGetAll(ctx, taskcard.Key(cp)).Val(); got["model"] != "opus-5.5" || got["harness"] != "claude-code" || got["child"] != "c7" {
		t.Fatalf("task:%s = %v", cp, got)
	}
	// the loop's first step runs at its start: the beat, under ns-friend
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
	if code != 0 || !strings.HasPrefix(out, "FRIEND BEAT as="+k.String()+" host=laptop working=1 ") {
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
