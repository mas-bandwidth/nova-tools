package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotCompareJSONUsageRefusals keeps parser and positional failures in
// the same structured envelope as the verbs' own validation refusals.
func TestSnapshotCompareJSONUsageRefusals(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"snapshot", "compare"} {
		for _, suffix := range [][]string{{"--unknown"}, {"extra"}} {
			args := append([]string{verb, "--json"}, suffix...)
			code, out, errs := newHarness().run(t, args...)
			require.Equal(t, 2, code, "%v: %s", args, errs)
			assert.Empty(t, errs)
			var result struct {
				Result struct {
					Status string   `json:"status"`
					Exit   int      `json:"exit"`
					Why    []string `json:"why"`
					Remedy string   `json:"remedy"`
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &result), "%v: %s", args, out)
			assert.Equal(t, "refused", result.Result.Status)
			assert.Equal(t, 2, result.Result.Exit)
			assert.NotEmpty(t, result.Result.Why)
			assert.Contains(t, result.Result.Remedy, "nova-config")
		}
	}
}

// TestSnapshotVerbFeedsCompare exercises the read-only file contract from the
// verb through compare with an injected capture clock and in-memory history.
func TestSnapshotVerbFeedsCompare(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.Now = func() time.Time { return time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC) }
	_, err := h.store.Insert(ctx, config.KindProvider, config.Row{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}, config.MigrationActor)
	require.NoError(t, err)
	take := func(at time.Time, jsonOutput bool) string {
		t.Helper()
		dir := t.TempDir()
		d := h.deps()
		d.now = func() time.Time { return at }
		args := []string{"snapshot", "--out", dir}
		if jsonOutput {
			args = append(args, "--json")
		}
		var out, errs bytes.Buffer
		require.Zero(t, run(args, &out, &errs, d), errs.String())
		if jsonOutput {
			assert.Contains(t, out.String(), `"at":"2026-10-13T22:00:00Z"`)
		} else {
			assert.Contains(t, out.String(), "CONFIG SNAPSHOT")
		}
		snap, err := config.LoadSnapshot(dir)
		require.NoError(t, err)
		assert.Equal(t, at.Format(time.RFC3339), snap.At)
		assert.Equal(t, len(config.Kinds), len(snap.Kinds))
		return dir
	}
	before := take(time.Date(2026, 10, 13, 20, 0, 0, 0, time.UTC), false)
	h.store.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) }
	_, _, err = h.store.Update(ctx, config.KindProvider, "p", map[string]string{"funded": "true"}, "operator")
	require.NoError(t, err)
	after := take(time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC), true)
	code, out, errs := h.run(t, "compare", "--before", before, "--after", after)
	require.Zero(t, code, errs)
	assert.Contains(t, out, "OPERATOR-DIFF provider p")
}

// TestCompareMaxKeepsFullVerdictAndCounts pins a presentation ceiling without
// changing the comparison's full verdict or hiding the omitted row count.
func TestCompareMaxKeepsFullVerdictAndCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	before, err := config.TakeSnapshot(ctx, h.store, "2026-10-13T20:00:00Z", "test")
	require.NoError(t, err)
	after, err := config.TakeSnapshot(ctx, h.store, "2026-10-13T22:00:00Z", "test")
	require.NoError(t, err)
	for _, name := range []string{"a", "b", "c"} {
		before.Kinds[config.KindProvider] = config.CmpKind{Kind: config.KindProvider, Rows: append(before.Kinds[config.KindProvider].Rows, config.CmpRow{Name: name, Fields: map[string]string{"funded": "false"}})}
		after.Kinds[config.KindProvider] = config.CmpKind{Kind: config.KindProvider, Rows: append(after.Kinds[config.KindProvider].Rows, config.CmpRow{Name: name, Fields: map[string]string{"funded": "true"}})}
	}
	bdir, adir := t.TempDir(), t.TempDir()
	require.NoError(t, config.WriteSnapshot(bdir, before))
	require.NoError(t, config.WriteSnapshot(adir, after))
	args := []string{"compare", "--before", bdir, "--after", adir, "--max", "1"}
	code, lines, errs := h.run(t, args...)
	require.Equal(t, 1, code, errs)
	assert.Contains(t, lines, "CRIT-DIFF provider a")
	assert.NotContains(t, lines, "CRIT-DIFF provider b")
	assert.Contains(t, lines, "COMPARE MORE kind=row shown=1 total=3")
	assert.True(t, strings.HasSuffix(lines, "COMPARE CRIT\n"))
	code, out, errs := h.run(t, append(args, "--json")...)
	require.Equal(t, 1, code, errs)
	var result struct {
		Facts map[string]any `json:"facts"`
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
		More []struct {
			Kind  string `json:"kind"`
			Shown int    `json:"shown"`
			Total int    `json:"total"`
		} `json:"more"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, float64(3), result.Facts["rows"])
	var shownRows int
	for _, item := range result.Items {
		if item.Kind == "row" {
			shownRows++
		}
	}
	assert.Equal(t, 1, shownRows)
	require.Len(t, result.More, 1)
	assert.Equal(t, "row", result.More[0].Kind)
	assert.Equal(t, 1, result.More[0].Shown)
	assert.Equal(t, 3, result.More[0].Total)
	code, all, errs := h.run(t, "compare", "--before", bdir, "--after", adir, "--max", "0")
	require.Equal(t, 1, code, errs)
	assert.Equal(t, 3, strings.Count(all, "CRIT-DIFF provider"))
	code, _, errs = h.run(t, "compare", "--before", bdir, "--after", adir, "--max", "-1")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--max must be zero or more")
}

// TestCompareJSONIncludesChangedFieldValues pins both renderings of one
// comparison result (docs/SPEC-CONFIG.md, "Snapshot and compare").
func TestCompareJSONIncludesChangedFieldValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.Now = func() time.Time { return time.Date(2026, 10, 13, 19, 0, 0, 0, time.UTC) }
	_, err := h.store.Insert(ctx, config.KindProvider, config.Row{Name: "p", Fields: map[string]string{"billing": "metered", "funded": "false"}}, config.MigrationActor)
	require.NoError(t, err)
	before, err := config.TakeSnapshot(ctx, h.store, "2026-10-13T20:00:00Z", "test")
	require.NoError(t, err)
	h.store.Now = func() time.Time { return time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC) }
	_, _, err = h.store.Update(ctx, config.KindProvider, "p", map[string]string{"funded": "true"}, "operator")
	require.NoError(t, err)
	after, err := config.TakeSnapshot(ctx, h.store, "2026-10-13T22:00:00Z", "test")
	require.NoError(t, err)
	bdir, adir := t.TempDir(), t.TempDir()
	require.NoError(t, config.WriteSnapshot(bdir, before))
	require.NoError(t, config.WriteSnapshot(adir, after))
	code, lines, errs := h.run(t, "compare", "--before", bdir, "--after", adir)
	require.Zero(t, code, errs)
	assert.Contains(t, lines, "funded false -> true")
	code, out, errs := h.run(t, "compare", "--before", bdir, "--after", adir, "--json")
	require.Zero(t, code, errs)
	var result struct {
		Items []struct {
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	var changed []config.FieldDiff
	for _, item := range result.Items {
		if raw, ok := item.Fields["fields"]; ok {
			require.NoError(t, json.Unmarshal(raw, &changed))
		}
	}
	assert.Equal(t, []config.FieldDiff{{Field: "funded", Before: "false", After: "true"}}, changed)
}

// TestCompareVerbNamesTheActBehindAChangedRow drives the compare verb against
// snapshot directories under t.TempDir() and checks its lines, exit codes and
// --json. The store is the harness's Mem, whose clock the test holds, so the
// history rows it writes fall inside the before/after window.
func TestCompareVerbNamesTheActBehindAChangedRow(t *testing.T) {
	t.Parallel()

	mustTime := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return ts
	}
	ctx := context.Background()
	provider := func(funded string) config.CmpRow {
		return config.CmpRow{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": funded}}
	}
	seed := func(t *testing.T) *harness {
		t.Helper()
		h := newHarness()
		h.env["NOVA_PG_DSN"] = dsn
		h.store.Now = func() time.Time { return mustTime("2026-10-13T20:00:00Z") }
		_, err := h.store.Insert(ctx, config.KindProvider, config.Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, config.MigrationActor)
		require.NoError(t, err)
		return h
	}
	snap := func(t *testing.T, at string, rows ...config.CmpRow) string {
		t.Helper()
		dir := t.TempDir()
		all, err := config.TakeSnapshot(ctx, config.NewMem(), at, "1.2.24")
		require.NoError(t, err)
		all.Kinds[config.KindProvider] = config.CmpKind{Kind: config.KindProvider, Rows: rows}
		require.NoError(t, config.WriteSnapshot(dir, all))
		return dir
	}

	// The OpenRouter case: a top-up by the owner inside the window is an
	// OPERATOR-DIFF, exit 0, never a failure.
	t.Run("operator act exits 0", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		before := snap(t, "2026-10-13T20:00:00Z", provider("false"))
		h.store.Now = func() time.Time { return mustTime("2026-10-13T21:40:12Z") }
		_, _, err := h.store.Update(config.WithReason(ctx, "top-up (funded false -> true)"), config.KindProvider, "openrouter", map[string]string{"funded": "true"}, "the-owner")
		require.NoError(t, err)
		after := snap(t, "2026-10-13T22:00:00Z", provider("true"))

		code, out, errs := h.run(t, "compare", "--before", before, "--after", after)
		require.Zero(t, code, "an operator act is not a failure\nstdout: %s\nstderr: %s", out, errs)
		assert.Contains(t, out, "OPERATOR-DIFF provider openrouter by the-owner at 2026-10-13T21:40:12Z: top-up (funded false -> true)")
		assert.Contains(t, out, "COMPARE provider OPERATOR")
		assert.Contains(t, out, "COMPARE OPERATOR")
		assert.Equal(t, 1, strings.Count(out, "OPERATOR-DIFF"), "each differing row's line is printed once\n%s", out)

		code, out, _ = h.run(t, "compare", "--json", "--before", before, "--after", after)
		require.Zero(t, code)
		assert.Contains(t, out, `"class":"OPERATOR-DIFF"`)
		assert.Contains(t, out, `"actor":"the-owner"`)
	})

	// A difference only the migration actor explains, or none at all, is a
	// CRIT-DIFF, exit 1.
	t.Run("only the migration actor is CRIT", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		before := snap(t, "2026-10-13T20:00:00Z", provider("false"))
		h.store.Now = func() time.Time { return mustTime("2026-10-13T21:40:12Z") }
		_, _, err := h.store.Update(ctx, config.KindProvider, "openrouter", map[string]string{"funded": "true"}, config.MigrationActor)
		require.NoError(t, err)
		after := snap(t, "2026-10-13T22:00:00Z", provider("true"))

		code, out, errs := h.run(t, "compare", "--before", before, "--after", after)
		require.Equal(t, 1, code, "a migration-only change is CRIT\nstdout: %s\nstderr: %s", out, errs)
		assert.Contains(t, out, "CRIT-DIFF provider openrouter unexplained: funded")
		assert.Contains(t, out, "COMPARE CRIT")

		// The --json form carries the same exit as the lines: a CRIT is exit 1
		// in both renderings, on the same fixtures.
		code, out, _ = h.run(t, "compare", "--json", "--before", before, "--after", after)
		require.Equal(t, 1, code, "a CRIT is exit 1 in --json too\nstdout: %s", out)
		assert.Contains(t, out, `"class":"CRIT-DIFF"`)
		assert.Contains(t, out, `"exit":1`)
	})

	// A row the operator added between the snapshots (present after, absent
	// before) is an OPERATOR-ADD, named with the actor, time and reason, and
	// never a failure; a row only the migration actor added, or with no
	// history, is CRIT.
	t.Run("an operator-added row is OPERATOR-ADD", func(t *testing.T) {
		t.Parallel()
		h := newHarness()
		h.env["NOVA_PG_DSN"] = dsn
		before := snap(t, "2026-10-13T20:00:00Z")
		h.store.Now = func() time.Time { return mustTime("2026-10-13T21:40:12Z") }
		_, err := h.store.Insert(config.WithReason(ctx, "add openrouter"), config.KindProvider, config.Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, "the-owner")
		require.NoError(t, err)
		after := snap(t, "2026-10-13T22:00:00Z", provider("false"))

		code, out, errs := h.run(t, "compare", "--before", before, "--after", after)
		require.Zero(t, code, "an operator add is not a failure\nstdout: %s\nstderr: %s", out, errs)
		assert.Contains(t, out, "OPERATOR-ADD provider openrouter by the-owner at 2026-10-13T21:40:12Z: add openrouter")
		assert.Contains(t, out, "COMPARE provider OPERATOR")
		assert.Contains(t, out, "COMPARE OPERATOR")
		assert.Equal(t, 1, strings.Count(out, "OPERATOR-ADD"), "the added row's line is printed once\n%s", out)
	})

	t.Run("an added row with only the migration actor is CRIT", func(t *testing.T) {
		t.Parallel()
		h := newHarness()
		h.env["NOVA_PG_DSN"] = dsn
		before := snap(t, "2026-10-13T20:00:00Z")
		h.store.Now = func() time.Time { return mustTime("2026-10-13T21:00:00Z") }
		_, err := h.store.Insert(ctx, config.KindProvider, config.Row{Name: "openrouter", Fields: map[string]string{"billing": "metered", "funded": "false"}}, config.MigrationActor)
		require.NoError(t, err)
		after := snap(t, "2026-10-13T22:00:00Z", provider("false"))

		code, out, errs := h.run(t, "compare", "--before", before, "--after", after)
		require.Equal(t, 1, code, "a migration-only add is CRIT\nstdout: %s\nstderr: %s", out, errs)
		assert.Contains(t, out, "CRIT-DIFF provider openrouter")
		assert.Contains(t, out, "COMPARE CRIT")
	})

	// Identical snapshots are SAME, exit 0.
	t.Run("identical snapshots are SAME", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		dir := snap(t, "2026-10-13T20:00:00Z", provider("false"))
		code, out, errs := h.run(t, "compare", "--before", dir, "--after", dir)
		require.Zero(t, code, "identical snapshots are SAME\nstdout: %s\nstderr: %s", out, errs)
		assert.Contains(t, out, "COMPARE provider SAME")
		assert.Contains(t, out, "COMPARE SAME")
		assert.Equal(t, 9, strings.Count(out, "COMPARE "), "one per kind plus the last line")
	})

	// A missing --after is a usage refusal, exit 2.
	t.Run("a missing after dir is usage", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		code, _, errs := h.run(t, "compare", "--before", t.TempDir())
		require.Equal(t, 2, code)
		assert.Contains(t, errs, "--after is required")
	})

	t.Run("a missing kind file refuses in both renderings", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		before := snap(t, "2026-10-13T20:00:00Z", provider("false"))
		after := snap(t, "2026-10-13T22:00:00Z", provider("false"))
		require.NoError(t, os.Remove(filepath.Join(after, "provider.json")))
		for _, jsonFlag := range []bool{false, true} {
			args := []string{"compare", "--before", before, "--after", after}
			if jsonFlag {
				args = append(args, "--json")
			}
			code, out, errs := h.run(t, args...)
			assert.Equal(t, 2, code)
			assert.Contains(t, out+errs, "provider.json")
		}
	})

	t.Run("a removed row has matching text and JSON verdicts", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		before := snap(t, "2026-10-13T20:00:00Z", provider("false"))
		h.store.Now = func() time.Time { return mustTime("2026-10-13T21:00:00Z") }
		_, err := h.store.Delete(ctx, config.KindProvider, "openrouter", "operator")
		require.NoError(t, err)
		after := snap(t, "2026-10-13T22:00:00Z")
		code, lines, errs := h.run(t, "compare", "--before", before, "--after", after)
		require.Zero(t, code, errs)
		assert.Contains(t, lines, "OPERATOR-DIFF provider openrouter removed by operator")
		assert.Equal(t, 1, strings.Count(lines, "OPERATOR-DIFF provider openrouter"))
		code, out, errs := h.run(t, "compare", "--before", before, "--after", after, "--json")
		require.Zero(t, code, errs)
		assert.Contains(t, out, `"removed":true`)
		assert.Contains(t, out, `"class":"OPERATOR-DIFF"`)
	})
}
