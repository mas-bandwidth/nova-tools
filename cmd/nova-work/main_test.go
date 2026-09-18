package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/record"
)

var cmdNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func testDeps(store record.Store) deps {
	return deps{
		openStore: func(string) (record.Store, error) { return store, nil },
		openConsumer: func(ctx context.Context, addr, stream, group, name string) (record.Consumer, error) {
			return record.NewRedisConsumer(ctx, addr, stream, group, name)
		},
		now: func() time.Time { return cmdNow },
	}
}

func mustRun(t *testing.T, d deps, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf strings.Builder
	code := run(args, &out, &errBuf, d)
	return code, out.String(), errBuf.String()
}

func TestRecordMigrateCreatesTheSchema(t *testing.T) {
	store := record.NewFakeStore()
	code, out, errOut := mustRun(t, testDeps(store), "record", "--postgres", "postgres://space/nova", "--migrate")
	if code != 0 {
		t.Fatalf("record --migrate exit = %d, stderr:\n%s", code, errOut)
	}
	if store.Versions() != 1 {
		t.Fatalf("schema versions = %d, want 1", store.Versions())
	}
	if !strings.Contains(out, "MIGRATE OK") {
		t.Fatalf("migrate output does not name the schema:\n%s", out)
	}
}

func TestRecordOnceWritesTheRowAndAcksIt(t *testing.T) {
	mr := miniredis.RunT(t)
	store := record.NewFakeStore()
	if _, err := mr.XAdd(record.Stream, "1-0", []string{
		"label", "9347", "bench", "space", "exit", "0",
		"result", "RESULT: CARD-9347", "job", "/jobs/card-9347",
		"commit", "abc1234", "branch", "rowan/postgres-card-results",
	}); err != nil {
		t.Fatalf("seed stream: %s", err)
	}
	code, out, errOut := mustRun(t, testDeps(store), "record", "--redis", mr.Addr(), "--postgres", "postgres://space/nova", "--once")
	if code != 0 {
		t.Fatalf("record --once exit = %d, stderr:\n%s", code, errOut)
	}
	if len(store.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(store.Rows()))
	}
	if !strings.Contains(out, "RECORD") || !strings.Contains(out, "inserted=true") {
		t.Fatalf("record output does not name the row:\n%s", out)
	}
}

func TestRecordRefusesWithoutPostgres(t *testing.T) {
	code, _, errOut := mustRun(t, testDeps(record.NewFakeStore()), "record", "--redis", "127.0.0.1:6379")
	if code != 2 {
		t.Fatalf("record without --postgres exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--postgres") {
		t.Fatalf("the refusal does not name --postgres:\n%s", errOut)
	}
}

func TestRecordRefusesRedisWithoutPostgres(t *testing.T) {
	code, _, errOut := mustRun(t, testDeps(record.NewFakeStore()), "record", "--migrate", "--redis", "127.0.0.1:6379")
	if code != 2 {
		t.Fatalf("record --migrate with no --postgres exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--postgres") {
		t.Fatalf("the refusal does not name --postgres:\n%s", errOut)
	}
}

func seedResults(t *testing.T, n int) *record.FakeStore {
	t.Helper()
	store := record.NewFakeStore()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		exit := 0
		if i%5 == 0 {
			exit = 1
		}
		done := cmdNow.Add(-time.Duration(i) * time.Minute)
		row := record.Row{
			StreamID: fmt.Sprintf("%d-0", i), Label: fmt.Sprintf("card-%d", i),
			Bench: "space", Exit: exit, DoneAt: &done, RecordedAt: cmdNow,
		}
		if _, err := store.Insert(ctx, row); err != nil {
			t.Fatalf("seed %d: %s", i, err)
		}
	}
	return store
}

func TestResultsPrintsOneLinePerRowAndAMoreLine(t *testing.T) {
	code, out, errOut := mustRun(t, testDeps(seedResults(t, 25)), "results", "--postgres", "postgres://space/nova")
	if code != 0 {
		t.Fatalf("results exit = %d, stderr:\n%s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var resultLines, moreLines int
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "RESULT "):
			resultLines++
		case strings.HasPrefix(line, "RESULTS MORE "):
			moreLines++
		}
	}
	if resultLines != 20 || moreLines != 1 {
		t.Fatalf("results printed %d RESULT lines and %d MORE lines, want 20 and 1:\n%s", resultLines, moreLines, out)
	}
	if !strings.Contains(out, "total=25") {
		t.Fatalf("the MORE line does not carry the total:\n%s", out)
	}
}

func TestResultsFiltersByFailedBenchAndSince(t *testing.T) {
	store := seedResults(t, 10)
	// Add one mac row that must not survive --bench space.
	done := cmdNow
	if _, err := store.Insert(context.Background(), record.Row{
		StreamID: "mac-0", Label: "mac-card", Bench: "mac", Exit: 1, DoneAt: &done, RecordedAt: cmdNow,
	}); err != nil {
		t.Fatalf("seed mac: %s", err)
	}
	code, out, errOut := mustRun(t, testDeps(store),
		"results", "--postgres", "postgres://space/nova", "--bench", "space", "--failed", "--since", "1h", "--max", "0")
	if code != 0 {
		t.Fatalf("results exit = %d, stderr:\n%s", code, errOut)
	}
	if strings.Contains(out, "mac-card") {
		t.Fatalf("--bench space let a mac row through:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "RESULT ") {
			continue
		}
		if !strings.Contains(line, "exit=1") {
			t.Fatalf("--failed let a passing row through: %s", line)
		}
	}
	if !strings.Contains(out, "RESULT ") {
		t.Fatalf("no failed rows printed:\n%s", out)
	}
}

func TestResultsMaxZeroPrintsEverything(t *testing.T) {
	code, out, errOut := mustRun(t, testDeps(seedResults(t, 25)),
		"results", "--postgres", "postgres://space/nova", "--max", "0")
	if code != 0 {
		t.Fatalf("results exit = %d, stderr:\n%s", code, errOut)
	}
	if strings.Contains(out, "MORE") {
		t.Fatalf("--max 0 printed a MORE line:\n%s", out)
	}
	if got := strings.Count(out, "RESULT "); got != 25 {
		t.Fatalf("--max 0 printed %d rows, want 25", got)
	}
}

func TestResultsRefusesWithoutPostgres(t *testing.T) {
	code, _, errOut := mustRun(t, testDeps(record.NewFakeStore()), "results")
	if code != 2 {
		t.Fatalf("results without --postgres exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "--postgres") {
		t.Fatalf("the refusal does not name --postgres:\n%s", errOut)
	}
}

func TestUnknownVerbIsRefused(t *testing.T) {
	code, _, errOut := mustRun(t, testDeps(record.NewFakeStore()), "frobnicate")
	if code != 2 {
		t.Fatalf("unknown verb exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown verb") {
		t.Fatalf("the refusal does not name the verb:\n%s", errOut)
	}
}

func writeSeed(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deps.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	return path
}

func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "work.work")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const openNeedSeed = `{"nodes":[{"id":"a","needs":["b"]},{"id":"b"},{"id":"c"}]}`

const cycleSeed = `{"nodes":[{"id":"a","needs":["b"]},{"id":"b","needs":["a"]}]}`

func invoke(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The help text carries the dependencies and ready verb lines exactly as section 1
// prints them.
func TestHelpNamesTheDependenciesAndReadyVerbs(t *testing.T) {
	code, stdout, _ := invoke("help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	for _, want := range []string{"nova-work dependencies", "ready --node X"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help does not name %q:\n%s", want, stdout)
		}
	}
}

// dependencies refuses a :deps cycle before it publishes anything: exit 2, one
// remedy line naming validator rule 3.
func TestDependenciesRefusesANeedsCycle(t *testing.T) {
	seed := writeSeed(t, cycleSeed)
	code, stdout, stderr := invoke("dependencies", "--graph", seed)
	if code != 2 {
		t.Fatalf("dependencies on a cycle exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	for _, want := range []string{"rule 3", ":deps", "cycle", "nova-work help"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("cycle refusal does not name %q:\n%s", want, stderr)
		}
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refused cycle wrote to stdout: %q", stdout)
	}
}

// A seeded acyclic graph publishes once, as one line.
func TestDependenciesPublishesAnAcyclicGraph(t *testing.T) {
	seed := writeSeed(t, openNeedSeed)
	code, stdout, stderr := invoke("dependencies", "--graph", seed)
	if code != 0 {
		t.Fatalf("dependencies exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	line := strings.TrimSpace(stdout)
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("dependencies printed more than one line: %q", stdout)
	}
	if !strings.HasPrefix(line, "DEPENDENCIES OK") {
		t.Fatalf("dependencies line = %q, want a DEPENDENCIES OK line", line)
	}
}

// ready --node X is the ready set: the row for an open-need node names its exact
// blocker and its resolver, and a ready node's row says so.
func TestReadyPrintsEachRowsBlockerAndResolver(t *testing.T) {
	seed := writeSeed(t, openNeedSeed)

	code, stdout, stderr := invoke("ready", "--graph", seed, "--node", "a")
	if code != 0 {
		t.Fatalf("ready --node a exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{"node=a", "ready=false", "blocker=b", "state=open", `resolver="nova-merge queue"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("ready --node a line %q does not name %q", line, want)
		}
	}

	code, stdout, stderr = invoke("ready", "--graph", seed, "--node", "b")
	if code != 0 {
		t.Fatalf("ready --node b exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	if line := strings.TrimSpace(stdout); !strings.Contains(line, "node=b") || !strings.Contains(line, "ready=true") {
		t.Fatalf("ready --node b line = %q, want ready=true", line)
	}
}

// An unknown node is a refusal, never a guess.
func TestReadyRefusesAnUnknownNode(t *testing.T) {
	seed := writeSeed(t, openNeedSeed)
	code, stdout, stderr := invoke("ready", "--graph", seed, "--node", "zzz")
	if code != 2 {
		t.Fatalf("ready on an unknown node exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "zzz") || !strings.Contains(stderr, "nova-work help") {
		t.Fatalf("unknown-node refusal = %q, want the node named and the remedy", stderr)
	}
}

func TestPlanCheckReadsAValidPlan(t *testing.T) {
	path := writePlan(t, "(:plan :version 1 (:node :id \"n1\" :kind docs :bespoke \"kept\"))\n")
	var out, errb bytes.Buffer
	code := run([]string{"plan", "check", "--file", path}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "PLAN OK file=") || strings.Count(strings.TrimSpace(out.String()), "\n") != 0 {
		t.Errorf("want exactly one PLAN OK line, got %q", out.String())
	}
}

func TestPlanCheckRefusals(t *testing.T) {
	cases := []struct {
		name string
		body string
		args []string
		want string
	}{
		{
			name: "dispatch macro",
			body: "(:plan :version 1 :goal #.(error \"x\"))\n",
			want: "dispatch macro",
		},
		{
			name: "unknown kind",
			body: "(:plan :version 1 (:node :id \"n1\" :kind bogus))\n",
			want: ":kind",
		},
		{
			name: "max-bytes",
			body: "(:plan :version 1 (:node :id \"n1\" :kind docs))\n",
			args: []string{"--max-bytes", "8"},
			want: "max-bytes",
		},
		{
			name: "max-depth",
			body: "(:plan (:a (:b (:c))))\n",
			args: []string{"--max-depth", "2"},
			want: "max-depth",
		},
		{
			name: "max-nodes",
			body: "(:plan :version 1 :clip :per-node)\n",
			args: []string{"--max-nodes", "2"},
			want: "max-nodes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writePlan(t, tc.body)
			args := append([]string{"plan", "check", "--file", path}, tc.args...)
			var out, errb bytes.Buffer
			code := run(args, &out, &errb)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stderr=%s", code, errb.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("refusal does not name %q: %s", tc.want, errb.String())
			}
			if !strings.Contains(errb.String(), "run: nova-work help") {
				t.Errorf("refusal is not one remedy line: %s", errb.String())
			}
			if out.Len() != 0 {
				t.Errorf("a refusal printed on stdout: %q", out.String())
			}
		})
	}
}

func TestPlanCheckRefusesAMissingFile(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"plan", "check"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--file is required") {
		t.Errorf("missing --file refusal does not name it: %s", errb.String())
	}
}
