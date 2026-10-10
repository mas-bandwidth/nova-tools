// Command nova-sprint enforces server-side friend row=lanes contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	ctx := context.Background()
	opts := redisconn.Options{
		Addr: os.Getenv("NOVA_REDIS_URL"),
	}
	if opts.Addr == "" {
		opts.Addr = "localhost:6379"
	}
	conn, err := redisconn.Open(ctx, opts, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to connect to Redis: %v\n", err)
		os.Exit(1)
	}
	st := store.New(conn.Client())

	switch os.Args[1] {
	case "beat":
		handleBeat(st, os.Args[2:])
	case "validate":
		handleValidate(st, os.Args[2:])
	case "expire":
		handleExpire(st, os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`nova-sprint: server-side enforcement of friend row=lanes contract

Usage:
  nova-sprint beat --lanes <id,...> --friend <name>  Process beat and enforce row=lanes
  nova-sprint validate <friend>                      Check friend's row working set vs lanes
  nova-sprint expire <friend>                        Clear stale taken cards past deadline

The server enforces:
1. Live lanes must be on cards taken on the friend's row
2. Taken cards without live lanes past deadline return to ready
3. Violations are recorded as judgments`)
}

func handleBeat(st *store.Store, args []string) {
	fs := flag.NewFlagSet("beat", flag.ExitOnError)
	lanesStr := fs.String("lanes", "", "Comma-separated list of live lane card ids")
	friendName := fs.String("friend", "", "Friend name")
	fs.Parse(args)

	if *lanesStr == "" {
		fmt.Println("ERROR: --lanes is required")
		os.Exit(1)
	}
	if *friendName == "" {
		fmt.Println("ERROR: --friend is required")
		os.Exit(1)
	}

	laneIDs := strings.Split(*lanesStr, ",")
	if len(laneIDs) == 0 {
		fmt.Println("ERROR: --lanes cannot be empty")
		os.Exit(1)
	}

	var lanes []friend.LiveLane
	for _, id := range laneIDs {
		lanes = append(lanes, friend.LiveLane{ID: id, Target: id})
	}

	if err := friend.UpdateLiveLanes(context.Background(), st, *friendName, lanes); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to record beat: %v\n", err)
		os.Exit(1)
	}

	v := sprint.NewWorkingSetValidator(st)
	judgments, err := v.ValidateRowEqualsLanes(context.Background(), *friendName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: validation failed: %v\n", err)
		os.Exit(1)
	}

	stale, err := v.ExpireStaleCards(context.Background(), *friendName, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stale check failed: %v\n", err)
		os.Exit(1)
	}

	// Report violations found in this beat
	if len(judgments) > 0 {
		for _, j := range judgments {
			fmt.Printf("JUDGMENT: %s\n", sprint.BuildJudgmentMessage(j))
		}
	}
	if len(stale) > 0 {
		fmt.Printf("Stale cards returned to ready: %v\n", stale)
	}

	fmt.Printf("Beat recorded for %s with %d live lanes\n", *friendName, len(lanes))
}

func handleValidate(st *store.Store, args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Println("ERROR: friend name required")
		os.Exit(1)
	}

	friendName := fs.Arg(0)
	if friendName == "" {
		fmt.Println("ERROR: friend name cannot be empty")
		os.Exit(1)
	}

	v := sprint.NewWorkingSetValidator(st)
	judgments, err := v.ValidateRowEqualsLanes(context.Background(), friendName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: validation failed: %v\n", err)
		os.Exit(1)
	}

	stale, err := v.ExpireStaleCards(context.Background(), friendName, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stale check failed: %v\n", err)
		os.Exit(1)
	}

	if len(judgments) == 0 && len(stale) == 0 {
		fmt.Printf("OK: friend %s row working set equals live lanes\n", friendName)
	} else {
		if len(stale) > 0 {
			fmt.Printf("Stale cards returned to ready: %v\n", stale)
		}
		for _, j := range judgments {
			fmt.Printf("JUDGMENT: %s\n", sprint.BuildJudgmentMessage(j))
		}
	}
}

func handleExpire(st *store.Store, args []string) {
	fs := flag.NewFlagSet("expire", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Println("ERROR: friend name required")
		os.Exit(1)
	}

	friendName := fs.Arg(0)
	if friendName == "" {
		fmt.Println("ERROR: friend name cannot be empty")
		os.Exit(1)
	}

	v := sprint.NewWorkingSetValidator(st)
	stale, err := v.ExpireStaleCards(context.Background(), friendName, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to expire stale cards: %v\n", err)
		os.Exit(1)
	}

	if len(stale) == 0 {
		fmt.Printf("No stale cards for friend %s\n", friendName)
	} else {
		fmt.Printf("Expired stale cards for %s: %v\n", friendName, stale)
	}
}
