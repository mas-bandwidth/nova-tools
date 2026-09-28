//go:build functional

package tablemodel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
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
	p := filepath.Join("..", "nsprint", "fn", "lua", "table.lua")
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWitnessesReproduceAgainstThePinnedLibrary(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	var got []Finding
	err := RunWitnesses(context.Background(), filepath.Join("testdata", "table-pinned.lua"), so, func(f Finding) { got = append(got, f) })
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{
		{"OnePlacePerTable", "confirmed", ""}, {"scope-control", "pass", ""}, {"BindPreservesOwned-removal", "confirmed", ""},
		{"BindPreservesOwned-retained", "confirmed", ""}, {"DropPreservesBound-alias", "confirmed", ""},
		{"CellWritesPreserveBoundSets-alias", "confirmed", ""}, {"controls", "pass", ""}, {"clear-control", "pass", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("%d findings, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Result != want[i].Result || got[i].Text == "" {
			t.Errorf("finding %d = %+v, want %s %s", i, got[i], want[i].Name, want[i].Result)
		}
	}
}

func TestWitnessesFailWhenTheLibraryDoesNotHaveTheDefect(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	// A library with only a stub: the first call the finding makes is refused,
	// and the run says so rather than passing.
	stub := filepath.Join(t.TempDir(), "stub.lua")
	code := "redis.register_function('ns_table_create', function(keys, args) return {'REFUSED'} end)\n"
	if err := os.WriteFile(stub, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RunWitnesses(context.Background(), stub, so, func(Finding) { t.Error("a finding was reported for a stub") })
	var f *Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v, want a failed check", err)
	}
}

func TestAStoreThatCannotStartCannotRun(t *testing.T) {
	t.Parallel()
	err := WithStore(context.Background(), ServerOptions{RedisServer: filepath.Join(t.TempDir(), "no-redis"), TmpDir: t.TempDir()}, func(*Store) {
		t.Error("the check ran without a store")
	})
	var c *CannotRun
	if !errors.As(err, &c) {
		t.Fatalf("error = %v, want CannotRun", err)
	}
}

func TestTheStoreListensOnASocketOnlyAndIsRemoved(t *testing.T) {
	t.Parallel()
	so := redisServer(t)
	var dir string
	err := WithStore(context.Background(), so, func(r *Store) {
		if pong := r.Cmd("PING"); pong != "PONG" {
			t.Errorf("PING = %v", pong)
		}
		cfg := list(r.Cmd("CONFIG", "GET", "port"))
		if cfg[1] != "0" {
			t.Errorf("the store listens on TCP port %v", cfg[1])
		}
		dir = str(list(r.Cmd("CONFIG", "GET", "dir"))[1])
	})
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("no directory reported")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("the store's directory %s was left behind", dir)
	}
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
		if err := os.WriteFile(log, []byte(out), 0o644); err != nil {
			t.Error(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, e := range events {
		steps = append(steps, e.Step)
	}
	if want := []string{"capture", "receipt-replay", "mutation-controls", "tlc-execution", "tlc-mutated-observation"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	if len(seen) != 2 || seen[0].Workers != 1 || seen[0].Config != HarnessName+".cfg" || seen[0].Module != HarnessName+".tla" {
		t.Fatalf("TLC runs = %+v", seen)
	}

	// What this capture saw is what the one recorded on a bench saw: the same
	// states, refusals and model actions for the same 32 calls.
	var got Trace
	raw, err := os.ReadFile(filepath.Join(dir, "trace.json"))
	if err != nil || json.Unmarshal(raw, &got) != nil {
		t.Fatalf("trace.json: %v", err)
	}
	want := loadTrace(t)
	if !reflect.DeepEqual(got.Initial, want.Initial) || len(got.Steps) != len(want.Steps) {
		t.Fatal("the initial state or the number of steps differs from the recorded trace")
	}
	for i := range want.Steps {
		g, w := got.Steps[i], want.Steps[i]
		if !reflect.DeepEqual(g.State, w.State) || g.Model != w.Model || !reflect.DeepEqual(g.Refused, w.Refused) || (g.Receipt == nil) != (w.Receipt == nil) {
			t.Errorf("step %d (%s) differs from the recorded trace", i, w.Verb)
		}
	}
	// The harness a bench generated is the harness written now.
	written, err := os.ReadFile(filepath.Join(dir, "execution", HarnessName+".tla"))
	if err != nil || string(written) != string(replayFile(t, "MemberReceiptReplay.tla")) {
		t.Fatalf("the generated harness differs from the recorded one: %v", err)
	}
	mutated, err := os.ReadFile(filepath.Join(dir, "mutated-observation", HarnessName+".tla"))
	if err != nil || string(mutated) != string(replayFile(t, "MemberReceiptReplay.mutated.tla")) {
		t.Fatalf("the mutated harness differs from the recorded one: %v", err)
	}
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
	if err == nil || !strings.Contains(err.Error(), "execution harness") {
		t.Fatalf("error = %v", err)
	}
	// TLC accepts the corrupted observation: the check could not have caught it.
	passing := func(ctx context.Context, r tlc.Run, log string) int {
		_ = os.WriteFile(log, []byte("1 states generated, 1 distinct states found, 0 states left on queue.\nModel checking completed. No error has been found.\n"), 0o644)
		return 0
	}
	err = RunReplay(ReplayOptions{Source: currentTable(t), Jar: "j", Java: "java", Models: harnessModels(), Dir: t.TempDir(),
		Budget: time.Minute, Server: so, Exec: passing})
	if err == nil || !strings.Contains(err.Error(), "mutated-observation harness") {
		t.Fatalf("error = %v", err)
	}
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
		if err := RunReplay(o); !errors.As(err, &c) {
			t.Errorf("%s: error = %v, want CannotRun", name, err)
		}
	}
}
