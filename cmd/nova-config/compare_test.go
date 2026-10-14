package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		require.NoError(t, config.WriteSnapshot(dir, config.CmpSnap{At: at, Tool: "1.2.24", Kinds: map[string]config.CmpKind{
			config.KindProvider: {Kind: config.KindProvider, Rows: rows},
		}}))
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
		assert.Equal(t, 1, strings.Count(out, "COMPARE SAME"), "one per kind plus the last line")
	})

	// A missing --after is a usage refusal, exit 2.
	t.Run("a missing after dir is usage", func(t *testing.T) {
		t.Parallel()
		h := seed(t)
		code, _, errs := h.run(t, "compare", "--before", t.TempDir())
		require.Equal(t, 2, code)
		assert.Contains(t, errs, "--after is required")
	})
}
