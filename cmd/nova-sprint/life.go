// The life verb registers itself through the S0 registry (registry.go), so
// adding friend/bench presence never edits main.go or task.go. Each subverb is
// one call into internal/nsprint/life, which is one Redis Function call.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "friend",
		Summary: "hello, bye, wake, row and roles for a friend; report a friend state, show friends, or run one ladder sweep; declare the wake registry from the fleet file; wake-health [--repair]",
		Run:     runFriend,
	})
	register(Verb{
		Name:    "life",
		Summary: "append a friend lifecycle event (beat, deliver, turn-start, ...) or declare a proven wake mode",
		Run:     runLife,
	})
	register(Verb{
		Name:    "bench",
		Summary: "beat or release one bench; reindex a sprint's card views once; ls the registry with each bench's role",
		Run:     runBench,
	})
}

func runFriend(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "friend", "want hello, bye, wake, row, roles, report, show, sweep, down, up, declare or wake-health")
	}
	switch args[0] {
	case "hello":
		return runFriendHello(ctx, args[1:], out, errOut)
	case "bye":
		return runFriendBye(ctx, args[1:], out, errOut)
	case "wake":
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
	case "declare":
		return runFriendDeclare(ctx, args[1:], out, errOut)
	case "wake-health":
		return runFriendWakeHealth(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "friend", fmt.Sprintf("unknown subverb %s; want hello, bye, wake, row, roles, report, show, sweep, down, up, declare or wake-health", args[0]))
	}
}

func runBench(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "bench", "want beat, release, reindex or ls")
	}
	switch args[0] {
	case "beat":
		return runBenchBeat(ctx, args[1:], out, errOut)
	case "release":
		return runBenchRelease(ctx, args[1:], out, errOut)
	case "reindex":
		return runBenchReindex(ctx, args[1:], out, errOut)
	case "ls":
		return runBenchLs(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "bench", fmt.Sprintf("unknown subverb %s; want beat, release, reindex or ls", args[0]))
	}
}

// lifeFlags is the quiet flag set shared by the presence subverbs, with the
// Redis endpoint from --redis, then NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR,
// then the local test endpoint. A real production host is never hardcoded.
func lifeFlags(name string) (*flag.FlagSet, *string) {
	fs := verbflag.New(name)
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
	launcher := fs.String("launcher", life.BatchLauncher, "launcher identity the beat names (preflight 7.4)")
	why := fs.String("why", "", "why the bench is live")
	session := fs.String("session", "", "fenced owner session identity")
	live := fs.String("live", "", "comma separated card identities")
	once := fs.Bool("once", false, "write one beat and return without the 1 s loop")
	root := fs.String("root", "", "the bench root whose harness, mirrors and free disk the beat carries (default ~/"+life.DefaultRoot+")")
	copies := fs.Bool("copies", true, "each beat, when bench:<b> is enrolled in consumers, open the copy session: card work --fill and one nova-card copy per copy (#3998)")
	wrapper := fs.String("wrapper", "", "the nova-card the copy session starts (default: beside this executable)")
	cardEnv := fs.String("card-env", "", "the bench's card environment file the copy session hands nova-card (default ~/nova-bench/launch/card.env, the fleet play's; absent is refused when --copies is on)")
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
	if *launcher == "" {
		*launcher = preflight.BatchLauncherName
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
	req.CPU = life.CPUBusyNow()
	req.CI = life.CILegsNow()
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
	// #3048: every WakeRepairEvery-th beat repairs the friends declared on
	// this host; no new unit, no tokens.
	wakeOn := *host
	if wakeOn == "" {
		h, err := os.Hostname()
		if err != nil {
			// The repair tick names this host; a bench that cannot say
			// its name refuses now, not every tenth beat with an empty host.
			return refuse(errOut, "bench beat", "no --host and no hostname: "+err.Error())
		}
		wakeOn = h
	}
	beats := 0
	var launchEnv []string
	if *copies {
		path := *cardEnv
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return refuse(errOut, "bench beat", err.Error())
			}
			path = filepath.Join(home, "nova-bench", "launch", "card.env")
		}
		env, err := launch.ReadEnvFile(path)
		if err != nil {
			return refuse(errOut, "bench beat", "card env: "+err.Error()+" (the fleet play in rowan-tools writes it)")
		}
		launchEnv = env
	}
	copySession := benchCopySession(st, *bench, *copies, *wrapper, launchEnv, out, errOut)
	return benchBeatLoop(signalCtx, ticker.C, life.BeatInterval, *bench, errOut,
		func(ctx context.Context) (life.BenchResult, error) {
			req.Facts, req.RowAt = life.MeasureBench(*root), time.Now()
			if measureLoad {
				req.Load1 = life.Load1Now()
			}
			req.CPU = life.CPUBusyNow()
			req.CI = life.CILegsNow()
			res, err := life.BenchBeat(ctx, st, req)
			if beats++; err == nil && res.Accepted && beats%life.WakeRepairEvery == 0 {
				benchWakeRepair(ctx, st, wakeOn, *session, errOut)
			}
			if err == nil && res.Accepted {
				copySession(ctx)
			}
			return res, err
		})
}

// benchCopySession is the bench harness's session start on each beat
// (#3998): when bench:<b> is enrolled in consumers, card work --as
// bench:<b> --fill (one call) and one detached `nova-card copy <copy>` per
// copy (card.OpenCopySession); a copy that cannot start is given back. A
// session that took nothing prints nothing; an error prints once until it
// changes, and never stops the beat.
func benchCopySession(st *store.Store, bench string, on bool, wrapperFlag string, launchEnv []string, out, errOut io.Writer) func(context.Context) {
	if !on {
		return func(context.Context) {}
	}
	last := ""
	return func(ctx context.Context) {
		s, err := card.OpenCopySession(ctx, st.Client(), bench, "bench:"+bench, func(l card.CopyLaunch) error {
			wrapper, err := wrapperPath(wrapperFlag)
			if err != nil {
				return err
			}
			_, err = launch.LaunchCopyEnv(wrapper, launch.CopyLine{Copy: l.Copy, Token: l.Token}, launch.DefaultBudget, launchEnv)
			return err
		})
		if err != nil {
			if msg := err.Error(); msg != last {
				last = msg
				fmt.Fprintf(errOut, "bench %s copy session: %v\n", bench, err)
			}
			return
		}
		last = ""
		if len(s.Launched)+len(s.GivenBack) > 0 {
			fmt.Fprintln(out, s.Line(bench))
		}
	}
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
	steps := livePresenceSteps(st, p, sprint)
	for {
		select {
		case <-signalCtx.Done():
			return 0
		case <-ticker.C:
			if code, done := presenceCycle(signalCtx, p.Friend, steps, out, errOut); done {
				return code
			}
		}
	}
}

// presenceSteps are the calls of one presence cycle. Production binds them
// to the store (livePresenceSteps); a test injects the clock and observes
// the order.
type presenceSteps struct {
	now   func() time.Time
	beat  func(context.Context) error
	event func(context.Context, time.Time) error
	poll  func(context.Context) error
	take  func(context.Context) ([]task.Claim, error)
}

func livePresenceSteps(st *store.Store, p life.Presence, sprint string) presenceSteps {
	return presenceSteps{
		now:  time.Now,
		beat: func(ctx context.Context) error { return life.Beat(ctx, st, p) },
		// The process beat as a lifecycle event (#3153). p.Friend is the
		// initiator: hello refused any --as != NOVA_FRIEND.
		event: func(ctx context.Context, at time.Time) error {
			_, err := life.AppendEvent(ctx, st, p.Friend, life.EventBeat, "", at, p.Friend)
			return err
		},
		poll: func(ctx context.Context) error {
			_, err := life.PollWake(ctx, st, p.Friend)
			return err
		},
		take: func(ctx context.Context) ([]task.Claim, error) {
			return task.TakeAvailable(ctx, st, p.Friend, sprint, "", 0, p.Friend, "")
		},
	}
}

// presenceCycle is one tick: the process beat, then its beat event on
// friend:<f>:events at the cycle clock's UTC ms, then the wake poll and the
// take. A failed beat or event append ends the loop (exit 3) before any poll
// or take, so a loop that cannot record its beat never keeps a beat-only
// liveness. done reports that the loop must return code.
func presenceCycle(ctx context.Context, friend string, s presenceSteps, out, errOut io.Writer) (code int, done bool) {
	if err := s.beat(ctx); err != nil {
		fmt.Fprintf(errOut, "friend %s beat: %v\n", friend, err)
		return 3, true
	}
	if err := s.event(ctx, s.now().UTC()); err != nil {
		fmt.Fprintf(errOut, "friend %s beat event: %v\n", friend, err)
		return 3, true
	}
	if err := s.poll(ctx); err != nil {
		fmt.Fprintf(errOut, "friend %s wake: %v\n", friend, err)
		return 0, false
	}
	claims, err := s.take(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "friend %s take: %v\n", friend, err)
		return 0, false
	}
	printLifeClaims(out, claims)
	return 0, false
}

// runLife is the #3153 lifecycle verb: `life event` appends one event to
// friend:<f>:events through ns_friend_event, and `life wake-mode` declares
// scheduled-model-turn only on a firing receipt ns_friend_wakemode re-reads.
func runLife(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "life", "want event or wake-mode")
	}
	switch args[0] {
	case "event":
		return runLifeEvent(ctx, args[1:], out, errOut)
	case "wake-mode":
		return runLifeWakeMode(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "life", fmt.Sprintf("unknown subverb %s; want event or wake-mode", args[0]))
	}
}

// runLifeEvent: `life event --as <f> --kind <k> [--cause <id>]`. The seat
// check (NOVA_FRIEND == --as) comes before any dial, as hello's does.
func runLifeEvent(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "life event"
	fs, addr := lifeFlags(verb)
	as := fs.String("as", "", "friend whose event this is")
	kind := fs.String("kind", "", "beat, deliver, turn-start, turn-end, turn-error or usage-limit")
	cause := fs.String("cause", "", "for turn-start: the deliver event id that started the turn")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if *as == "" {
		return refuse(errOut, verb, "--as is required")
	}
	initiator := os.Getenv(seatEnv)
	if initiator == "" {
		return refuse(errOut, verb, "want NOVA_FRIEND")
	}
	if *as != initiator {
		return refuse(errOut, verb, fmt.Sprintf("want --as equal to NOVA_FRIEND (NOVA_FRIEND=%s, --as %s)", initiator, *as))
	}
	if *kind == "" {
		return refuse(errOut, verb, "--kind is required")
	}
	if !life.ValidEventKind(*kind) {
		return refuse(errOut, verb, fmt.Sprintf("--kind %s is not one of %s", *kind, strings.Join(life.EventKinds, ", ")))
	}
	if *cause != "" && *kind != life.EventTurnStart {
		return refuse(errOut, verb, "--cause is only for --kind turn-start")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	id, err := life.AppendEvent(ctx, st, *as, *kind, *cause, time.Now(), initiator)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	line := fmt.Sprintf("EVENT friend=%s kind=%s id=%s", *as, *kind, id)
	if *cause != "" {
		line += " cause=" + *cause
	}
	fmt.Fprintln(out, line)
	return 0
}

// runLifeWakeMode: `life wake-mode --as <f> --set scheduled-model-turn`.
// Exit 1 when the events hold no firing receipt: the call was well formed
// and the bus said no.
func runLifeWakeMode(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "life wake-mode"
	fs, addr := lifeFlags(verb)
	as := fs.String("as", "", "friend whose wake mode this is")
	set := fs.String("set", "", "the wake mode to declare: scheduled-model-turn")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if *as == "" {
		return refuse(errOut, verb, "--as is required")
	}
	if *set == "" {
		return refuse(errOut, verb, "--set is required")
	}
	if *set != life.WakeModeScheduled {
		return refuse(errOut, verb, fmt.Sprintf("--set %s is not %s", *set, life.WakeModeScheduled))
	}
	actor := os.Getenv(seatEnv)
	if actor == "" {
		actor = *as
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	res, err := life.DeclareWakeMode(ctx, st, *as, time.Now(), actor, "")
	if errors.Is(err, life.ErrNoReceipt) {
		fmt.Fprintf(errOut, "nova-sprint life wake-mode: REFUSED friend=%s no firing receipt: no deliver followed by a turn-start with its cause within 120s in the last 20 min\n", *as)
		return 1
	}
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "WAKE-MODE friend=%s mode=%s deliver=%s turn=%s lag_ms=%d\n", *as, life.WakeModeScheduled, res.Deliver, res.Turn, res.LagMS)
	return 0
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

// newWakeHost is the supervisor and git seam the repair tick uses; controls
// replace it with a fake.
var newWakeHost = func() life.WakeHost { return life.ExecWakeHost{} }

// benchWakeRepair is the repair tick `bench beat` runs every 10th beat: the
// friends declared on this host, through the one bench connection.
func benchWakeRepair(ctx context.Context, st *store.Store, host, session string, errOut io.Writer) {
	res, err := life.RepairWake(ctx, st, newWakeHost(), life.RepairRequest{Host: host, Session: session, Actor: "bench"})
	if err != nil {
		fmt.Fprintf(errOut, "bench wake repair on %s: %v\n", host, err)
		return
	}
	for f, holder := range res.Held {
		fmt.Fprintf(errOut, "bench wake repair on %s: %s held by %s\n", host, f, holder)
	}
}

// runFriendDeclare is `friend declare --from <fleet/group_vars/all.yml>
// [--check] [--redis]` (#3048 rev 3). Exit 0 declared or unchanged, 1 a
// store error, 2 usage, an invalid entry or a dirty file, 3 stale or conflict.
func runFriendDeclare(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend declare"
	fs, addr := lifeFlags(verb)
	from := fs.String("from", "", "the committed fleet group_vars file holding friends:")
	check := fs.Bool("check", false, "print the diff against Redis and write nothing")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 || *from == "" {
		return refuse(errOut, verb, "want --from <fleet/group_vars/all.yml> [--check]")
	}
	src, err := life.ReadDeclSource(ctx, *from)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		fmt.Fprintf(errOut, "FRIEND DECLARE REFUSED: %v\n", err)
		return 1
	}
	defer st.Close()
	rev8 := func(r string) string {
		if len(r) > 8 {
			return r[:8]
		}
		if r == "" {
			return "-"
		}
		return r
	}
	if *check {
		c, err := life.CheckDecl(ctx, st, src)
		if err != nil {
			fmt.Fprintf(errOut, "FRIEND DECLARE REFUSED: %v\n", err)
			return 1
		}
		for _, n := range c.Add {
			fmt.Fprintf(out, "+ %s\n", n)
		}
		for _, n := range c.Change {
			fmt.Fprintf(out, "~ %s\n", n)
		}
		for _, n := range c.Remove {
			fmt.Fprintf(out, "- %s\n", n)
		}
		fmt.Fprintf(out, "check registry=%s file=%s add=%d change=%d remove=%d\n",
			rev8(c.Rev), rev8(src.Rev), len(c.Add), len(c.Change), len(c.Remove))
		return 0
	}
	res, err := life.Declare(ctx, st, src)
	switch {
	case errors.Is(err, life.ErrDeclStale), errors.Is(err, life.ErrDeclConflict):
		fmt.Fprintf(errOut, "%v; declare from a checkout at or past the registry's rev\n", err)
		return 3
	case err != nil:
		fmt.Fprintf(errOut, "FRIEND DECLARE REFUSED: %v\n", err)
		return 1
	case res.Unchanged:
		fmt.Fprintf(out, "unchanged %s\n", rev8(res.Rev))
		return 0
	}
	fmt.Fprintf(out, "declared %d unit=%d human=%d removed=%d rev=%s\n", res.N, res.Unit, res.Human, res.Removed, rev8(res.Rev))
	return 0
}

// runFriendWakeHealth is `friend wake-health [--as <f> | --all] [--repair
// [--host <h>]]`. Without --repair it reads Redis only and touches no host.
// With it, it first runs the repair tick for the friends declared on this
// host (a named friend declared elsewhere is refused, exit 1). Either way it
// prints one row per friend and exits 1 when the gate names any friend:
// down, undeclared, or a wake cell older than 30 s (`wake: ?`).
func runFriendWakeHealth(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend wake-health"
	fs, addr := lifeFlags(verb)
	as := fs.String("as", "", "one friend")
	all := fs.Bool("all", false, "every registered or declared friend")
	repair := fs.Bool("repair", false, "run the repair tick for the friends declared on this host first")
	host := fs.String("host", "", "this host (default the hostname); only with --repair")
	session := fs.String("session", "", "the repairer's lock identity (default a fresh one)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 || (*as == "") == !*all {
		return refuse(errOut, verb, "want exactly one of --as <f> or --all")
	}
	if *host != "" && !*repair {
		return refuse(errOut, verb, "--host is read only with --repair")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		fmt.Fprintf(errOut, "WAKE REFUSED: %v\n", err)
		return 1
	}
	defer st.Close()
	if *repair {
		if *host == "" {
			if *host, err = os.Hostname(); err != nil {
				return refuse(errOut, verb, err.Error())
			}
		}
		if *session == "" {
			if *session, err = newLifeSession(); err != nil {
				return refuse(errOut, verb, err.Error())
			}
		}
		res, err := life.RepairWake(ctx, st, newWakeHost(), life.RepairRequest{Host: *host, Only: *as, Session: *session, Actor: "wake-health"})
		if err != nil {
			fmt.Fprintf(errOut, "WAKE REFUSED: %v\n", err)
			return 1
		}
		for f, holder := range res.Held {
			fmt.Fprintf(out, "%s wake: repair held by %s\n", f, holder)
		}
		fmt.Fprintf(out, "REPAIRED host=%s checked=%d held=%d\n", *host, len(res.Checked), len(res.Held))
	}
	snap, err := life.ReadWake(ctx, st.Client())
	if err != nil {
		fmt.Fprintf(errOut, "WAKE REFUSED: %v\n", err)
		return 1
	}
	rows := snap.Rows()
	if *as != "" {
		var one []life.WakeRow
		for _, r := range rows {
			if r.Friend == *as {
				one = append(one, r)
			}
		}
		if len(one) == 0 {
			one = []life.WakeRow{{Friend: *as, Row: "wake: undeclared", Named: true, Why: life.StateUndeclared}}
		}
		rows = one
	}
	var named []string
	for _, r := range rows {
		fmt.Fprintf(out, "%s %s\n", r.Friend, r.Row)
		if r.Named {
			named = append(named, r.Friend)
		}
	}
	summary := fmt.Sprintf("WAKE friends=%d named=%d", len(rows), len(named))
	if len(named) > 0 {
		summary += " " + strings.Join(named, ",")
	}
	fmt.Fprintln(out, summary)
	if len(named) > 0 {
		return 1
	}
	return 0
}
