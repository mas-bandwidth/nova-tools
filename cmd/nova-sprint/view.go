// Package main implements the nova-sprint view command.
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Pinger interface for store round-trip measurement.
type Pinger interface {
	Ping(context.Context) (time.Duration, error)
}

// storePinger implements Pinger using the store's ping method.
type storePinger struct {
	st *store.Store
}

func (p *storePinger) Ping(ctx context.Context) (time.Duration, error) {
	return p.st.Ping(ctx)
}

// fakePinger returns a fixed RTT.
type fakePinger struct {
	rtt time.Duration
}

func (p *fakePinger) Ping(_ context.Context) (time.Duration, error) {
	return p.rtt, nil
}

// view measures store RTT and displays the median.
func view(ctx context.Context, p Pinger, threshold time.Duration) (string, time.Duration, error) {
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

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	// Simple sort
	for i := 0; i < len(d); i++ {
		for j := i + 1; j < len(d); j++ {
			if d[i] > d[j] {
				d[i], d[j] = d[j], d[i]
			}
		}
	}
	return d[len(d)/2]
}

// Main is the entry point for the view command.
func Main(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	stress := fs.Bool("stress", false, "run in stress mode")
	_ = fs.Parse(args)

	if *stress {
		// For testing with injected pinger
		p := &fakePinger{rtt: 9 * time.Millisecond}
		msg, median, err := view(ctx, p, 5*time.Millisecond)
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
	msg, median, err := view(ctx, p, 5*time.Millisecond)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	fmt.Printf("Median: %d ms\n", median/time.Millisecond)

	return nil
}
