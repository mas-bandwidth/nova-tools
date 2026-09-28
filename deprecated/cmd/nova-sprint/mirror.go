// The mirror verb (nova-tools #2922) keeps this bench's ~/nova-bench/mirror
// fresh from the one source bench and records each pass on bench:<b>:mirrors;
// internal/nsprint/mirror holds the logic. It replaces rowan-tools
// bin/mirror-refresh.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/mirror"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "mirror",
		Summary: "refresh this bench's mirrors from the source bench (--loop <secs>), check them read-only, or print every bench's status",
		Run:     runMirror,
	})
}

const mirrorUsage = "want refresh, check or status"

func runMirror(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "mirror", mirrorUsage)
	}
	switch args[0] {
	case "refresh":
		return runMirrorRefresh(ctx, args[1:], out, errOut)
	case "check":
		return runMirrorCheck(ctx, args[1:], out, errOut)
	case "status":
		return runMirrorStatus(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "mirror", "unknown subverb "+args[0]+"; "+mirrorUsage)
	}
}

type mirrorFlags struct {
	fs                                                   *flag.FlagSet
	addr, bench, source, sourceURL, upstream, dir, repos *string
}

func newMirrorFlags(name string) mirrorFlags {
	fs, addr := lifeFlags(name)
	return mirrorFlags{
		fs: fs, addr: addr,
		bench:     fs.String("bench", "", "this bench"),
		source:    fs.String("source", "", "the source bench: the one that fetches --upstream"),
		sourceURL: fs.String("source-url", "", "where a follower fetches: <source-url>/<repo>.git (for example space:nova-bench/mirror)"),
		upstream:  fs.String("upstream", mirror.DefaultUpstream, "where the source fetches: <upstream>/<repo>.git"),
		dir:       fs.String("dir", "", "mirror root, mirror = <dir>/<repo>.git (default ~/nova-bench/mirror)"),
		repos:     fs.String("repos", mirror.DefaultRepos, "<repo>:<ref>, comma-joined"),
	}
}

func (f mirrorFlags) config() (mirror.Config, error) {
	repos, err := mirror.ParseRepos(*f.repos)
	if err != nil {
		return mirror.Config{}, err
	}
	dir := *f.dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return mirror.Config{}, fmt.Errorf("no home for the default --dir: %v", err)
		}
		dir = filepath.Join(home, "nova-bench", "mirror")
	}
	return mirror.Config{
		Bench: *f.bench, Source: *f.source, SourceURL: *f.sourceURL, Upstream: *f.upstream,
		Dir: dir, Repos: repos, Git: mirror.ExecGit{},
	}, nil
}

func runMirrorRefresh(ctx context.Context, args []string, out, errOut io.Writer) int {
	f := newMirrorFlags("mirror refresh")
	loop := f.fs.Int("loop", 0, "seconds between passes; 0 is one pass")
	if err := f.fs.Parse(args); err != nil {
		return refuse(errOut, "mirror refresh", err.Error())
	}
	if f.fs.NArg() != 0 {
		return refuse(errOut, "mirror refresh", "takes flags, not positional arguments")
	}
	if *loop < 0 {
		return refuse(errOut, "mirror refresh", "--loop wants whole seconds >= 1, or 0 for one pass")
	}
	cfg, err := f.config()
	if err == nil {
		err = cfg.Validate()
	}
	if err != nil {
		return refuse(errOut, "mirror refresh", err.Error())
	}
	st, err := store.OpenSingle(ctx, lifeAddr(*f.addr))
	if err != nil {
		return refuse(errOut, "mirror refresh", err.Error())
	}
	defer st.Close()
	if *loop == 0 {
		ok, err := mirror.Pass(ctx, cfg, st.Client(), nil, true, out)
		if err != nil {
			fmt.Fprintf(errOut, "REFUSED %s - reason=redis err=%s\n", cfg.Bench, strings.TrimSpace(err.Error()))
			return 1
		}
		if !ok {
			return 1
		}
		return 0
	}
	session, err := newLifeSession()
	if err != nil {
		return refuse(errOut, "mirror refresh", err.Error())
	}
	every := time.Duration(*loop) * time.Second
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mirror.Loop(signalCtx, cfg, st.Client(), ticker.C, every, session, out, errOut)
}

func runMirrorCheck(ctx context.Context, args []string, out, errOut io.Writer) int {
	f := newMirrorFlags("mirror check")
	if err := f.fs.Parse(args); err != nil {
		return refuse(errOut, "mirror check", err.Error())
	}
	if f.fs.NArg() != 0 {
		return refuse(errOut, "mirror check", "takes flags, not positional arguments")
	}
	cfg, err := f.config()
	if err != nil {
		return refuse(errOut, "mirror check", err.Error())
	}
	if cfg.Bench == "" {
		cfg.Bench = "-"
	}
	code := 0
	for _, r := range mirror.Check(ctx, cfg) {
		fmt.Fprintln(out, r.Line(cfg.Bench))
		if !r.OK {
			code = 1
		}
	}
	return code
}

func runMirrorStatus(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("mirror status")
	repos := fs.String("repos", mirror.DefaultRepos, "<repo>:<ref>, comma-joined")
	repo := fs.String("repo", "", "report only this repo")
	source := fs.String("source", "", "the source bench, whose recorded tip is expected when --expect names none")
	stale := fs.Duration("stale", 3*time.Minute, "a receipt older than this reads UNREACHABLE")
	var expects loginFlags
	fs.Var(&expects, "expect", "<repo>=<40-hex> (repeatable)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "mirror status", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "mirror status", "takes flags, not positional arguments")
	}
	list, err := mirror.ParseRepos(*repos)
	if err != nil {
		return refuse(errOut, "mirror status", err.Error())
	}
	o := mirror.StatusOptions{Expect: map[string]string{}, Source: *source, Stale: *stale}
	for _, r := range list {
		if *repo == "" || r.Name == *repo {
			o.Repos = append(o.Repos, r.Name)
		}
	}
	if *repo != "" && len(o.Repos) == 0 {
		o.Repos = []string{*repo}
	}
	for _, e := range expects {
		name, sha, ok := strings.Cut(e, "=")
		if !ok || len(sha) != 40 {
			return refuse(errOut, "mirror status", "--expect wants <repo>=<40-hex>, got "+e)
		}
		o.Expect[name] = sha
	}
	if o.Source == "" {
		for _, r := range o.Repos {
			if o.Expect[r] == "" {
				return refuse(errOut, "mirror status", "no --expect for "+r+" and no --source to read the expected tip from")
			}
		}
	}
	st, err := store.Open(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "mirror status", err.Error())
	}
	defer st.Close()
	ok, err := mirror.Status(ctx, st.Client(), o, out)
	if err != nil {
		return refuse(errOut, "mirror status", err.Error())
	}
	if !ok {
		return 1
	}
	return 0
}
