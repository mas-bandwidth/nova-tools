package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// runCardPool runs card push, release and bench-side stop; runCard (card_run.go) routes them here.
func runCardPool(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "card", "needs push, release or stop")
	}
	switch args[0] {
	case "push":
		return cmdCardPush(ctx, args[1:], stdout, stderr)
	case "release":
		return cmdCardRelease(ctx, args[1:], stdout, stderr)
	case "stop":
		return cmdCardStop(ctx, args[1:], os.Stdin, stdout, stderr)
	default:
		return refuse(stderr, "card", "unknown subcommand "+args[0]+"; it wants push, release or stop")
	}
}

func cmdCardPush(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	stdin := fs.Bool("stdin", false, "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" {
		return refuse(stderr, "card", "push needs --sprint <name>, --redis <addr>, and one card file (or --stdin)")
	}
	if *stdin && fs.NArg() > 0 {
		return refuse(stderr, "card", "push reads --stdin or card files, not both")
	}
	if !*stdin && fs.NArg() == 0 {
		return refuse(stderr, "card", "push needs a card file, or --stdin")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	bodies := [][]byte{}
	if *stdin {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			return refuse(stderr, "card", "cannot read stdin: "+err.Error())
		}
		bodies = append(bodies, body)
	}
	for _, path := range fs.Args() {
		body, err := os.ReadFile(path)
		if err != nil {
			return refuse(stderr, "card", "cannot read "+path+": "+err.Error())
		}
		bodies = append(bodies, body)
	}
	for _, body := range bodies {
		res := card.Push(ctx, client, *sprint, body)
		if wrote := writeCardResult(stdout, stderr, res); wrote != 0 {
			return wrote
		}
	}
	return 0
}

func cmdCardRelease(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card release", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "card", "release needs --sprint <name> and --redis <addr>, and no card file")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	return writeCardResult(stdout, stderr, card.Release(ctx, client, *sprint))
}

func openCardRedis(ctx context.Context, addr string, stderr io.Writer) (*redis.Client, int) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, refuse(stderr, "card", err.Error())
	}
	return st.Client(), 0
}

func writeCardResult(stdout, stderr io.Writer, res card.VerbResult) int {
	if res.Code != 0 {
		fmt.Fprintf(stderr, "nova-sprint card: %s; run: nova-sprint help\n", oneline.Escape(strings.TrimSpace(res.Stderr)))
		return res.Code
	}
	if _, err := io.WriteString(stdout, res.Stdout); err != nil {
		return refuse(stderr, "card", err.Error())
	}
	return 0
}

func cmdCardStop(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fromStdin := fs.Bool("stdin", false, "")
	grace := fs.Duration("grace", launch.DefaultStopGrace, "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "card stop", err.Error())
	}
	if !*fromStdin || fs.NArg() != 0 {
		return refuse(stderr, "card stop", "wants --stdin [--grace <duration>]")
	}
	if *grace <= 0 {
		return refuse(stderr, "card stop", "--grace must be positive")
	}
	sum, err := launch.Stop(ctx, stdin, stdout, *grace, launch.OSGroupManager{}, nil)
	if err != nil {
		return refuse(stderr, "card stop", err.Error())
	}
	fmt.Fprintf(stdout, "STOP stopped=%s gone=%s alive=%s\n", strconv.Itoa(sum.Stopped), strconv.Itoa(sum.Gone), strconv.Itoa(sum.Alive))
	if sum.Alive > 0 {
		return 1
	}
	return 0
}
