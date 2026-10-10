// Command nova-friend manages friend's row working set and live lanes.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "beat":
		handleBeat(os.Args[2:])
	case "status":
		handleStatus(os.Args[2:])
	case "lanes":
		handleLanes(os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`nova-friend: manage friend's row working set and live lanes

Usage:
  nova-friend beat --lanes <id,...>  Report live lanes in a beat
  nova-friend status <friend>        Check friend's row working set
  nova-friend lanes <friend>         List friend's live lanes

Live lanes are the cards a friend is currently working on.
The server enforces that row working set equals live lanes.`)
}

func handleBeat(args []string) {
	fs := flag.NewFlagSet("beat", flag.ExitOnError)
	lanesStr := fs.String("lanes", "", "Comma-separated list of live lane card ids")
	fs.Parse(args)

	if *lanesStr == "" {
		fmt.Println("ERROR: --lanes is required")
		os.Exit(1)
	}

	// Parse lane IDs
	laneIDs := strings.Split(*lanesStr, ",")
	if len(laneIDs) == 0 {
		fmt.Println("ERROR: --lanes cannot be empty")
		os.Exit(1)
	}

	// In production, this would call UpdateLiveLanes
	fmt.Printf("Beat recorded with %d live lanes: %s\n", len(laneIDs), *lanesStr)
}

func handleStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Println("ERROR: friend name required")
		os.Exit(1)
	}

	friend := fs.Arg(0)
	if friend == "" {
		fmt.Println("ERROR: friend name cannot be empty")
		os.Exit(1)
	}

	// In production, this would call ValidateRowEqualsLanes
	fmt.Printf("Checking status for friend: %s\n", friend)
}

func handleLanes(args []string) {
	fs := flag.NewFlagSet("lanes", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Println("ERROR: friend name required")
		os.Exit(1)
	}

	friend := fs.Arg(0)
	if friend == "" {
		fmt.Println("ERROR: friend name cannot be empty")
		os.Exit(1)
	}

	// In production, this would call GetLiveLanes
	fmt.Printf("Listing live lanes for friend: %s\n", friend)
}
