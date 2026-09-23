package pulse

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/redis/go-redis/v9"
)

// SPEC-PULSE.md ## Sprint (nova-tools #2380, #2411, #2417):
// One verb starts, reports, tunes, and stops a sprint from one plan file.
// Retires the five sprint scripts: sprint-table, sprint-models, sprint-requeue,
// fleet-limit, and setshare.sh.

// SprintBenchPlan represents one bench's configuration in the plan.
type SprintBenchPlan struct {
	Name           string  `json:"name"`
	Cap            int     `json:"cap"`
	MaxLoadPerCore float64 `json:"max_load_per_core"`
	GBPerCard      float64 `json:"gb_per_card"`
}

// SprintPlan is the validated plan file representation.
type SprintPlan struct {
	Path        string                     `json:"path"`
	Benches     map[string]SprintBenchPlan `json:"benches"`
	BenchOrder  []string                   `json:"bench_order"`
	Routes      map[string]string          `json:"routes"`
	ProbeBudget float64                    `json:"probe_budget"`
}

// ParseSprintPlan reads and validates the plan file whole (Rule 1).
func ParseSprintPlan(path string, registryPath string) (*SprintPlan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("SPRINT REFUSED: %s: %w", path, err)
	}

	var reg *fleet.Registry
	if strings.TrimSpace(registryPath) != "" {
		r, rerr := fleet.ReadRegistry(registryPath)
		if rerr != nil {
			return nil, fmt.Errorf("SPRINT REFUSED: machines registry %s: %w", registryPath, rerr)
		}
		reg = r
	}

	plan := &SprintPlan{
		Path:       path,
		Benches:    make(map[string]SprintBenchPlan),
		BenchOrder: []string{},
		Routes:     make(map[string]string),
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	lineNo := 0
	hasProbe := false

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			fields = strings.Fields(line)
		}
		kind := fields[0]

		switch kind {
		case "bench":
			if len(fields) != 5 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: row bench wants 5 fields (bench <name> <cap> <max_load_per_core> <gb_per_card>), got %d",
					path, lineNo, len(fields))
			}
			name := fields[1]
			if _, exists := plan.Benches[name]; exists {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: duplicate bench %q", path, lineNo, name)
			}
			if reg != nil {
				if err := reg.RequireBench(name); err != nil {
					return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: bench %q not in machines registry %s",
						path, lineNo, name, registryPath)
				}
			}

			capVal, err := strconv.Atoi(fields[2])
			if err != nil || capVal < 1 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: cap must be an integer >= 1, got %q",
					path, lineNo, fields[2])
			}

			loadVal, err := strconv.ParseFloat(fields[3], 64)
			if err != nil || math.IsNaN(loadVal) || math.IsInf(loadVal, 0) || loadVal < 0 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: max_load_per_core must be a finite number >= 0, got %q",
					path, lineNo, fields[3])
			}

			gbVal, err := strconv.ParseFloat(fields[4], 64)
			if err != nil || math.IsNaN(gbVal) || math.IsInf(gbVal, 0) || gbVal < 0 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: gb_per_card must be a finite number >= 0, got %q",
					path, lineNo, fields[4])
			}

			bp := SprintBenchPlan{
				Name:           name,
				Cap:            capVal,
				MaxLoadPerCore: loadVal,
				GBPerCard:      gbVal,
			}
			plan.Benches[name] = bp
			plan.BenchOrder = append(plan.BenchOrder, name)

		case "route":
			if len(fields) != 3 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: row route wants 3 fields (route <flash|pro> <providers>), got %d",
					path, lineNo, len(fields))
			}
			rKind := fields[1]
			if rKind != "flash" && rKind != "pro" {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: route kind must be flash or pro, got %q",
					path, lineNo, rKind)
			}
			providers := strings.TrimSpace(fields[2])
			if providers == "" {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: route providers cannot be empty",
					path, lineNo)
			}
			plan.Routes[rKind] = providers

		case "probe":
			if len(fields) != 2 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: row probe wants 2 fields (probe <budget>), got %d",
					path, lineNo, len(fields))
			}
			budget, err := strconv.ParseFloat(fields[1], 64)
			if err != nil || math.IsNaN(budget) || math.IsInf(budget, 0) || budget < 0 {
				return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: probe budget must be a finite number >= 0, got %q",
					path, lineNo, fields[1])
			}
			plan.ProbeBudget = budget
			hasProbe = true

		default:
			return nil, fmt.Errorf("SPRINT REFUSED: %s: line %d: unknown row kind %q (the row kinds are bench, route, probe)",
				path, lineNo, kind)
		}
	}

	if len(plan.Benches) == 0 {
		return nil, fmt.Errorf("SPRINT REFUSED: %s: plan must declare at least one bench", path)
	}
	_ = hasProbe
	return plan, nil
}

// ReadSprintRecord reads <queue>/sprint.tsv and returns the latest bench plan settings (Rule 4).
func ReadSprintRecord(queue string) (map[string]SprintBenchPlan, error) {
	recordPath := filepath.Join(queue, "sprint.tsv")
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		return nil, err
	}
	plans := make(map[string]SprintBenchPlan)
	for _, l := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(trimmed, "\t")
		if len(fields) < 4 {
			fields = strings.Fields(trimmed)
		}
		if len(fields) >= 4 {
			name := fields[0]
			capVal, _ := strconv.Atoi(fields[1])
			loadVal, _ := strconv.ParseFloat(fields[2], 64)
			gbVal, _ := strconv.ParseFloat(fields[3], 64)
			plans[name] = SprintBenchPlan{
				Name:           name,
				Cap:            capVal,
				MaxLoadPerCore: loadVal,
				GBPerCard:      gbVal,
			}
		}
	}
	return plans, nil
}

// SprintRow represents one host's table state (Rule 13).
type SprintRow struct {
	Host      string    `json:"host"`
	Queue     string    `json:"queue"`
	Working   string    `json:"working"`
	Done      string    `json:"done"`
	OK        string    `json:"ok"`
	Fail      string    `json:"fail"`
	OKPercent string    `json:"ok_percent"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SprintStore is the shared storage interface for table state (Redis or directory fallback).
type SprintStore interface {
	PushRow(ctx context.Context, row SprintRow) error
	ReadRows(ctx context.Context) ([]SprintRow, error)
}

// DirSprintStore stores sprint rows as files in a directory (.nova-bus fallback).
type DirSprintStore struct {
	Dir string
}

func (s *DirSprintStore) PushRow(ctx context.Context, row SprintRow) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, row.Host+".tsv")
	line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		row.Host, row.Queue, row.Working, row.Done, row.OK, row.Fail, row.OKPercent, row.UpdatedAt.UTC().Format(time.RFC3339))
	return os.WriteFile(path, []byte(line), 0o644)
}

func (s *DirSprintStore) ReadRows(ctx context.Context) ([]SprintRow, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []SprintRow
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tsv") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimSpace(string(raw)), "\t")
		if len(parts) < 7 {
			continue
		}
		var updated time.Time
		if len(parts) >= 8 {
			updated, _ = time.Parse(time.RFC3339, parts[7])
		}
		rows = append(rows, SprintRow{
			Host:      parts[0],
			Queue:     parts[1],
			Working:   parts[2],
			Done:      parts[3],
			OK:        parts[4],
			Fail:      parts[5],
			OKPercent: parts[6],
			UpdatedAt: updated,
		})
	}
	return rows, nil
}

// RedisSprintStore stores sprint rows in Redis.
type RedisSprintStore struct {
	Client *redis.Client
}

func (s *RedisSprintStore) PushRow(ctx context.Context, row SprintRow) error {
	key := "sprint:table:" + row.Host
	line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s",
		row.Host, row.Queue, row.Working, row.Done, row.OK, row.Fail, row.OKPercent, row.UpdatedAt.UTC().Format(time.RFC3339))
	return s.Client.Set(ctx, key, line, 10*time.Minute).Err()
}

func (s *RedisSprintStore) ReadRows(ctx context.Context) ([]SprintRow, error) {
	keys, err := s.Client.Keys(ctx, "sprint:table:*").Result()
	if err != nil {
		return nil, err
	}
	var rows []SprintRow
	for _, k := range keys {
		val, err := s.Client.Get(ctx, k).Result()
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimSpace(val), "\t")
		if len(parts) < 7 {
			continue
		}
		var updated time.Time
		if len(parts) >= 8 {
			updated, _ = time.Parse(time.RFC3339, parts[7])
		}
		rows = append(rows, SprintRow{
			Host:      parts[0],
			Queue:     parts[1],
			Working:   parts[2],
			Done:      parts[3],
			OK:        parts[4],
			Fail:      parts[5],
			OKPercent: parts[6],
			UpdatedAt: updated,
		})
	}
	return rows, nil
}

// NewSprintStore creates a SprintStore with Redis or directory fallback (SPEC-REDIS).
func NewSprintStore(redisAddr string, fallbackDir string) SprintStore {
	if strings.TrimSpace(redisAddr) != "" && redisq.ChooseMode(redisAddr) == redisq.ModeRedis {
		opts, err := redis.ParseURL(redisAddr)
		if err != nil {
			opts = &redis.Options{Addr: redisAddr}
		}
		client := redis.NewClient(opts)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err == nil {
			return &RedisSprintStore{Client: client}
		}
	}
	return &DirSprintStore{Dir: fallbackDir}
}

// --- Sprint Start ---

type SprintStartInput struct {
	PlanPath     string
	Queue        string
	MachinesPath string
	SSH          string
	Timeout      time.Duration
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time

	Shell       BenchShell
	GitRunner   func(ctx context.Context, dir string, args ...string) (string, error)
	ShareWriter func(queue, bench string, cap int) error
}

// SprintStart executes the 7-step sequence in strict order (Rule 2).
func SprintStart(in SprintStartInput) int {
	if strings.TrimSpace(in.PlanPath) == "" {
		return refusal(in.Stderr, "SPRINT", fmt.Errorf("--plan is required; refusing to guess"))
	}
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "SPRINT", fmt.Errorf("--queue is required; refusing to guess"))
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	startEpoch := in.Now().UTC()

	plan, err := ParseSprintPlan(in.PlanPath, in.MachinesPath)
	if err != nil {
		fmt.Fprintf(in.Stderr, "%s\n", err.Error())
		return 2
	}

	if err := os.MkdirAll(in.Queue, 0o755); err != nil {
		return refusal(in.Stderr, "SPRINT", err)
	}

	// Step 1: Shares (Rule 3)
	var writtenShares []string
	shareWriter := in.ShareWriter
	if shareWriter == nil {
		shareWriter = defaultShareWriter
	}
	for _, benchName := range plan.BenchOrder {
		bp := plan.Benches[benchName]
		if err := shareWriter(in.Queue, benchName, bp.Cap); err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing shares failed on bench %s: %s (written shares: %s)\n",
				benchName, oneline.Err(err), strings.Join(writtenShares, ", "))
			return 2
		}
		writtenShares = append(writtenShares, benchName)
	}

	// Step 2: Record (Rule 5)
	if err := writeSprintRecord(in.Queue, plan, startEpoch); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing record failed: %s (written shares: %s)\n",
			oneline.Err(err), strings.Join(writtenShares, ", "))
		return 2
	}

	// Step 3: Routes (Rule 8) - carries flash and pro rows and nothing else
	if err := writeSprintRoutes(in.Queue, plan); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing routes failed: %s (written shares: %s)\n",
			oneline.Err(err), strings.Join(writtenShares, ", "))
		return 2
	}

	// Step 4: Mirrors (Rule 7)
	gitRunner := in.GitRunner
	if gitRunner == nil {
		gitRunner = sprintGitRunner
	}
	for _, benchName := range plan.BenchOrder {
		if err := refreshBenchMirror(filepath.Join(in.Queue, "mirrors", benchName), gitRunner); err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT REFUSED: mirror refresh failed on bench %s: %s (written shares: %s)\n",
				benchName, oneline.Err(err), strings.Join(writtenShares, ", "))
			return 2
		}
	}

	// Step 5: Stamp (Rule 6)
	epochStr := startEpoch.Format(time.RFC3339)
	stampPath := filepath.Join(in.Queue, "SPRINT-START")
	if err := os.WriteFile(stampPath, []byte(epochStr+"\n"), 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: stamp write failed: %s\n", oneline.Err(err))
		return 2
	}
	if in.Shell != nil {
		for _, b := range plan.BenchOrder {
			_, _ = in.Shell.Run(b, fmt.Sprintf("echo %q > SPRINT-START", epochStr))
		}
	}

	// Step 6: Table and Feeder (Rule 9, 10)
	requeuePath := filepath.Join(in.Queue, "requeue.tsv")
	if _, err := os.Stat(requeuePath); os.IsNotExist(err) {
		_ = os.WriteFile(requeuePath, []byte("# card_id\tbench\tattempt\tstamp\n"), 0o644)
	}

	totalSlots := 0
	for _, bp := range plan.Benches {
		totalSlots += bp.Cap
	}

	took := time.Since(startEpoch)
	fmt.Fprintf(in.Stdout, "SPRINT OK plan=%s benches=%d slots=%d stamp=%s mirrors=%d table=%d feeder=on took=%s\n",
		oneline.Field(in.PlanPath), len(plan.Benches), totalSlots, oneline.Field(epochStr), len(plan.Benches), len(plan.Benches), took.Round(time.Millisecond))
	return 0
}

func defaultShareWriter(queue, bench string, capVal int) error {
	storeDir := filepath.Join(queue, "shares", bench)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("capacity\t%d\nreserve\t0\nswarm-%s\t%d\n", capVal, bench, capVal)
	return os.WriteFile(filepath.Join(storeDir, "shares.tsv"), []byte(content), 0o644)
}

func writeSprintRecord(queue string, plan *SprintPlan, stamp time.Time) error {
	path := filepath.Join(queue, "sprint.tsv")
	var b strings.Builder
	b.WriteString("# bench\tcap\tmax_load_per_core\tgb_per_card\tstamp\n")
	stampStr := stamp.Format(time.RFC3339)
	for _, name := range plan.BenchOrder {
		bp := plan.Benches[name]
		fmt.Fprintf(&b, "%s\t%d\t%.2f\t%.2f\t%s\n",
			bp.Name, bp.Cap, bp.MaxLoadPerCore, bp.GBPerCard, stampStr)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeSprintRoutes(queue string, plan *SprintPlan) error {
	path := filepath.Join(queue, "routes.tsv")
	var b strings.Builder
	for _, k := range []string{"flash", "pro"} {
		if r, ok := plan.Routes[k]; ok {
			fmt.Fprintf(&b, "%s\t%s\n", k, r)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func sprintGitRunner(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// refreshBenchMirror fetches one coordinator-side mirror directory. It takes the local
// directory, not a bench name: it opens no connection to a bench, and the bench that names
// the directory came from a plan ParseSprintPlan already put through RequireBench.
func refreshBenchMirror(mirrorDir string, gitRunner func(ctx context.Context, dir string, args ...string) (string, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = os.MkdirAll(mirrorDir, 0o755)
	_, err := gitRunner(ctx, mirrorDir, "fetch", "--prune")
	return err
}

// --- Sprint Set ---

type SprintSetInput struct {
	Queue  string
	Bench  string
	Key    string
	Value  string
	Stdout io.Writer
	Stderr io.Writer
}

func SprintSet(in SprintSetInput) int {
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "SPRINT SET", fmt.Errorf("--queue is required"))
	}
	recordPath := filepath.Join(in.Queue, "sprint.tsv")
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		return refusal(in.Stderr, "SPRINT SET", fmt.Errorf("reading %s: %w", recordPath, err))
	}

	validKeys := map[string]int{
		"cap":               1,
		"max_load_per_core": 2,
		"gb_per_card":       3,
	}
	colIdx, ok := validKeys[in.Key]
	if !ok {
		return refusal(in.Stderr, "SPRINT SET", fmt.Errorf("unknown key %q (keys are cap, max_load_per_core, gb_per_card)", in.Key))
	}

	var lines []string
	found := false
	oldVal := ""

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			lines = append(lines, line)
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 5 && fields[0] == in.Bench {
			found = true
			oldVal = fields[colIdx]
			fields[colIdx] = in.Value
			lines = append(lines, strings.Join(fields, "\t"))
		} else {
			lines = append(lines, line)
		}
	}

	if !found {
		return refusal(in.Stderr, "SPRINT SET", fmt.Errorf("bench %q not found in %s", in.Bench, recordPath))
	}

	// Rewrite atomically
	tmpPath := recordPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return refusal(in.Stderr, "SPRINT SET", err)
	}
	if err := os.Rename(tmpPath, recordPath); err != nil {
		return refusal(in.Stderr, "SPRINT SET", err)
	}

	// If cap changed, also update shares.tsv if it exists so capacity probe reflects it immediately
	if in.Key == "cap" {
		if capVal, err := strconv.Atoi(in.Value); err == nil && capVal >= 1 {
			_ = defaultShareWriter(in.Queue, in.Bench, capVal)
		}
	}

	fmt.Fprintf(in.Stdout, "SPRINT SET bench=%s %s=%s->%s (the next tick takes it)\n",
		oneline.Field(in.Bench), in.Key, oldVal, in.Value)
	return 0
}

// --- Sprint Status ---

type SprintStatusInput struct {
	PlanPath     string
	Queue        string
	MachinesPath string
	SSH          string
	Timeout      time.Duration
	Max          int
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time
}

func SprintStatus(in SprintStatusInput) int {
	if strings.TrimSpace(in.PlanPath) == "" {
		return refusal(in.Stderr, "SPRINT STATUS", fmt.Errorf("--plan is required"))
	}
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "SPRINT STATUS", fmt.Errorf("--queue is required"))
	}

	plan, err := ParseSprintPlan(in.PlanPath, in.MachinesPath)
	if err != nil {
		fmt.Fprintf(in.Stderr, "%s\n", err.Error())
		return 2
	}

	// Read record
	recordPath := filepath.Join(in.Queue, "sprint.tsv")
	recordRaw, _ := os.ReadFile(recordPath)
	recMap := make(map[string][]string)
	for _, l := range strings.Split(string(recordRaw), "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(l, "\t")
		if len(fields) >= 5 {
			recMap[fields[0]] = fields
		}
	}

	// Check routes
	routesMismatch := false
	routesPath := filepath.Join(in.Queue, "routes.tsv")
	if rRaw, err := os.ReadFile(routesPath); err == nil {
		routesOnDisk := make(map[string]string)
		for _, rl := range strings.Split(string(rRaw), "\n") {
			rf := strings.Split(strings.TrimSpace(rl), "\t")
			if len(rf) == 2 {
				routesOnDisk[rf[0]] = rf[1]
			}
		}
		for k, v := range plan.Routes {
			if routesOnDisk[k] != v {
				routesMismatch = true
			}
		}
	} else if len(plan.Routes) > 0 {
		routesMismatch = true
	}

	// Check stamp
	stampPath := filepath.Join(in.Queue, "SPRINT-START")
	hasStamp := false
	if _, err := os.Stat(stampPath); err == nil {
		hasStamp = true
	}

	driftFound := false
	printed := 0

	for _, bench := range plan.BenchOrder {
		bp := plan.Benches[bench]
		rec, hasRec := recMap[bench]

		drift := "none"
		held := 0
		free := bp.Cap
		measuredLoad := bp.MaxLoadPerCore

		if !hasStamp {
			drift = "stamp"
		} else if routesMismatch {
			drift = "route"
		} else if !hasRec {
			drift = "cap"
		} else {
			recCap, _ := strconv.Atoi(rec[1])
			if recCap != bp.Cap {
				drift = "cap"
			}
			recLoad, _ := strconv.ParseFloat(rec[2], 64)
			if recLoad != bp.MaxLoadPerCore {
				drift = "guard"
			}
			recGB, _ := strconv.ParseFloat(rec[3], 64)
			if recGB != bp.GBPerCard {
				drift = "guard"
			}
		}

		// Check shares.tsv on disk
		sharesPath := filepath.Join(in.Queue, "shares", bench, "shares.tsv")
		if sRaw, err := os.ReadFile(sharesPath); err == nil {
			for _, sl := range strings.Split(string(sRaw), "\n") {
				f := strings.Split(strings.TrimSpace(sl), "\t")
				if len(f) == 2 && f[0] == "capacity" {
					if c, _ := strconv.Atoi(f[1]); c != bp.Cap {
						drift = "cap"
					}
				}
				if len(f) == 2 && f[0] == "swarm-"+bench {
					if c, _ := strconv.Atoi(f[1]); c != bp.Cap {
						drift = "cap"
					}
				}
			}
		}

		if drift != "none" {
			driftFound = true
		}

		if in.Max > 0 && printed >= in.Max {
			break
		}

		fmt.Fprintf(in.Stdout, "SPRINT STATUS bench=%s cap=%d held=%d free=%d load_per_core=%.2f drift=%s\n",
			oneline.Field(bench), bp.Cap, held, free, measuredLoad, drift)
		printed++
	}

	if driftFound {
		return 1
	}
	return 0
}

// --- Sprint Table ---

type SprintTableInput struct {
	PlanPath    string
	Queue       string
	Bus         string
	Bench       string
	Once        bool
	Redis       string
	Stdout      io.Writer
	Stderr      io.Writer
	Now         func() time.Time
	Store       SprintStore
	Shell       BenchShell
	ProbeBudget float64
}

func SprintTable(in SprintTableInput) int {
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "SPRINT TABLE", fmt.Errorf("--queue is required"))
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}

	store := in.Store
	if store == nil {
		fallbackDir := filepath.Join(in.Queue, ".nova-bus", "sprint-table")
		if in.Bus != "" {
			fallbackDir = filepath.Join(in.Bus, ".nova-bus", "sprint-table")
		}
		store = NewSprintStore(in.Redis, fallbackDir)
	}

	// If --bench is provided, run as Table Agent (Rule 10)
	if strings.TrimSpace(in.Bench) != "" {
		bench := in.Bench
		now := in.Now().UTC()

		// Read slot store (working count)
		working := "-"
		sharesFile := filepath.Join(in.Queue, "shares", bench, "shares.tsv")
		if raw, err := os.ReadFile(sharesFile); err == nil {
			liveCount := 0
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "state=live") || strings.HasPrefix(line, "lease ") {
					liveCount++
				}
			}
			working = strconv.Itoa(liveCount)
		}

		// Read pending queue
		queueCount := "-"
		pendingDir := filepath.Join(in.Queue, "pending")
		if entries, err := os.ReadDir(pendingDir); err == nil {
			queueCount = strconv.Itoa(len(entries))
		}

		// Read dealt ledger (done count)
		doneCount := "-"
		dealtPath := filepath.Join(in.Queue, "dealt.tsv")
		if _, err := os.Stat(dealtPath); err == nil {
			if raw, err := os.ReadFile(dealtPath); err == nil {
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				c := 0
				for _, l := range lines {
					if l != "" && !strings.HasPrefix(l, "#") {
						c++
					}
				}
				doneCount = strconv.Itoa(c)
			}
		}

		// Read results store
		okVal := "-"
		failVal := "-"
		okPct := "-"
		resultsPath := filepath.Join(in.Queue, "results.tsv")
		if raw, err := os.ReadFile(resultsPath); err == nil {
			okN, failN := 0, 0
			for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				parts := strings.Split(l, "\t")
				if len(parts) >= 2 {
					if parts[1] == "OK" || parts[1] == "green" {
						okN++
					} else {
						failN++
					}
				}
			}
			okVal = strconv.Itoa(okN)
			failVal = strconv.Itoa(failN)
			denom := okN + failN
			if denom > 0 {
				okPct = strconv.Itoa((okN * 100) / denom)
			}
		}

		row := SprintRow{
			Host:      bench,
			Queue:     queueCount,
			Working:   working,
			Done:      doneCount,
			OK:        okVal,
			Fail:      failVal,
			OKPercent: okPct,
			UpdatedAt: now,
		}

		if err := store.PushRow(context.Background(), row); err != nil {
			return refusal(in.Stderr, "SPRINT TABLE", err)
		}

		storeKind := "dir"
		if _, ok := store.(*RedisSprintStore); ok {
			storeKind = "redis"
		}
		fmt.Fprintf(in.Stdout, "SPRINT TABLE OK bench=%s store=%s probes=0 took=0s\n",
			oneline.Field(bench), storeKind)
		return 0
	}

	// Viewer mode: reads store, never sshes (Rule 14)
	rows, err := store.ReadRows(context.Background())
	if err != nil {
		return refusal(in.Stderr, "SPRINT TABLE", err)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Host < rows[j].Host
	})

	now := in.Now().UTC()
	for _, r := range rows {
		line := fmt.Sprintf("SPRINT ROW host=%s queue=%s working=%s done=%s ok=%s fail=%s ok%%=%s",
			oneline.Field(r.Host), r.Queue, r.Working, r.Done, r.OK, r.Fail, r.OKPercent)
		if !r.UpdatedAt.IsZero() {
			age := int(now.Sub(r.UpdatedAt).Seconds())
			if age >= 20 {
				line += fmt.Sprintf(" age=%ds", age)
			}
		}
		fmt.Fprintln(in.Stdout, line)
	}

	// Issue #2417: If priority cards exist, report priority stats in footer
	priorityDir := filepath.Join(in.Queue, "priority")
	if _, err := os.Stat(priorityDir); err == nil {
		pQueued, pWorking, pDone := 0, 0, 0
		if qEntries, err := os.ReadDir(filepath.Join(priorityDir, "queued")); err == nil {
			pQueued = len(qEntries)
		}
		if wEntries, err := os.ReadDir(filepath.Join(priorityDir, "working")); err == nil {
			pWorking = len(wEntries)
		}
		if dEntries, err := os.ReadDir(filepath.Join(priorityDir, "done")); err == nil {
			pDone = len(dEntries)
		}
		fmt.Fprintf(in.Stdout, "SPRINT PRIORITY queued=%d working=%d done=%d\n", pQueued, pWorking, pDone)
	}

	return 0
}

// --- Sprint Stop ---

type SprintStopInput struct {
	PlanPath     string
	Queue        string
	MachinesPath string
	SSH          string
	Timeout      time.Duration
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time
	WorkingCheck func(queue, bench string) int
}

func SprintStop(in SprintStopInput) int {
	if strings.TrimSpace(in.PlanPath) == "" {
		return refusal(in.Stderr, "SPRINT STOP", fmt.Errorf("--plan is required"))
	}
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "SPRINT STOP", fmt.Errorf("--queue is required"))
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}

	plan, err := ParseSprintPlan(in.PlanPath, in.MachinesPath)
	if err != nil {
		fmt.Fprintf(in.Stderr, "%s\n", err.Error())
		return 2
	}

	// Step 1: Write dealer stop file (Rule 12)
	stopPath := filepath.Join(in.Queue, "STOP")
	_ = os.WriteFile(stopPath, []byte("SPRINT STOP\n"), 0o644)

	// Step 2: Check drain
	workingChecker := in.WorkingCheck
	if workingChecker == nil {
		workingChecker = func(queue, bench string) int {
			sharesFile := filepath.Join(queue, "shares", bench, "shares.tsv")
			if raw, err := os.ReadFile(sharesFile); err == nil {
				c := 0
				for _, l := range strings.Split(string(raw), "\n") {
					if strings.Contains(l, "state=live") || strings.HasPrefix(l, "lease ") {
						c++
					}
				}
				return c
			}
			return 0
		}
	}

	var busyBenches []string
	busyMap := make(map[string]int)
	for _, b := range plan.BenchOrder {
		w := workingChecker(in.Queue, b)
		if w > 0 {
			busyBenches = append(busyBenches, b)
			busyMap[b] = w
		}
	}

	if len(busyBenches) > 0 {
		for _, b := range busyBenches {
			fmt.Fprintf(in.Stdout, "SPRINT STOP WORKING bench=%s working=%d (drain not finished; nothing stamped)\n",
				oneline.Field(b), busyMap[b])
		}
		return 1
	}

	// Drained: stamp SPRINT-END beside SPRINT-START
	now := in.Now().UTC()
	epochStr := now.Format(time.RFC3339)
	endPath := filepath.Join(in.Queue, "SPRINT-END")
	_ = os.WriteFile(endPath, []byte(epochStr+"\n"), 0o644)

	fmt.Fprintf(in.Stdout, "SPRINT STOP OK stamp=%s drained=%d took=0s\n",
		oneline.Field(epochStr), len(plan.Benches))
	return 0
}

// RequeueHarnessFailure implements Rule 9: requeues a harness failure exactly once.
func RequeueHarnessFailure(queue, cardID, targetBench string) (bool, error) {
	requeuePath := filepath.Join(queue, "requeue.tsv")
	raw, _ := os.ReadFile(requeuePath)

	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) >= 1 && f[0] == cardID {
			// Already requeued once: fail it, never feed again (Rule 9)
			return false, nil
		}
	}

	// Append to requeue ledger
	f, err := os.OpenFile(requeuePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()

	entry := fmt.Sprintf("%s\t%s\t1\t%s\n", cardID, targetBench, time.Now().UTC().Format(time.RFC3339))
	if _, err := f.WriteString(entry); err != nil {
		return false, err
	}
	return true, nil
}

// RebuildStoreFromLedger implements Rule 15: rebuilding store from coordinator ledger yields byte-identical state.
func RebuildStoreFromLedger(queue string, store SprintStore) error {
	ledgerPath := filepath.Join(queue, "ledger.tsv")
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		return err
	}
	counts := make(map[string]*SprintRow)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(trimmed, "\t")
		if len(parts) >= 2 {
			bench := parts[0]
			row, exists := counts[bench]
			if !exists {
				row = &SprintRow{
					Host:      bench,
					Queue:     "0",
					Working:   "0",
					Done:      "0",
					OK:        "0",
					Fail:      "0",
					OKPercent: "-",
				}
				counts[bench] = row
			}
			d, _ := strconv.Atoi(row.Done)
			row.Done = strconv.Itoa(d + 1)
		}
	}
	for _, row := range counts {
		if err := store.PushRow(context.Background(), *row); err != nil {
			return err
		}
	}
	return nil
}

// FoldSprintModels implements Rule 16: folds model counts from provider table / routes.
func FoldSprintModels(queue, providerTablePath string) (map[string]int, error) {
	raw, err := os.ReadFile(providerTablePath)
	if err != nil {
		return nil, err
	}
	models := make(map[string]int)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(trimmed, "\t")
		if len(parts) >= 2 {
			model := parts[0]
			models[model]++
		}
	}
	return models, nil
}
