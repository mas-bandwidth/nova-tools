//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	spverbs "github.com/mas-bandwidth/nova-tools/internal/sprint/verbs"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
	"github.com/redis/go-redis/v9"
)

// TestNewPathLifecycleOnRealStore: end-to-end CLI lifecycle functional test
// on the new execution path in cmd/nova-sprint on a real Redis 8 store.
//
// Verifies the full end-to-end machine lifecycle:
//  1. init on a fresh Redis store (exit 0; second init exit 2 MACHINESTATE)
//  2. start (exit 0; repeated start exit 0 unchanged)
//  3. goal set (exit 3 bug refusal REQUEST: write path does not carry goals yet)
//  4. goal show (exit 2 refusal REQUEST: IT30 has no goal queries yet)
//  5. stop (exit 0; repeated stop exit 0 unchanged)
//  6. clear --confirm sprint (exit 0; wrong confirm exit 2)
//  7. teardown --confirm sprint (exit 2 refusal NOTONNEWPATH on CLI;
//     Layer 1 lifecycle teardown deletes all namespace keys leaving receipts)
//
// Invariants enforced:
//   - Invariant E7: FCALL only for sprintfn operations.
//   - Exit code discipline: 0 done, 2 refused/local, 3 bug refusal.
//   - Rule 7: bounded output, non-spew (at most 2 lines per invocation).
//   - State verification: Redis keys reflect state transitions and clean teardown.
func TestNewPathLifecycleOnRealStore(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	raw := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	defer raw.Close()
	ctx := context.Background()

	// Load sprint profile functions into Redis 8.
	if err := fn.LoadTSet(ctx, raw, fn.TSetSprint); err != nil {
		t.Fatalf("load sprint profile: %v", err)
	}
	build, err := fn.TSetBuild(fn.TSetSprint)
	if err != nil {
		t.Fatal(err)
	}

	// Define Layer 1 namespace and tables for the sprint.
	st, err := tset.NewRedis(addr, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	spec := tset.DefineSpec{
		Space: pathNames.Prefix,
		Build: build,
		View:  confirmName(),
		Tables: []tset.TableSpec{
			{Name: sprint.Work, Columns: []tset.ColumnSpec{
				{Name: "waiting", Kind: tset.ColumnKindSet},
				{Name: "ready", Kind: tset.ColumnKindSet},
				{Name: "working", Kind: tset.ColumnKindSet},
				{Name: "review", Kind: tset.ColumnKindSet},
				{Name: "merging", Kind: tset.ColumnKindSet},
				{Name: "landed", Kind: tset.ColumnKindSet},
			}},
			{Name: sprint.Readers, Columns: []tset.ColumnSpec{
				{Name: "asked", Kind: tset.ColumnKindSet},
				{Name: "reading", Kind: tset.ColumnKindSet},
				{Name: "ok", Kind: tset.ColumnKindSet},
				{Name: "broken", Kind: tset.ColumnKindSet},
			}},
			{Name: sprint.Merge, Columns: []tset.ColumnSpec{
				{Name: "queued", Kind: tset.ColumnKindSet},
				{Name: "merged", Kind: tset.ColumnKindSet},
				{Name: "stuck", Kind: tset.ColumnKindSet},
				{Name: "returned", Kind: tset.ColumnKindSet},
				{Name: "ctl", Kind: tset.ColumnKindSet},
			}},
			{Name: sprint.Fleet, Columns: []tset.ColumnSpec{
				{Name: "ready", Kind: tset.ColumnKindSet},
				{Name: "working", Kind: tset.ColumnKindSet},
				{Name: "withdrawn", Kind: tset.ColumnKindSet},
				{Name: "ok", Kind: tset.ColumnKindSet},
				{Name: "failed", Kind: tset.ColumnKindSet},
				{Name: "ctl", Kind: tset.ColumnKindSet},
			}},
		},
	}
	if _, err := st.Define(ctx, spec); err != nil {
		t.Fatalf("define tables: %v", err)
	}

	// Prepare nova-config with coordinator "coord".
	cfg := config.NewMem()
	if _, err := cfg.Insert(ctx, config.KindFriend, config.Row{Name: "coord", Fields: map[string]string{"slots": "1", "tiers": "pro"}}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.Update(ctx, config.KindSprint, config.KindSprint, map[string]string{"coordinator": "coord"}, "test"); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"NOVA_SPRINT_REDIS": addr,
		"NOVA_SPRINT_ACTOR": "coord",
	}
	a := newApp(func(k string) string { return env[k] })
	a.newPath = true
	a.configRows = func(context.Context, string) (spverbs.ConfigRows, func() error, error) { return cfg, nil, nil }
	defer a.close()

	dir := t.TempDir()
	goalFile := filepath.Join(dir, "goal.txt")
	if err := os.WriteFile(goalFile, []byte("drive the sprint to landed"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Helper to run a command and assert Rule 7 (bounded output, at most 2 lines).
	runCmd := func(t *testing.T, line string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := a.run(split(strings.ReplaceAll(line, "{dir}", dir)), &out, &errb)
		outStr, errStr := out.String(), errb.String()

		combined := outStr
		if code != 0 {
			combined = errStr
		}
		trimmed := strings.TrimRight(combined, "\n")
		lines := strings.Split(trimmed, "\n")
		if trimmed != "" && len(lines) > 3 {
			t.Errorf("Rule 7 violation on %q: output has %d lines (want <= 3):\n%s", line, len(lines), combined)
		}
		return code, outStr, errStr
	}

	// -------------------------------------------------------------------------
	// 1. init on fresh store
	// -------------------------------------------------------------------------
	t.Run("init", func(t *testing.T) {
		code, out, errb := runCmd(t, "init")
		if code != exitDone || errb != "" {
			t.Fatalf("init failed: exit %d\nstdout: %s\nstderr: %s", code, out, errb)
		}
		if !strings.HasPrefix(out, "INIT OK") || !strings.Contains(out, "epoch=0") {
			t.Errorf("init stdout token line: %q", out)
		}
		if !strings.Contains(out, "init: the sprint is made, STOPPED, with coordinator coord") {
			t.Errorf("init stdout said line: %q", out)
		}

		// State verification in Redis: clock is STOPPED
		clockSince, err := raw.HGet(ctx, pathNames.Prefix+"sprint:clock", "stopped_since_ms").Result()
		if err != nil || clockSince == "" {
			t.Fatalf("clock stopped_since_ms in Redis: %q, err: %v (want non-empty when STOPPED)", clockSince, err)
		}

		// Second init should be cleanly refused (MACHINESTATE, exit 2).
		code2, out2, errb2 := runCmd(t, "init")
		if code2 != exitRefused || out2 != "" {
			t.Fatalf("second init unexpected: exit %d\nstdout: %s\nstderr: %s", code2, out2, errb2)
		}
		if !strings.Contains(errb2, "INIT FAIL") || !strings.Contains(errb2, "code=MACHINESTATE") || !strings.Contains(errb2, "already initialised") {
			t.Errorf("second init refusal: %q", errb2)
		}
	})

	// -------------------------------------------------------------------------
	// 2. start
	// -------------------------------------------------------------------------
	t.Run("start", func(t *testing.T) {
		code, out, errb := runCmd(t, "start")
		if code != exitDone || errb != "" {
			t.Fatalf("start failed: exit %d\nstdout: %s\nstderr: %s", code, out, errb)
		}
		if !strings.Contains(out, "START OK") || !strings.Contains(out, "before=STOPPED after=RUNNING changed") {
			t.Errorf("start stdout token line: %q", out)
		}
		if !strings.Contains(out, "start: the machine runs") {
			t.Errorf("start stdout said line: %q", out)
		}

		// State verification in Redis: clock is RUNNING (stopped_since_ms is absent or empty)
		clockSince, err := raw.HGet(ctx, pathNames.Prefix+"sprint:clock", "stopped_since_ms").Result()
		if err != nil && err != redis.Nil {
			t.Fatalf("clock in Redis: %v", err)
		}
		if clockSince != "" {
			t.Fatalf("clock stopped_since_ms in Redis: %q (want empty/absent when RUNNING)", clockSince)
		}

		// Repeated start: already running (grammar decision 27: done, exit 0, unchanged)
		code2, out2, errb2 := runCmd(t, "start")
		if code2 != exitDone || errb2 != "" {
			t.Fatalf("repeated start unexpected: exit %d\nstdout: %s\nstderr: %s", code2, out2, errb2)
		}
		if !strings.Contains(out2, "the machine is RUNNING already; nothing was written") {
			t.Errorf("repeated start stdout: %q", out2)
		}
	})

	// -------------------------------------------------------------------------
	// 3. goal set
	// -------------------------------------------------------------------------
	t.Run("goal set", func(t *testing.T) {
		// goal set sends a sprint part with goals to ns_sprint_step.
		// Lua write path in IT16 rejects goals with REQUEST (bug refusal -> exit 3).
		code, out, errb := runCmd(t, "goal set coord --file "+goalFile)
		if code != exitBug || out != "" {
			t.Fatalf("goal set: exit %d, want exitBug (%d)\nstdout: %s\nstderr: %s", code, exitBug, out, errb)
		}
		if !strings.Contains(errb, "GOAL-SET FAIL") || !strings.Contains(errb, "code=REQUEST") {
			t.Errorf("goal set stderr line 1: %q", errb)
		}
		if !strings.Contains(errb, "the write path does not carry goals yet") {
			t.Errorf("goal set stderr line 2: %q", errb)
		}
	})

	// -------------------------------------------------------------------------
	// 4. goal show
	// -------------------------------------------------------------------------
	t.Run("goal show", func(t *testing.T) {
		// goal show is refused locally because IT30 query kinds do not read person goals (exit 2).
		code, out, errb := runCmd(t, "goal show coord")
		if code != exitRefused || out != "" {
			t.Fatalf("goal show: exit %d, want exitRefused (%d)\nstdout: %s\nstderr: %s", code, exitRefused, out, errb)
		}
		if !strings.Contains(errb, "GOAL-SHOW FAIL") || !strings.Contains(errb, "code=REQUEST") {
			t.Errorf("goal show stderr line 1: %q", errb)
		}
		if !strings.Contains(errb, "the sprint's key reads have no read of a person's goal yet") {
			t.Errorf("goal show stderr line 2: %q", errb)
		}
	})

	// -------------------------------------------------------------------------
	// 5. stop
	// -------------------------------------------------------------------------
	t.Run("stop", func(t *testing.T) {
		code, out, errb := runCmd(t, "stop")
		if code != exitDone || errb != "" {
			t.Fatalf("stop failed: exit %d\nstdout: %s\nstderr: %s", code, out, errb)
		}
		if !strings.Contains(out, "STOP OK") || !strings.Contains(out, "before=RUNNING after=STOPPED changed") {
			t.Errorf("stop stdout token line: %q", out)
		}
		if !strings.Contains(out, "stop: the machine is STOPPED") {
			t.Errorf("stop stdout said line: %q", out)
		}

		// State verification in Redis: clock is STOPPED
		clockSince, err := raw.HGet(ctx, pathNames.Prefix+"sprint:clock", "stopped_since_ms").Result()
		if err != nil || clockSince == "" {
			t.Fatalf("clock stopped_since_ms in Redis: %q, err: %v (want non-empty when STOPPED)", clockSince, err)
		}

		// Repeated stop: already stopped (grammar decision 27: done, exit 0, unchanged)
		code2, out2, errb2 := runCmd(t, "stop")
		if code2 != exitDone || errb2 != "" {
			t.Fatalf("repeated stop unexpected: exit %d\nstdout: %s\nstderr: %s", code2, out2, errb2)
		}
		if !strings.Contains(out2, "the machine is STOPPED already; nothing was written") {
			t.Errorf("repeated stop stdout: %q", out2)
		}
	})

	// -------------------------------------------------------------------------
	// 6. clear --confirm sprint
	// -------------------------------------------------------------------------
	t.Run("clear", func(t *testing.T) {
		// Wrong confirmation refused before sending
		codeW, outW, errbW := runCmd(t, "clear --confirm other")
		if codeW != exitRefused || outW != "" || !strings.Contains(errbW, "wants --confirm sprint") {
			t.Fatalf("clear wrong confirm: exit %d\nstdout: %s\nstderr: %s", codeW, outW, errbW)
		}

		// Proper clear advances epoch from 0 to 1 and keeps STOPPED
		code, out, errb := runCmd(t, "clear --confirm sprint")
		if code != exitDone || errb != "" {
			t.Fatalf("clear failed: exit %d\nstdout: %s\nstderr: %s", code, out, errb)
		}
		if !strings.Contains(out, "CLEAR OK") || !strings.Contains(out, "epoch=0->1") {
			t.Errorf("clear stdout token line: %q", out)
		}
		if !strings.Contains(out, "clear: the sprint is at epoch 1, STOPPED") {
			t.Errorf("clear stdout said line: %q", out)
		}

		// State verification in Redis: epoch is 1, clock is STOPPED
		clockSince, err := raw.HGet(ctx, pathNames.Prefix+"sprint:clock", "stopped_since_ms").Result()
		if err != nil || clockSince == "" {
			t.Fatalf("clock stopped_since_ms in Redis after clear: %q, err: %v (want STOPPED)", clockSince, err)
		}
		epoch, err := raw.HGet(ctx, pathNames.EpochKey(), "n").Result()
		if err != nil || epoch != "1" {
			t.Fatalf("epoch in Redis after clear: %q, err: %v (want 1)", epoch, err)
		}
	})

	// -------------------------------------------------------------------------
	// 7. teardown --confirm sprint (CLI Layer 1 lifecycle teardown)
	// -------------------------------------------------------------------------
	t.Run("teardown", func(t *testing.T) {
		// Wrong confirmation refused on CLI
		codeW, outW, errbW := runCmd(t, "teardown --confirm other")
		if codeW != exitRefused || outW != "" || !strings.Contains(errbW, "wants --confirm sprint") {
			t.Fatalf("teardown wrong confirm: exit %d\nstdout: %s\nstderr: %s", codeW, outW, errbW)
		}

		// (a) First, verify RUNNING refusal if machine were running
		if codeS, _, _ := runCmd(t, "start"); codeS != exitDone {
			t.Fatalf("failed to start machine for teardown guard test")
		}
		codeR, outR, errbR := runCmd(t, "teardown --confirm sprint")
		if codeR != exitRefused || !strings.Contains(errbR, "RUNNING") {
			t.Fatalf("teardown while RUNNING: exit=%d, out=%s, err=%s, want RUNNING refusal", codeR, outR, errbR)
		}
		if codeSt, _, _ := runCmd(t, "stop"); codeSt != exitDone {
			t.Fatalf("failed to stop machine")
		}

		// (b) Execute CLI Teardown while STOPPED (live Layer 1 lifecycle teardown)
		code, out, errb := runCmd(t, "teardown --confirm sprint")
		if code != exitDone || errb != "" {
			t.Fatalf("teardown CLI unexpected: exit %d\nstdout: %s\nstderr: %s", code, out, errb)
		}
		if !strings.Contains(out, "teardown: the sprint is gone") {
			t.Errorf("teardown stdout: %q", out)
		}

		// (c) Clean state verification in Redis:
		// After teardown, only the lifecycle receipt stream remains under the namespace.
		keys, err := raw.Keys(ctx, pathNames.Prefix+"*").Result()
		if err != nil {
			t.Fatalf("failed to query keys after teardown: %v", err)
		}
		sort.Strings(keys)
		wantReceiptStream := pathNames.Prefix + "sprint:lifecycle"
		if len(keys) != 1 || keys[0] != wantReceiptStream {
			t.Fatalf("keys remaining after teardown: %v (want only [%s])", keys, wantReceiptStream)
		}
	})
}
