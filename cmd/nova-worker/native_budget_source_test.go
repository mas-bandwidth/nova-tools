//go:build slow || functional

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A BUDGET NEEDS A SOURCE THE TOOL CAN READ, AND THAT IS CHECKED BEFORE ANYTHING IS MADE
// (SPEC-WORKER rule 13d, demanded test 13d, issue #1545). Slice 2 of the cap.
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
	require.NoError(t, os.WriteFile(desc["key_file"].(string), []byte("a fake key\n"), 0o600))
	raw, err := json.MarshalIndent(desc, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "worker.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))
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
		_, err := os.Stat(made)
		assert.Error(t, err, "the refusal made %s; rule 13d refuses before any directory is made", made)
	}
}
