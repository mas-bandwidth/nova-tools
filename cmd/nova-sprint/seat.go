// Package main implements the nova-sprint seat command.
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// seat measures store round-trip time and shows alarm state.
func seat(ctx context.Context, p Pinger, threshold time.Duration) (string, time.Duration, error) {
	// Perform 20 pings
	rtts := make([]time.Duration, 0, 20)
	for i := 0; i < 20; i++ {
		rtt, err := p.Ping(ctx)
		if err != nil {
			return "", 0, err
		}
		rtts = append(rtts, rtt)
	}

	// Compute median
	median := median(rtts)

	// Check alarm state
	var msg string
	if median > threshold {
		msg = fmt.Sprintf("store is slow: %d ms", median/time.Millisecond)
	} else {
		msg = fmt.Sprintf("store RTT: %d ms", median/time.Millisecond)
	}

	return msg, median, nil
}

// Main is the entry point for the seat command.
func Main(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seat", flag.ContinueOnError)
	stress := fs.Bool("stress", false, "run in stress mode")
	_ = fs.Parse(args)

	if *stress {
		// For testing with injected pinger
		p := &fakePinger{rtt: 9 * time.Millisecond}
		msg, median, err := seat(ctx, p, 5*time.Millisecond)
		if err != nil {
			return err
		}
		fmt.Println(msg)
		fmt.Printf("Median: %d ms\n", median/time.Millisecond)
		return nil
	}

	// Regular mode: use real store
	st, err := store.Open(ctx)
	if err != nil {
		return err
	}

	p := &storePinger{st: st}
	msg, median, err := seat(ctx, p, 5*time.Millisecond)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	fmt.Printf("Median: %d ms\n", median/time.Millisecond)

	return nil
}
