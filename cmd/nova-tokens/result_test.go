package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// Every verb but version takes --json: one object on stdout, nothing on stderr, the same
// exit as the lines, and the same values (T-4: no verb took --json). The bench is the
// example bench folded once into its ./out: the day file every reading verb reads.
func TestEveryVerbAnswersAsOneJSONObject(t *testing.T) {
	t.Parallel()

	dir := exampleBench(t)
	out, repos, tr := filepath.Join(dir, "out"), filepath.Join(dir, "repos.tsv"), filepath.Join(dir, "transcripts")
	novaTokens.Do(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr).Exit(0)
	session := testkit.WriteFile(t, filepath.Join(dir, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3, "output_tokens": 4})+"\n")
	for _, tc := range []struct {
		name   string
		args   []string
		exit   int
		status string
		facts  map[string]any
		items  map[string]int
	}{
		{"fold", []string{"fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr},
			0, "ok", map[string]any{"days": 1.0, "rows": 2.0, "unreadable": 0.0}, map[string]int{"fold": 1, "source": 1, "day": 1}},
		{"sum", []string{"sum", "--out", out, "--month", "2026-09"},
			0, "ok", map[string]any{"month": "2026-09", "days": 1.0, "pairs": 2.0}, map[string]int{"month": 1, "pair": 2, "model": 1, "total": 1}},
		{"check", []string{"check", "--out", out},
			0, "ok", map[string]any{"files": 1.0, "missing": 0.0}, nil},
		{"sources", []string{"sources", "--repos", repos, "--all", "--claude", "bench=" + tr},
			0, "ok", map[string]any{"sources": 1.0}, map[string]int{"source": 1}},
		{"report", []string{"report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr},
			0, "ok", map[string]any{"who": "ada", "rows": 7.0}, map[string]int{"avg": 1, "avg-all": 1}},
		{"session", []string{"session", "--claude-session", session},
			0, "ok", map[string]any{"turns": 1.0, "input": 3.0}, nil},
		{"profiles", []string{"profiles", "--swarm-root", dir},
			0, "ok", map[string]any{"models": 0.0}, nil},
		{"ledger dry run", []string{"ledger", "--out", out, "--day", "2026-09-11", "--redis", "127.0.0.1:0", "--dry-run"},
			0, "ok", map[string]any{"day": "2026-09-11", "days": 1.0, "dry_run": true}, map[string]int{"day": 1}},
		{"a refusal", []string{"sum", "--out", out},
			2, "refused", nil, nil},
		{"an unknown flag", []string{"sum", "--out", out, "--month", "2026-09", "--mnth"},
			2, "refused", nil, nil},
		{"an unknown verb", []string{"collate"},
			2, "refused", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text := novaTokens.Do(t, tc.args...)
			r := novaTokens.Do(t, append(tc.args[:len(tc.args):len(tc.args)], "--json")...).Exit(tc.exit)
			assert.Equal(t, text.Code, r.Code, "--json and the lines end with different exits")
			assert.Empty(t, r.Stderr, "--json wrote to stderr")
			assert.Equal(t, 1, strings.Count(strings.TrimSpace(r.Stdout), "\n")+1, "stdout holds more than one line:\n%s", r.Stdout)
			got := testkit.JSON[struct {
				Result struct {
					Status, Remedy string
					Exit           int
					Why            []string
				}
				Facts map[string]any
				Items []struct{ Kind string }
			}](t, r.Stdout)
			assert.Equal(t, tc.status, got.Result.Status)
			assert.Equal(t, tc.exit, got.Result.Exit)
			for k, v := range tc.facts {
				assert.Equal(t, v, got.Facts[k], "fact %s", k)
			}
			kinds := map[string]int{}
			for _, it := range got.Items {
				kinds[it.Kind]++
			}
			for k, n := range tc.items {
				assert.Equal(t, n, kinds[k], "items of kind %s", k)
			}
			if tc.status == "refused" {
				assert.NotEmpty(t, got.Result.Why)
				assert.NotEmpty(t, got.Result.Remedy)
			}
		})
	}
}

// Every verb's -h ends its lines with its effect, and every verb that writes takes
// --dry-run (docs/STANDARD.md section 2; the tool-answers ledger's dry-run row). Around it,
// what the help says beyond one verb.
func TestEveryVerbsHelpStatesItsEffect(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		verb, effect string
		dryRun       bool
	}{
		{"fold", "effect: local write:", true},
		{"report", "effect: local write:", true},
		{"ledger", "effect: delivery:", true},
		{"session", "effect: local write:", true},
		{"sum", "effect: inspection: reads, writes nothing", false},
		{"check", "effect: inspection: reads, writes nothing", false},
		{"sources", "effect: inspection: reads, writes nothing", false},
		{"profiles", "effect: inspection: reads, writes nothing", false},
		{"version", "effect: inspection: reads, writes nothing", false},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			t.Parallel()
			r := novaTokens.Do(t, tc.verb, "-h").Exit(0).Out("\n" + tc.effect)
			assert.Equal(t, tc.dryRun, strings.Contains(r.Stdout, "  --dry-run"), "--dry-run listed:\n%s", r.Stdout)
			if tc.verb != "version" {
				r.Out("  --json")
			}
		})
	}
	// The banner's word on the environment is the code's: the Redis verbs read the seat
	// from the variables redisauth names, and every other verb reads none (T-6). The
	// verbs' own help is where what each reads is said.
	t.Run("the environment the Redis verbs read", func(t *testing.T) {
		t.Parallel()
		var help string
		for _, verb := range []string{"fold", "report", "ledger", "sources", "session"} {
			help += novaTokens.Do(t, "help", verb).Stdout
		}
		assert.NotContains(t, help, "no environment variable is consulted")
		for _, name := range []string{redisauth.UserEnv, redisauth.PasswordEnvEnv, redisauth.DefaultPasswordEnv, "$PATH", tokens.LockName} {
			assert.Contains(t, help, name)
		}
	})
	// A verb a reader cannot find is a verb behind the source.
	t.Run("help names the session verb", func(t *testing.T) {
		t.Parallel()
		novaTokens.Do(t, "help").Exit(0).Out("nova-tokens session --claude-session <jsonl>")
		novaTokens.Do(t, "help", "session").Exit(0).Out("<model>/coordinator")
	})
	// The definition meets the standard the banner and help carry by construction only
	// when it is complete: every verb's effect, the how text's size, the status words.
	t.Run("the definition meets the standard", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, tokensTool(foldStamp).Problems())
	})
}

// A dry run reads what the real run reads, says what it would write, and writes nothing:
// no day file, no lock, no directory, no note (X12: fold, ledger and session wrote with no
// --dry-run).
func TestADryRunWritesNothing(t *testing.T) {
	t.Parallel()

	dir := exampleBench(t)
	out, repos, tr := filepath.Join(dir, "out"), filepath.Join(dir, "repos.tsv"), filepath.Join(dir, "transcripts")
	novaTokens.Do(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--dry-run").
		Exit(0).Out("written=false would_write=true", "TOKENS OK days=1 rows=2 ", " dry_run=true\n")
	ents, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Empty(t, ents, "a dry-run fold wrote into --out (a day file or the lock)")

	session := testkit.WriteFile(t, filepath.Join(dir, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3})+"\n")
	newOut := filepath.Join(dir, "new-out")
	novaTokens.Do(t, "session", "--claude-session", session, "--out", newOut, "--dry-run").Exit(0).Out("TOKENS DAY day=2026-09-11 written=false")
	assert.NoDirExists(t, newOut, "a dry-run session made its --out")

	note := filepath.Join(dir, "note.txt")
	novaTokens.Do(t, "report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--note", note, "--dry-run").
		Exit(0).Err(" dry_run=true note=" + note + " subject=")
	assert.NoFileExists(t, note, "a dry-run report wrote --note")

	// The real fold writes, and a dry-run ledger reads that day and dials no store: an
	// address nothing listens on is never reached.
	novaTokens.Do(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr).Exit(0)
	r := novaTokens.Do(t, "ledger", "--out", out, "--day", "2026-09-11", "--redis", "127.0.0.1:0", "--dry-run").Exit(0)
	assert.Equal(t, "LEDGER day=2026-09-11 rows=2\nLEDGER OK day=2026-09-11 days=1 rows=2 bad=0 dry_run=true\n", r.Stdout)
}

// A gate that cannot go red is no gate: an --out holding no day file is a finding, with
// the fold that makes the first one as the remedy (T-1: check was green on nothing).
func TestCheckOverAnOutWithNoDayFileSaysNo(t *testing.T) {
	t.Parallel()

	out := testkit.Mkdir(t, filepath.Join(t.TempDir(), "out"))
	r := novaTokens.Do(t, "check", "--out", out).Exit(1).Err(
		"CHECK FAIL --out "+out+" holds no day file, so there is nothing to check; fold one first: nova-tokens fold --out "+out,
		"CHECK FAIL files=0 rows=0 first=- last=- bad=0 missing=0 stray=0 gap=0 notes=0\n")
	assert.Empty(t, r.Stdout, r)
}

// A source that fed nothing for a day its file names is said on the note, never "nothing
// was wrong" (T-3: TOKENS OK ... quiet=1 and then the all-clear).
func TestAQuietSourceIsTheNoteNotTheAllClear(t *testing.T) {
	t.Parallel()

	out, repos, poolA, poolB := foldPools(t, "410", "100", "2000", "420")
	fold := []string{"fold", "--out", out, "--day", "2026-09-14", "--repos", repos, "--swarm", "ada=" + poolA, "--swarm", "bo=" + poolB}
	novaTokens.Do(t, fold...).Exit(0)
	require.NoError(t, os.Remove(filepath.Join(poolA, "usage", "j1.tsv")))
	// bo's row grows past what ada's was, so the day does not shrink: ada is quiet alone.
	swarmUsage(t, poolB, "j2", swarmRow("j2", "1", "-", "mercury-2.5", "serialize", "2026-09-14T02:00:00Z", "5000", "5000", "0", "81000", "50"))
	novaTokens.Do(t, fold...).Exit(0).Out(" quiet=1\n", "TOKENS NOTE a declared source fed no message for a day its file names (swarm:ada on 2026-09-14)").
		NotOut("nothing was wrong").Err("TOKENS QUIET label=swarm:ada day=2026-09-14")
}

// `help <verb>` puts --help right after the verb, so a word or a -- after it never turns
// the request for help into a run or a refusal of the verb.
func TestHelpForAVerbIsHelpWhateverFollowsIt(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help", "sum", "extra"}, {"help", "fold", "--", "x"}} {
		r := novaTokens.Do(t, args...).Exit(0).Out("usage: nova-tokens " + args[1] + " [flags]")
		assert.Empty(t, r.Stderr, r)
	}
}

// A model fed by a priced source and an unpriced one: usd= is the cost the sources
// reported, usd_per_mtok= divides it by the tokens that cost covers and no others, and
// unpriced= counts the tokens no source priced, so the rate never stands for tokens whose
// cost is unknown (review 5084: the rate divided the reported cost by every token).
func TestAMixedPricedAndUnpricedModelRatesOnlyItsPricedTokens(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-14T03:00:00Z", "mercury-2.5", map[string]int{"input_tokens": 3000}, "/x/serialize/a.go"))
	pool := testkit.Mkdir(t, filepath.Join(b.dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRowCost("j1", "1", "-", "deepseek", "mercury-2.5", "serialize", "2026-09-14T01:00:00Z", "1000", "0", "0", "0", "-", "0.5"))
	report := func(sources ...string) testkit.Ran {
		return novaTokens.Do(t, append([]string{"report", "--who", "ada", "--day", "2026-09-14", "--repos", b.repos}, sources...)...).Exit(0)
	}
	report("--claude", "g="+b.tr, "--swarm", "b="+pool).Err(
		"TOKENS AVG day=2026-09-14 model=deepseek/mercury-2.5 tokens=4000 usd=0.5 usd_per_mtok=500.0000 unpriced=3000\n",
		"TOKENS AVG-ALL day=2026-09-14 tokens=4000 usd=0.5 usd_per_mtok=500.0000 unpriced=3000\n")
	// Wholly priced: unpriced=0. Wholly unpriced: every cost field a dash.
	report("--swarm", "b="+pool).Err("TOKENS AVG day=2026-09-14 model=deepseek/mercury-2.5 tokens=1000 usd=0.5 usd_per_mtok=500.0000 unpriced=0\n")
	report("--claude", "g="+b.tr).Err("TOKENS AVG day=2026-09-14 model=mercury-2.5 tokens=3000 usd=- usd_per_mtok=- unpriced=3000\n")
}
