package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A BUDGET NEEDS A SOURCE THE TOOL CAN READ, AND THAT IS CHECKED BEFORE ANYTHING IS MADE
// (SPEC-SWARM rule 13d, demanded test 13d, issue #1545). Slice 2 of the cap.
//
// The clauses this file holds:
//
//	"`native` accepts `--usage-interval`, and refuses at exit 2 one under a second and one
//	 not shorter than `--deadline`"
//	"a numeric budget beside `usage: none`, or with no `sqlite3` on `PATH`, is
//	 `NATIVE REFUSED` at exit 2 before any directory is made, so is `--tokens unmetered`
//	 beside a description that sets `max_turns` under either condition, and `unmetered`
//	 with no such description runs under both"
//
// THE REASON IS RULE 13's OWN, quoted in 13d: a budget nothing can observe is a promise the
// tool cannot keep. The refusal is BEFORE any directory because a card refused after its
// job directory was made leaves the bench's hygiene pass a job that never ran.

// budgetWorker writes a worker description with the usage source and card budget a case
// wants, pinned to the model every test here launches.
func budgetWorker(t *testing.T, usage string, maxTurns, maxCacheRead int) string {
	t.Helper()
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": "fake-model",
		"env_var": "CAP_BUDGET_ENV", "key_file": filepath.Join(t.TempDir(), "key"),
		"usage": usage, "harness": "fake-harness", "worker_dir": t.TempDir(),
		"deadline":     "30s",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
	}
	if maxTurns > 0 {
		desc["max_turns"] = maxTurns
	}
	if maxCacheRead > 0 {
		desc["max_cache_read"] = maxCacheRead
	}
	if err := os.WriteFile(desc["key_file"].(string), []byte("a fake key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// noSQLiteOnPath puts a PATH in front of this test whose only entry is an empty directory,
// so `sqlite3` cannot be resolved. It is what a bench with no reader looks like from inside
// the process, without uninstalling anything.
func noSQLiteOnPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// madeNothing fails when a refusal left any of the run's own directories behind.
func madeNothing(t *testing.T, slot string) {
	t.Helper()
	for _, made := range []string{
		filepath.Join(slot, "jobs"),
		filepath.Join(slot, "data"),
		filepath.Join(slot, "tmp"),
	} {
		if _, err := os.Stat(made); err == nil {
			t.Errorf("the refusal made %s; rule 13d refuses before any directory is made", made)
		}
	}
}

// TestNativeRefusesABudgetNothingCanObserve is the heart of slice 2. Each case names what
// the caller asked for and what the bench could offer, and every one of them is
// `NATIVE REFUSED` at exit 2 with nothing made.
func TestNativeRefusesABudgetNothingCanObserve(t *testing.T) {
	bin := nativeHarness(t)
	t.Setenv("CAP_BUDGET_ENV", "a fake key")
	for _, tc := range []struct {
		name      string
		tokens    string
		usage     string // "" means no --worker at all, which is `opencode` by rule 13d
		maxTurns  int
		maxCache  int
		noSQLite  bool
		wantRefus bool
		names     string // a word the refusal must carry
	}{
		// A NUMBER BESIDE A SOURCE THAT REPORTS NOTHING.
		{name: "numeric_beside_usage_none", tokens: "100000", usage: swarm.UsageNone, wantRefus: true, names: "usage"},
		// A NUMBER ON A BENCH WITH NO READER. There is no --worker here, so the source is
		// `opencode` by rule 13d -- and `opencode` is read with sqlite3.
		{name: "numeric_without_sqlite3", tokens: "100000", noSQLite: true, wantRefus: true, names: swarm.SQLiteBinary},
		// THE CARD'S OWN BUDGET IS READ FROM THE SAME SOURCE, so it meets the same refusal
		// WHATEVER --tokens says: decision 19's careless caller, who typed `unmetered` and
		// a max_turns and would otherwise have had a cap nothing enforced.
		{name: "max_turns_unmetered_beside_usage_none", tokens: "unmetered", usage: swarm.UsageNone, maxTurns: 40, wantRefus: true, names: "max_turns"},
		{name: "max_cache_read_unmetered_beside_usage_none", tokens: "unmetered", usage: swarm.UsageNone, maxCache: 900000, wantRefus: true, names: "max_cache_read"},
		{name: "max_turns_unmetered_without_sqlite3", tokens: "unmetered", usage: swarm.UsageOpenCode, maxTurns: 40, noSQLite: true, wantRefus: true, names: "max_turns"},
		// AND `unmetered` WITH NO SUCH DESCRIPTION RUNS UNDER BOTH, "as it does today".
		{name: "unmetered_beside_usage_none_runs", tokens: "unmetered", usage: swarm.UsageNone},
		{name: "unmetered_without_sqlite3_runs", tokens: "unmetered", usage: swarm.UsageOpenCode, noSQLite: true},
		{name: "unmetered_no_worker_without_sqlite3_runs", tokens: "unmetered", noSQLite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := budgetCard(t, root)
			args := budgetNativeArgs(t, bin, card, slot, root, tc.tokens)
			if tc.usage != "" || tc.maxTurns > 0 || tc.maxCache > 0 {
				usage := tc.usage
				if usage == "" {
					usage = swarm.UsageOpenCode
				}
				args = append(args, "--worker", budgetWorker(t, usage, tc.maxTurns, tc.maxCache))
			}
			// The PATH is emptied LAST, after every fixture that needed a real one has
			// been built, so the only thing this takes away is the usage reader.
			if tc.noSQLite {
				noSQLiteOnPath(t)
			}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if !tc.wantRefus {
				if rc != 0 {
					t.Fatalf("this launch runs, got exit %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
				}
				return
			}
			if rc != 2 {
				t.Fatalf("this launch is NATIVE REFUSED at exit 2, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "NATIVE REFUSED") {
				t.Errorf("the refusal is a NATIVE REFUSED line:\n%s", stderr.String())
			}
			if tc.names != "" && !strings.Contains(stderr.String(), tc.names) {
				t.Errorf("the refusal names %q:\n%s", tc.names, stderr.String())
			}
			madeNothing(t, slot)
		})
	}
}

// TestNativeUsageIntervalFloorAndCeiling: rule 13d, "On `native` an interval under one
// second, or one not shorter than `--deadline`, is refused at exit 2: the first could end
// an honest card on three quick reads, and under the second no sample would ever run."
func TestNativeUsageIntervalFloorAndCeiling(t *testing.T) {
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name     string
		interval string
		deadline string
		want     int
	}{
		{"under_a_second", "900ms", "30s", 2},
		{"exactly_the_deadline", "30s", "30s", 2},
		{"longer_than_the_deadline", "45s", "30s", 2},
		{"a_second_exactly_is_the_floor", "1s", "30s", 0},
		{"shorter_than_the_deadline_runs", "2s", "30s", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := budgetCard(t, root)
			args := budgetNativeArgs(t, bin, card, slot, root, "unmetered")
			// The deadline the case names replaces the helper's own.
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = tc.deadline
				}
			}
			args = append(args, "--usage-interval", tc.interval)
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != tc.want {
				t.Fatalf("--usage-interval %s beside --deadline %s is exit %d, got %d\nstdout:\n%s\nstderr:\n%s",
					tc.interval, tc.deadline, tc.want, rc, stdout.String(), stderr.String())
			}
			if tc.want == 2 {
				if !strings.Contains(stderr.String(), "--usage-interval") {
					t.Errorf("the refusal names --usage-interval:\n%s", stderr.String())
				}
				madeNothing(t, slot)
			}
		})
	}
}
