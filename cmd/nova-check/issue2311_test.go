package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// Issue #2311: `docs/SPEC.md` claims `record` writes a dated clock stamp,
// `ledger`/`gate` run git only with `--repo` and help only with `--tools`, each
// under its own timeout, and `--repo` warns on stderr that the read can take
// seconds. This test pins all four "only-when" boundaries and the progress
// notice.
func TestIssue2311(t *testing.T) {
	t.Run("RecordWritesTheClockTimestamp", func(t *testing.T) {
		fixed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		dogfoodClock = func() time.Time { return fixed }
		defer func() { dogfoodClock = time.Now }()

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")

		code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--cli", cli,
			"--tool", "nova-example", "--verb", "links", "--by", "Stella", "--ok",
			"--notes", "ran it over the lane's own docs", "--receipts", receipts)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "at=2026-09-22T12:00:00Z") {
			t.Fatalf("record did not write the pinned clock timestamp:\n%s", stdout)
		}
	})

	t.Run("RunsGitLogOnlyWithRepoUnderGitTimeout", func(t *testing.T) {
		calls := 0
		dogfoodGitRunner = func(ctx context.Context, dir string, args ...string) (string, error) {
			calls++
			return "", nil
		}
		defer func() { dogfoodGitRunner = nil }()

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")
		writeReceipt(t, receipts, "a.json", map[string]any{
			"tool": "nova-example", "verb": "links", "by": "Stella",
			"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
		})

		// With --repo every verb gets exactly one git log invocation.
		code, _, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts, "--repo", dir)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, stderr)
		}
		if calls != 3 {
			t.Fatalf("git log calls=%d, want 3", calls)
		}

		// Without --repo git is not reached at all.
		calls = 0
		code, _, stderr = dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, stderr)
		}
		if calls != 0 {
			t.Fatalf("git log calls=%d without --repo, want 0", calls)
		}

		// A non-positive --git-timeout is refused before any git call.
		calls = 0
		code, _, stderr = dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts, "--repo", dir, "--git-timeout", "0")
		if code != 2 {
			t.Fatalf("non-positive --git-timeout exit %d, want 2\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "--git-timeout") {
			t.Fatalf("refusal does not name --git-timeout:\n%s", stderr)
		}
		if calls != 0 {
			t.Fatalf("git log was called after a non-positive timeout: calls=%d", calls)
		}
	})

	t.Run("RunsHelpOnlyWithToolsUnderToolsTimeout", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("fixture is a shell script")
		}
		tools := t.TempDir()
		script := "#!/bin/sh\ncat <<'EOF'\nnova-example: fixture\n\nusage:\n  nova-example links --dir <dir>\nEOF\n"
		if err := testbin.WriteExecutable(filepath.Join(tools, "nova-example"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}

		calls := 0
		dogfoodHelpRunner = func(ctx context.Context, bin string) (string, error) {
			calls++
			return "", nil
		}
		defer func() { dogfoodHelpRunner = nil }()

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")
		if err := os.MkdirAll(receipts, 0o755); err != nil {
			t.Fatal(err)
		}

		// With --tools every binary gets exactly one help invocation.
		code, _, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--tools", tools, "--receipts", receipts)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, stderr)
		}
		if calls != 1 {
			t.Fatalf("help calls=%d, want 1", calls)
		}

		// Without --tools no binary is asked for help.
		calls = 0
		code, _, stderr = dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, stderr)
		}
		if calls != 0 {
			t.Fatalf("help calls=%d without --tools, want 0", calls)
		}

		// A non-positive --tools-timeout is refused before any help call.
		calls = 0
		code, _, stderr = dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--tools", tools, "--receipts", receipts, "--tools-timeout", "0")
		if code != 2 {
			t.Fatalf("non-positive --tools-timeout exit %d, want 2\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "--tools-timeout") {
			t.Fatalf("refusal does not name --tools-timeout:\n%s", stderr)
		}
		if calls != 0 {
			t.Fatalf("help was called after a non-positive timeout: calls=%d", calls)
		}
	})

	t.Run("RepoWarnsOnStderrItCanTakeSeconds", func(t *testing.T) {
		dogfoodGitRunner = func(ctx context.Context, dir string, args ...string) (string, error) {
			return "", nil
		}
		defer func() { dogfoodGitRunner = nil }()

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")
		writeReceipt(t, receipts, "a.json", map[string]any{
			"tool": "nova-example", "verb": "links", "by": "Stella",
			"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
		})

		code, _, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts, "--repo", dir)
		if code != 0 {
			t.Fatalf("exit %d, want 0\n%s", code, stderr)
		}
		if !strings.Contains(stderr, "can take seconds") {
			t.Fatalf("--repo run did not warn that it can take seconds:\n%s", stderr)
		}
	})
}
