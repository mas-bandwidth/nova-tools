package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grokUsage is a `grok usage` export (the xAI/Grok JSON shape) of one synthetic, sanitized
// turn ending 2026-09-12T00:05:00Z; more is the turn's further fields, each ending ", ".
func grokUsage(session, model string, in, out int, more string) string {
	return fmt.Sprintf(`{"sessionId": %q, "updatedAt": "2026-09-12T00:06:00Z", "session": {}, "turns": [{"turnNumber": 1, "endedAt": "2026-09-12T00:05:00Z", "inputTokens": %d, "outputTokens": %d, %s"primaryModelId": %q}]}`, session, in, out, more, model)
}

// #626, Johnny's dogfood: a `grok usage` export folds through --provider xai to ONE day-file
// row for its turn, carrying its input, output and cost columns, and check accepts the day.
// The turn's cost, costUsdTicks, is 77 micro-dollar ticks (the unit the ledger's usd= holds,
// per the spec's "from the usage `usd` column or a cost tick the source reported"), so the
// model's one cost row of the day carries usd=0.000077. Before the fix the tokens folded and
// the cost was dropped: TOKENS AVG printed usd=0 where the source had reported a tick.
func TestGrokUsageFoldsToOneRowWithInOutUsd(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name, more string
		when       []string
		want       string
		avg        []string
	}{
		{"a turn with cache and reasoning counts, folded --all",
			`"cachedReadTokens": 800, "cacheCreationTokens": 0, "reasoningTokens": 40, "totalTokens": 1100, "modelCalls": 2, "costUsdTicks": 77, "turnCount": 1, `, []string{"--all"},
			"2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t0\t800\t40\t0\tutc\txai:johnny", []string{"usd=0.000077"}},
		{"a turn with in, out and cost only, folded --day", `"costUsdTicks": 77, `, []string{"--day", "2026-09-12"},
			"2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t-\t-\t-\t0\tutc\txai:johnny", []string{"usd=0.000077", "usd_per_mtok=0.0700"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			repos, out := reposFile(t, dir), testkit.Mkdir(t, filepath.Join(dir, "out"))
			grok := testkit.WriteFile(t, filepath.Join(dir, "grok-usage.json"), grokUsage("fixture-grok-session", "grok-model-example", 1000, 100, row.more))
			r := novaTokens.Do(t, append([]string{"fold", "--out", out, "--repos", repos, "--provider", "xai:johnny=" + grok}, row.when...)...)
			source := lineWith(r.Exit(0).Out("TOKENS DAY date=2026-09-12 rows=1 ").Stdout, "TOKENS SOURCE")
			for _, w := range []string{"kind=provider", "label=xai:johnny", "day_basis=utc"} {
				assert.Contains(t, source, w)
			}
			assert.Equal(t, row.want, lineWith(testkit.ReadFile(t, filepath.Join(out, "2026-09-12.tsv")), "grok-model-example"))
			novaTokens.Do(t, "check", "--out", out).Exit(0).Out("CHECK OK")
			rep := novaTokens.Do(t, "report", "--who", "johnny", "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+grok).Exit(0)
			avg := lineWith(rep.Stderr, "TOKENS AVG ")
			for _, w := range append(row.avg, "model=grok-model-example") {
				assert.Contains(t, avg, w)
			}
		})
	}
}

// TestXaiProviderOneUsageFileFoldsRow is #2671: --provider xai names one
// usage.json. A fixture file folds to one ledger row. A missing path is
// *XaiUsageMissingError, and a directory is not walked. A session store
// planted under HOME is never opened, nor a sibling usage.json the flag does not name.
func TestXaiProviderOneUsageFileFoldsRow(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	const bait = "424242"
	sessions := filepath.Join(home, ".grok", "sessions")
	testkit.WriteFile(t, filepath.Join(sessions, "encoded-cwd", "session-id", "usage.json"), grokUsage("bait-session", "bait-model", 424242, 1, ""))
	testkit.WriteFile(t, filepath.Join(dir, "other-usage.json"), grokUsage("sibling", "sibling-model", 515151, 1, ""))
	grok := testkit.WriteFile(t, filepath.Join(dir, "usage.json"), grokUsage("fixture-grok-session", "grok-model-example", 1000, 100, `"costUsdTicks": 77, `))
	repos := reposFile(t, dir)
	// fold is one fold of --provider xai:johnny=<path> into its own out directory, and the
	// count of source files it opened (tokens.Opens is process-wide: this test is serial).
	fold := func(out, path string) (testkit.Ran, int64) {
		before := tokens.Opens()
		r := novaTokens.Do(t, "fold", "--out", testkit.Mkdir(t, filepath.Join(dir, out)), "--day", "2026-09-12", "--repos", repos, "--provider", "xai:johnny="+path)
		return r.NotOut(bait).NotErr(bait), tokens.Opens() - before
	}

	r, opened := fold("out", grok)
	r.Exit(0).Out("TOKENS DAY date=2026-09-12 rows=1 ")
	assert.Equal(t, int64(1), opened, "source files opened; want the one usage.json the flag names")
	body := testkit.ReadFile(t, filepath.Join(dir, "out", "2026-09-12.tsv"))
	require.Equal(t, []string{"2026-09-12\tgrok-model-example\tunattributed\t1000\t100\t-\t-\t-\t0\tutc\txai:johnny"}, regexp.MustCompile(`(?m)^2026-.*$`).FindAllString(body, -1), "the folded rows")
	require.NotContains(t, body, bait, "the fold counted the session store")
	require.NotContains(t, body, "515151", "the fold counted a usage.json the flag did not name")

	missing := filepath.Join(dir, "no-such-usage.json")
	r, opened = fold("out-missing", missing)
	r.Exit(1).Err("TOKENS UNREADABLE", "does not scan a session store")
	assert.Zero(t, opened, "a missing xai path opened source files; that is a scan")
	_, err := tokens.ReadXaiUsageFile(missing)
	var typed *tokens.XaiUsageMissingError
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, missing, typed.Path)
	assert.ErrorIs(t, err, os.ErrNotExist)

	before := tokens.Opens()
	_, err = tokens.ReadXaiUsageFile(sessions)
	var notFile *tokens.XaiUsageNotFileError
	require.ErrorAs(t, err, &notFile)
	assert.NotErrorIs(t, err, os.ErrNotExist, "a directory that is there unwrapped as not-exist")
	assert.Zero(t, tokens.Opens()-before, "reading the sessions directory opened files")
	r, opened = fold("out-dir", sessions)
	r.Exit(1).Err("does not scan a directory")
	assert.Zero(t, opened, "fold of a directory opened source files; that is a scan")
}
