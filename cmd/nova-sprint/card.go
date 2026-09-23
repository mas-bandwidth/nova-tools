package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{
		Name:    "card",
		Summary: "push a card into the pool or waiting, and release it after its parent lands",
		Run:     runCard,
	})
}

func runCard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "card", "needs push or release")
	}
	switch args[0] {
	case "push":
		return cmdCardPush(ctx, args[1:], stdout, stderr)
	case "release":
		return cmdCardRelease(ctx, args[1:], stdout, stderr)
	default:
		return refuse(stderr, "card", "unknown subcommand "+args[0]+"; it wants push or release")
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

func writeCardResult(stdout, stderr io.Writer, res card.Result) int {
	if res.Code != 0 {
		fmt.Fprintf(stderr, "nova-sprint card: %s; run: nova-sprint help\n", oneline.Escape(strings.TrimSpace(res.Stderr)))
		return res.Code
	}
	if _, err := io.WriteString(stdout, res.Stdout); err != nil {
		return refuse(stderr, "card", err.Error())
	}
	return 0
}
