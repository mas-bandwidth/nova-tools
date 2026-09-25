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
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/unit"
)

func init() {
	register(Verb{
		Name:    "friend",
		Summary: "hello, bye and wake a friend's presence",
		Run:     runFriend,
	})
	register(Verb{
		Name:    "bench",
		Summary: "beat one bench's presence once per second",
		Run:     runBench,
	})
}

func runFriend(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "friend", "want hello, bye or wake")
	}
	switch args[0] {
	case "hello":
		return runFriendHello(ctx, args[1:], out, errOut)
	case "bye":
		return runFriendBye(ctx, args[1:], out, errOut)
	case "wake":
		return runFriendWake(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "friend", fmt.Sprintf("unknown subverb %s; want hello, bye or wake", args[0]))
	}
}

func runBench(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "bench", "want beat or release")
	}
	switch args[0] {
	case "beat":
		return runBenchBeat(ctx, args[1:], out, errOut)
	case "release":
		return runBenchRelease(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "bench", fmt.Sprintf("unknown subverb %s; want beat or release", args[0]))
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
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "friend hello", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "friend hello", "takes flags, not positional arguments")
	}
	if *as == "" {
		return refuse(errOut, "friend hello", "--as is required")
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
		Host: *host, Machine: *machine, Session: *session, Actor: "friend", Idem: "",
	})
	if err != nil {
		return refuse(errOut, "friend hello", err.Error())
	}
	fmt.Fprintf(out, "%s up slots=%d taken=%d\n", *as, res.Slots, len(res.Claims))
	printLifeClaims(out, res.Claims)
	if !*once {
		lease, err := unit.AcquireLease(ctx, st, fmt.Sprintf("lease:friend-hello:%s", *as), 6*time.Second)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint friend hello: %s\n", err.Error())
			return 2
		}
		defer lease.Release(ctx)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				_ = lease.Renew(ctx)
			}
		}()
	}
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
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bench beat", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "bench beat", "takes flags, not positional arguments")
	}
	if *bench == "" {
		return refuse(errOut, "bench beat", "--bench is required")
	}
	if *session == "" {
		var err error
		*session, err = newLifeSession()
		if err != nil {
			return refuse(errOut, "bench beat", err.Error())
		}
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "bench beat", err.Error())
	}
	defer st.Close()

	req := life.BenchRequest{
		Bench: *bench, Host: *host, User: *user, Load1: *load1, SSH: *ssh,
		Probe: *probe, Launcher: *launcher, Why: *why, Session: *session,
		Live: splitLive(*live), Actor: "bench",
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
	for {
		select {
		case <-signalCtx.Done():
			return 0
		case <-ticker.C:
			beat, err := life.BenchBeat(signalCtx, st, req)
			if err != nil {
				fmt.Fprintf(errOut, "bench %s beat: %v\n", *bench, err)
				continue
			}
			if !beat.Accepted {
				fmt.Fprintf(errOut, "bench %s lost ownership to %s\n", *bench, beat.Owner)
				return 2
			}
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
			claims, err := task.TakeAvailable(signalCtx, st, p.Friend, sprint, "", 0, "friend", "")
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
