//go:build darwin

// The darwin side of the stuck verb: the three read-only probes it runs outside
// the wall, each through pkg/subproc's bounded context so none of them can hold
// the diagnostic up.
package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// stuckProbeTimeout bounds each read-only probe. `sample` and `log show` both
// open real archives; a diagnostic that hangs longer than the launch it explains
// is not worth having.
const stuckProbeTimeout = 15 * time.Second

// psText is the process table, in the six columns parsePS reads.
func psText() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), stuckProbeTimeout)
	defer cancel()
	out, err := subproc.Context(ctx, "/bin/ps", "-Ao", "pid=,ppid=,etime=,time=,state=,command=").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// sampleText is /usr/bin/sample's call graph for one pid.
func sampleText(pid int) string {
	if pid == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), stuckProbeTimeout)
	defer cancel()
	out, err := subproc.Context(ctx, "/usr/bin/sample", strconv.Itoa(pid), "2").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// logText is the unified log's sandbox denials for the window.
func logText(window time.Duration) string {
	if window <= 0 {
		window = stuckWindow
	}
	ctx, cancel := context.WithTimeout(context.Background(), stuckProbeTimeout)
	defer cancel()
	predicate := `sender == "Sandbox" OR subsystem == "com.apple.sandbox"`
	out, err := subproc.Context(ctx, "/usr/bin/log", "show",
		"--last", strconv.Itoa(int(window.Seconds()))+"s",
		"--predicate", predicate, "--style", "compact").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// swVersText is the host's macOS product version, so a healthy host and the
// bench host compare on one line.
func swVersText() string {
	ctx, cancel := context.WithTimeout(context.Background(), stuckProbeTimeout)
	defer cancel()
	out, err := subproc.Context(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
