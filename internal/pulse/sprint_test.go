package pulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Helper to create a valid plan file
func makeValidPlan(t *testing.T, dir string) string {
	t.Helper()
	content := strings.Join([]string{
		"# Valid sprint plan",
		"bench\thulk\t32\t2.50\t50.00",
		"bench\tspace\t16\t1.50\t25.00",
		"route\tflash\tgemini-2.5-flash,claude-3-5-haiku",
		"route\tpro\tgemini-2.5-pro,claude-3-5-sonnet",
		"probe\t10.0",
		"",
	}, "\n")
	path := filepath.Join(dir, "plan.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var dummyGitRunner = func(ctx context.Context, dir string, args ...string) (string, error) {
	return "ok", nil
}

// 1. TestSprintStartRefusesAPlanItCannotReadWhole (rule 1)
func TestSprintStartRefusesAPlanItCannotReadWhole(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	machinesFile := exampleMachines(t)

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "bad row kind",
			content: "unknown\tfoo\tbar\n" +
				"bench\thulk\t32\t2.50\t50.00\n",
			wantErr: "unknown row kind \"unknown\"",
		},
		{
			name: "short bench row",
			content: "bench\thulk\t32\t2.50\n" +
				"route\tflash\tprovider\n",
			wantErr: "row bench wants 5 fields",
		},
		{
			name: "NaN guard",
			content: "bench\thulk\t32\tNaN\t50.00\n" +
				"route\tflash\tprovider\n",
			wantErr: "max_load_per_core must be a finite number",
		},
		{
			name: "bench not in registry",
			content: "bench\tnot-in-fleet\t32\t2.50\t50.00\n" +
				"route\tflash\tprovider\n",
			wantErr: "not in machines registry",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planPath := filepath.Join(dir, "bad-plan-"+tc.name+".tsv")
			_ = os.WriteFile(planPath, []byte(tc.content), 0o644)

			var out, errb bytes.Buffer
			code := SprintStart(SprintStartInput{
				PlanPath:     planPath,
				Queue:        queueDir,
				MachinesPath: machinesFile,
				Stdout:       &out,
				Stderr:       &errb,
			})

			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(errb.String(), tc.wantErr) {
				t.Fatalf("stderr %q does not contain %q", errb.String(), tc.wantErr)
			}

			// No share, record, route, mirror or stamp exists afterwards
			if _, err := os.Stat(filepath.Join(queueDir, "shares")); !os.IsNotExist(err) {
				t.Fatalf("shares directory created on refusal")
			}
			if _, err := os.Stat(filepath.Join(queueDir, "sprint.tsv")); !os.IsNotExist(err) {
				t.Fatalf("sprint.tsv record created on refusal")
			}
			if _, err := os.Stat(filepath.Join(queueDir, "routes.tsv")); !os.IsNotExist(err) {
				t.Fatalf("routes.tsv created on refusal")
			}
			if _, err := os.Stat(filepath.Join(queueDir, "mirrors")); !os.IsNotExist(err) {
				t.Fatalf("mirrors directory created on refusal")
			}
			if _, err := os.Stat(filepath.Join(queueDir, "SPRINT-START")); !os.IsNotExist(err) {
				t.Fatalf("SPRINT-START created on refusal")
			}
		})
	}
}

// 2. TestSprintStartAppliesThePlanInOrderOrNotAtAll (rule 2)
func TestSprintStartAppliesThePlanInOrderOrNotAtAll(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")

	planContent := strings.Join([]string{
		"bench\tb1\t10\t1.0\t10.0",
		"bench\tb2\t10\t1.0\t10.0",
		"bench\tb3\t10\t1.0\t10.0",
		"bench\tb4\t10\t1.0\t10.0",
		"route\tflash\tp1",
		"probe\t5.0",
		"",
	}, "\n")
	planPath := filepath.Join(dir, "plan-four.tsv")
	_ = os.WriteFile(planPath, []byte(planContent), 0o644)

	var gitCalls []string
	failingGit := func(ctx context.Context, d string, args ...string) (string, error) {
		gitCalls = append(gitCalls, d)
		if strings.Contains(d, "b3") {
			return "", fmt.Errorf("connection timeout to b3 mirror")
		}
		return "ok", nil
	}

	var out, errb bytes.Buffer
	code := SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: failingGit,
		Stdout:    &out,
		Stderr:    &errb,
	})

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}

	// Names the shares already written on the refusal line
	if !strings.Contains(errb.String(), "written shares: b1, b2, b3, b4") &&
		!strings.Contains(errb.String(), "b1, b2") {
		t.Fatalf("stderr does not name written shares: %s", errb.String())
	}

	// Leaves no stamp on any host or queue
	if _, err := os.Stat(filepath.Join(queueDir, "SPRINT-START")); !os.IsNotExist(err) {
		t.Fatalf("stamp SPRINT-START exists after mirror failure")
	}
}

// 3. TestSprintStartWritesTheSharesFromThePlanCap (rule 3)
func TestSprintStartWritesTheSharesFromThePlanCap(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	var out, errb bytes.Buffer
	code := SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    &out,
		Stderr:    &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errb.String())
	}

	// Verify hulk shares.tsv
	hulkShares := filepath.Join(queueDir, "shares", "hulk", "shares.tsv")
	raw, err := os.ReadFile(hulkShares)
	if err != nil {
		t.Fatalf("failed reading hulk shares: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "capacity\t32") {
		t.Errorf("hulk shares missing capacity 32: %s", content)
	}
	if !strings.Contains(content, "swarm-hulk\t32") {
		t.Errorf("hulk shares missing owner row swarm-hulk 32: %s", content)
	}
}

// 4. TestSprintGuardsAreReadFromTheRecordEveryTick (rule 4)
func TestSprintGuardsAreReadFromTheRecordEveryTick(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	var out, errb bytes.Buffer
	SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    &out,
		Stderr:    &errb,
	})

	// Tick 1 read
	rec1, err := ReadSprintRecord(queueDir)
	if err != nil {
		t.Fatalf("ReadSprintRecord tick 1: %v", err)
	}
	if rec1["hulk"].MaxLoadPerCore != 2.50 || rec1["hulk"].GBPerCard != 50.00 {
		t.Fatalf("tick 1 got %+v, want load 2.50, gb 50.00", rec1["hulk"])
	}

	// Edit record between ticks by hand
	recPath := filepath.Join(queueDir, "sprint.tsv")
	edited := strings.Join([]string{
		"# edited",
		"hulk\t32\t4.00\t80.00\t2026-09-21T10:00:00Z",
		"space\t16\t3.00\t40.00\t2026-09-21T10:00:00Z",
		"",
	}, "\n")
	_ = os.WriteFile(recPath, []byte(edited), 0o644)

	// Tick 2 read reflects updated guards without restart or flag
	rec2, err := ReadSprintRecord(queueDir)
	if err != nil {
		t.Fatalf("ReadSprintRecord tick 2: %v", err)
	}
	if rec2["hulk"].MaxLoadPerCore != 4.00 || rec2["hulk"].GBPerCard != 80.00 {
		t.Fatalf("tick 2 got %+v, want load 4.00, gb 80.00", rec2["hulk"])
	}
}

// 5. TestSprintSetTakesEffectOnTheNextTickWithoutARestart (rule 5)
func TestSprintSetTakesEffectOnTheNextTickWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})

	var out, errb bytes.Buffer
	code := SprintSet(SprintSetInput{
		Queue:  queueDir,
		Bench:  "space",
		Key:    "cap",
		Value:  "64",
		Stdout: &out,
		Stderr: &errb,
	})

	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errb.String())
	}
	wantLine := "SPRINT SET bench=space cap=16->64 (the next tick takes it)\n"
	if out.String() != wantLine {
		t.Fatalf("output = %q, want %q", out.String(), wantLine)
	}

	// Next tick deals against 64
	rec, err := ReadSprintRecord(queueDir)
	if err != nil {
		t.Fatalf("ReadSprintRecord: %v", err)
	}
	if rec["space"].Cap != 64 {
		t.Fatalf("space cap = %d, want 64", rec["space"].Cap)
	}

	// Unknown key refusal
	out.Reset()
	errb.Reset()
	badKey := SprintSet(SprintSetInput{
		Queue:  queueDir,
		Bench:  "space",
		Key:    "unknown_key",
		Value:  "123",
		Stdout: &out,
		Stderr: &errb,
	})
	if badKey != 2 {
		t.Errorf("unknown key exit = %d, want 2", badKey)
	}

	// Unknown bench refusal
	out.Reset()
	errb.Reset()
	badBench := SprintSet(SprintSetInput{
		Queue:  queueDir,
		Bench:  "ghost_bench",
		Key:    "cap",
		Value:  "10",
		Stdout: &out,
		Stderr: &errb,
	})
	if badBench != 2 {
		t.Errorf("unknown bench exit = %d, want 2", badBench)
	}
}

// 6. TestSprintStartStampsOneEpochOnEveryHost (rule 6)
func TestSprintStartStampsOneEpochOnEveryHost(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	stamped := make(map[string]string)
	shell := &fakeShell{
		answer: func(bench, script string) (string, error) {
			stamped[bench] = script
			return "ok", nil
		},
	}

	fixedNow := time.Date(2026, 9, 21, 14, 0, 0, 0, time.UTC)
	var out, errb bytes.Buffer
	code := SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		Now:       func() time.Time { return fixedNow },
		Shell:     shell,
		GitRunner: dummyGitRunner,
		Stdout:    &out,
		Stderr:    &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	epochStr := fixedNow.Format(time.RFC3339)
	queueStamp, err := os.ReadFile(filepath.Join(queueDir, "SPRINT-START"))
	if err != nil {
		t.Fatalf("reading queue stamp: %v", err)
	}
	if strings.TrimSpace(string(queueStamp)) != epochStr {
		t.Errorf("queue stamp = %q, want %q", string(queueStamp), epochStr)
	}

	for _, bench := range []string{"hulk", "space"} {
		script, ok := stamped[bench]
		if !ok || !strings.Contains(script, epochStr) {
			t.Errorf("bench %s did not receive stamp script with epoch %s: got %q", bench, epochStr, script)
		}
	}
}

// 7. TestSprintStartRefreshesTheMirrorsBeforeTheFirstDeal (rule 7)
func TestSprintStartRefreshesTheMirrorsBeforeTheFirstDeal(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	var log []string
	runner := func(ctx context.Context, d string, args ...string) (string, error) {
		log = append(log, fmt.Sprintf("%s %s", d, strings.Join(args, " ")))
		return "ok", nil
	}

	code := SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: runner,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	if len(log) != 2 {
		t.Fatalf("expected 2 mirror fetches, got %d: %v", len(log), log)
	}
	for _, entry := range log {
		if !strings.Contains(entry, "fetch --prune") {
			t.Errorf("git runner command %q did not fetch --prune", entry)
		}
	}
}

// 8. TestSprintStartWritesTheProviderRoutesTheLauncherReads (rule 8)
func TestSprintStartWritesTheProviderRoutesTheLauncherReads(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	code := SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	routesRaw, err := os.ReadFile(filepath.Join(queueDir, "routes.tsv"))
	if err != nil {
		t.Fatalf("routes.tsv missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(routesRaw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("routes.tsv must carry exactly flash and pro rows, got %d lines: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "flash\t") || !strings.HasPrefix(lines[1], "pro\t") {
		t.Fatalf("routes.tsv lines = %v, want flash and pro only", lines)
	}
}

// 9. TestSprintRequeueFeedsAHarnessFailureBackOnce (rule 9)
func TestSprintRequeueFeedsAHarnessFailureBackOnce(t *testing.T) {
	queueDir := t.TempDir()

	requeued, err := RequeueHarnessFailure(queueDir, "card-1234", "hulk")
	if err != nil || !requeued {
		t.Fatalf("first requeue should succeed, got requeued=%t, err=%v", requeued, err)
	}

	// Second attempt fails, never fed back
	requeued2, err2 := RequeueHarnessFailure(queueDir, "card-1234", "space")
	if err2 != nil {
		t.Fatalf("second requeue call error: %v", err2)
	}
	if requeued2 {
		t.Fatalf("second requeue should return false (failed, not fed), got true")
	}

	// Ledger row count should be exactly 1
	raw, _ := os.ReadFile(filepath.Join(queueDir, "requeue.tsv"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("requeue.tsv line count = %d, want 1", len(lines))
	}
}

// 10. TestSprintTableRowIsComputedOnTheBenchAndPushedToTheStore (rule 10)
func TestSprintTableRowIsComputedOnTheBenchAndPushedToTheStore(t *testing.T) {
	queueDir := t.TempDir()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	// Setup Redis via miniredis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	redisStore := &RedisSprintStore{Client: rClient}
	dirStore := &DirSprintStore{Dir: filepath.Join(queueDir, "dir-store")}

	// Push via Redis store
	var outRedis bytes.Buffer
	codeRedis := SprintTable(SprintTableInput{
		Queue:  queueDir,
		Bench:  "space",
		Store:  redisStore,
		Now:    func() time.Time { return now },
		Stdout: &outRedis,
		Stderr: io.Discard,
	})
	if codeRedis != 0 {
		t.Fatalf("codeRedis = %d, want 0", codeRedis)
	}
	if !strings.Contains(outRedis.String(), "store=redis") {
		t.Errorf("output missing store=redis: %s", outRedis.String())
	}

	// Push via Directory fallback store
	var outDir bytes.Buffer
	codeDir := SprintTable(SprintTableInput{
		Queue:  queueDir,
		Bench:  "space",
		Store:  dirStore,
		Now:    func() time.Time { return now },
		Stdout: &outDir,
		Stderr: io.Discard,
	})
	if codeDir != 0 {
		t.Fatalf("codeDir = %d, want 0", codeDir)
	}
	if !strings.Contains(outDir.String(), "store=dir") {
		t.Errorf("output missing store=dir: %s", outDir.String())
	}

	// Assert the same rows land in miniredis and in dir fallback
	redisRows, _ := redisStore.ReadRows(context.Background())
	dirRows, _ := dirStore.ReadRows(context.Background())
	if len(redisRows) != 1 || len(dirRows) != 1 {
		t.Fatalf("rows count mismatch: redis=%d, dir=%d", len(redisRows), len(dirRows))
	}
	if redisRows[0].Host != dirRows[0].Host || redisRows[0].Queue != dirRows[0].Queue {
		t.Fatalf("row content differs: redis=%+v, dir=%+v", redisRows[0], dirRows[0])
	}
}

// 11. TestSprintStatusNamesTheKeyThatDriftedFromThePlan (rule 11)
func TestSprintStatusNamesTheKeyThatDriftedFromThePlan(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})

	// Clean status: exit 0
	var out, errb bytes.Buffer
	code := SprintStatus(SprintStatusInput{
		PlanPath: planPath,
		Queue:    queueDir,
		Stdout:   &out,
		Stderr:   &errb,
	})
	if code != 0 {
		t.Fatalf("clean status exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "drift=none") {
		t.Errorf("clean status did not print drift=none: %s", out.String())
	}

	// Lower cap by hand in shares.tsv
	sharesPath := filepath.Join(queueDir, "shares", "hulk", "shares.tsv")
	_ = os.WriteFile(sharesPath, []byte("capacity\t10\nswarm-hulk\t10\n"), 0o644)

	out.Reset()
	errb.Reset()
	codeDrift := SprintStatus(SprintStatusInput{
		PlanPath: planPath,
		Queue:    queueDir,
		Stdout:   &out,
		Stderr:   &errb,
	})
	if codeDrift != 1 {
		t.Fatalf("drift status exit = %d, want 1", codeDrift)
	}
	if !strings.Contains(out.String(), "bench=hulk") || !strings.Contains(out.String(), "drift=cap") {
		t.Errorf("drift output missing drift=cap for hulk: %s", out.String())
	}
}

// 12. TestSprintStopDrainsBeforeItStamps (rule 12)
func TestSprintStopDrainsBeforeItStamps(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})

	stopCheckedBeforeDrain := false
	checker := func(q, b string) int {
		// Stop file must exist before first drain read
		if _, err := os.Stat(filepath.Join(q, "STOP")); err == nil {
			stopCheckedBeforeDrain = true
		}
		return 0 // drained
	}

	var out, errb bytes.Buffer
	code := SprintStop(SprintStopInput{
		PlanPath:     planPath,
		Queue:        queueDir,
		WorkingCheck: checker,
		Stdout:       &out,
		Stderr:       &errb,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errb.String())
	}
	if !stopCheckedBeforeDrain {
		t.Fatalf("stop file did not exist before first drain check")
	}

	// SPRINT-END sits beside SPRINT-START
	if _, err := os.Stat(filepath.Join(queueDir, "SPRINT-END")); err != nil {
		t.Fatalf("SPRINT-END stamp file missing: %v", err)
	}
	if !strings.Contains(out.String(), "SPRINT STOP OK") {
		t.Errorf("output missing SPRINT STOP OK: %s", out.String())
	}
}

// 13. TestSprintStopNamesEveryBenchStillWorking (rule 12)
func TestSprintStopNamesEveryBenchStillWorking(t *testing.T) {
	dir := t.TempDir()
	queueDir := filepath.Join(dir, "queue")
	planPath := makeValidPlan(t, dir)

	SprintStart(SprintStartInput{
		PlanPath:  planPath,
		Queue:     queueDir,
		GitRunner: dummyGitRunner,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})

	workingChecker := func(q, b string) int {
		if b == "hulk" {
			return 3
		}
		if b == "space" {
			return 1
		}
		return 0
	}

	var out, errb bytes.Buffer
	code := SprintStop(SprintStopInput{
		PlanPath:     planPath,
		Queue:        queueDir,
		WorkingCheck: workingChecker,
		Stdout:       &out,
		Stderr:       &errb,
	})

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}

	// Two WORKING lines, no stamp
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 WORKING lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(out.String(), "bench=hulk working=3") || !strings.Contains(out.String(), "bench=space working=1") {
		t.Errorf("output missing working bench details: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(queueDir, "SPRINT-END")); !os.IsNotExist(err) {
		t.Fatalf("SPRINT-END was stamped even though work was still running")
	}
}

// 14. TestSprintTableRowReadsADashForAStoreItCouldNotRead (rule 13)
func TestSprintTableRowReadsADashForAStoreItCouldNotRead(t *testing.T) {
	queueDir := t.TempDir()
	store := &DirSprintStore{Dir: filepath.Join(queueDir, "store")}

	// Do not create results.tsv or shares
	code := SprintTable(SprintTableInput{
		Queue:  queueDir,
		Bench:  "isolated-bench",
		Store:  store,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	rows, _ := store.ReadRows(context.Background())
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.OK != "-" || r.Fail != "-" || r.OKPercent != "-" {
		t.Fatalf("unreadable store gave non-dash: ok=%q, fail=%q, ok%%=%q", r.OK, r.Fail, r.OKPercent)
	}
}

// 15. TestSprintViewerRendersTheStoreAndNeverSshes (rule 14)
func TestSprintViewerRendersTheStoreAndNeverSshes(t *testing.T) {
	queueDir := t.TempDir()
	store := &DirSprintStore{Dir: filepath.Join(queueDir, "store")}

	now := time.Now().UTC()
	oldTime := now.Add(-30 * time.Second)

	_ = store.PushRow(context.Background(), SprintRow{
		Host:      "host-fresh",
		Queue:     "5",
		Working:   "2",
		Done:      "10",
		OK:        "9",
		Fail:      "1",
		OKPercent: "90",
		UpdatedAt: now,
	})
	_ = store.PushRow(context.Background(), SprintRow{
		Host:      "host-stale",
		Queue:     "0",
		Working:   "0",
		Done:      "20",
		OK:        "20",
		Fail:      "0",
		OKPercent: "100",
		UpdatedAt: oldTime,
	})

	shellCalls := 0
	shell := &fakeShell{
		answer: func(bench, script string) (string, error) {
			shellCalls++
			return "", nil
		},
	}

	var out, errb bytes.Buffer
	code := SprintTable(SprintTableInput{
		Queue:  queueDir,
		Store:  store,
		Shell:  shell,
		Now:    func() time.Time { return now },
		Stdout: &out,
		Stderr: &errb,
	})

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if shellCalls > 0 {
		t.Fatalf("viewer invoked shell/ssh %d times, want 0", shellCalls)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 SPRINT ROW lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "host=host-stale") || !strings.Contains(lines[1], "age=30s") {
		t.Errorf("stale row missing age=30s: %s", lines[1])
	}
}

// 16. TestSprintLedgerBeginsAtTheStampAndSurvivesAJobCleanup (rule 15)
func TestSprintLedgerBeginsAtTheStampAndSurvivesAJobCleanup(t *testing.T) {
	queueDir := t.TempDir()
	ledgerPath := filepath.Join(queueDir, "ledger.tsv")

	// Write 100 dealt rows into ledger
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "hulk\tcard-%03d\tdeal\t2026-09-21T10:00:00Z\n", i)
	}
	_ = os.WriteFile(ledgerPath, []byte(b.String()), 0o644)

	// Simulate hygiene prune of bench job directory
	benchJobDir := filepath.Join(queueDir, "jobs", "hulk")
	_ = os.MkdirAll(benchJobDir, 0o755)
	_ = os.RemoveAll(benchJobDir)

	// Ledger is coordinator-side and still holds all 100 rows
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("ledger deleted during cleanup: %v", err)
	}
	if len(strings.Split(strings.TrimSpace(string(raw)), "\n")) != 100 {
		t.Fatalf("ledger rows altered by cleanup")
	}

	// Rebuild store from ledger
	store := &DirSprintStore{Dir: filepath.Join(queueDir, "store")}
	if err := RebuildStoreFromLedger(queueDir, store); err != nil {
		t.Fatalf("RebuildStoreFromLedger: %v", err)
	}

	rows, _ := store.ReadRows(context.Background())
	if len(rows) != 1 || rows[0].Done != "100" {
		t.Fatalf("rebuilt store done count = %s, want 100", rows[0].Done)
	}
}

// 17. TestSprintModelRowsComeFromTheProviderTable (rule 16)
func TestSprintModelRowsComeFromTheProviderTable(t *testing.T) {
	tmpDir := t.TempDir()
	providerTable := filepath.Join(tmpDir, "provider_table.tsv")
	content := strings.Join([]string{
		"gemini-2.5-flash\tok",
		"gemini-2.5-flash\tok",
		"claude-3-5-haiku\tok",
		"gemini-2.5-pro\tok",
		"",
	}, "\n")
	_ = os.WriteFile(providerTable, []byte(content), 0o644)

	models, err := FoldSprintModels(tmpDir, providerTable)
	if err != nil {
		t.Fatalf("FoldSprintModels: %v", err)
	}
	if models["gemini-2.5-flash"] != 2 || models["claude-3-5-haiku"] != 1 || models["gemini-2.5-pro"] != 1 {
		t.Fatalf("model counts mismatch: %+v", models)
	}
}

// 21. TestSprintPriorityCardVisibility (Issue #2417)
func TestSprintPriorityCardVisibility(t *testing.T) {
	queueDir := t.TempDir()
	priorityDir := filepath.Join(queueDir, "priority")
	_ = os.MkdirAll(filepath.Join(priorityDir, "queued"), 0o755)
	_ = os.MkdirAll(filepath.Join(priorityDir, "working"), 0o755)
	_ = os.MkdirAll(filepath.Join(priorityDir, "done"), 0o755)

	_ = os.WriteFile(filepath.Join(priorityDir, "queued", "card-p1"), []byte("data"), 0o644)
	_ = os.WriteFile(filepath.Join(priorityDir, "working", "card-p2"), []byte("data"), 0o644)
	_ = os.WriteFile(filepath.Join(priorityDir, "working", "card-p3"), []byte("data"), 0o644)
	_ = os.WriteFile(filepath.Join(priorityDir, "done", "card-p4"), []byte("data"), 0o644)

	store := &DirSprintStore{Dir: filepath.Join(queueDir, "store")}
	var out bytes.Buffer
	code := SprintTable(SprintTableInput{
		Queue:  queueDir,
		Store:  store,
		Stdout: &out,
		Stderr: io.Discard,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	want := "SPRINT PRIORITY queued=1 working=2 done=1\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output missing priority summary: %s", out.String())
	}
}

// 22. TestSprintMutationTeeth (hard assertions verifying mutation failure)
func TestSprintMutationTeeth(t *testing.T) {
	t.Run("mutant_stamp_before_drain_teeth", func(t *testing.T) {
		// Stop with live workers MUST NOT stamp SPRINT-END
		queueDir := t.TempDir()
		planPath := makeValidPlan(t, queueDir)
		SprintStart(SprintStartInput{PlanPath: planPath, Queue: queueDir, GitRunner: dummyGitRunner, Stdout: io.Discard, Stderr: io.Discard})

		_ = SprintStop(SprintStopInput{
			PlanPath:     planPath,
			Queue:        queueDir,
			WorkingCheck: func(q, b string) int { return 1 },
			Stdout:       io.Discard,
			Stderr:       io.Discard,
		})

		if _, err := os.Stat(filepath.Join(queueDir, "SPRINT-END")); !os.IsNotExist(err) {
			t.Fatalf("TEETH: SPRINT-END stamped despite active leases!")
		}
	})
}
