// The read verb (nova-tools#3599): a friend read takes the PR record, the
// lines and CI from Redis and the diff from the bench mirror, zero GitHub
// calls. `read brief` writes the read brief; `read post` stores the typed
// line and, until #3595 lands, mirrors it as one PR comment (REST) unless
// --no-github. Registered through registry.go; main.go is untouched.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "read",
		Summary: "brief: the read brief from Redis and the mirror (zero GitHub calls); post: store a typed line",
		Run:     runRead,
	})
}

const readUsage = "want brief --repo <r> --n <n> --out <dir> [--mirror <dir>] [--redis <addr>] or post --repo <r> --n <n> --line <typed line> [--no-github] [--owner <o>] [--redis <addr>]"

func runRead(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "read", readUsage)
	}
	sub := args[0]
	fs := taskFlags("read " + sub)
	redisAddr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	repo := fs.String("repo", "", "")
	n := fs.String("n", "", "")
	outDir := fs.String("out", "", "")
	mirror := fs.String("mirror", "", "")
	line := fs.String("line", "", "")
	noGitHub := fs.Bool("no-github", false, "")
	owner := fs.String("owner", "mas-bandwidth", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return refuse(errOut, "read "+sub, readUsage)
	}
	if *redisAddr == "" {
		*redisAddr = os.Getenv("NOVA_REDIS_ADDR")
	}
	if *repo == "" || strings.ContainsAny(*repo, "/: \t") || *redisAddr == "" {
		return refuse(errOut, "read "+sub, "needs --repo <name> (no owner, no slash), --n <number> and --redis <addr> (or NOVA_SPRINT_REDIS); "+readUsage)
	}
	if v, err := strconv.Atoi(*n); err != nil || v <= 0 {
		return refuse(errOut, "read "+sub, "--n wants a positive PR number, got "+strconv.Quote(*n))
	}
	switch sub {
	case "brief":
		if *outDir == "" || *line != "" || *noGitHub {
			return refuse(errOut, "read brief", "want --repo <r> --n <n> --out <dir> [--mirror <dir>] [--redis <addr>]")
		}
	case "post":
		if *line == "" || *outDir != "" || *mirror != "" {
			return refuse(errOut, "read post", "want --repo <r> --n <n> --line <typed line> [--no-github] [--owner <o>] [--redis <addr>]")
		}
	default:
		return refuse(errOut, "read", "unknown subverb "+sub+"; "+readUsage)
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "read "+sub, err.Error())
	}
	defer func() { _ = st.Close() }()
	if sub == "brief" {
		return read.Brief(ctx, st.Client(), *repo, *n, *mirror, *outDir, out, errOut)
	}
	var poster *read.Poster
	if !*noGitHub {
		token := os.Getenv("GH_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
		if token == "" {
			fmt.Fprintf(errOut, "READ POST REFUSED repo=%s n=%s why=no GH_TOKEN or GITHUB_TOKEN in the environment for the comment mirror; run under nova-secrets exec --only GH_TOKEN, or pass --no-github (Redis only)\n", *repo, *n)
			return 1
		}
		poster = &read.Poster{BaseURL: os.Getenv("GITHUB_API_URL"), Owner: *owner, Token: token}
	}
	return read.Post(ctx, st.Client(), *repo, *n, *line, poster, out, errOut)
}
