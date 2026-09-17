package main

import (
	"path/filepath"
	"testing"
)

// TestGrokUsageFileFoldsToLedgerRow is the red test issue #626 asks for: a fixture `grok
// usage` export (the xAI/Grok JSON shape) folds through the xAI provider parser into one
// day-file row carrying the turn's input, output and cost columns, and check accepts the
// day it wrote. The fixture is the sanitized turn docs/MAPPING-TOKENS-GROK.md names.
func TestGrokUsageFileFoldsToLedgerRow(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	grok := write(t, filepath.Join(dir, "grok-usage.json"), `{
  "sessionId": "fixture-grok-session",
  "updatedAt": "2026-09-12T00:06:00Z",
  "session": {},
  "turns": [
    {
      "turnNumber": 1,
      "endedAt": "2026-09-12T00:05:00Z",
      "inputTokens": 1000,
      "outputTokens": 100,
      "cachedReadTokens": 800,
      "cacheCreationTokens": 0,
      "reasoningTokens": 40,
      "totalTokens": 1100,
      "modelCalls": 2,
      "costUsdTicks": 77,
      "turnCount": 1,
      "primaryModelId": "grok-model-example"
    }
  ]
}`)

	r := invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--provider", "xai:johnny="+grok)
	wantExit(t, r, 0)
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "kind=provider")
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "label=xai:johnny")
	wantContains(t, lineWith(r.stdout, "TOKENS SOURCE"), "day_basis=utc")

	body := read(t, filepath.Join(out, "2026-09-12.tsv"))
	if line := lineWith(body, "grok-model-example"); line == "" {
		t.Fatalf("no Grok ledger row in the day file:\n%s", body)
	} else if line != "2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t0\t800\t40\t0\tutc\txai:johnny" {
		t.Errorf("Grok row is %q, want input/output/cost columns filled", line)
	}

	c := invoke(t, "check", "--out", out)
	wantExit(t, c, 0)
	wantContains(t, c.stdout, "CHECK OK")
}
