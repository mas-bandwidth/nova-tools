// The fenced stream-PR lander verbs (nova-tools #2942 rev 6; the engine is
// internal/nsprint/land/fenced, the Redis functions lua/land_take.lua):
//
//	nova-sprint land run --sprint <S> --repo <owner/name> --base <branch> [--max 8]
//	    [--interval 1s | --once] [--mirror <bare git dir>] [--remote <git url>] [--dry-run] [--redis <addr>]
//	nova-sprint land offer --ref <owner/name>#<n> --sprint <S> --stream <slug> --base <branch>
//	    --created <RFC 3339> --body-first "<line>" --as <friend> [--withdraw] [--redis <addr>]
//	nova-sprint land list --sprint <S> [--repo <owner/name>] [--base <branch>] [--redis <addr>]
//
// An offered stream PR lives on its unit record pr:<name>:<n> (the record pr
// record writes) as the lander's land_* fields (nova-tools #4079). migrate is
// the one-time move of a sprint's stream PRs offered before that: one
// ns_land_migrate call, LAND MIGRATE sprint=<S> moved= kept= missing=; a
// second run moves nothing.
//
// run: one line per pass, LAND run=<id> repo= base= gen= offered= skipped=
// pushed= landed= fenced=; a second seat on a held (repo, base) prints
// LAND repo= base= writer=<runner> gen=<g> and writes nothing. No REST: git
// fetch and git push only. The landing token (NOVA_LAND_TOKEN, from
// nova-secrets) reaches git through GIT_ASKPASS (this binary), never argv or
// disk; commits are made as NOVA_LAND_NAME <NOVA_LAND_EMAIL>, never the seat's
// git identity.
//
// Exit 0 ran, 1 fenced or a push refused (--once), 2 usage or a fence the
// pass cannot trust, 6 no Redis.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/fenced"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// landAskpassEnv puts this binary in askpass mode for git: it prints the
// landing token (or the user name) and exits, so the token never touches
// argv or disk.
const landAskpassEnv = "NOVA_SPRINT_LAND_ASKPASS"

func init() {
	if os.Getenv(landAskpassEnv) != "1" {
		return
	}
	prompt := strings.ToLower(strings.Join(os.Args[1:], " "))
	if strings.Contains(prompt, "username") {
		fmt.Println("x-access-token")
	} else {
		fmt.Println(os.Getenv("NOVA_LAND_TOKEN"))
	}
	os.Exit(0)
}

// landRunner is the runner id, <host>:<pid>; a control swaps it to run two
// seats in one process.
var landRunner = func() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "host"
	}
	return h + ":" + strconv.Itoa(os.Getpid())
}

// landRunHooks are the engine's pause seams; only a control sets them.
var landRunHooks fenced.Hooks

// landRedis is --redis, else the one resolver (seat.go).
func landRedis(flagVal string) string {
	return redisOr(flagVal)
}

// landRepoFull refuses anything but owner/name.
func landRepoFull(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && owner != "" && name != "" && !strings.ContainsAny(name, "/: \t")
}

func isBareRepo(dir string) bool {
	if st, err := os.Stat(filepath.Join(dir, "objects")); err != nil || !st.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return os.IsNotExist(err)
}

func landOpen(ctx context.Context, verb, addr string, errOut io.Writer) (*store.Store, *redis.Client, int) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return nil, nil, 6
	}
	if _, _, err := fn.Ensure(ctx, st.Client()); err != nil {
		st.Close()
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return nil, nil, 6
	}
	return st, st.Client(), 0
}

func runLandRun(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land run"
	fs := taskFlags(verb)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	base := fs.String("base", "", "the base branch the stream PRs land on")
	max := fs.Int("max", 8, "the most PRs one pass lands")
	interval := fs.Duration("interval", time.Second, "how long between passes (with no --once)")
	once := fs.Bool("once", false, "one pass, then return; no loop")
	mirror := fs.String("mirror", "", "the bare git dir the clone references")
	remote := fs.String("remote", "", "the git url pushed to")
	dry := fs.Bool("dry-run", false, verbflag.HelpDryRun)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, fmt.Sprintf("takes flags, not %q", fs.Arg(0)))
	}
	if !landRepoFull(*repo) {
		return refuse(errOut, verb, "want --repo owner/name")
	}
	if *base == "" || *sprint == "" {
		return refuse(errOut, verb, "want --sprint <S> and --base <branch>")
	}
	_, name, _ := strings.Cut(*repo, "/")
	dir := *mirror
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "nova-bench", "mirror", name+".git")
	}
	if !isBareRepo(dir) {
		return refuse(errOut, verb, fmt.Sprintf("want --mirror <bare git dir>: %s is not one", dir))
	}
	addr := landRedis(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "want --redis <addr> (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	}
	rem := *remote
	if rem == "" {
		rem = "https://github.com/" + *repo + ".git"
	}
	st, client, code := landOpen(ctx, verb, addr, errOut)
	if code != 0 {
		return code
	}
	defer st.Close()
	cfg := fenced.Config{
		Client: client, Sprint: *sprint, Repo: *repo, Base: *base, Remote: rem, Mirror: dir,
		Runner: landRunner(), Max: *max, DryRun: *dry, Hooks: landRunHooks,
		Identity: fenced.Identity{Name: os.Getenv("NOVA_LAND_NAME"), Email: os.Getenv("NOVA_LAND_EMAIL")},
	}
	if cfg.Identity.Name == "" || cfg.Identity.Email == "" {
		cfg.Identity = fenced.Identity{}
	}
	if strings.HasPrefix(rem, "https://") && os.Getenv("NOVA_LAND_TOKEN") != "" {
		if exe, err := os.Executable(); err == nil {
			cfg.Env = []string{"GIT_ASKPASS=" + exe, landAskpassEnv + "=1"}
		}
	}
	w := fenced.New(cfg)
	var held int64
	defer func() {
		if held > 0 && !*once {
			_, _ = w.Release(context.Background(), held)
		}
	}()
	for {
		res, err := w.Pass(ctx)
		var fatal *fenced.FatalError
		if errors.As(err, &fatal) {
			return refuse(errOut, verb, fatal.Msg)
		}
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
			if *once {
				return 1
			}
		} else if *dry {
			for _, c := range res.Candidates {
				reason := c.Reason
				if reason == "" {
					reason = "ok"
				}
				fmt.Fprintf(out, "CANDIDATE %s#%s head=%s facts=%s\n", *repo, c.N, c.Head, reason)
			}
			fmt.Fprintf(out, "LAND dry-run repo=%s base=%s offered=%d\n", *repo, *base, res.Offered)
			return 0
		} else {
			held = res.Gen
			fmt.Fprintln(out, res.Line(*repo, *base))
			if res.Fenced {
				return 1
			}
			if *once {
				if res.PushRefused != "" {
					return 1
				}
				return 0
			}
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(*interval):
		}
	}
}

func runLandOffer(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land offer"
	const usage = "land offer --ref <owner/name>#<n> --sprint <S> --stream <slug> --base <branch> --created <RFC 3339> --body-first \"<line>\" --as <friend> [--withdraw]"
	fs := taskFlags(verb)
	ref := fs.String("ref", "", "the pull request, <owner/name>#<n>")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	stream := fs.String("stream", "", verbflag.HelpStream)
	base := fs.String("base", "", "the base branch the PR lands on")
	created := fs.String("created", "", "when the PR was opened, RFC 3339 (the landing order)")
	body := fs.String("body-first", "", "the PR body's first line")
	as := fs.String("as", "", verbflag.HelpAs)
	withdraw := fs.Bool("withdraw", false, "withdraw the offer instead of making it")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || *ref == "" {
		return refuse(errOut, verb, usage)
	}
	repo, n, ok := strings.Cut(*ref, "#")
	if _, err := strconv.Atoi(n); !ok || err != nil || !landRepoFull(repo) {
		return refuse(errOut, verb, "want --ref <owner/name>#<n>, not "+strconv.Quote(*ref))
	}
	if *sprint == "" || *base == "" || *as == "" {
		return refuse(errOut, verb, usage)
	}
	var unix int64
	if !*withdraw {
		if *stream == "" {
			return refuse(errOut, verb, usage)
		}
		t, err := time.Parse(time.RFC3339, *created)
		if err != nil {
			return refuse(errOut, verb, "want --created <RFC 3339>: "+err.Error())
		}
		unix = t.Unix()
	}
	addr := landRedis(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "want --redis <addr> (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	}
	st, client, code := landOpen(ctx, verb, addr, errOut)
	if code != 0 {
		return code
	}
	defer st.Close()
	w := "0"
	if *withdraw {
		w = "1"
	}
	res, err := client.FCall(ctx, "ns_land_offer", nil, *sprint, repo, *base, n, *stream,
		strconv.FormatInt(unix, 10), *created, *body, *as, w).Slice()
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 1
	}
	fmt.Fprintf(out, "LAND %s %s#%s base=%s %s\n", fmt.Sprint(res[0]), repo, n, *base, fmt.Sprint(res[1:]...))
	return 0
}

func runLandList(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land list"
	fs := taskFlags(verb)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	base := fs.String("base", "", "list only the offers on this base")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *sprint == "" {
		return refuse(errOut, verb, "want --sprint <S>")
	}
	addr := landRedis(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "want --redis <addr> (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	lines, err := fenced.List(ctx, st.Client(), *sprint, *repo, *base)
	if storeDown(errOut, verb, err) {
		return 6
	}
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 1
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return 0
}
