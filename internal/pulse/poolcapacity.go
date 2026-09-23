package pulse

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// BenchCapacity holds capacity, resource measurements, and in-flight counts for a single bench.
type BenchCapacity struct {
	Name       string  `json:"name"`
	Slots      int     `json:"slots"`       // total configured capacity in slots
	SlotsTotal int     `json:"slots_total"` // total slots
	SlotsUsed  int     `json:"slots_used"`  // active/leased slots
	SlotsFree  int     `json:"slots_free"`  // available slots
	MemoryGB   float64 `json:"memory_gb"`   // available/free memory in GB
	MemTotalGB float64 `json:"mem_total_gb"`
	Load       float64 `json:"load"`      // 1-minute load average
	Cores      int     `json:"cores"`     // number of CPU cores
	InFlight   int     `json:"in_flight"` // active jobs running on this bench
	Headroom   int     `json:"headroom"`  // rate-limit headroom
}

// PoolCapacitySummary holds the aggregated pool capacity metrics across all benches.
type PoolCapacitySummary struct {
	Timestamp        time.Time       `json:"timestamp"`
	Benches          int             `json:"benches"`
	Slots            int             `json:"slots"` // total aggregate slots
	SlotsTotal       int             `json:"slots_total"`
	SlotsUsed        int             `json:"slots_used"`
	SlotsFree        int             `json:"slots_free"`
	MemoryGB         float64         `json:"memory_gb"`         // aggregate memory in GB
	Load             float64         `json:"load"`              // sum of load across benches
	LoadAvg          float64         `json:"load_avg"`          // average load per bench
	InFlight         int             `json:"in_flight"`         // total active in-flight jobs
	ProviderHeadroom int             `json:"provider_headroom"` // provider rate-limit headroom
	Details          []BenchCapacity `json:"details,omitempty"`
}

// PoolCapacityInput holds inputs and options for the PoolCapacity command.
type PoolCapacityInput struct {
	MachinesPath  string           // path to machines registry (machines.tsv)
	BenchesPath   string           // path to benches file (benches.tsv)
	Roots         string           // comma-separated bench swarm roots
	QueueDir      string           // queue directory (for in-flight cards in launched/)
	ProvidersPath string           // path to providers registry file
	MetricsPath   string           // output file for metrics.tsv
	Headroom      int              // explicit provider rate-limit headroom override (-1 for auto)
	AppendMetrics bool             // true = append row, false = overwrite
	Benches       []BenchCapacity  // direct bench list (for unit tests / programmatic calls)
	Now           func() time.Time // clock seam
	Stdout        io.Writer
	Stderr        io.Writer
}

// AggregatePoolCapacity computes fleet-wide totals across all benches.
func AggregatePoolCapacity(benches []BenchCapacity, providerHeadroom int, now time.Time) PoolCapacitySummary {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	summary := PoolCapacitySummary{
		Timestamp:        now.UTC(),
		Benches:          len(benches),
		ProviderHeadroom: providerHeadroom,
		Details:          benches,
	}

	for _, b := range benches {
		slotsTotal := b.SlotsTotal
		if slotsTotal == 0 && b.Slots > 0 {
			slotsTotal = b.Slots
		}
		slotsUsed := b.SlotsUsed
		slotsFree := b.SlotsFree
		if slotsFree == 0 && slotsTotal > slotsUsed {
			slotsFree = slotsTotal - slotsUsed
		} else if slotsTotal == 0 && slotsFree > 0 {
			slotsTotal = slotsFree + slotsUsed
		}

		summary.SlotsTotal += slotsTotal
		summary.SlotsUsed += slotsUsed
		summary.SlotsFree += slotsFree
		summary.Slots += slotsTotal

		mem := b.MemoryGB
		if mem == 0 && b.MemTotalGB > 0 {
			mem = b.MemTotalGB
		}
		summary.MemoryGB += mem
		summary.Load += b.Load
		summary.InFlight += b.InFlight
	}

	if len(benches) > 0 {
		summary.LoadAvg = summary.Load / float64(len(benches))
	}
	return summary
}

// FormatMetricsRow formats a PoolCapacitySummary into a 6-column TSV row:
// <RFC3339>\t<slots>\t<memory>\t<load>\t<in_flight>\t<headroom>\n
func FormatMetricsRow(summary PoolCapacitySummary) string {
	memStr := fmt.Sprintf("%.2f", summary.MemoryGB)
	if summary.MemoryGB == float64(int64(summary.MemoryGB)) {
		memStr = fmt.Sprintf("%d", int64(summary.MemoryGB))
	}

	loadStr := fmt.Sprintf("%.2f", summary.Load)

	return fmt.Sprintf("%s\t%d\t%s\t%s\t%d\t%d\n",
		summary.Timestamp.UTC().Format(time.RFC3339),
		summary.Slots,
		memStr,
		loadStr,
		summary.InFlight,
		summary.ProviderHeadroom,
	)
}

// WriteMetricsTSVAtomic atomically writes or appends the metrics row into metrics.tsv
// using an flock on <destPath>.lock, an O_EXCL tempfile, and an atomic filesystem rename.
func WriteMetricsTSVAtomic(destPath string, row string, appendMode bool) error {
	if strings.TrimSpace(destPath) == "" {
		return fmt.Errorf("metrics path cannot be empty")
	}
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	lockPath := filepath.Clean(destPath) + ".lock"
	unlock, err := bus.LockFile(lockPath, 10*time.Second)
	if err != nil {
		return fmt.Errorf("acquiring lock on %s: %w", lockPath, err)
	}
	defer unlock()

	var existing []byte
	if appendMode {
		if data, err := os.ReadFile(destPath); err == nil {
			existing = data
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("reading existing metrics file %s: %w", destPath, err)
		}
	}

	var buf bytes.Buffer
	buf.Write(existing)
	buf.WriteString(row)

	base := filepath.Base(destPath)
	var randBytes [6]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return fmt.Errorf("reading random bytes: %w", err)
	}
	randHex := hex.EncodeToString(randBytes[:])
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.%d-%s.tmp", base, os.Getpid(), randHex))

	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("creating atomic temp file %s: %w", tmpPath, err)
	}

	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing to atomic temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing atomic temp file %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpPath, destPath, err)
	}
	cleaned = true
	return nil
}

// ReadProvidersHeadroom parses a provider registry or routes TSV and sums the concurrency limits.
func ReadProvidersHeadroom(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("cannot read provider file %s: %w", path, err)
	}

	totalConcurrency := 0
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		// Skip header if present
		if strings.EqualFold(fields[0], "route") || strings.EqualFold(fields[0], "provider") {
			continue
		}

		concurrencyVal := 0
		if len(fields) >= 5 {
			// Standard provider registry format: route, provider, model, tier, concurrency, ...
			if c, err := strconv.Atoi(strings.TrimSpace(fields[4])); err == nil && c >= 0 {
				concurrencyVal = c
			}
		} else if len(fields) >= 2 {
			// Compact 2-column format: route<TAB>concurrency or provider<TAB>concurrency
			if c, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil && c >= 0 {
				concurrencyVal = c
			}
		}

		totalConcurrency += concurrencyVal
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("reading provider file %s: %w", path, err)
	}
	return totalConcurrency, nil
}

// ReadRootsCapacity reads bench capacity and in-flight slots across comma-separated root directories.
func ReadRootsCapacity(rootsStr string) ([]BenchCapacity, error) {
	var benches []BenchCapacity
	for _, root := range strings.Split(rootsStr, ",") {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}

		name := filepath.Base(root)
		running, totalSlots := slotCounts(root)
		freeSlots := totalSlots - running
		if freeSlots < 0 {
			freeSlots = 0
		}

		bc := BenchCapacity{
			Name:       name,
			Slots:      totalSlots,
			SlotsTotal: totalSlots,
			SlotsUsed:  running,
			SlotsFree:  freeSlots,
			InFlight:   running,
		}

		// Read capacity file if present (<root>/capacity or <root>/pool/capacity)
		capFiles := []string{
			filepath.Join(root, "capacity"),
			filepath.Join(root, "pool", "capacity"),
		}
		for _, capFile := range capFiles {
			if data, err := os.ReadFile(capFile); err == nil {
				parseBenchCapacityFile(string(data), &bc)
				break
			}
		}

		benches = append(benches, bc)
	}
	return benches, nil
}

// parseBenchCapacityFile parses optional capacity metrics: cores=<n> load=<f> mem_gb=<f> etc.
func parseBenchCapacityFile(content string, bc *BenchCapacity) {
	fields := strings.Fields(content)
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "cores":
			if n, err := strconv.Atoi(v); err == nil {
				bc.Cores = n
			}
		case "load", "load1":
			if l, err := strconv.ParseFloat(v, 64); err == nil {
				bc.Load = l
			}
		case "mem", "mem_gb", "memory_gb", "free_mem":
			if m, err := strconv.ParseFloat(v, 64); err == nil {
				bc.MemoryGB = m
			}
		case "slots":
			if s, err := strconv.Atoi(v); err == nil && bc.SlotsTotal == 0 {
				bc.Slots = s
				bc.SlotsTotal = s
				bc.SlotsFree = s - bc.SlotsUsed
			}
		case "in_flight", "running":
			if j, err := strconv.Atoi(v); err == nil {
				bc.InFlight = j
				bc.SlotsUsed = j
				if bc.SlotsTotal > 0 {
					bc.SlotsFree = bc.SlotsTotal - j
				}
			}
		}
	}
}

// ReadQueueInFlight counts in-flight cards under <queue>/launched.
func ReadQueueInFlight(queueDir string) (int, error) {
	launchedDir := filepath.Join(queueDir, "launched")
	entries, err := os.ReadDir(launchedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		count++
	}
	return count, nil
}

// ReadMachinesCapacity reads bench entries from the machines registry.
func ReadMachinesCapacity(machinesPath string) ([]BenchCapacity, error) {
	reg, err := fleet.ReadRegistry(machinesPath)
	if err != nil {
		return nil, fmt.Errorf("reading machines registry %s: %w", machinesPath, err)
	}

	var benches []BenchCapacity
	for _, m := range reg.Machines() {
		if !m.HasRole("bench") {
			continue
		}
		bc := BenchCapacity{
			Name:       m.Name,
			Cores:      m.Cores,
			Slots:      m.Cores, // default slots to cores when not otherwise specified
			SlotsTotal: m.Cores,
			SlotsFree:  m.Cores,
		}
		benches = append(benches, bc)
	}
	return benches, nil
}

// ReadBenchesCapacity reads bench names from a legacy benches.tsv file. The benches file
// carries only name, ssh target, home and mac: no slots, memory, load or in-flight count,
// and pool-capacity runs no remote probe. A bench named only here therefore has no
// capacity source, and PoolCapacity refuses it rather than report it as zero capacity.
func ReadBenchesCapacity(benchesPath string) ([]BenchCapacity, error) {
	benchesMap, err := ReadFleetBenches(benchesPath)
	if err != nil {
		return nil, fmt.Errorf("reading benches file %s: %w", benchesPath, err)
	}

	names := make([]string, 0, len(benchesMap))
	for name := range benchesMap {
		names = append(names, name)
	}
	sort.Strings(names)
	benches := make([]BenchCapacity, 0, len(names))
	for _, name := range names {
		benches = append(benches, BenchCapacity{Name: name})
	}
	return benches, nil
}

// PoolCapacity executes the PULSE POOL-CAPACITY reporting workflow:
// aggregates slots, memory, load, in-flight jobs, and provider rate-limit headroom,
// atomically writes metrics.tsv via O_EXCL tempfile rename, and prints the summary line.
func PoolCapacity(in PoolCapacityInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}

	if in.Headroom < -1 {
		fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED headroom=%d: want -1 (auto) or a count >= 0\n", in.Headroom)
		return 2
	}

	var benches []BenchCapacity
	if len(in.Benches) > 0 {
		benches = append(benches, in.Benches...)
	} else {
		// Discover from roots, machines, or benches
		if in.Roots != "" {
			rb, err := ReadRootsCapacity(in.Roots)
			if err != nil {
				fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED roots=%s: %s\n", oneline.Field(in.Roots), oneline.Err(err))
				return 2
			}
			benches = append(benches, rb...)
		}

		if in.MachinesPath != "" {
			mb, err := ReadMachinesCapacity(in.MachinesPath)
			if err != nil {
				fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED machines=%s: %s\n", oneline.Field(in.MachinesPath), oneline.Err(err))
				return 2
			}
			// Merge machines not already in benches
			existing := make(map[string]bool)
			for _, b := range benches {
				existing[b.Name] = true
			}
			for _, b := range mb {
				if !existing[b.Name] {
					benches = append(benches, b)
					existing[b.Name] = true
				}
			}
		}

		if in.BenchesPath != "" {
			bb, err := ReadBenchesCapacity(in.BenchesPath)
			if err != nil {
				fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED benches=%s: %s\n", oneline.Field(in.BenchesPath), oneline.Err(err))
				return 2
			}
			// A benches-file line names a bench but measures nothing (no probe runs), so
			// every bench it names must already have a measured source from --roots or
			// --machines; an unmeasured bench is a refusal, never a zero-capacity row.
			existing := make(map[string]bool)
			for _, b := range benches {
				existing[b.Name] = true
			}
			var unmeasured []string
			for _, b := range bb {
				if !existing[b.Name] {
					unmeasured = append(unmeasured, b.Name)
				}
			}
			if len(unmeasured) > 0 {
				fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED benches=%s: no capacity source for %s (the benches file has no slots/memory/load and pool-capacity runs no remote probe; pass --roots or --machines covering them)\n",
					oneline.Field(in.BenchesPath), oneline.Field(strings.Join(unmeasured, ",")))
				return 2
			}
		}
	}

	if len(benches) == 0 {
		fmt.Fprintln(in.Stderr, "POOL-CAPACITY REFUSED: no benches specified (pass --roots, --machines, or --benches)")
		return 2
	}

	// Calculate in-flight jobs from queue if provided
	queueInFlight := 0
	if in.QueueDir != "" {
		qf, err := ReadQueueInFlight(in.QueueDir)
		if err != nil {
			fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED queue=%s: %s\n", oneline.Field(in.QueueDir), oneline.Err(err))
			return 2
		}
		queueInFlight = qf
	}

	// Calculate provider rate-limit headroom
	providerHeadroom := 0
	if in.ProvidersPath != "" {
		ph, err := ReadProvidersHeadroom(in.ProvidersPath)
		if err != nil {
			fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED providers=%s: %s\n", oneline.Field(in.ProvidersPath), oneline.Err(err))
			return 2
		}
		providerHeadroom = ph
	}
	// Headroom -1 is auto (the providers sum, else 0); any value >= 0 is an explicit
	// override, including 0, and wins over the providers file.
	if in.Headroom >= 0 {
		providerHeadroom = in.Headroom
	}

	summary := AggregatePoolCapacity(benches, providerHeadroom, in.Now())
	if queueInFlight > summary.InFlight {
		summary.InFlight = queueInFlight
	}

	metricsPath := in.MetricsPath
	if metricsPath == "" {
		if in.QueueDir != "" {
			metricsPath = filepath.Join(in.QueueDir, "metrics.tsv")
		} else {
			metricsPath = "metrics.tsv"
		}
	}

	row := FormatMetricsRow(summary)
	if err := WriteMetricsTSVAtomic(metricsPath, row, in.AppendMetrics); err != nil {
		fmt.Fprintf(in.Stderr, "POOL-CAPACITY REFUSED metrics=%s: %s\n", oneline.Field(metricsPath), oneline.Err(err))
		return 2
	}

	memStr := fmt.Sprintf("%.2f", summary.MemoryGB)
	if summary.MemoryGB == float64(int64(summary.MemoryGB)) {
		memStr = fmt.Sprintf("%d", int64(summary.MemoryGB))
	}

	fmt.Fprintf(in.Stdout, "PULSE POOL-CAPACITY benches=%d slots=%d memory=%s load=%.2f in_flight=%d headroom=%d metrics=%s\n",
		summary.Benches,
		summary.Slots,
		memStr,
		summary.Load,
		summary.InFlight,
		summary.ProviderHeadroom,
		oneline.Field(metricsPath),
	)

	return 0
}
