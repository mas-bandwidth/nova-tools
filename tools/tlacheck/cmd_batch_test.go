package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/batchmodel"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

const batchPassLog = "Model checking completed. No error has been found.\n"
const batchRejectLog = "Error: Invariant MatchesExecution is violated.\n"

func batchHarness(t *testing.T, capture func(context.Context, batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error), run func(context.Context, tlc.Run, string) int) (env, string, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	e, _, _ := testEnv(t, run)
	var out, errs bytes.Buffer
	e.stdout, e.stderr = &out, &errs
	dir := filepath.Join(t.TempDir(), "out")
	jar := filepath.Join(t.TempDir(), "tla2tools.jar")
	if err := os.WriteFile(jar, []byte("jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.capture = capture
	return e, dir, jar, &out, &errs
}

func scriptedBatchCapture(t *testing.T, names ...string) func(context.Context, batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
	t.Helper()
	return func(ctx context.Context, opts batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("capture has no overall deadline")
		} else if remaining := time.Until(deadline); remaining <= 0 || remaining > tlc.BoundedCap {
			t.Errorf("capture deadline remaining = %s, want positive and at most %s", remaining, tlc.BoundedCap)
		}
		if err := os.Mkdir(opts.OutputDir, 0o700); err != nil {
			return nil, err
		}
		var out []batchmodel.SuiteCase
		for _, name := range names {
			caseDir := filepath.Join(opts.OutputDir, name)
			if err := os.MkdirAll(caseDir, 0o700); err != nil {
				return nil, err
			}
			work := filepath.Join(caseDir, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				return nil, err
			}
			config := filepath.Join(work, "BatchReceiptReplay.cfg")
			module := filepath.Join(work, "BatchReceiptReplay.tla")
			if err := os.WriteFile(config, []byte("\"MatchesExecution\"\n"), 0o600); err != nil {
				return nil, err
			}
			if err := os.WriteFile(module, []byte("---- MODULE BatchReceiptReplay ----\n"), 0o600); err != nil {
				return nil, err
			}
			expected := "pass"
			if name == "corrupt-observation" {
				expected = "invariant-reject"
			}
			out = append(out, batchmodel.SuiteCase{Name: name, Expected: expected, Property: "MatchesExecution", SourceSHA256: "src-hash", Bundle: batchmodel.Bundle{
				Work: work, Module: module, Config: config, Evidence: filepath.Join(caseDir, "evidence.json"), ConfigSHA256: "cfg-hash",
			}, EvidenceSHA256: "evidence-hash"})
		}
		return out, nil
	}
}

func TestBatchReplayRunsCapturedCasesAndRequiresCorruptObservationToRejectExecution(t *testing.T) {
	t.Parallel()
	e, dir, jar, out, errs := batchHarness(t, scriptedBatchCapture(t, "positive", "corrupt-observation"), func(ctx context.Context, r tlc.Run, log string) int {
		if r.Workers != 1 || r.TmpDir == "" || strings.Join(r.JVM, ",") != "-Xmx512m,-XX:ActiveProcessorCount=2,-XX:+UseParallelGC" {
			t.Errorf("TLC resource bounds = %+v", r)
		}
		text, code := batchPassLog, 0
		if strings.HasSuffix(r.Dir, filepath.Join("corrupt-observation", "work")) {
			text, code = batchRejectLog, tlc.ExitInvariant
		}
		if err := os.WriteFile(log, []byte(text), 0o600); err != nil {
			t.Error(err)
		}
		return code
	})
	res := do(e, out, errs, "batch-replay", "--source", "table.lua", "--dir", dir, "--jar", jar)
	if res.code != 0 || strings.Count(res.stdout, "BATCH OK case=") != 2 || !strings.Contains(res.stdout, "BATCH OK cases=2") || res.stderr != "" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.stdout, "lua_sha256=src-hash") || !strings.Contains(res.stdout, "config_sha256=cfg-hash") || !strings.Contains(res.stdout, "evidence_sha256=evidence-hash") || !strings.Contains(res.stdout, "suite=") {
		t.Fatalf("case output omitted hashes/evidence: %s", res.stdout)
	}
}

func TestBatchReplayStopsOnMismatchAndTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		code int
		log  string
	}{
		{"mismatch", tlc.ExitInvariant, batchRejectLog},
		{"timeout", tlc.ExitTimeout, "TLC suite budget exhausted during this case\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, dir, jar, out, errs := batchHarness(t, scriptedBatchCapture(t, "positive", "later"), func(_ context.Context, _ tlc.Run, log string) int {
				if err := os.WriteFile(log, []byte(tc.log), 0o600); err != nil {
					t.Error(err)
				}
				return tc.code
			})
			res := do(e, out, errs, "batch-replay", "--source", "table.lua", "--dir", dir, "--jar", jar)
			if res.code != 1 || strings.Count(res.stdout+res.stderr, "case=") != 1 || strings.Contains(res.stdout+res.stderr, "case=later") {
				t.Fatalf("result = %+v", res)
			}
		})
	}
}

func TestBatchReplayRejectsBadCorruptControlAndTimesOutCapture(t *testing.T) {
	t.Parallel()
	e, dir, jar, out, errs := batchHarness(t, scriptedBatchCapture(t, "corrupt-observation"), func(_ context.Context, _ tlc.Run, log string) int {
		if err := os.WriteFile(log, []byte(batchRejectLog), 0o600); err != nil {
			t.Error(err)
		}
		return tlc.ExitInvariant
	})
	e.capture = func(ctx context.Context, opts batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
		cases, err := scriptedBatchCapture(t, "corrupt-observation")(ctx, opts)
		cases[0].Property = "SomeOtherInvariant"
		return cases, err
	}
	res := do(e, out, errs, "batch-replay", "--source", "table.lua", "--dir", dir, "--jar", jar)
	if res.code != 1 {
		t.Fatalf("wrong control property accepted: %+v", res)
	}

	e, dir, jar, out, errs = batchHarness(t, func(ctx context.Context, _ batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("capture has no deadline")
		}
		return nil, context.DeadlineExceeded
	}, func(context.Context, tlc.Run, string) int { t.Error("TLC ran after capture timeout"); return 0 })
	res = do(e, out, errs, "batch-replay", "--source", "table.lua", "--dir", dir, "--jar", jar)
	if res.code != 1 || !strings.Contains(res.stderr, "budget expired during capture") {
		t.Fatalf("capture timeout result = %+v", res)
	}
}

func TestBatchReplayHelpAndLinuxGuardHaveNoSideEffects(t *testing.T) {
	t.Parallel()
	e, out, errs := testEnv(t, nil)
	e.capture = func(context.Context, batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
		t.Error("captured for help/guard")
		return nil, nil
	}
	e.lookPath = func(string) (string, error) { t.Error("looked up a helper before Linux guard"); return "", nil }
	e.goos = "darwin"
	r := do(e, out, errs, "batch-replay", "-h")
	if r.code != 0 || r.stdout != helpBatchReplay || r.stderr != "" {
		t.Fatalf("help = %+v", r)
	}
	e, out, errs = testEnv(t, nil)
	e.capture = func(context.Context, batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
		t.Error("captured on non-Linux")
		return nil, nil
	}
	e.lookPath = func(string) (string, error) { t.Error("looked up helper on non-Linux"); return "", nil }
	e.goos = "darwin"
	r = do(e, out, errs, "batch-replay", "--source", "x.lua", "--dir", "out")
	if r.code != 2 || !strings.Contains(r.stderr, "Linux bench") {
		t.Fatalf("Linux guard = %+v", r)
	}
}

func TestBatchReplayDistinguishesSemanticCaptureFailureFromCannotRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"semantic", errors.New("invalid captured history"), 1},
		{"cannot-run", &tablemodel.CannotRun{Err: errors.New("redis unavailable")}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, dir, jar, out, errs := batchHarness(t, func(context.Context, batchmodel.CaptureSuiteOptions) ([]batchmodel.SuiteCase, error) {
				return nil, tc.err
			}, func(context.Context, tlc.Run, string) int { t.Error("TLC ran after capture error"); return 0 })
			res := do(e, out, errs, "batch-replay", "--source", "table.lua", "--dir", dir, "--jar", jar)
			if res.code != tc.code || strings.Contains(res.stdout, "BATCH") || res.stderr == "" {
				t.Fatalf("capture error result = %+v", res)
			}
		})
	}
}
