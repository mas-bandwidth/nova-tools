package main

import (
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// cmdPoolCapacity executes the pool-capacity verb:
// aggregates slots, memory, load, in-flight jobs, and provider rate-limit headroom
// across all benches into metrics.tsv atomically using an O_EXCL tempfile and rename.
func cmdPoolCapacity(args []string, stdout, stderr io.Writer) int {
	f := newFlags("pool-capacity")
	machines := f.fs.String("machines", "", "path to machines registry")
	benches := f.fs.String("benches", "", "path to benches file")
	roots := f.fs.String("roots", "", "comma-separated bench roots")
	queue := f.fs.String("queue", "", "queue directory")
	providers := f.fs.String("providers", "", "path to providers registry")
	headroom := f.fs.Int("headroom", -1, "explicit provider rate-limit headroom override")
	out := f.fs.String("out", "", "path to metrics.tsv")
	metrics := f.fs.String("metrics", "", "alias for --out")
	overwrite := f.fs.Bool("overwrite", false, "overwrite metrics.tsv instead of appending")
	timeout := f.fs.Int("timeout", 120, "bound on remote probe in seconds")

	if !f.parse(args, stderr) {
		return 2
	}

	metricsPath := strings.TrimSpace(*out)
	if metricsPath == "" {
		metricsPath = strings.TrimSpace(*metrics)
	}

	if f.refused(stderr) {
		return 2
	}

	_ = timeout

	return pulse.PoolCapacity(pulse.PoolCapacityInput{
		MachinesPath:  strings.TrimSpace(*machines),
		BenchesPath:   strings.TrimSpace(*benches),
		Roots:         strings.TrimSpace(*roots),
		QueueDir:      strings.TrimSpace(*queue),
		ProvidersPath: strings.TrimSpace(*providers),
		MetricsPath:   metricsPath,
		Headroom:      *headroom,
		AppendMetrics: !*overwrite,
		Now:           func() time.Time { return time.Now().UTC() },
		Stdout:        stdout,
		Stderr:        stderr,
	})
}

// CmdPoolCapacity is the exported entrypoint for testing and library usage.
func CmdPoolCapacity(args []string, stdout, stderr io.Writer) int {
	return cmdPoolCapacity(args, stdout, stderr)
}
