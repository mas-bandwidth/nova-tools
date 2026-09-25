package main

import (
	"path/filepath"
	"testing"
)

// TestGrokUsageFoldsToOneRowWithInOutUsd is #626's red test (Johnny's dogfood):
// a fixture `grok usage` export, folded through --provider xai, lands ONE row
// for its turn -- the in and the out in the day file -- and the turn's cost,
// costUsdTicks, is 77 micro-dollar ticks (the unit the ledger's usd= holds, per
// the spec's "from the usage `usd` column or a cost tick the source reported"),
// so the model's one cost row of the day carries usd=0.000077. Before the fix
// the tokens folded and the cost was dropped: TOKENS AVG printed usd=0 where
// the source had reported a tick, and nothing anywhere carried the dollars.
func TestGrokUsageFoldsToOneRowWithInOutUsd(t *testing.T) {
	dir := t.TempDir()
	repos := reposFile(t, dir)
	out := mkdir(t, filepath.Join(dir, "out"))
	grok := write(t, filepath.Join(dir, "grok-usage.json"), `{
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

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+grok)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS DAY date=2026-09-12 rows=1 ")
	wantContains(t, read(t, filepath.Join(out, "2026-09-12.tsv")),
		"2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t-\t-\t-\t0\tutc\txai:johnny")
	if c := invoke(t, "check", "--out", out); c.exit != 0 {
		t.Errorf("check over the folded day: exit %d, want 0\n%s", c.exit, c.stderr)
	}

	rep := invoke(t, "report", "--who", "johnny", "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+grok)
	wantExit(t, rep, 0)
	avg := lineWith(rep.stderr, "TOKENS AVG ")
	wantContains(t, avg, "model=grok-model-example")
	wantContains(t, avg, "usd=0.000077")
	wantContains(t, avg, "usd_per_mtok=0.0700")
}
