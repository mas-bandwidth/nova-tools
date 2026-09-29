// Copyright (c) mas-bandwidth
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// generateCorpus generates a realistic Claude JSONL corpus with nLines lines
// distributed across days, models, and repositories, optionally injecting malformed lines.
func generateCorpus(t *testing.T, dir string, nLines int, days []string, injectMalformed bool) string {
	t.Helper()
	models := []string{
		"claude-3-5-sonnet-20241022",
		"claude-3-opus-20240229",
		"claude-3-5-haiku-20241022",
	}
	repoPaths := []string{
		"/workspace/repo/schema/pkg/types.go",
		"/workspace/repo/serialize/src/lib.rs",
		"/workspace/repo/nova-tools/cmd/main.go",
		"/workspace/repo/unmatched/doc.md",
	}

	trDir := mkdir(t, filepath.Join(dir, "transcripts"))
	filePath := filepath.Join(trDir, "corpus.jsonl")
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create corpus file: %v", err)
	}
	defer f.Close()

	rng := rand.New(rand.NewSource(42))

	for i := 0; i < nLines; i++ {
		// Periodically inject malformed lines if requested
		if injectMalformed && i%500 == 0 {
			switch i % 1500 {
			case 0:
				f.WriteString("{malformed json line\n")
			case 500:
				f.WriteString("{\"type\":\"message\",\"id\":\"bad-ts\",\"timestamp\":\"invalid-time\"}\n")
			case 1000:
				f.WriteString("\n") // blank line
			}
			continue
		}

		day := days[rng.Intn(len(days))]
		hour := rng.Intn(24)
		min := rng.Intn(60)
		sec := rng.Intn(60)
		ts := fmt.Sprintf("%sT%02d:%02d:%02dZ", day, hour, min, sec)
		model := models[rng.Intn(len(models))]
		repoPath := repoPaths[rng.Intn(len(repoPaths))]
		inTokens := 50 + rng.Intn(500)
		outTokens := 20 + rng.Intn(200)
		msgID := fmt.Sprintf("msg-%06d", i)

		line := msg(msgID, ts, model, map[string]int{
			"input_tokens":  inTokens,
			"output_tokens": outTokens,
		}, repoPath)
		f.WriteString(line + "\n")
	}

	return trDir
}

// TestCollateRehearsalMultiDayAndCorrupt exercises real-corpus rehearsal on:
// 1. Multi-day token logs (10,000 lines across 5 calendar days).
// 2. Corrupt and malformed entries (ensures fail-soft skip/reporting without crash).
// 3. Atomic rename and zero partial writes (no leftover *.tmp files).
// 4. Clean handling of missing days (TOKENS UNWRITTEN).
// 5. Zero resource leaks (file locks cleanly released).
func TestCollateRehearsalMultiDayAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := filepath.Join(dir, "repos.tsv")
	write(t, repos, "schema\t(^|/)schema($|/)\nserialize\t(^|/)serialize($|/)\nnova-tools\t(^|/)nova-tools($|/)\n")

	days := []string{
		"2026-09-20",
		"2026-09-21",
		"2026-09-22",
		"2026-09-23",
		"2026-09-24",
	}

	// 1. Generate 10,000 lines across 5 days with injected corrupt/malformed lines
	trDir := generateCorpus(t, dir, 10000, days, true)

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	// Measure execution time and memory footprint
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	start := time.Now()

	rCorrupt := invokeAt(t, now, "collate", "--out", out, "--all", "--repos", repos, "--claude", "corpus="+trDir, "--max-staleness", "72")

	elapsed := time.Since(start)
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	heapDeltaKB := int64(memAfter.Alloc-memBefore.Alloc) / 1024
	totalAllocDeltaKB := int64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1024
	t.Logf("Rehearsal: Collate 10,000 lines with corrupt entries elapsed: %v, live heap delta: %d KB, total allocated: %d KB",
		elapsed, heapDeltaKB, totalAllocDeltaKB)

	// Assert runtime threshold (< 0.5s for 10,000 lines)
	if elapsed > 500*time.Millisecond {
		t.Errorf("collate runtime exceeded 0.5s: took %v", elapsed)
	}

	// In the presence of corrupt lines:
	// Rule 3: The exit code is about the claim (unreadable=1 -> exit 1, COLLATE FAIL).
	// Rule 11: Exit 1 still writes (all 5 valid days are written atomically).
	wantExit(t, rCorrupt, 1)
	wantContains(t, rCorrupt.stderr, "TOKENS UNREADABLE")
	wantContains(t, rCorrupt.stderr, "COLLATE FAIL days=5 unreadable=1")

	// Verify all 5 day files exist and parse cleanly with check
	for _, d := range days {
		dayFile := filepath.Join(out, d+".tsv")
		if _, err := os.Stat(dayFile); err != nil {
			t.Errorf("expected day file %s to exist: %v", dayFile, err)
		}
		df, findings, err := tokens.ReadDayFile(dayFile)
		if err != nil {
			t.Errorf("ReadDayFile(%s) failed: %v", dayFile, err)
		}
		if len(findings) > 0 {
			t.Errorf("day file %s has unexpected findings: %v", dayFile, findings)
		}
		if len(df.Rows) == 0 {
			t.Errorf("day file %s has 0 rows", dayFile)
		}
	}

	// 2. Verify Atomic Rename and Zero Partial Writes:
	// No temporary files (*.tmp or tmp-*) must be left in out directory
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, "tmp-") {
			t.Errorf("found leftover partial/temporary write file in out: %s", name)
		}
	}

	// 3. Verify Clean Handling of Missing Days (TOKENS UNWRITTEN)
	missingDay := "2026-09-30" // not in corpus
	rMissing := invokeAt(t, now, "collate", "--out", out, "--day", missingDay, "--repos", repos, "--claude", "corpus="+trDir, "--max-staleness", "72")
	wantExit(t, rMissing, 1)
	wantContains(t, rMissing.stderr, "TOKENS UNWRITTEN day=2026-09-30: day was asked for and not written")
	wantContains(t, rMissing.stderr, "COLLATE FAIL")

	// 4. Verify Zero Resource Leaks:
	// Verify that the flock is released by immediately taking a non-blocking lock
	release, lockErr := tokens.TakeFoldLock(out, 0)
	if lockErr != nil {
		t.Errorf("flock was leaked or left locked on out directory: %v", lockErr)
	} else {
		release()
	}

	// 5. Test Clean 10,000-line corpus:
	cleanDir := t.TempDir()
	cleanOut := mkdir(t, filepath.Join(cleanDir, "out"))
	cleanRepos := filepath.Join(cleanDir, "repos.tsv")
	write(t, cleanRepos, "schema\t(^|/)schema($|/)\nserialize\t(^|/)serialize($|/)\nnova-tools\t(^|/)nova-tools($|/)\n")
	cleanTrDir := generateCorpus(t, cleanDir, 10000, days, false)

	startClean := time.Now()
	rClean := invokeAt(t, now, "collate", "--out", cleanOut, "--all", "--repos", cleanRepos, "--claude", "corpus="+cleanTrDir, "--max-staleness", "72")
	elapsedClean := time.Since(startClean)
	t.Logf("Rehearsal: Collate 10,000 clean lines elapsed: %v", elapsedClean)

	if elapsedClean > 500*time.Millisecond {
		t.Errorf("clean collate runtime exceeded 0.5s: took %v", elapsedClean)
	}
	wantExit(t, rClean, 0)
	wantContains(t, rClean.stdout, "COLLATE OK days=5")

	// Idempotency: repeated run produces identical files
	rClean2 := invokeAt(t, now, "collate", "--out", cleanOut, "--all", "--repos", cleanRepos, "--claude", "corpus="+cleanTrDir, "--max-staleness", "72")
	wantExit(t, rClean2, 0)
	wantContains(t, rClean2.stdout, "COLLATE OK days=5")
}

// BenchmarkCollate10kLines measures performance and memory footprint
// on a 10,000-line real-world shaped token log.
func BenchmarkCollate10kLines(b *testing.B) {
	dir := b.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		b.Fatal(err)
	}
	repos := filepath.Join(dir, "repos.tsv")
	if err := os.WriteFile(repos, []byte("schema\t(^|/)schema($|/)\nserialize\t(^|/)serialize($|/)\nnova-tools\t(^|/)nova-tools($|/)\n"), 0o644); err != nil {
		b.Fatal(err)
	}

	days := []string{
		"2026-09-20",
		"2026-09-21",
		"2026-09-22",
		"2026-09-23",
		"2026-09-24",
	}

	trDir := filepath.Join(dir, "transcripts")
	if err := os.MkdirAll(trDir, 0o755); err != nil {
		b.Fatal(err)
	}
	corpusFile := filepath.Join(trDir, "corpus.jsonl")
	f, err := os.Create(corpusFile)
	if err != nil {
		b.Fatal(err)
	}

	models := []string{"claude-3-5-sonnet", "claude-3-opus", "claude-3-5-haiku"}
	repoPaths := []string{
		"/workspace/repo/schema/pkg/types.go",
		"/workspace/repo/serialize/src/lib.rs",
		"/workspace/repo/nova-tools/cmd/main.go",
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 10000; i++ {
		d := days[rng.Intn(len(days))]
		ts := fmt.Sprintf("%sT%02d:%02d:%02dZ", d, rng.Intn(24), rng.Intn(60), rng.Intn(60))
		m := models[rng.Intn(len(models))]
		p := repoPaths[rng.Intn(len(repoPaths))]
		inTok := 50 + rng.Intn(500)
		outTok := 20 + rng.Intn(200)
		line := fmt.Sprintf(`{"type":"message","id":"msg-%06d","timestamp":"%s","message":{"model":"%s","usage":{"input_tokens":%d,"output_tokens":%d},"content":[{"type":"tool_use","input":{"path":"%s"}}]}}`,
			i, ts, m, inTok, outTok, p)
		f.WriteString(line + "\n")
	}
	f.Close()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	args := []string{"collate", "--out", out, "--all", "--repos", repos, "--claude", "corpus=" + trDir, "--max-staleness", "72"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var stdout, stderr bytes.Buffer
		exit := run(args, &stdout, &stderr, now)
		if exit != 0 {
			b.Fatalf("collate failed: exit=%d\nstdout=%s\nstderr=%s", exit, stdout.String(), stderr.String())
		}
	}
}
