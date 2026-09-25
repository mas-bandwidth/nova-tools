// The life verb registers itself through the S0 registry (registry.go), so
// adding friend/bench presence never edits main.go or task.go. Each subverb is
// one call into internal/nsprint/life, which is one Redis Function call.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func init() {
	register(Verb{
		Name:    "friend",
		Summary: "hello, bye, serve, wake, row and roles for a friend; report a friend state, show friends, or run one ladder sweep",
		Run:     runFriend,
	})
	register(Verb{
		Name:    "bench",
		Summary: "beat, release, or reset one bench; reindex a sprint's card views once; ls the registry with each bench's role",
		Run:     runBench,
	})
}

func runFriend(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "friend", "want hello, bye, serve, wake, row, roles, report, show, sweep, down or up")
	}
	switch args[0] {
	case "hello":
		return runFriendHello(ctx, args[1:], out, errOut)
	case "bye":
		return runFriendBye(ctx, args[1:], out, errOut)
	case "serve":
		return runFriendServe(ctx, args[1:], out, errOut, false)
	case "wake":
		// `friend wake --as <f>` is one serve pass on the seat (#2938);
		// `friend wake <f>` routes a wake through the reconciler's list.
		if hasAsFlag(args[1:]) {
			return runFriendServe(ctx, args[1:], out, errOut, true)
		}
		return runFriendWake(ctx, args[1:], out, errOut)
	case "row":
		return runFriendRow(ctx, args[1:], out, errOut)
	case "roles":
		return runFriendRoles(ctx, args[1:], out, errOut)
	case "report":
		return runFriendReport(ctx, args[1:], out, errOut)
	case "show":
		return runFriendShow(ctx, args[1:], out, errOut)
	case "sweep":
		return runFriendSweep(ctx, args[1:], out, errOut)
	case "down":
		return runFriendDown(ctx, true, args[1:], out, errOut)
	case "up":
		return runFriendDown(ctx, false, args[1:], out, errOut)
	default:
		return refuse(errOut, "friend", fmt.Sprintf("unknown subverb %s; want hello, bye, serve, wake, row, roles, report, show, sweep, down or up", args[0]))
	}
}

func runBench(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "bench", "want beat, release, reset, reindex or ls")
	}
	switch args[0] {
	case "beat":
		return runBenchBeat(ctx, args[1:], out, errOut)
	case "release":
		return runBenchRelease(ctx, args[1:], out, errOut)
	case "reset":
		return runBenchReset(ctx, args[1:], out, errOut)
	case "reindex":
		return runBenchReindex(ctx, args[1:], out, errOut)
	case "ls":
		return runBenchLs(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "bench", fmt.Sprintf("unknown subverb %s; want beat, release, reset, reindex or ls", args[0]))
	}
}

// lifeFlags is the quiet flag set shared by the presence subverbs, with the
// Redis endpoint from --redis, then NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR,
// then the local test endpoint. A real production host is never hardcoded.
func lifeFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "redis address (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR)")
	return fs, addr
}

func lifeAddr(addr string) string {
	if addr != "" {
		return addr
	}
	if v := os.Getenv("NOVA_SPRINT_REDIS"); v != "" {
		return v
	}
	if v := os.Getenv("NOVA_REDIS_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:6379"
}

func openLifeStore(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, addr)
}

func runFriendHello(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("friend hello")
	as := fs.String("as", "", "friend name")
	slots := fs.Int("slots", -1, "desired slots; omitted preserves configured capacity")
	sprint := fs.String("sprint", "", "sprint to take returned work from")
	harness := fs.String("harness", "", "harness identity")
	host := fs.String("host", "", "host the friend runs on")
	machine := fs.String("machine", "", "configured capacity machine, if different from host")
	session := fs.String("session", "", "presence session identity")
	once := fs.Bool("once", false, "register once and return without the 1 s loop")
	var logins loginFlags
	fs.Var(&logins, "login", "a login alias for this friend (repeatable; #3092)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend hello", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "friend hello", "takes flags, not positional arguments")
	}
	if *as == "" {
		return refuse(errOut, "friend hello", "--as is required")
	}
	if *slots != -1 {
		fmt.Fprintf(errOut, "FRIEND CAPACITY %s: use nova-sprint capacity friend --as <actor> --machine <m> %s <slots>\n", *as, *as)
		return 1
	}
	// #2929 rev 4: hello is a take and obeys the seat contract. Both refusals
	// come before openLifeStore: no dial, no UP write, no beat, no receipt.
	initiator := os.Getenv(seatEnv)
	if initiator == "" {
		return refuse(errOut, "friend hello", "want NOVA_FRIEND")
	}
	if *as != initiator {
		return refuse(errOut, "friend hello", fmt.Sprintf("want --as equal to NOVA_FRIEND (NOVA_FRIEND=%s, --as %s)", initiator, *as))
	}
	if *host == "" {
		var err error
		*host, err = os.Hostname()
		if err != nil {
			return refuse(errOut, "friend hello", err.Error())
		}
	}
	if *session == "" {
		var err error
		*session, err = newLifeSession()
		if err != nil {
			return refuse(errOut, "friend hello", err.Error())
		}
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend hello", err.Error())
	}
	defer st.Close()

	res, err := life.Hello(ctx, st, life.HelloRequest{
		Sprint: *sprint, As: *as, Slots: *slots, Harness: *harness,
		Host: *host, Machine: *machine, Session: *session, Actor: initiator, Idem: "",
		Logins: logins,
	})
	if err != nil {
		return refuse(errOut, "friend hello", err.Error())
	}
	fmt.Fprintf(out, "%s up slots=%d taken=%d\n", *as, res.Slots, len(res.Claims))
	printLifeClaims(out, res.Claims)
	if *once {
		return 0
	}
	return runPresenceLoop(ctx, st, life.Presence{Friend: *as, Harness: *harness, Host: *host, Session: *session}, *sprint, out, errOut)
}

func runFriendBye(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("friend bye")
	as := fs.String("as", "", "friend name")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend bye", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "friend bye", "takes flags, not positional arguments")
	}
	if *as == "" {
		return refuse(errOut, "friend bye", "--as is required")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend bye", err.Error())
	}
	defer st.Close()
	was, err := life.Bye(ctx, st, *as, "friend", "")
	if err != nil {
		return refuse(errOut, "friend bye", err.Error())
	}
	fmt.Fprintf(out, "%s down registered=%t\n", *as, was)
	return 0
}

func runFriendWake(ctx context.Context, args []string, out, errOut io.Writer) int {
	positional := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[0], args[1:]
	}
	fs, addr := lifeFlags("friend wake")
	friend := fs.String("friend", "", "friend name")
	reason := fs.String("reason", "", "wake reason")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend wake", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "friend wake", "takes one friend name followed by flags")
	}
	if positional != "" {
		if *friend != "" && *friend != positional {
			return refuse(errOut, "friend wake", "positional friend and --friend disagree")
		}
		*friend = positional
	}
	if *friend == "" {
		return refuse(errOut, "friend wake", "--friend is required")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend wake", err.Error())
	}
	defer st.Close()
	if err := life.Wake(ctx, st, *friend, *reason, "reconciler", ""); err != nil {
		return refuse(errOut, "friend wake", err.Error())
	}
	fmt.Fprintf(out, "%s woken reason=%s\n", *friend, *reason)
	return 0
}

func runBenchBeat(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bench beat")
	bench := fs.String("bench", "", "bench name")
	host := fs.String("host", "", "bench host")
	user := fs.String("user", "", "bench user")
	load1 := fs.String("load1", "", "one minute load")
	ssh := fs.String("ssh", "", "ssh endpoint")
	probe := fs.String("probe", "", "probe endpoint")
	launcher := fs.String("launcher", "", "launcher identity")
	why := fs.String("why", "", "why the bench is live")
	session := fs.String("session", "", "fenced owner session identity")
	live := fs.String("live", "", "comma separated card identities")
	once := fs.Bool("once", false, "write one beat and return without the 1 s loop")
	root := fs.String("root", "", "the bench root whose harness, mirrors and free disk the beat carries (default ~/"+life.DefaultRoot+")")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bench beat", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "bench beat", "takes flags, not positional arguments")
	}
	if *bench == "" {
		return refuse(errOut, "bench beat", "--bench is required")
	}
	if *root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return refuse(errOut, "bench beat", "no home for the default --root: "+err.Error())
		}
		*root = filepath.Join(home, life.DefaultRoot)
	}
	if *session == "" {
		var err error
		*session, err = newLifeSession()
		if err != nil {
			return refuse(errOut, "bench beat", err.Error())
		}
	}
	// One connection for the whole loop (#3372): no dial, auth or fork per tick.
	st, err := store.OpenSingle(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "bench beat", err.Error())
	}
	defer st.Close()

	req := life.BenchRequest{
		Bench: *bench, Host: *host, User: *user, Load1: *load1, SSH: *ssh,
		Probe: *probe, Launcher: *launcher, Why: *why, Session: *session,
		Live: splitLive(*live), Actor: "bench", Facts: life.MeasureBench(*root),
		RowAt: time.Now(), NCPU: runtime.NumCPU(),
	}
	// The host row's load (#3440): the flag when given, else measured each beat.
	measureLoad := *load1 == ""
	if measureLoad {
		req.Load1 = life.Load1Now()
	}
	res, err := life.BenchBeat(ctx, st, req)
	if err != nil {
		return refuse(errOut, "bench beat", err.Error())
	}
	if !res.Accepted {
		fmt.Fprintf(errOut, "bench %s busy; owner=%s\n", *bench, res.Owner)
		return 2
	}
	fmt.Fprintf(out, "%s beat owner=%s\n", *bench, res.Owner)
	if *once {
		return 0
	}
	ticker := time.NewTicker(life.BeatInterval)
	defer ticker.Stop()
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return benchBeatLoop(signalCtx, ticker.C, life.BeatInterval, *bench, errOut,
		func(ctx context.Context) (life.BenchResult, error) {
			req.Facts, req.RowAt = life.MeasureBench(*root), time.Now()
			if measureLoad {
				req.Load1 = life.Load1Now()
			}
			return life.BenchBeat(ctx, st, req)
		})
}

// benchBeatMaxBackoff caps the wait between failed beats.
const benchBeatMaxBackoff = 16 * time.Second

// benchBeatLoop writes one beat per tick through the caller's one store
// (#3372). A failed beat backs off: the next attempt waits one interval, then
// doubles up to benchBeatMaxBackoff, and each attempt redials through the same
// client; the first success resets it. Losing ownership exits 2; ctx ending
// exits 0.
func benchBeatLoop(ctx context.Context, ticks <-chan time.Time, interval time.Duration, bench string, errOut io.Writer, beat func(context.Context) (life.BenchResult, error)) int {
	var backoff time.Duration
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return 0
		case now := <-ticks:
			if now.Before(next) {
				continue
			}
			res, err := beat(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return 0
				}
				backoff = benchBeatBackoff(backoff, interval)
				next = now.Add(backoff)
				fmt.Fprintf(errOut, "bench %s beat: %v; retry in %s\n", bench, err, backoff)
				continue
			}
			backoff, next = 0, time.Time{}
			if !res.Accepted {
				fmt.Fprintf(errOut, "bench %s lost ownership to %s\n", bench, res.Owner)
				return 2
			}
		}
	}
}

// benchBeatBackoff is the wait after one more failure: one interval first,
// then double the last wait, never above benchBeatMaxBackoff.
func benchBeatBackoff(last, interval time.Duration) time.Duration {
	next := interval
	if last > 0 {
		next = 2 * last
	}
	if next > benchBeatMaxBackoff {
		next = benchBeatMaxBackoff
	}
	return next
}

// runFriendRow writes friend:<f>, the friend's sprint-table row, once a
// second through one connection (#3440; it replaces rowan-tools
// bin/friend-row). Each pass is one ns_friend_row call. --once writes one
// row and prints it. A failed pass backs off as bench beat does.
func runFriendRow(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("friend row")
	as := fs.String("as", "", "friend whose row this is")
	sprint := fs.String("sprint", "", "sprint whose friend-queue index sets are counted")
	once := fs.Bool("once", false, "write one row and return without the 1 s loop")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend row", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "friend row", "takes flags, not positional arguments")
	}
	if *as == "" || *sprint == "" {
		return refuse(errOut, "friend row", "--as and --sprint are required")
	}
	st, err := store.OpenSingle(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "friend row", err.Error())
	}
	defer st.Close()
	pass := func(ctx context.Context) (life.FriendRowResult, error) {
		return life.FriendRow(ctx, st, life.FriendRowRequest{Friend: *as, Sprint: *sprint})
	}
	res, err := pass(ctx)
	if err != nil {
		return refuse(errOut, "friend row", err.Error())
	}
	up := 0
	if res.Up {
		up = 1
	}
	fmt.Fprintf(out, "%s row up=%d ready=%d working=%d waiting=%d done=%d slots=%s at=%s\n",
		res.Friend, up, res.Ready, res.Working, res.Waiting, res.Done, res.Slots, res.At)
	if *once {
		return 0
	}
	ticker := time.NewTicker(life.BeatInterval)
	defer ticker.Stop()
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	var backoff time.Duration
	var next time.Time
	for {
		select {
		case <-signalCtx.Done():
			return 0
		case now := <-ticker.C:
			if now.Before(next) {
				continue
			}
			if _, err := pass(signalCtx); err != nil {
				if signalCtx.Err() != nil {
					return 0
				}
				backoff = benchBeatBackoff(backoff, life.BeatInterval)
				next = now.Add(backoff)
				fmt.Fprintf(errOut, "friend %s row: %v; retry in %s\n", res.Friend, err, backoff)
				continue
			}
			backoff, next = 0, time.Time{}
		}
	}
}

func runBenchRelease(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bench release")
	bench := fs.String("bench", "", "bench name")
	session := fs.String("session", "", "fenced owner session identity")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bench release", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "bench release", "takes flags, not positional arguments")
	}
	if *bench == "" || *session == "" {
		return refuse(errOut, "bench release", "--bench and --session are required")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "bench release", err.Error())
	}
	defer st.Close()
	if err := life.BenchRelease(ctx, st, *bench, *session, "bench", ""); err != nil {
		return refuse(errOut, "bench release", err.Error())
	}
	fmt.Fprintf(out, "%s released\n", *bench)
	return 0
}

// runPresenceLoop keeps a friend's beat fresh at 1 s until the process is
// interrupted. It is the stoppable loop #2756 v5 requires: SIGINT/SIGTERM
// cancels it and the deferred Stop waits for the goroutine to leave.
func runPresenceLoop(ctx context.Context, st *store.Store, p life.Presence, sprint string, out, errOut io.Writer) int {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(life.BeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-signalCtx.Done():
			return 0
		case <-ticker.C:
			if err := life.Beat(signalCtx, st, p); err != nil {
				fmt.Fprintf(errOut, "friend %s beat: %v\n", p.Friend, err)
				return 3
			}
			if _, err := life.PollWake(signalCtx, st, p.Friend); err != nil {
				fmt.Fprintf(errOut, "friend %s wake: %v\n", p.Friend, err)
				continue
			}
			// p.Friend is the initiator: hello refused any --as != NOVA_FRIEND.
			claims, err := task.TakeAvailable(signalCtx, st, p.Friend, sprint, "", 0, p.Friend, "")
			if err != nil {
				fmt.Fprintf(errOut, "friend %s take: %v\n", p.Friend, err)
				continue
			}
			printLifeClaims(out, claims)
		}
	}
}

func printLifeClaims(out io.Writer, claims []task.Claim) {
	for _, c := range claims {
		fmt.Fprintf(out, "CLAIMED %s/%s attempt=%d token=%s\n", c.Sprint, c.ID, c.Attempt, c.Token)
	}
}

func newLifeSession() (string, error) {
	var bits [16]byte
	if _, err := rand.Read(bits[:]); err != nil {
		return "", fmt.Errorf("create presence session: %w", err)
	}
	return hex.EncodeToString(bits[:]), nil
}

func splitLive(live string) []string {
	if live == "" {
		return nil
	}
	parts := strings.Split(live, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loginFlags is the repeatable `friend hello --login <alias>`.
type loginFlags []string

func (l *loginFlags) String() string { return strings.Join(*l, ",") }

func (l *loginFlags) Set(v string) error {
	*l = append(*l, v)
	return nil
}
