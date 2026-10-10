// Command nova-friend manages friend's row working set and live lanes.
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

	// Connect to store first
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
	case "status":
		handleStatus(st, os.Args[2:])
	case "lanes":
		handleLanes(st, os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`nova-friend: manage friend's row working set and live lanes

Usage:
  nova-friend beat --lanes <id,...> --friend <name>  Report live lanes in a beat
  nova-friend status <friend>                        Check friend's row working set
  nova-friend lanes <friend>                         List friend's live lanes

Live lanes are the cards a friend is currently working on.
The server enforces that row working set equals live lanes.`)
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

	// Parse lane IDs
	laneIDs := strings.Split(*lanesStr, ",")
	if len(laneIDs) == 0 {
		fmt.Println("ERROR: --lanes cannot be empty")
		os.Exit(1)
	}

	// Build LiveLane objects
	var lanes []friend.LiveLane
	for _, id := range laneIDs {
		lanes = append(lanes, friend.LiveLane{ID: id, Target: id})
	}

	// Update live lanes in Redis
	if err := friend.UpdateLiveLanes(context.Background(), st, *friendName, lanes); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to record beat: %v\n", err)
		os.Exit(1)
	}

	// Validate row equals lanes per spec RULE 3
	v := sprint.NewWorkingSetValidator(st)
	judgments, err := v.ValidateRowEqualsLanes(context.Background(), *friendName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: validation failed: %v\n", err)
		os.Exit(1)
	}

	// Expire stale cards per spec RULE 3
	stale, err := v.ExpireStaleCards(context.Background(), *friendName, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stale check failed: %v\n", err)
		os.Exit(1)
	}

	// Report judgment/stale findings from beat
	if len(stale) > 0 {
		fmt.Printf("Stale cards returned to ready: %v\n", stale)
	}
	for _, j := range judgments {
		fmt.Printf("JUDGMENT: %s\n", sprint.BuildJudgmentMessage(j))
	}

	fmt.Printf("Beat recorded for %s with %d live lanes: %s\n", *friendName, len(lanes), *lanesStr)
}

func handleStatus(st *store.Store, args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
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

	// Validate row equals lanes
	v := sprint.NewWorkingSetValidator(st)
	judgments, err := v.ValidateRowEqualsLanes(context.Background(), friendName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: validation failed: %v\n", err)
		os.Exit(1)
	}

	// Check for stale cards
	stale, err := v.ExpireStaleCards(context.Background(), friendName, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stale check failed: %v\n", err)
		os.Exit(1)
	}

	// Report results
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

func handleLanes(st *store.Store, args []string) {
	fs := flag.NewFlagSet("lanes", flag.ExitOnError)
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

	// Get live lanes from Redis
	lanes, err := friend.GetLiveLanes(context.Background(), st, friendName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to get live lanes: %v\n", err)
		os.Exit(1)
	}

	if len(lanes) == 0 {
		fmt.Printf("%s: no live lanes\n", friendName)
		return
	}

	ids := make([]string, len(lanes))
	for i, lane := range lanes {
		ids[i] = lane.ID
	}
	fmt.Printf("%s: %s\n", friendName, strings.Join(ids, ","))
}
