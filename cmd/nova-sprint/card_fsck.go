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
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ONE PLACE (nova-tools#3692): card fsck, card ls --unplaced and bench
// reindex read and check the card model of
// internal/nsprint/fn/lua/02_card_move.lua (the one move primitive).

// cmdCardFsck: card fsck --sprint <S> --redis <addr> [--repair]. One
// function call walks the sprint's cards both ways; exit 0 clean (or every
// drift repaired), 1 drift left (the remedy is --repair), 2 usage or Redis.
func cmdCardFsck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card fsck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	repair := fs.Bool("repair", false, "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "card", "fsck needs --sprint <name> and --redis <addr> [--repair]")
	}
	return runFsck(ctx, *addr, *sprint, *repair, "CARD FSCK", "card fsck --sprint "+*sprint+" --repair", stdout, stderr)
}

// runBenchReindex: bench reindex --sprint <S> --redis <addr>. The one-time
// rebuild: every record the sprint's state indexes, waiting set and pool name
// is adopted into sprint:<S>:cards with its one place and every view,
// including bench:<b>:cards:*, and every stray link is removed.
func runBenchReindex(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench reindex", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || fs.NArg() > 0 {
		return refuse(stderr, "bench", "reindex needs --sprint <name> and --redis <addr>")
	}
	return runFsck(ctx, *addr, *sprint, true, "BENCH REINDEX", "card fsck --sprint "+*sprint, stdout, stderr)
}

func runFsck(ctx context.Context, addr, sprint string, repair bool, verb, remedy string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	rep, err := card.Fsck(ctx, client, sprint, repair)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	for _, l := range rep.Lines {
		fmt.Fprintf(stderr, "DRIFT %s\n", oneline.Escape(l))
	}
	fmt.Fprintln(stdout, rep.Line(verb))
	if !rep.Clean() {
		fmt.Fprintf(stderr, "nova-sprint card: %d drift left; run: nova-sprint %s\n", rep.Drift-rep.Fixed, remedy)
		return 1
	}
	return 0
}

// cmdCardLs: card ls --unplaced --sprint <S> --redis <addr>: the null cards
// (where empty, in no table set), oldest first, then the receipt line.
func cmdCardLs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("card ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sprint := fs.String("sprint", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	unplaced := fs.Bool("unplaced", false, "")
	if err := fs.Parse(args); err != nil || *sprint == "" || *addr == "" || !*unplaced || fs.NArg() > 0 {
		return refuse(stderr, "card", "ls needs --unplaced --sprint <name> --redis <addr>")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, code := openCardRedis(ctx, *addr, stderr)
	if code != 0 {
		return code
	}
	defer client.Close()
	ids, err := card.Unplaced(ctx, client, *sprint)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	if len(ids) > 0 {
		fmt.Fprintln(stdout, strings.Join(ids, "\n"))
	}
	fmt.Fprintf(stdout, "CARD LS sprint=%s unplaced=%d\n", *sprint, len(ids))
	return 0
}
