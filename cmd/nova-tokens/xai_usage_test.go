package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// TestXaiProviderOneUsageFileFoldsRow is #2671: --provider xai names one
// usage.json. A fixture file folds to one ledger row. A missing path is
// *XaiUsageMissingError, and a directory is not walked. A session store
// planted under HOME is never opened.
func TestXaiProviderOneUsageFileFoldsRow(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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
	before := tokens.Opens()
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+grok)
	wantExit(t, r, 0)
	if opened := tokens.Opens() - before; opened != 1 {
		t.Errorf("opened %d source files, want the one usage.json the flag names", opened)
	}
	wantContains(t, r.stdout, "TOKENS DAY date=2026-09-12 rows=1 ")
	body := read(t, filepath.Join(out, "2026-09-12.tsv"))
	const wantRow = "2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t-\t-\t-\t0\tutc\txai:johnny\t-"
	var data []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if strings.HasPrefix(line, "2026-") {
			data = append(data, line)
		}
	}
	if len(data) != 1 || data[0] != wantRow {
		t.Fatalf("folded rows = %q, want [%s]", data, wantRow)
	}
	if strings.Contains(body, bait) || strings.Contains(r.all(), bait) || strings.Contains(body, "515151") {
		t.Fatalf("the fold counted a usage.json the flag did not name:\n%s\n%s", body, r.all())
	}

	missing := filepath.Join(dir, "no-such-usage.json")
	outMiss := mkdir(t, filepath.Join(dir, "out-missing"))
	before = tokens.Opens()
	miss := invoke(t, "fold", "--out", outMiss, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+missing)
	wantExit(t, miss, 1)
	if opened := tokens.Opens() - before; opened != 0 {
		t.Errorf("a missing xai path opened %d source files; that is a scan", opened)
	}
	wantContains(t, miss.stderr, "TOKENS UNREADABLE")
	wantContains(t, miss.stderr, "does not scan a session store")
	if strings.Contains(miss.all(), bait) {
		t.Fatalf("a missing path folded the session store:\n%s", miss.all())
	}
	_, err := tokens.ReadXaiUsageFile(missing)
	var typed *tokens.XaiUsageMissingError
	if !errors.As(err, &typed) {
		t.Fatalf("missing file: got %v, want *XaiUsageMissingError", err)
	}
	if typed.Path != missing {
		t.Errorf("missing error path = %q, want %q", typed.Path, missing)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file does not unwrap to not-exist: %v", err)
	}

	sessions := filepath.Join(home, ".grok", "sessions")
	before = tokens.Opens()
	_, err = tokens.ReadXaiUsageFile(sessions)
	var notFile *tokens.XaiUsageNotFileError
	if !errors.As(err, &notFile) {
		t.Fatalf("directory: got %v, want *XaiUsageNotFileError", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("a directory that is there unwrapped as not-exist: %v", err)
	}
	if opened := tokens.Opens() - before; opened != 0 {
		t.Errorf("reading the sessions directory opened %d files", opened)
	}
	outDir := mkdir(t, filepath.Join(dir, "out-dir"))
	before = tokens.Opens()
	dirFold := invoke(t, "fold", "--out", outDir, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+sessions)
	wantExit(t, dirFold, 1)
	if opened := tokens.Opens() - before; opened != 0 {
		t.Errorf("fold of a directory opened %d source files; that is a scan", opened)
	}
	wantContains(t, dirFold.stderr, "does not scan a directory")
	if strings.Contains(dirFold.all(), bait) {
		t.Fatalf("a directory flag folded the session store:\n%s", dirFold.all())
	}
}
