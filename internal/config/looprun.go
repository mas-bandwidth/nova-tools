package config

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatLoopMarker is the disk marker a loop run reads: free_gib and stop.
// stop is 1 when the floor says the loops should exit.
func FormatLoopMarker(freeBytes uint64, stop bool) string {
	bit := 0
	if stop {
		bit = 1
	}
	return fmt.Sprintf("free_gib=%d\nstop=%d\n", freeBytes/(1<<30), bit)
}

// LoopMarkerStops reports whether the marker text says stop=1. An empty
// text, or a missing file the caller turned into "", is not a stop.
func LoopMarkerStops(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == "stop" && strings.TrimSpace(v) == "1" {
			return true
		}
	}
	return false
}

// NextRestart is the next restart count. Empty text is the first run.
// A negative or non-integer text is an error.
func NextRestart(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("restart counter %q is not a non-negative integer", text)
	}
	return n + 1, nil
}

// RestartProm is the textfile a node exporter reads for one loop.
func RestartProm(name string, n int) string {
	return fmt.Sprintf("nova_loop_restarts_total{loop=%q} %d\n", name, n)
}
