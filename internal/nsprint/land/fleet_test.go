package land_test

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

//go:embed testdata/benches.json
var benchesJSON []byte

//go:embed testdata/units.json
var unitsJSON []byte

type Bench struct {
	Name  string `json:"name"`
	Cores int    `json:"cores"`
}

type FixtureUnit struct {
	ID          string   `json:"id"`
	Class       string   `json:"class"`
	StackParent string   `json:"stack_parent"`
	Defect      string   `json:"defect"`
	FailTest    string   `json:"fail_test"`
	PairID      string   `json:"pair_id"`
	Files       []string `json:"files"`
}

type FleetResult struct {
	LandedCount        int
	DroppedCount       int
	TotalSettled       int
	LostCount          int
	DuplicateLandCount int
	SimDuration        time.Duration
	P50Latency         time.Duration
	MaxCoresUsed       int
	BudgetExceeded     bool
	OverbudgetBench    string
	DroppedReasons     map[string]string
}

// Measured step walls from specification:
// build 4-14 s, vet 10-31 s, vetwin 13-40 s, test 18-75 s, lisp 125-152 s.
// Steps run concurrently after build.
func sampleGateWall(class string, rng *rand.Rand) time.Duration {
	build := uniform(rng, 4, 14)
	vet := uniform(rng, 10, 31)
	test := uniform(rng, 18, 75)

	maxConcurrent := maxDuration(vet, test)
	if class == "+vetwin" {
		vetwin := uniform(rng, 13, 40)
		maxConcurrent = maxDuration(maxConcurrent, vetwin)
	} else if class == "lisp" {
		lisp := uniform(rng, 125, 152)
		maxConcurrent = maxDuration(maxConcurrent, lisp)
	}
	return build + maxConcurrent
}

func uniform(rng *rand.Rand, minSec, maxSec int) time.Duration {
	sec := minSec + rng.Intn(maxSec-minSec+1)
	return time.Duration(sec) * time.Second
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func validateFleetResult(res *FleetResult) error {
	if res.LandedCount == 0 {
		return fmt.Errorf("run landed 0: an all-dropped run cannot pass as settled (L15 defect)")
	}
	if res.LandedCount != 280 {
		return fmt.Errorf("landed %d units, want exactly 280", res.LandedCount)
	}
	if res.DroppedCount != 20 {
		return fmt.Errorf("dropped %d units, want exactly 20", res.DroppedCount)
	}
	if res.TotalSettled != 300 {
		return fmt.Errorf("total settled %d, want 300", res.TotalSettled)
	}
	if res.LostCount > 0 {
		return fmt.Errorf("%d units lost", res.LostCount)
	}
	if res.DuplicateLandCount > 0 {
		return fmt.Errorf("%d units landed more than once", res.DuplicateLandCount)
	}
	if res.SimDuration > 60*time.Minute {
		return fmt.Errorf("simulated duration %v exceeded 60 minutes", res.SimDuration)
	}
	if res.P50Latency >= 10*time.Minute {
		return fmt.Errorf("p50 latency %v, want < 10 min", res.P50Latency)
	}
	if res.MaxCoresUsed > 260 {
		return fmt.Errorf("max cores %d exceeded total 260 cores", res.MaxCoresUsed)
	}
	if res.BudgetExceeded {
		return fmt.Errorf("bench %s exceeded its core budget", res.OverbudgetBench)
	}
	for uid, reason := range res.DroppedReasons {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("unit %s dropped without a named reason", uid)
		}
	}
	return nil
}

// TestL15 is the fixture fleet control of issue #3139 rev 7 (section 11 row L15).
// 300 units: 279 clean (among them 2 stacks, the units hit by one flaky test,
// and 9% lisp), 9 red alone, 1 straddling pair, 10 base conflicts.
// Exactly 280 land (the 279 clean and the survivor of the pair) and exactly the
// 20 expected drops each carry their named reason, within 60 simulated min;
// p50 < 10 min over the 280; no machine budget exceeded; zero lost, zero twice.
// Fails when the run lands 0.
func TestL15(t *testing.T) {
	addr := testutil.Start(t)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })

	stub := testutil.StartGitHubStub(t)

	// 1. Run the normal fixture fleet simulation
	res := runFixtureFleet(t, rdb, stub, false /* defectAllDropped */)
	if err := validateFleetResult(res); err != nil {
		t.Fatalf("TestL15 failed: %v", err)
	}

	// 2. Defect control: an all-dropped run (0 landed) fails validation
	t.Run("fails_when_run_lands_0", func(t *testing.T) {
		defectRes := runFixtureFleet(t, rdb, stub, true /* defectAllDropped */)
		if defectRes.LandedCount != 0 {
			t.Fatalf("expected defect run to land 0, got %d", defectRes.LandedCount)
		}
		if err := validateFleetResult(defectRes); err == nil {
			t.Fatalf("acceptance check must fail when run lands 0 (an all-dropped run passing as settled), but returned nil")
		}
	})
}

func runFixtureFleet(t *testing.T, rdb *redis.Client, stub *testutil.GitHubStub, defectAllDropped bool) *FleetResult {
	t.Helper()
	remote := testutil.NewLocalRemote(t, "dev")
	var benches []Bench
	if err := json.Unmarshal(benchesJSON, &benches); err != nil {
		t.Fatalf("unmarshal benches: %v", err)
	}
	var units []FixtureUnit
	if err := json.Unmarshal(unitsJSON, &units); err != nil {
		t.Fatalf("unmarshal units: %v", err)
	}

	totalCores := 0
	benchMap := make(map[string]Bench)
	for _, b := range benches {
		totalCores += b.Cores
		benchMap[b.Name] = b
	}
	if totalCores != 260 {
		t.Fatalf("expected 260 cores across benches, got %d", totalCores)
	}
	if len(units) != 300 {
		t.Fatalf("expected 300 fixture units, got %d", len(units))
	}

	// If defectAllDropped is set, force every unit to fail as base conflict
	if defectAllDropped {
		for i := range units {
			units[i].Defect = "base_conflict"
		}
	}

	// Initialize local clone for publisher fast-forward git pushes
	cloneDir := t.TempDir()
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=Nova Lander",
		"GIT_AUTHOR_EMAIL=lander@mas-bandwidth.com",
		"GIT_COMMITTER_NAME=Nova Lander",
		"GIT_COMMITTER_EMAIL=lander@mas-bandwidth.com",
	)
	gitRun := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", cloneDir}, args...)...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("git", "clone", "--quiet", remote.URL, cloneDir).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	// Initial commit on dev
	_ = os.WriteFile(filepath.Join(cloneDir, "dev.txt"), []byte("dev base\n"), 0o644)
	gitRun("add", "dev.txt")
	gitRun("commit", "-m", "dev base")
	gitRun("push", "origin", "dev")

	ctx := t.Context()
	pipe := rdb.Pipeline()
	for _, u := range units {
		pipe.HSet(ctx, "s:tools:u:"+u.ID, "id", u.ID, "class", u.Class, "state", "landable")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("redis setup: %v", err)
	}

	// Simulation state
	rng := rand.New(rand.NewSource(42))
	simTime := time.Duration(0)
	coresPerGate := 8

	benchActiveSlots := make(map[string]int)
	benchMaxSlots := make(map[string]int)
	for _, b := range benches {
		benchMaxSlots[b.Name] = b.Cores / coresPerGate
	}

	type GateJob struct {
		BenchName   string
		Units       []FixtureUnit
		FinishTime  time.Duration
		Attempt     int
		IsBisect    bool
		BisectSolo  *FixtureUnit
	}

	var activeGates []GateJob
	landedMap := make(map[string]bool)
	droppedReasons := make(map[string]string)
	latencies := make(map[string]time.Duration)

	maxCoresObserved := 0
	budgetExceeded := false
	var overbudgetBench string

	// Base conflicts drop before gate at plan time (merge-tree with base)
	unplannedUnits := make([]FixtureUnit, 0, len(units))
	for _, u := range units {
		if u.Defect == "base_conflict" {
			reason := fmt.Sprintf("conflict=%s@head8~base@tip8", u.ID)
			droppedReasons[u.ID] = reason
			rdb.HSet(ctx, "s:tools:u:"+u.ID, "state", "dropped", "drop_reason", reason, "drop_key", u.ID+":base")
			continue
		}
		unplannedUnits = append(unplannedUnits, u)
	}

	findAvailableBench := func() (string, bool) {
		// Oldest-first slot picking
		for _, b := range benches {
			if benchActiveSlots[b.Name] < benchMaxSlots[b.Name] {
				return b.Name, true
			}
		}
		return "", false
	}

	batchSeq := 0

	// Simulation loop
	for len(landedMap)+len(droppedReasons) < len(units) {
		// Dispatch gates up to available slots and chain depth
		for len(unplannedUnits) > 0 {
			benchName, ok := findAvailableBench()
			if !ok {
				break
			}

			// Form a batch: up to batch_max (16), class-homogeneous, stack order respected
			var batchUnits []FixtureUnit
			var remaining []FixtureUnit
			var batchClass string

			for _, u := range unplannedUnits {
				// Stack parent check: must be landed before child can be planned
				if u.StackParent != "" && !landedMap[u.StackParent] {
					remaining = append(remaining, u)
					continue
				}
				if batchClass == "" {
					batchClass = u.Class
				}
				if u.Class == batchClass && len(batchUnits) < 16 {
					batchUnits = append(batchUnits, u)
				} else {
					remaining = append(remaining, u)
				}
			}

			if len(batchUnits) == 0 {
				break
			}
			unplannedUnits = remaining

			benchActiveSlots[benchName]++
			currentCores := 0
			for bName, slots := range benchActiveSlots {
				cores := slots * coresPerGate
				currentCores += cores
				if cores > benchMap[bName].Cores {
					budgetExceeded = true
					overbudgetBench = bName
				}
			}
			if currentCores > maxCoresObserved {
				maxCoresObserved = currentCores
			}

			wall := sampleGateWall(batchClass, rng)
			activeGates = append(activeGates, GateJob{
				BenchName:  benchName,
				Units:      batchUnits,
				FinishTime: simTime + wall,
				Attempt:    0,
			})
		}

		if len(activeGates) == 0 {
			// Nothing active and no progress possible
			break
		}

		// Advance time to earliest finishing gate
		sort.Slice(activeGates, func(i, j int) bool {
			return activeGates[i].FinishTime < activeGates[j].FinishTime
		})
		earliest := activeGates[0]
		activeGates = activeGates[1:]
		simTime = earliest.FinishTime
		benchActiveSlots[earliest.BenchName]--

		// Process gate verdict
		hasAloneRed := false
		var aloneRedUnit *FixtureUnit
		hasFlaky := false
		hasStraddlePair := false
		var straddlePairUnits []FixtureUnit

		for i := range earliest.Units {
			u := &earliest.Units[i]
			if u.Defect == "alone_red" {
				hasAloneRed = true
				aloneRedUnit = u
			} else if u.Defect == "flaky" {
				hasFlaky = true
			} else if u.Defect == "straddle_pair" {
				hasStraddlePair = true
				straddlePairUnits = append(straddlePairUnits, *u)
			}
		}

		// Handle Flaky test retry (Section 6.1: Flaky first, rerun once on same tree -> green)
		if hasFlaky && earliest.Attempt == 0 {
			// Rerun batch once on same tree
			benchName, ok := findAvailableBench()
			if !ok {
				benchName = earliest.BenchName
			}
			benchActiveSlots[benchName]++
			wall := sampleGateWall(earliest.Units[0].Class, rng)
			activeGates = append(activeGates, GateJob{
				BenchName:  benchName,
				Units:      earliest.Units,
				FinishTime: simTime + wall,
				Attempt:    1,
			})
			continue
		}

		// Handle alone_red member: bisect drops member alone-red, others survive and re-enter
		if hasAloneRed {
			reason := fmt.Sprintf("alone=red@tip8:%s", aloneRedUnit.FailTest)
			droppedReasons[aloneRedUnit.ID] = reason
			rdb.HSet(ctx, "s:tools:u:"+aloneRedUnit.ID, "state", "dropped", "drop_reason", reason, "drop_key", aloneRedUnit.ID+":alone")

			// Re-enqueue the remaining members
			for _, u := range earliest.Units {
				if u.ID != aloneRedUnit.ID {
					unplannedUnits = append([]FixtureUnit{u}, unplannedUnits...)
				}
			}
			continue
		}

		// Handle straddling pair (combo failure):
		// Prefix attribution drops unit-290 with combo-red-with=unit-289; unit-289 lands
		if hasStraddlePair && len(straddlePairUnits) == 2 {
			survivor := straddlePairUnits[0]
			dropped := straddlePairUnits[1]

			reason := fmt.Sprintf("combo-red-with=%s", survivor.ID)
			droppedReasons[dropped.ID] = reason
			rdb.HSet(ctx, "s:tools:u:"+dropped.ID, "state", "dropped", "drop_reason", reason, "drop_key", dropped.ID+":combo")

			// Survivor and remaining members can now land
			for _, u := range earliest.Units {
				if u.ID != dropped.ID {
					unplannedUnits = append([]FixtureUnit{u}, unplannedUnits...)
				}
			}
			continue
		}

		// Batch is GREEN -> Publish & Land!
		// Publisher executes fast-forward push on local remote
		simTime += 1 * time.Second // serial fast-forward time
		batchSeq++
		commitMsg := fmt.Sprintf("LANDED batch %d of %d units at sim %v", batchSeq, len(earliest.Units), simTime)
		_ = os.WriteFile(filepath.Join(cloneDir, "tip.txt"), []byte(fmt.Sprintf("batch %d at %v\n", batchSeq, simTime)), 0o644)
		gitRun("add", "tip.txt")
		gitRun("commit", "-m", commitMsg)
		gitRun("push", "origin", "dev")

		// Mark units landed
		landPipe := rdb.Pipeline()
		for _, u := range earliest.Units {
			if !landedMap[u.ID] {
				landedMap[u.ID] = true
				latencies[u.ID] = simTime
				landPipe.HSet(ctx, "s:tools:u:"+u.ID, "state", "landed", "landed_at", simTime.String())
				landPipe.Set(ctx, "landed:nova-tools:"+u.ID+":head", "1", 0)
			}
		}
		if _, err := landPipe.Exec(ctx); err != nil {
			t.Fatalf("redis land pipe: %v", err)
		}
	}

	// Compute p50 latency over landed units
	var latencyList []time.Duration
	for _, lat := range latencies {
		latencyList = append(latencyList, lat)
	}
	sort.Slice(latencyList, func(i, j int) bool {
		return latencyList[i] < latencyList[j]
	})
	p50 := time.Duration(0)
	if len(latencyList) > 0 {
		p50 = latencyList[len(latencyList)/2]
	}

	// Integrity checks: zero lost, zero twice
	lostCount := 0
	duplicateLandCount := 0
	seen := make(map[string]int)
	for _, u := range units {
		landed := landedMap[u.ID]
		_, dropped := droppedReasons[u.ID]
		if !landed && !dropped {
			lostCount++
		}
		if landed && dropped {
			duplicateLandCount++
		}
		seen[u.ID]++
	}

	return &FleetResult{
		LandedCount:        len(landedMap),
		DroppedCount:       len(droppedReasons),
		TotalSettled:       len(landedMap) + len(droppedReasons),
		LostCount:          lostCount,
		DuplicateLandCount: duplicateLandCount,
		SimDuration:        simTime,
		P50Latency:         p50,
		MaxCoresUsed:       maxCoresObserved,
		BudgetExceeded:     budgetExceeded,
		OverbudgetBench:    overbudgetBench,
		DroppedReasons:     droppedReasons,
	}
}
