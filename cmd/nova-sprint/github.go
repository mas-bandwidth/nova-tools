// The github verb (nova-tools #2657): ingest is the ingest consumer of
// ev:github, the one inbound path from GitHub into our records besides
// import. It registers itself through the verb registry, so it never edits
// main.go.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ghingest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "github",
		Summary: "ingest pull_request and issues deliveries from ev:github into the PR records and cards",
		Run:     runGitHub,
	})
}

const githubUsage = "want ingest --redis <addr> --lander <login>[,<login>] [--sprint <S>] [--consumer <seat>] [--once]"

func runGitHub(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "github", githubUsage)
	}
	switch args[0] {
	case "ingest":
		return runGitHubIngest(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "github", "unknown subverb "+args[0]+"; "+githubUsage)
	}
}

// runGitHubIngest is the ingest group's consumer:
//
//	nova-sprint github ingest --redis <addr> --lander <login>[,<login>] [--sprint <S>] [--consumer <seat>] [--once]
//
// Each pull_request entry updates the PR record pr:<name>:<n> (head, state,
// merge_sha) when we hold one; each issues closed entry lands the issue's
// cards when a --lander login closed it as completed, and is one line on
// gh:findings otherwise. Every such entry is one FCALL that writes and acks;
// other kinds are acked as no-ops. --sprint backfills that sprint's issue
// index once at start. It prints one INGEST line per pass that handled an
// entry (every pass with --once, which drains and exits) and runs until
// SIGINT or SIGTERM otherwise. Exit 0 stopped or drained, 1 a pass failed,
// 2 usage.
func runGitHubIngest(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("github ingest")
	redisAddr := fs.String("redis", "", "")
	landers := fs.String("lander", "", "")
	sprint := fs.String("sprint", "", "")
	consumer := fs.String("consumer", "", "")
	once := fs.Bool("once", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "github ingest", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "github ingest", githubUsage+", nothing positional")
	}
	var logins []string
	for _, l := range strings.Split(*landers, ",") {
		if l = strings.TrimSpace(l); l != "" {
			logins = append(logins, l)
		}
	}
	if len(logins) == 0 {
		return refuse(errOut, "github ingest", "--lander names the login our lander closes issues as; "+githubUsage)
	}
	if *consumer == "" {
		host, _ := os.Hostname()
		*consumer = host + "-" + strconv.Itoa(os.Getpid())
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "github ingest", err.Error())
	}
	defer st.Close()
	c := &ghingest.Consumer{Client: st.Client(), Name: *consumer, Landers: logins}
	if *once {
		c.Block = -1
	}
	indexed, err := c.Start(ctx, *sprint)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint github ingest: %v\n", err)
		return 1
	}
	if indexed > 0 {
		fmt.Fprintf(out, "INGEST indexed=%d sprint=%s\n", indexed, *sprint)
	}
	for {
		n, err := c.Pass(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint github ingest: %v\n", err)
			return 1
		}
		if *once {
			fmt.Fprintln(out, n.Line())
			return 0
		}
		if n != (ghingest.Counts{}) {
			fmt.Fprintln(out, n.Line())
		}
	}
}
