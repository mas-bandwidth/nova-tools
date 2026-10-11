//go:build functional

package tablemodel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redisServer names the redis-server the store-backed checks start. The
// program comes from the one helper that skips (and fails under CI) when there
// is none. The socket path must stay under the platform's limit, which a test's
// own temporary directory does not on macOS: the default directory does.
func redisServer(t *testing.T) ServerOptions {
	t.Helper()
	return ServerOptions{RedisServer: testutil.Program(t), Startup: 15 * time.Second}
}

// currentTable is the library the replay captures, the tree's own.
func currentTable(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "pkg", "nsprint", "fn", "lua", "table.lua")
	_, err := os.Stat(p)
	require.NoError(t, err)
	return p
}

func TestWitnessesReproduceAgainstThePinnedLibrary(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	var got []Finding
	err := RunWitnesses(context.Background(), filepath.Join("testdata", "table-pinned.lua"), so, func(f Finding) { got = append(got, f) })
	require.NoError(t, err)
	want := []Finding{
		{"OnePlacePerTable", "confirmed", ""}, {"scope-control", "pass", ""}, {"BindPreservesOwned-removal", "confirmed", ""},
		{"BindPreservesOwned-retained", "confirmed", ""}, {"DropPreservesBound-alias", "confirmed", ""},
		{"CellWritesPreserveBoundSets-alias", "confirmed", ""}, {"controls", "pass", ""}, {"clear-control", "pass", ""},
	}
	require.Equal(t, len(want), len(got), "%d findings, want %d: %v", len(got), len(want), got)
	for i := range want {
		tassert.Equal(t, want[i].Name, got[i].Name, "finding %d = %+v, want %s %s", i, got[i], want[i].Name, want[i].Result)
		tassert.Equal(t, want[i].Result, got[i].Result, "finding %d = %+v, want %s %s", i, got[i], want[i].Name, want[i].Result)
		tassert.NotEmpty(t, got[i].Text, "finding %d = %+v, want %s %s", i, got[i], want[i].Name, want[i].Result)
	}
}

func TestWitnessesFailWhenTheLibraryDoesNotHaveTheDefect(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	// A library with only a stub: the first call the finding makes is refused,
	// and the run says so rather than passing.
	stub := filepath.Join(t.TempDir(), "stub.lua")
	code := "redis.register_function('ns_table_create', function(keys, args) return {'REFUSED'} end)\n"
	err := os.WriteFile(stub, []byte(code), 0o600)
	require.NoError(t, err)
	err = RunWitnesses(context.Background(), stub, so, func(Finding) { tassert.Fail(t, "a finding was reported for a stub") })
	var f *Failure
	require.ErrorAs(t, err, &f, "error = %v, want a failed check", err)
}

func TestAStoreThatCannotStartCannotRun(t *testing.T) {
	t.Parallel()
	err := WithStore(context.Background(), ServerOptions{RedisServer: filepath.Join(t.TempDir(), "no-redis"), TmpDir: t.TempDir()}, func(*Store) {
		tassert.Fail(t, "the check ran without a store")
	})
	var c *CannotRun
	require.ErrorAs(t, err, &c, "error = %v, want CannotRun", err)
}

func TestTheStoreListensOnASocketOnlyAndIsRemoved(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	var dir string
	err := WithStore(context.Background(), so, func(r *Store) {
		pong := r.Cmd("PING")
		tassert.Equal(t, "PONG", pong, "PING = %v", pong)
		cfg := list(r.Cmd("CONFIG", "GET", "port"))
		tassert.Equal(t, "0", cfg[1], "the store listens on TCP port %v", cfg[1])
		dir = str(list(r.Cmd("CONFIG", "GET", "dir"))[1])
	})
	require.NoError(t, err)
	require.NotEmpty(t, dir, "no directory reported")
	_, err = os.Stat(dir)
	require.Error(t, err, "the store's directory %s was left behind", dir)
}

// harnessModels is the tree's tla/, for the two things the replay copies.
func harnessModels() string { return filepath.Join("..", "..", "tla") }

// fakeTLC answers the two harness runs the way TLC does: the real trace passes
// and the corrupted one violates the invariant.
func fakeTLC(t *testing.T, seen *[]tlc.Run) tlc.Executor {
	return func(ctx context.Context, r tlc.Run, log string) int {
		*seen = append(*seen, r)
		out, code := "5 states generated, 5 distinct states found, 0 states left on queue.\nModel checking completed. No error has been found.\n", 0
		if len(*seen) == 2 {
			out, code = "Error: Invariant MatchesExecution is violated.\n5 states generated, 5 distinct states found, 1 states left on queue.\n", 12
		}
		err := os.WriteFile(log, []byte(out), 0o644)
		tassert.NoError(t, err)
		return code
	}
}

func TestReplayCapturesTheRecordedTraceAndReplaysItsReceipts(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	dir := t.TempDir()
	var seen []tlc.Run
	var events []ReplayEvent
	err := RunReplay(ReplayOptions{
		Source: currentTable(t), Jar: "j.jar", Java: "java", Models: harnessModels(), Dir: dir,
		Budget: time.Minute, Server: so, Exec: fakeTLC(t, &seen), OnStep: func(e ReplayEvent) { events = append(events, e) },
	})
	require.NoError(t, err)
	var steps []string
	for _, e := range events {
		steps = append(steps, e.Step)
	}
	want := []string{"capture", "receipt-replay", "mutation-controls", "tlc-execution", "tlc-mutated-observation"}
	require.Equal(t, want, steps, "steps = %v, want %v", steps, want)
	require.Len(t, seen, 2, "TLC runs = %+v", seen)
	require.Equal(t, 1, seen[0].Workers, "TLC runs = %+v", seen)
	require.Equal(t, HarnessName+".cfg", seen[0].Config, "TLC runs = %+v", seen)
	require.Equal(t, HarnessName+".tla", seen[0].Module, "TLC runs = %+v", seen)

	// What this capture saw is what the one recorded on a bench saw: the same
	// states, refusals and model actions for the same 32 calls.
	var got Trace
	raw, err := os.ReadFile(filepath.Join(dir, "trace.json"))
	require.NoError(t, err, "trace.json: %v", err)
	require.NoError(t, json.Unmarshal(raw, &got), "trace.json: %v", err)
	wantTrace := loadTrace(t)
	require.Equal(t, wantTrace.Initial, got.Initial, "the initial state or the number of steps differs from the recorded trace")
	require.Equal(t, len(wantTrace.Steps), len(got.Steps), "the initial state or the number of steps differs from the recorded trace")
	for i := range wantTrace.Steps {
		g, w := got.Steps[i], wantTrace.Steps[i]
		tassert.Equal(t, w.State, g.State, "step %d (%s) differs from the recorded trace", i, w.Verb)
		tassert.Equal(t, w.Model, g.Model, "step %d (%s) differs from the recorded trace", i, w.Verb)
		tassert.Equal(t, w.Refused, g.Refused, "step %d (%s) differs from the recorded trace", i, w.Verb)
		tassert.Equal(t, w.Receipt == nil, g.Receipt == nil, "step %d (%s) differs from the recorded trace", i, w.Verb)
	}
	// The harness a bench generated is the harness written now.
	written, err := os.ReadFile(filepath.Join(dir, "execution", HarnessName+".tla"))
	require.NoError(t, err, "the generated harness differs from the recorded one: %v", err)
	require.Equal(t, string(replayFile(t, "MemberReceiptReplay.tla")), string(written), "the generated harness differs from the recorded one: %v", err)
	mutated, err := os.ReadFile(filepath.Join(dir, "mutated-observation", HarnessName+".tla"))
	require.NoError(t, err, "the mutated harness differs from the recorded one: %v", err)
	require.Equal(t, string(replayFile(t, "MemberReceiptReplay.mutated.tla")), string(mutated), "the mutated harness differs from the recorded one: %v", err)
}

func TestReplayRefusesAHarnessTLCDoesNotAccept(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	// TLC reports a violation on the real trace: the model and the store part.
	violating := func(ctx context.Context, r tlc.Run, log string) int {
		_ = os.WriteFile(log, []byte("Error: Invariant MatchesExecution is violated.\n"), 0o644)
		return 12
	}
	err := RunReplay(ReplayOptions{Source: currentTable(t), Jar: "j", Java: "java", Models: harnessModels(), Dir: t.TempDir(),
		Budget: time.Minute, Server: so, Exec: violating})
	require.ErrorContains(t, err, "execution harness", "error = %v", err)
	// TLC accepts the corrupted observation: the check could not have caught it.
	passing := func(ctx context.Context, r tlc.Run, log string) int {
		_ = os.WriteFile(log, []byte("1 states generated, 1 distinct states found, 0 states left on queue.\nModel checking completed. No error has been found.\n"), 0o644)
		return 0
	}
	err = RunReplay(ReplayOptions{Source: currentTable(t), Jar: "j", Java: "java", Models: harnessModels(), Dir: t.TempDir(),
		Budget: time.Minute, Server: so, Exec: passing})
	require.ErrorContains(t, err, "mutated-observation harness", "error = %v", err)
}

func TestReplayNeedsItsInputs(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	for name, o := range map[string]ReplayOptions{
		"a missing source":    {Source: filepath.Join(t.TempDir(), "no.lua"), Models: harnessModels()},
		"a missing model dir": {Source: currentTable(t), Models: filepath.Join(t.TempDir(), "none")},
	} {
		o.Dir, o.Budget, o.Server, o.Jar, o.Java = t.TempDir(), time.Minute, so, "j", "java"
		var c *CannotRun
		err := RunReplay(o)
		tassert.ErrorAs(t, err, &c, "%s: error = %v, want CannotRun", name, err)
	}
}

// A read in progress ends with the budget: a command that blocks in the store
// for thirty seconds, under a budget of ten, is a failed check. Were the budget
// not to reach the socket, the store would answer nil after thirty seconds and
// the check would not fail; the test asserts that event, not the clock.
func TestASlowCommandUnderAShortBudgetFailsWithinTheBudget(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := WithStore(ctx, so, func(r *Store) { r.Cmd("BLPOP", "no-such-list", 30) })
	var f *Failure
	require.ErrorAs(t, err, &f, "error = %v, want a failed check when the budget ends first", err)
}
