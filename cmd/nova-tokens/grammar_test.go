package main

// The printed OUTPUT GRAMMAR of docs/SPEC-TOKENS.md is the contract with a scanner, and a
// line the tool prints that the grammar does not admit is a line no scanner can parse.
// Two were found by a cold read: `TOKENS UNPARSED label=bus:<name>` while four readers
// print `claude:`, `opencode:`, `swarm:` and `provider:` labels, and `day_basis=<utc|zone>`
// while a bus lane carrying two bases prints `mixed` (the prose beside the block already
// said so; the block did not).

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outputGrammar is the fenced grammar block of docs/SPEC-TOKENS.md -- the one that carries
// `TOKENS FOLD at=` -- keyed by the first two words of each line.
func outputGrammar(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, err)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "SPEC-TOKENS.md"))
	require.NoError(t, err, err)
	var block, cur []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in {
				for _, l := range cur {
					if strings.HasPrefix(l, "TOKENS FOLD at=") {
						block = cur
					}
				}
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, line)
		}
	}
	require.NotNil(t, block, "docs/SPEC-TOKENS.md has no OUTPUT GRAMMAR block carrying `TOKENS FOLD at=`; this test was reading the wrong thing and would have passed by checking nothing")
	out := map[string]string{}
	for _, l := range block {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		out[f[0]+" "+strings.TrimSuffix(f[1], ":")] = l
	}
	return out
}

// grammarPairs splits the head of a line (everything before the free-text tail) into its
// key=value pairs. A token with no `=` continues the value before it, because a rendered
// value can carry whitespace inside quotes.
func grammarPairs(s string) map[string]string {
	out := map[string]string{}
	key := ""
	for _, tok := range strings.Fields(s) {
		if i := strings.Index(tok, "="); i > 0 && !strings.ContainsAny(tok[:i], "<>:\"") {
			key = tok[:i]
			out[key] = tok[i+1:]
			continue
		}
		if key != "" {
			out[key] += " " + tok
		}
	}
	return out
}

// grammarEnum is the set of members a `<a|b|c>` value admits. A member in angle brackets --
// `<zone>` in `<utc|mixed|<zone>>` -- is a PLACEHOLDER for a class of values, not a literal,
// and comes back with its brackets so the matcher can check the class instead of the word:
// the grammar used to spell that member `zone`, a word the tool has never printed, and the
// zone name it does print was admitted by nothing. A one-letter member is a placeholder too
// (`<all|d>`, `<n|->`), and this test knows no class for it, so such a template is not a set.
func grammarEnum(spec string) ([]string, bool) {
	if !strings.HasPrefix(spec, "<") || !strings.HasSuffix(spec, ">") {
		return nil, false
	}
	alts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">"), "|")
	if len(alts) < 2 {
		return nil, false
	}
	for _, a := range alts {
		if strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") && len(a) > 2 {
			continue // a named placeholder, checked by grammarPlaceholder
		}
		if len(a) < 2 || strings.ContainsAny(a, "<>") {
			return nil, false
		}
	}
	return alts, true
}

// grammarAdmits reports whether an enumerated value is admitted: a literal member matches
// exactly, a bracketed member by its class.
func grammarAdmits(alts []string, v string) bool {
	for _, a := range alts {
		if strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") && len(a) > 2 {
			if grammarPlaceholder(a[1:len(a)-1], v, alts) {
				return true
			}
			continue
		}
		if v == a {
			return true
		}
	}
	return false
}

// grammarPlaceholder is the class each named placeholder stands for. `<zone>` is the zone
// an export declares, as rule 17 and rule 13 accept it: non-empty, no whitespace, and not
// one of the literal members standing beside it (`utc` spelled out is refused by rule 6).
// A placeholder this test knows no class for admits anything, which is the old behaviour.
func grammarPlaceholder(name, v string, alts []string) bool {
	switch name {
	case "zone":
		if v == "" || strings.ContainsAny(v, " \t") {
			return false
		}
		for _, a := range alts {
			if v == a {
				return false
			}
		}
		return true
	}
	return true
}

func head(s string) string {
	if i := strings.Index(s, ": "); i >= 0 {
		return s[:i]
	}
	return s
}

// checkAgainstGrammar fails if the grammar has no line of this kind, does not name a key
// the line prints, or admits a narrower value than the line carries.
func checkAgainstGrammar(t *testing.T, grammar map[string]string, line string) {
	t.Helper()
	f := strings.Fields(head(line))
	if len(f) < 2 {
		return
	}
	kind := f[0] + " " + strings.TrimSuffix(f[1], ":")
	tmpl, ok := grammar[kind]
	if !ok {
		assert.Failf(t, "output grammar lacks printed line", "the tool prints %q; the output grammar has no line for %s, so a consumer scanning the grammar cannot parse it", line, kind)
		return
	}
	want := grammarPairs(head(tmpl))
	for k, v := range grammarPairs(head(line)) {
		spec, ok := want[k]
		if !ok {
			assert.Failf(t, "output grammar field missing", "%s prints %s=%s; the output grammar's %s line has no %s= field", kind, k, v, kind, k)
			continue
		}
		if i := strings.Index(spec, "<"); i > 0 {
			assert.True(t, strings.HasPrefix(v, spec[:i]), "%s prints %s=%s; the output grammar admits only %s=%s", kind, k, v, k, spec)
			continue
		}
		if alts, closed := grammarEnum(spec); closed {
			assert.True(t, grammarAdmits(alts, v), "%s prints %s=%s; the output grammar enumerates %s=%s", kind, k, v, k, spec)
		}
	}
}

// printedLines is every line of a run that starts with one of the tool's tokens.
func printedLines(r result) []string {
	var out []string
	for _, s := range []string{r.stdout, r.stderr} {
		for _, line := range strings.Split(s, "\n") {
			switch strings.Fields(line + " x")[0] {
			case "TOKENS", "SOURCES", "SUM", "CHECK", "REPORT":
				out = append(out, line)
			}
		}
	}
	return out
}

func TestTheOutputGrammarAdmitsTheLinesTheToolPrints(t *testing.T) {
	t.Parallel()

	grammar := outputGrammar(t)

	// A transcript with a stamp this tool cannot read: the UNPARSED line carries a
	// `claude:` label, not a `bus:` one.
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := write(t, filepath.Join(dir, "a.jsonl"), strings.Join([]string{
		`{"type":"assistant","timestamp":"2026-09-11T10:00:00Z","message":{"id":"m1","model":"f","usage":{"input_tokens":7}},"cwd":"/x/schema"}`,
		`{"type":"assistant","timestamp":"the eleventh","message":{"id":"m3","model":"f","usage":{"input_tokens":9}}}`,
	}, "\n")+"\n")
	runs := []result{invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--claude", "g="+tr)}

	// A bus lane carrying a six-field and a seven-field line: day_basis=mixed.
	dir2 := t.TempDir()
	out2 := mkdir(t, filepath.Join(dir2, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir2, "bus")), "operator")
	busNote(t, bus, "operator", "a.md", "operator-000000000001", "tokens 2026-09-11", busDate, strings.Join([]string{
		"2026-09-11\toperator\tutcmodel\tschema\tinput\t100",
		"2026-09-11\toperator\tzonemodel\tschema\tinput\t5\tday_basis=America/Los_Angeles",
		"",
	}, "\n"))
	runs = append(runs,
		invoke(t, "fold", "--out", out2, "--all", "--repos", reposFile(t, dir2), "--bus", bus),
		invoke(t, "sources", "--all", "--repos", reposFile(t, dir2), "--bus", bus),
		invoke(t, "sum", "--out", out2, "--month", "2026-09"),
		invoke(t, "check", "--out", out2))

	// A provider export of per-day totals in its own zone (rule 17): the SOURCE line's
	// `day_basis=` is the zone NAME, `America/Los_Angeles`, never the word `zone`. The
	// grammar said `<utc|zone|mixed>` and the tool has never printed `zone`.
	dir3 := t.TempDir()
	out3 := mkdir(t, filepath.Join(dir3, "out"))
	xai := write(t, filepath.Join(dir3, "xai.csv"), strings.Join([]string{
		"# timezone: America/Los_Angeles",
		"date,model,input,output,reasoning",
		"2026-09-11,grok-4,9912340,301122,55",
		"",
	}, "\n"))
	runs = append(runs,
		invoke(t, "fold", "--out", out3, "--all", "--repos", reposFile(t, dir3), "--provider", "xai:reader-f="+xai),
		invoke(t, "sources", "--all", "--repos", reposFile(t, dir3), "--provider", "xai:reader-f="+xai),
		invoke(t, "sum", "--out", out3, "--month", "2026-09"),
		invoke(t, "check", "--out", out3))

	// A partial-source fold: produces a TOKENS PARTIAL line when a row was blended across
	// declared and undeclared sources (#268).
	dir4 := t.TempDir()
	out4 := mkdir(t, filepath.Join(dir4, "out"))
	repos4 := reposFile(t, dir4)
	poolA := mkdir(t, filepath.Join(dir4, "poolA"))
	poolB := mkdir(t, filepath.Join(dir4, "poolB"))
	swarmUsage(t, poolA, "j1", swarmRow("j1", "1", "-", "claude-x", "serialize", "2026-09-14T01:00:00Z", "410", "100", "0", "0", "-"))
	swarmUsage(t, poolB, "j2", swarmRow("j2", "1", "-", "claude-x", "serialize", "2026-09-14T02:00:00Z", "2000", "420", "0", "0", "-"))
	invoke(t, "fold", "--out", out4, "--day", "2026-09-14", "--repos", repos4, "--swarm", "seat-a="+poolA, "--swarm", "reader-b="+poolB)
	runs = append(runs,
		invoke(t, "fold", "--out", out4, "--day", "2026-09-14", "--repos", repos4, "--swarm", "reader-b="+poolB))

	sawPartial := false
	n := 0
	for _, r := range runs {
		for _, line := range printedLines(r) {
			n++
			checkAgainstGrammar(t, grammar, line)
			if strings.HasPrefix(line, "TOKENS PARTIAL ") {
				sawPartial = true
			}
		}
	}
	require.True(t, sawPartial, "no TOKENS PARTIAL line was checked against the grammar")
	require.GreaterOrEqual(t, n, 10, "%d printed lines checked against the grammar; the fixtures printed nothing and this test would have passed by checking nothing", n)
}

// TestStatusGrammar pins the first word after the verb token together with the exit code
// (docs/STANDARD.md section 2): OK at 0, REFUSED at 2, FAILED at 1. Every verb runs
// through run() over fixtures in t.TempDir(); ledger runs --dry-run, which dials no
// store, so no case opens a socket. sources, sum and profiles have no exit-1 path:
// cmdSources ends in SOURCES OK (main.go), cmdSum exits 0 whenever it ran (its comment
// above it), and profileSwarmRoot ends in PROFILES OK (profiles.go), so their rows stop
// at REFUSED. session's exit 1 carries TOKENS REFUSED, never FAILED (session.go), and
// version prints no status word on success and never exits 1 (version.go); those rows pin
// the exit code alone.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   func(*testing.T) []string
		token  string
		word   string
		exit   int
		stream string
	}{
		{
			"fold ok",
			func(t *testing.T) []string { return statusFoldArgs(t, false) },
			"TOKENS", "OK", 0, "stdout",
		},
		{
			"fold refused",
			func(t *testing.T) []string { return []string{"fold"} },
			"TOKENS", "REFUSED", 2, "stderr",
		},
		{
			"fold failed",
			func(t *testing.T) []string { return statusFoldArgs(t, true) },
			"TOKENS", "FAILED", 1, "stderr",
		},
		{
			"check ok",
			func(t *testing.T) []string { return []string{"check", "--out", statusFoldedOut(t)} },
			"CHECK", "OK", 0, "stdout",
		},
		{
			"check refused",
			func(t *testing.T) []string { return []string{"check"} },
			"CHECK", "REFUSED", 2, "stderr",
		},
		{
			"check failed",
			func(t *testing.T) []string {
				return []string{"check", "--out", mkdir(t, filepath.Join(t.TempDir(), "out"))}
			},
			"CHECK", "FAILED", 1, "stderr",
		},
		{
			"sources ok",
			func(t *testing.T) []string {
				dir, tr := statusTranscriptDir(t, false)
				return []string{"sources", "--repos", reposFile(t, dir), "--day", "2026-09-11", "--claude", "bench=" + tr}
			},
			"SOURCES", "OK", 0, "stdout",
		},
		{
			"sources refused",
			func(t *testing.T) []string { return []string{"sources"} },
			"SOURCES", "REFUSED", 2, "stderr",
		},
		{
			"ledger ok",
			func(t *testing.T) []string {
				return []string{"ledger", "--out", statusFoldedOut(t), "--day", "2026-09-11", "--redis", "127.0.0.1:0", "--dry-run"}
			},
			"LEDGER", "OK", 0, "stdout",
		},
		{
			"ledger refused",
			func(t *testing.T) []string { return []string{"ledger"} },
			"LEDGER", "REFUSED", 2, "stderr",
		},
		{
			"ledger failed",
			func(t *testing.T) []string {
				empty := mkdir(t, filepath.Join(t.TempDir(), "out"))
				return []string{"ledger", "--out", empty, "--day", "2026-09-11", "--redis", "127.0.0.1:0", "--dry-run"}
			},
			"LEDGER", "FAILED", 1, "stdout",
		},
		{
			"report ok",
			func(t *testing.T) []string {
				dir, tr := statusTranscriptDir(t, false)
				return []string{"report", "--who", "ada", "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "bench=" + tr}
			},
			"REPORT", "OK", 0, "stderr",
		},
		{
			"report refused",
			func(t *testing.T) []string { return []string{"report"} },
			"REPORT", "REFUSED", 2, "stderr",
		},
		{
			"report redis refused",
			func(t *testing.T) []string { return []string{"report", "--redis", "127.0.0.1:0"} },
			"REPORT", "REFUSED", 2, "stderr",
		},
		{
			"report failed",
			func(t *testing.T) []string {
				dir, tr := statusTranscriptDir(t, true)
				return []string{"report", "--who", "ada", "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "bench=" + tr}
			},
			"REPORT", "FAILED", 1, "stderr",
		},
		{
			"sum ok",
			func(t *testing.T) []string { return []string{"sum", "--out", statusFoldedOut(t), "--month", "2026-09"} },
			"SUM", "OK", 0, "stdout",
		},
		{
			"sum refused",
			func(t *testing.T) []string { return []string{"sum"} },
			"SUM", "REFUSED", 2, "stderr",
		},
		{
			"profiles ok",
			func(t *testing.T) []string {
				root := mkdir(t, filepath.Join(t.TempDir(), "root"))
				job := filepath.Join(root, "batch-a", "jobs", "j1")
				cardPrompt(t, job, "500")
				cardUsageFile(t, filepath.Join(job, "usage.tsv"),
					"deepseek", "deepseek-v4", "2026-09-15T10:00:00Z", "1000", "100", "0.0100")
				return []string{"profiles", "--swarm-root", root}
			},
			"PROFILES", "OK", 0, "stdout",
		},
		{
			"profiles refused",
			func(t *testing.T) []string { return []string{"profiles"} },
			"PROFILES", "REFUSED", 2, "stderr",
		},
		{
			"session ok",
			func(t *testing.T) []string { return []string{"session", "--claude-session", writeSession(t)} },
			"", "", 0, "stdout",
		},
		{
			"session refused",
			func(t *testing.T) []string { return []string{"session"} },
			"TOKENS", "REFUSED", 2, "stderr",
		},
		{
			"session refused at exit 1",
			func(t *testing.T) []string {
				out := mkdir(t, filepath.Join(t.TempDir(), "out"))
				write(t, filepath.Join(out, "2026-09-11.tsv"), "not a day file\n")
				return []string{"session", "--claude-session", writeSession(t), "--out", out}
			},
			"TOKENS", "REFUSED", 1, "stderr",
		},
		{
			"version ok",
			func(t *testing.T) []string { return []string{"version"} },
			"", "", 0, "stdout",
		},
		{
			"version refused",
			func(t *testing.T) []string { return []string{"version", "extra"} },
			"VERSION", "REFUSED", 2, "stderr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := invoke(t, tc.args(t)...)
			assert.Equal(t, tc.exit, r.exit, "stdout=%s stderr=%s", r.stdout, r.stderr)
			if tc.token == "" {
				return
			}
			stream := r.stdout
			if tc.stream == "stderr" {
				stream = r.stderr
			}
			assert.Equal(t, tc.word, statusWord(stream, tc.token), "stdout=%q stderr=%q", r.stdout, r.stderr)
		})
	}
}

// statusTranscriptDir writes one transcript holding a single message and returns its
// directory and path. With noid the message carries no id, so a fold over it drops
// everything it read.
func statusTranscriptDir(t *testing.T, noid bool) (dir, tr string) {
	t.Helper()
	dir = t.TempDir()
	tr = mkdir(t, filepath.Join(dir, "transcripts"))
	id := "a1"
	if noid {
		id = ""
	}
	write(t, filepath.Join(tr, "a.jsonl"), msg(id, "2026-09-11T10:00:00Z", "fable",
		map[string]int{"input_tokens": 100, "output_tokens": 40}, "/x/schema/a.go")+"\n")
	return dir, tr
}

// statusFoldArgs is a fold over the status transcript: a valid day with noid false, a
// fold that dropped every message with noid true.
func statusFoldArgs(t *testing.T, noid bool) []string {
	t.Helper()
	dir, tr := statusTranscriptDir(t, noid)
	out := mkdir(t, filepath.Join(dir, "out"))
	return []string{"fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "bench=" + tr}
}

// statusFoldedOut folds one valid day and returns the output directory holding it.
func statusFoldedOut(t *testing.T) string {
	t.Helper()
	dir, tr := statusTranscriptDir(t, false)
	out := mkdir(t, filepath.Join(dir, "out"))
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "bench="+tr)
	require.Equal(t, 0, r.exit, "fixture fold failed\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	return out
}

// statusWord is the first status word after the token on any line of the stream, or the
// empty string when no line of the token carries one.
func statusWord(stream, token string) string {
	for _, line := range strings.Split(stream, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != token {
			continue
		}
		if w := strings.TrimSuffix(f[1], ":"); w == "OK" || w == "FAILED" || w == "REFUSED" {
			return w
		}
	}
	return ""
}
