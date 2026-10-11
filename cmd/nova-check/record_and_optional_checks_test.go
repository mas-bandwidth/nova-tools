package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"

	"github.com/stretchr/testify/require"
)

// Issue #2311: `docs/SPEC.md` claims `record` writes a dated clock stamp,
// `ledger`/`gate` run git only with `--repo` and help only with `--tools`, each
// under its own timeout, and `--repo` warns on stderr that the read can take
// seconds. This test pins all four "only-when" boundaries and the progress
// notice.
func TestIssue2311(t *testing.T) {
	t.Parallel()

	t.Run("RecordWritesTheClockTimestamp", func(t *testing.T) {
		fixed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		seams := dogfoodSeams{clock: func() time.Time { return fixed }}

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")

		code, stdout, stderr := dogfoodRunWith(seams, "record", "--cli", cli,
			"--tool", "nova-example", "--verb", "links", "--by", "Stella", "--ok",
			"--notes", "ran it over the lane's own docs", "--receipts", receipts)
		require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
		require.Contains(t, stdout, "at=2026-09-22T12:00:00Z", "record did not write the pinned clock timestamp:\n%s", stdout)
	})

	t.Run("RunsGitLogOnlyWithRepoUnderGitTimeout", func(t *testing.T) {
		calls := 0
		seams := dogfoodSeams{gitRunner: func(ctx context.Context, dir string, args ...string) (string, error) {
			calls++
			return "", nil
		}}

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")
		writeReceipt(t, receipts, "a.json", map[string]any{
			"tool": "nova-example", "verb": "links", "by": "Stella",
			"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
		})

		// With --repo every verb gets exactly one git log invocation.
		code, _, stderr := dogfoodRunWith(seams, "ledger", "--cli", cli, "--receipts", receipts, "--repo", dir)
		require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
		require.EqualValues(t, 3, calls, "git log calls=%d, want 3", calls)

		// Without --repo git is not reached at all.
		calls = 0
		code, _, stderr = dogfoodRunWith(seams, "ledger", "--cli", cli, "--receipts", receipts)
		require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
		require.EqualValues(t, 0, calls, "git log calls=%d without --repo, want 0", calls)

		// A non-positive --git-timeout is refused before any git call.
		calls = 0
		code, _, stderr = dogfoodRunWith(seams, "ledger", "--cli", cli, "--receipts", receipts, "--repo", dir, "--git-timeout", "0")
		require.EqualValues(t, 2, code, "non-positive --git-timeout exit %d, want 2\n%s", code, stderr)
		require.Contains(t, stderr, "--git-timeout", "refusal does not name --git-timeout:\n%s", stderr)
		require.EqualValues(t, 0, calls, "git log was called after a non-positive timeout: calls=%d", calls)
	})

	t.Run("RunsHelpOnlyWithToolsUnderToolsTimeout", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("fixture is a shell script")
		}
		tools := t.TempDir()
		script := "#!/bin/sh\ncat <<'EOF'\nnova-example: fixture\n\nusage:\n  nova-example links --dir <dir>\nEOF\n"
		require.NoError(t, testbin.WriteExecutable(filepath.Join(tools, "nova-example"), []byte(script), 0o755))

		calls := 0
		seams := dogfoodSeams{helpRunner: func(ctx context.Context, bin string) (string, error) {
			calls++
			return "", nil
		}}

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")
		require.NoError(t, os.MkdirAll(receipts, 0o755))

		// With --tools every binary gets exactly one help invocation.
		code, _, stderr := dogfoodRunWith(seams, "ledger", "--cli", cli, "--tools", tools, "--receipts", receipts)
		require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
		require.EqualValues(t, 1, calls, "help calls=%d, want 1", calls)

		// Without --tools no binary is asked for help.
		calls = 0
		code, _, stderr = dogfoodRunWith(seams, "ledger", "--cli", cli, "--receipts", receipts)
		require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
		require.EqualValues(t, 0, calls, "help calls=%d without --tools, want 0", calls)

		// A non-positive --tools-timeout is refused before any help call.
		calls = 0
		code, _, stderr = dogfoodRunWith(seams, "ledger", "--cli", cli, "--tools", tools, "--receipts", receipts, "--tools-timeout", "0")
		require.EqualValues(t, 2, code, "non-positive --tools-timeout exit %d, want 2\n%s", code, stderr)
		require.Contains(t, stderr, "--tools-timeout", "refusal does not name --tools-timeout:\n%s", stderr)
		require.EqualValues(t, 0, calls, "help was called after a non-positive timeout: calls=%d", calls)
	})

	t.Run("RepoWarnsOnStderrItCanTakeSeconds", func(t *testing.T) {
		seams := dogfoodSeams{gitRunner: func(ctx context.Context, dir string, args ...string) (string, error) {
			return "", nil
		}}

		dir := t.TempDir()
		cli := writeCLI(t, dir)
		receipts := filepath.Join(dir, "receipts")
		writeReceipt(t, receipts, "a.json", map[string]any{
			"tool": "nova-example", "verb": "links", "by": "Stella",
			"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
		})

		code, _, stderr := dogfoodRunWith(seams, "ledger", "--cli", cli, "--receipts", receipts, "--repo", dir)
		require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
		require.Contains(t, stderr, "can take seconds", "--repo run did not warn that it can take seconds:\n%s", stderr)
	})
}

// dogfoodRunWith runs the dogfood verb in process with the seams the caller
// injects, so a test shares the process with its neighbours instead of
// assigning a package variable.
func dogfoodRunWith(s dogfoodSeams, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = runWith(seams{dogfood: s}, append([]string{"dogfood"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}
