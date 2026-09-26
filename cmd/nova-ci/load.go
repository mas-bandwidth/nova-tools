package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
)

// hostLoad reads this host's load average for the slowtests load gate: the
// larger of the 1- and 5-minute figures, so a leg of up to two minutes is
// judged by the busiest reading that covers it, over runtime.NumCPU (the
// machine's logical CPUs, not the leg's GOMAXPROCS).
func hostLoad() slowtests.Load {
	return loadFrom(runtime.GOOS, runtime.NumCPU(), readLoadAvg)
}

// readLoadAvg is the one read of the host: darwin answers `sysctl -n
// vm.loadavg` ("{ 17.36 21.31 19.56 }"), Linux /proc/loadavg ("0.52 0.58 0.59
// 1/467 12345"); any other host has no load average.
func readLoadAvg(goos string) (string, error) {
	switch goos {
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return "", fmt.Errorf("sysctl -n vm.loadavg: %w", err)
		}
		return string(out), nil
	case "linux":
		out, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	return "", fmt.Errorf("%s has no load average", goos)
}

// loadFrom is hostLoad with the read handed in: a read that fails, or a
// figure that does not parse, is Known=false with the reason, and the gate
// then enforces no time budget (slowtests.Load.Enforced).
func loadFrom(goos string, cpus int, read func(goos string) (string, error)) slowtests.Load {
	load := slowtests.Load{CPUs: cpus}
	raw, err := read(goos)
	if err != nil {
		load.Why = err.Error()
		return load
	}
	avg, err := parseLoadAvg(raw)
	if err != nil {
		load.Why = err.Error()
		return load
	}
	load.Avg, load.Known = avg, true
	return load
}

// parseLoadAvg reads the first two figures of a sysctl vm.loadavg or
// /proc/loadavg line (braces ignored) and returns the larger.
func parseLoadAvg(raw string) (float64, error) {
	fields := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(raw))
	if len(fields) < 2 {
		return 0, fmt.Errorf("load average %q has fewer than two figures", strings.TrimSpace(raw))
	}
	var max float64
	for _, f := range fields[:2] {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("load average %q: %q is not a load", strings.TrimSpace(raw), f)
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}
