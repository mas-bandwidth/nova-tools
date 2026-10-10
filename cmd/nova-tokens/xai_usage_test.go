package main

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestXaiProviderOneUsageFileFoldsRow is #2671: --provider xai names one
// usage.json. A fixture file folds to one ledger row. A missing path is
// *XaiUsageMissingError, and a directory is not walked. A session store
// planted under HOME is never opened.
func TestXaiProviderOneUsageFileFoldsRow(t *testing.T) {
	t.Parallel()
	if os.Getenv(childTestEnv) == "" {
		// HOME and USERPROFILE are a process-wide environment; the body runs in a child
		// where this test owns the process, and sets them there.
		reenterTest(t, "TestXaiProviderOneUsageFileFoldsRow")
		return
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.Setenv("HOME", home))
	require.NoError(t, os.Setenv("USERPROFILE", home))
	const bait = "424242"
	write(t, filepath.Join(home, ".grok", "sessions", "encoded-cwd", "session-id", "usage.json"), `{
  "sessionId": "bait-session",
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-12T00:05:00Z",
      "inputTokens": `+bait+`,
      "outputTokens": 1,
      "primaryModelId": "bait-model"
    }
  ]
}`)
	write(t, filepath.Join(dir, "other-usage.json"), `{
  "sessionId": "sibling",
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-12T00:05:00Z",
      "inputTokens": 515151,
      "outputTokens": 1,
      "primaryModelId": "sibling-model"
    }
  ]
}`)

	out := mkdir(t, filepath.Join(dir, "out"))
	grok := write(t, filepath.Join(dir, "usage.json"), `{
  "sessionId": "fixture-grok-session",
  "updatedAt": "2026-09-12T00:06:00Z",
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-12T00:05:00Z",
      "inputTokens": 1000,
      "outputTokens": 100,
      "costUsdTicks": 77,
      "primaryModelId": "grok-model-example"
    }
  ]
}`)
	repos := reposFile(t, dir)
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:reader-f="+grok)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS DAY day=2026-09-12 rows=1 ")
	body := read(t, filepath.Join(out, "2026-09-12.tsv"))
	const wantRow = "2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t-\t-\t-\t0\tutc\txai:reader-f"
	var data []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if strings.HasPrefix(line, "2026-") {
			data = append(data, line)
		}
	}
	require.Equal(t, 1, len(data), "folded rows = %q, want [%s]", data, wantRow)
	require.Equal(t, wantRow, data[0], "folded rows = %q, want [%s]", data, wantRow)
	require.False(t, strings.Contains(body, bait), "the fold counted a usage.json the flag did not name:\n%s\n%s", body, r.all())
	require.False(t, strings.Contains(r.all(), bait), "the fold counted a usage.json the flag did not name:\n%s\n%s", body, r.all())
	require.False(t, strings.Contains(body, "515151"), "the fold counted a usage.json the flag did not name:\n%s\n%s", body, r.all())

	missing := filepath.Join(dir, "no-such-usage.json")
	outMiss := mkdir(t, filepath.Join(dir, "out-missing"))
	miss := invoke(t, "fold", "--out", outMiss, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:reader-f="+missing)
	wantExit(t, miss, 1)
	wantContains(t, miss.stderr, "TOKENS UNREADABLE")
	wantContains(t, miss.stderr, "does not scan a session store")
	require.False(t, strings.Contains(miss.all(), bait), "a missing path folded the session store:\n%s", miss.all())

	// The typed *XaiUsageMissingError and *XaiUsageNotFileError shapes are pinned against
	// the live reader in internal/tokens (TestProviderCoverReadXaiUsageFile); here the live
	// fold is what must refuse the directory without walking it.
	sessions := filepath.Join(home, ".grok", "sessions")
	outDir := mkdir(t, filepath.Join(dir, "out-dir"))
	dirFold := invoke(t, "fold", "--out", outDir, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:reader-f="+sessions)
	wantExit(t, dirFold, 1)
	wantContains(t, dirFold.stderr, "does not scan a directory")
	require.False(t, strings.Contains(dirFold.all(), bait), "a directory flag folded the session store:\n%s", dirFold.all())
}
