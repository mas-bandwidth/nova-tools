package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// jsonResult is the part of a --json result these tests read.
type jsonResult struct {
	Result struct {
		Verb   string   `json:"verb"`
		Status string   `json:"status"`
		Exit   int      `json:"exit"`
		Remedy string   `json:"remedy"`
		Why    []string `json:"why"`
	} `json:"result"`
	Facts map[string]any `json:"facts"`
	Items []struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"items"`
	Notes   []string `json:"notes"`
	Payload string   `json:"payload"`
}

// asJSON runs one invocation with --json and reads stdout as ONE object, nothing else on
// either stream.
func asJSON(t *testing.T, args ...string) (result, jsonResult) {
	t.Helper()
	r := invoke(t, args...)
	var got jsonResult
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &got), "stdout is not one JSON object:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(r.stdout), "\n")+1, "stdout holds more than one line:\n%s", r.stdout)
	assert.Empty(t, r.stderr, "--json wrote to stderr")
	return r, got
}

func kinds(j jsonResult) map[string]int {
	out := map[string]int{}
	for _, it := range j.Items {
		out[it.Kind]++
	}
	return out
}

// foldedBench is the example bench folded once into its ./out: the day file every reading
// verb below reads.
func foldedBench(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), dir)
	mkdir(t, filepath.Join(dir, "out"))
	wantExit(t, invoke(t, "fold", "--out", filepath.Join(dir, "out"), "--day", "2026-09-11", "--repos", filepath.Join(dir, "repos.tsv"),
		"--claude", "bench="+filepath.Join(dir, "transcripts")), 0)
	return dir
}

// Every verb but version takes --json: one object on stdout, nothing on stderr, the same
// exit as the lines, and the same values (T-4: no verb took --json).
func TestEveryVerbAnswersAsOneJSONObject(t *testing.T) {
	t.Parallel()

	dir := foldedBench(t)
	out, repos, tr := filepath.Join(dir, "out"), filepath.Join(dir, "repos.tsv"), filepath.Join(dir, "transcripts")
	session := write(t, filepath.Join(dir, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3, "output_tokens": 4})+"\n")
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
		{"an unknown verb", []string{"collate", "--json"},
			2, "refused", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := invoke(t, tc.args...)
			args := tc.args
			if tc.name != "an unknown verb" {
				args = append(append([]string(nil), tc.args...), "--json")
			}
			r, got := asJSON(t, args...)
			assert.Equal(t, tc.exit, r.exit)
			assert.Equal(t, text.exit, r.exit, "--json and the lines end with different exits")
			assert.Equal(t, tc.status, got.Result.Status)
			assert.Equal(t, tc.exit, got.Result.Exit)
			for k, v := range tc.facts {
				assert.Equal(t, v, got.Facts[k], "fact %s", k)
			}
			for k, n := range tc.items {
				assert.Equal(t, n, kinds(got)[k], "items of kind %s", k)
			}
			if tc.status == "refused" {
				assert.NotEmpty(t, got.Result.Why)
				assert.NotEmpty(t, got.Result.Remedy)
			}
		})
	}
}

// Every verb's -h ends its lines with its effect, and every verb that writes takes
// --dry-run (docs/STANDARD.md section 2; the tool-answers ledger's dry-run row).
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
			r := invoke(t, tc.verb, "-h")
			wantExit(t, r, 0)
			assert.Contains(t, r.stdout, "\n"+tc.effect)
			assert.Equal(t, tc.dryRun, strings.Contains(r.stdout, "  --dry-run"), "--dry-run listed:\n%s", r.stdout)
			if tc.verb != "version" {
				assert.Contains(t, r.stdout, "  --json")
			}
		})
	}
}

// A dry run reads what the real run reads, says what it would write, and writes nothing:
// no day file, no lock, no directory, no note (X12: fold, ledger and session wrote with no
// --dry-run).
func TestADryRunWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), dir)
	out := mkdir(t, filepath.Join(dir, "out"))
	repos, tr := filepath.Join(dir, "repos.tsv"), filepath.Join(dir, "transcripts")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--dry-run")
	wantExit(t, r, 0)
	assert.Contains(t, r.stdout, "written=false would_write=true")
	assert.Contains(t, r.stdout, "TOKENS OK days=1 rows=2 ")
	assert.Contains(t, r.stdout, " dry_run=true\n")
	ents, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Empty(t, ents, "a dry-run fold wrote into --out (a day file or the lock)")

	session := write(t, filepath.Join(dir, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3})+"\n")
	newOut := filepath.Join(dir, "new-out")
	r = invoke(t, "session", "--claude-session", session, "--out", newOut, "--dry-run")
	wantExit(t, r, 0)
	assert.Contains(t, r.stdout, "TOKENS DAY day=2026-09-11 written=false")
	assert.NoDirExists(t, newOut, "a dry-run session made its --out")

	note := filepath.Join(dir, "note.txt")
	r = invoke(t, "report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--note", note, "--dry-run")
	wantExit(t, r, 0)
	assert.Contains(t, r.stderr, " dry_run=true note="+note+" subject=")
	assert.NoFileExists(t, note, "a dry-run report wrote --note")

	// The real fold writes, and a dry-run ledger reads that day and dials no store: an
	// address nothing listens on is never reached.
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr), 0)
	r = invoke(t, "ledger", "--out", out, "--day", "2026-09-11", "--redis", "127.0.0.1:0", "--dry-run")
	wantExit(t, r, 0)
	assert.Equal(t, "LEDGER day=2026-09-11 rows=2\nLEDGER OK day=2026-09-11 days=1 rows=2 bad=0 dry_run=true\n", r.stdout)
}

// A gate that cannot go red is no gate: an --out holding no day file is a finding, with
// the fold that makes the first one as the remedy (T-1: check was green on nothing).
func TestCheckOverAnOutWithNoDayFileSaysNo(t *testing.T) {
	t.Parallel()

	out := mkdir(t, filepath.Join(t.TempDir(), "out"))
	r := invoke(t, "check", "--out", out)
	wantExit(t, r, 1)
	assert.Empty(t, r.stdout)
	assert.Contains(t, r.stderr, "CHECK FAIL --out "+out+" holds no day file, so there is nothing to check; fold one first: nova-tokens fold --out "+out)
	assert.Contains(t, r.stderr, "CHECK FAIL files=0 rows=0 first=- last=- bad=0 missing=0 stray=0 gap=0 notes=0\n")
}

// A source that fed nothing for a day its file names is said on the note, never "nothing
// was wrong" (T-3: TOKENS OK ... quiet=1 and then the all-clear).
func TestAQuietSourceIsTheNoteNotTheAllClear(t *testing.T) {
	t.Parallel()

	out, repos, poolA, poolB := foldPools(t, "410", "100", "2000", "420")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-14", "--repos", repos, "--swarm", "ada="+poolA, "--swarm", "bo="+poolB), 0)
	require.NoError(t, os.Remove(filepath.Join(poolA, "usage", "j1.tsv")))
	// bo's row grows past what ada's was, so the day does not shrink: ada is quiet alone.
	swarmUsage(t, poolB, "j2", swarmRow("j2", "1", "-", "mercury-2.5", "serialize", "2026-09-14T02:00:00Z", "5000", "5000", "0", "81000", "50"))

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-14", "--repos", repos, "--swarm", "ada="+poolA, "--swarm", "bo="+poolB)
	wantExit(t, r, 0)
	assert.Contains(t, r.stdout, " quiet=1\n")
	assert.Contains(t, r.stderr, "TOKENS QUIET label=swarm:ada day=2026-09-14")
	assert.NotContains(t, r.stdout, "nothing was wrong")
	assert.Contains(t, r.stdout, "TOKENS NOTE a declared source fed no message for a day its file names (swarm:ada on 2026-09-14)")
}

// The banner's word on the environment is the code's: the Redis verbs read the seat from
// the variables redisauth names, and every other verb reads none (T-6).
func TestTheBannerNamesTheEnvironmentTheRedisVerbsRead(t *testing.T) {
	t.Parallel()

	var help strings.Builder // the verbs' own help, where what each reads is said
	for _, verb := range []string{"fold", "report", "ledger", "sources", "session"} {
		help.WriteString(invoke(t, "help", verb).stdout)
	}
	assert.NotContains(t, help.String(), "no environment variable is consulted")
	for _, name := range []string{redisauth.UserEnv, redisauth.PasswordEnvEnv, redisauth.DefaultPasswordEnv, "$PATH", tokens.LockName} {
		assert.Contains(t, help.String(), name)
	}
}

// The definition meets the standard the banner and help carry by construction only when
// it is complete: every verb's effect, the how text's size, the status words.
func TestTokensToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, tokensTool(foldStamp).Problems())
}

// `help <verb>` puts --help right after the verb, so a word or a -- after it never turns
// the request for help into a run or a refusal of the verb.
func TestHelpForAVerbIsHelpWhateverFollowsIt(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"help", "sum", "extra"},
		{"help", "fold", "--", "x"},
	} {
		r := invoke(t, args...)
		wantExit(t, r, 0)
		assert.Contains(t, r.stdout, "usage: nova-tokens "+args[1]+" [flags]", "%v", args)
		assert.Empty(t, r.stderr, "%v", args)
	}
}

// A model fed by a priced source and an unpriced one: usd= is the cost the sources
// reported, usd_per_mtok= divides it by the tokens that cost covers and no others, and
// unpriced= counts the tokens no source priced, so the rate never stands for tokens whose
// cost is unknown (review 5084: the rate divided the reported cost by every token).
func TestAMixedPricedAndUnpricedModelRatesOnlyItsPricedTokens(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-14T03:00:00Z", "mercury-2.5", map[string]int{"input_tokens": 3000}, "/x/serialize/a.go")+"\n")
	pool := mkdir(t, filepath.Join(dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRowCost("j1", "1", "-", "deepseek", "mercury-2.5", "serialize", "2026-09-14T01:00:00Z", "1000", "0", "0", "0", "-", "0.5"))
	r := invoke(t, "report", "--who", "ada", "--day", "2026-09-14", "--repos", reposFile(t, dir), "--claude", "g="+tr, "--swarm", "b="+pool)
	wantExit(t, r, 0)
	assert.Contains(t, r.stderr, "TOKENS AVG day=2026-09-14 model=deepseek/mercury-2.5 tokens=4000 usd=0.5 usd_per_mtok=500.0000 unpriced=3000\n")
	assert.Contains(t, r.stderr, "TOKENS AVG-ALL day=2026-09-14 tokens=4000 usd=0.5 usd_per_mtok=500.0000 unpriced=3000\n")

	// Wholly priced: unpriced=0. Wholly unpriced: every cost field a dash.
	r = invoke(t, "report", "--who", "ada", "--day", "2026-09-14", "--repos", reposFile(t, dir), "--swarm", "b="+pool)
	assert.Contains(t, r.stderr, "TOKENS AVG day=2026-09-14 model=deepseek/mercury-2.5 tokens=1000 usd=0.5 usd_per_mtok=500.0000 unpriced=0\n")
	r = invoke(t, "report", "--who", "ada", "--day", "2026-09-14", "--repos", reposFile(t, dir), "--claude", "g="+tr)
	assert.Contains(t, r.stderr, "TOKENS AVG day=2026-09-14 model=mercury-2.5 tokens=3000 usd=- usd_per_mtok=- unpriced=3000\n")
}
