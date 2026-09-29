// The bench verb registers itself through the S0 registry (registry.go), so
// adding bench presence never edits main.go or task.go. Each subverb is one
// call into internal/nsprint/life, which is one Redis Function call.
//
// The friend verbs that lived beside it (hello, bye, wake, row, roles,
// report, show, sweep, down, up, declare, wake-health, pull, done, beat) and
// the life verb (event, wake-mode) are gone: nova-friend owns a friend's
// runtime and nova-config its configuration. The bench verb stays until
// nova-fleet owns a machine's runtime.
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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "bench",
		Summary: "beat or release one bench; reindex a sprint's card views once; ls the registry with each bench's role (owed to nova-fleet)",
		Run:     runBench,
	})
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

// lifeFlags is the quiet flag set shared by the bench subverbs, with the
// Redis endpoint from --redis, then NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR,
// then the local test endpoint. A real production host is never hardcoded.
func lifeFlags(name string) (*flag.FlagSet, *string) {
	fs := verbflag.New(name)
	addr := fs.String("redis", redisDefault(), "redis address (env NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR)")
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
func runBenchBeat(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bench beat")
	bench := fs.String("bench", "", "bench name")
	host := fs.String("host", "", "bench host")
	user := fs.String("user", "", "bench user")
	load1 := fs.String("load1", "", "one minute load")
	ssh := fs.String("ssh", "", "ssh endpoint")
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
		Launcher: *launcher, Why: *why, Session: *session,
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
	req.PS = life.ProcsNow()
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
		var clean []string
		for _, e := range env {
			if !strings.HasPrefix(e, "NOVA_CARD_HARNESS=") {
				clean = append(clean, e)
			}
		}
		clean = append(clean, "NOVA_CARD_HARNESS=")
		launchEnv = clean
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
			req.PS = life.ProcsNow()
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

// loginFlags is a repeatable string flag (mirror refresh --expect).
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
