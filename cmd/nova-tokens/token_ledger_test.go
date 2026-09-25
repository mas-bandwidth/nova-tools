package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// ledgerRedis is the Redis the ledger/report verbs are pointed at for the test: a miniredis
// on loopback, reached through the real client and the real --redis flag, no seam.
func ledgerRedis(t *testing.T) (string, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return mr.Addr(), mr
}

// foldedTuples is the monthly report computed from the day TSVs alone: every row of every
// day file of the month, summed per (day, model, repo) with the fold's own per-type rule (a
// dash adds nothing, a type any row reported is a number). It is the side of the parity the
// store must equal to the token.
func foldedTuples(t *testing.T, out, month string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(out, month+"-*"+tokens.FileSuffix))
	if err != nil {
		t.Fatal(err)
	}
	type acc struct {
		rows int
		c    tokens.Counts
	}
	sums := map[[3]string]*acc{}
	for _, p := range paths {
		d, findings, err := tokens.ReadDayFile(p)
		if err != nil || len(findings) > 0 {
			t.Fatalf("day file %s: %v %v", p, err, findings)
		}
		for _, r := range d.Rows {
			k := [3]string{r.Date, r.Model, r.Repo}
			a := sums[k]
			if a == nil {
				a = &acc{}
				sums[k] = a
			}
			a.rows++
			a.c.Add(r.Counts)
		}
	}
	var lines []string
	for k, a := range sums {
		line := "REPORT day=" + k[0] + " model=" + k[1] + " repo=" + k[2] + " rows=" + strconv.Itoa(a.rows)
		for ty := tokens.Type(0); ty < tokens.NTypes; ty++ {
			line += " " + tokens.TypeNames[ty] + "=" + a.c.Cell(ty)
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}

func reportTuples(stdout string) []string {
	var lines []string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "REPORT day=") {
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return lines
}

func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		got[e.Name()] = read(t, filepath.Join(dir, e.Name()))
	}
	return got
}

// TestTheMonthlyTokenReportFromTheRedisLedgerEqualsTheFoldedTsv is docs/SPEC-STATE.md's
// test 17 (#2201; recut of #3243 on Redis under #2623, Postgres retired): the day rows the
// fold writes are indexed into tokens:ledger:<day>, `report --redis` is a GROUP BY over the
// month's day hashes, and it equals the folded day TSVs for every one of the five types and
// every (day, model, repo) -- while the day files themselves are left byte for byte as the
// fold wrote them.
func TestTheMonthlyTokenReportFromTheRedisLedgerEqualsTheFoldedTsv(t *testing.T) {
	t.Parallel()

	dsn, mr := ledgerRedis(t)
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))

	// Two days folded by the real fold from Claude transcripts: two models, two repos,
	// cache writes and reads apart.
	write(t, filepath.Join(tr, "a.jsonl"), strings.Join([]string{
		msg("a1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 40, "cache_creation_input_tokens": 7, "cache_read_input_tokens": 900}, "/x/schema/a.go"),
		msg("a2", "2026-09-11T11:00:00Z", "opus", map[string]int{"input_tokens": 30, "output_tokens": 9, "cache_read_input_tokens": 11}, "/x/serialize/b.go"),
		msg("a3", "2026-09-12T09:00:00Z", "fable", map[string]int{"input_tokens": 5, "output_tokens": 3, "cache_creation_input_tokens": 2}, "/x/serialize/c.go"),
	}, "\n")+"\n")
	for _, day := range []string{"2026-09-11", "2026-09-12"} {
		wantExit(t, invoke(t, "fold", "--out", out, "--day", day, "--repos", repos, "--claude", "bench="+tr), 0)
	}
	// A third day carries two cards on one (day, model, repo), with reasoning measured on
	// one and a dash on the other: the store is keyed (day, card, model, repo), the report
	// sums the cards, and a dash is not a zero.
	var c1, c2 tokens.Counts
	c1.Set(tokens.Input, 10)
	c1.Set(tokens.Output, 20)
	c1.Set(tokens.Reasoning, 3)
	c2.Set(tokens.Input, 1)
	c2.Set(tokens.Output, 2)
	c2.Set(tokens.CacheWrite, 4)
	third := tokens.DayFile{Day: "2026-09-13", At: "2026-09-14T00:00:00Z", Build: "test", Turns: "2",
		Sources: []string{"openai:o"}, Rows: []tokens.DayRow{
			{Date: "2026-09-13", Model: "gpt", Repo: "schema", Unit: "card-a", Counts: c1, Basis: "utc", Sources: []string{"openai:o"}},
			{Date: "2026-09-13", Model: "gpt", Repo: "schema", Unit: "card-b", Counts: c2, Basis: "utc", Sources: []string{"openai:o"}},
		}}
	if err := third.Save(out); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, out)

	idx := invoke(t, "ledger", "--out", out, "--month", "2026-09", "--redis", dsn)
	wantExit(t, idx, 0)
	wantContains(t, idx.stdout, "LEDGER OK month=2026-09 days=3")
	// The key layout: one hash per day under tokens:ledger:<day>, nothing else written.
	if keys := mr.Keys(); strings.Join(keys, " ") != "tokens:ledger:2026-09-11 tokens:ledger:2026-09-12 tokens:ledger:2026-09-13" {
		t.Fatalf("the ledger wrote keys %v; want one tokens:ledger:<day> per folded day", keys)
	}

	rep := invoke(t, "report", "--redis", dsn, "--month", "2026-09", "--by", "tuple")
	wantExit(t, rep, 0)
	want := foldedTuples(t, out, "2026-09")
	got := reportTuples(rep.stdout)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the store report is not the folded TSV to the token\nstore:\n%s\nfolded:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(want) != 4 {
		t.Fatalf("want 4 (day, model, repo) tuples from the fixture, the folded side has %d:\n%s", len(want), strings.Join(want, "\n"))
	}
	wantContains(t, rep.stdout, "REPORT day=2026-09-13 model=gpt repo=schema rows=2 input=11 output=22 cache_write=4 cache_read=- reasoning=3")
	wantContains(t, rep.stdout, "REPORT OK month=2026-09 source=redis groups=4 rows=5 indexed=3 missing=27")

	// Re-indexing a day replaces it: the table is the day files' index, not an append log.
	wantExit(t, invoke(t, "ledger", "--out", out, "--day", "2026-09-13", "--redis", dsn), 0)
	again := invoke(t, "report", "--redis", dsn, "--month", "2026-09", "--by", "tuple")
	if strings.Join(reportTuples(again.stdout), "\n") != strings.Join(want, "\n") {
		t.Fatalf("re-indexing a day changed the report:\n%s", again.stdout)
	}

	// The per-model group carries cache_write and reasoning too.
	byModel := invoke(t, "report", "--redis", dsn, "--month", "2026-09")
	wantExit(t, byModel, 0)
	wantContains(t, byModel.stdout, "REPORT model=gpt rows=2 input=11 output=22 cache_write=4 cache_read=- reasoning=3")

	// The fold's day files are untouched by the index and the report.
	after := snapshotDir(t, out)
	if len(after) != len(before) {
		t.Fatalf("the day directory changed: %d files before, %d after", len(before), len(after))
	}
	for name, body := range before {
		if after[name] != body {
			t.Errorf("day file %s changed under ledger/report", name)
		}
	}
}

// TestReportRedisRefusesWithoutMonth: the store report names what it wants rather than
// guessing a month, and one report has one source.
func TestReportRedisRefusesWithoutMonth(t *testing.T) {
	t.Parallel()

	dsn, _ := ledgerRedis(t)
	r := invoke(t, "report", "--redis", dsn)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--month is required")
	r = invoke(t, "report", "--redis", dsn, "--month", "2026-09", "--by", "card")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--by is model, repo, day or tuple")
	r = invoke(t, "report", "--redis", dsn, "--ledger", "x.tsv", "--month", "2026-09")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--redis and --ledger are two sources for one report")
}

// TestLedgerRefusesWithoutRedisAndNamesAMissingDay: `ledger` wants its store named, and a
// day with no file is a NO naming the fold, with nothing written for it.
func TestLedgerRefusesWithoutRedisAndNamesAMissingDay(t *testing.T) {
	t.Parallel()

	out := mkdir(t, filepath.Join(t.TempDir(), "out"))
	r := invoke(t, "ledger", "--out", out, "--day", "2026-09-11")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--redis")
	addr, mr := ledgerRedis(t)
	r = invoke(t, "ledger", "--out", out, "--day", "2026-09-11", "--redis", addr)
	wantExit(t, r, 1)
	wantContains(t, r.stdout, "LEDGER BAD day=2026-09-11 why=no day file; fold --day 2026-09-11 first")
	wantContains(t, r.stdout, "LEDGER NO day=2026-09-11 days=0 rows=0 bad=1")
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("a missing day wrote %v", keys)
	}
}

// TestLedgerReadsThePasswordFromTheVariableItIsToldToOnly: the password is never a flag and
// no variable is consulted unless --password-env names it.
func TestLedgerReadsThePasswordFromTheVariableItIsToldToOnly(t *testing.T) {
	addr, mr := ledgerRedis(t)
	mr.RequireAuth("sesame")
	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "sesame")
	r := invoke(t, "report", "--redis", addr, "--month", "2026-09")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "REPORT FAILED store=redis")
	t.Setenv("LEDGER_TEST_PW", "sesame")
	r = invoke(t, "report", "--redis", addr, "--month", "2026-09", "--password-env", "LEDGER_TEST_PW")
	wantExit(t, r, 1)
	wantContains(t, r.stdout, "REPORT NO month=2026-09 source=redis indexed=0")
}

// TestReportRedisNoIndexedDaysExitsOne and TestReportRedisPartialMonthNamesIndexedMissing
// cover #3462: a month with no indexed calendar-day keys is REPORT NO exit 1, and a month
// with only some days indexed names indexed and missing on the OK line.
func TestReportRedisNoIndexedDaysExitsOne(t *testing.T) {
	t.Parallel()

	addr, _ := ledgerRedis(t)
	r := invoke(t, "report", "--redis", addr, "--month", "2026-08")
	wantExit(t, r, 1)
	wantContains(t, r.stdout, "REPORT NO month=2026-08 source=redis indexed=0")
}

func TestReportRedisPartialMonthNamesIndexedMissing(t *testing.T) {
	t.Parallel()

	addr, mr := ledgerRedis(t)
	// Seed only two of September's 30 days.
	mr.HSet("tokens:ledger:2026-09-05", `["c","m","r"]`, `{"provider":"x","tokens":[10,20,null,null,3],"sources":"x:o"}`)
	mr.HSet("tokens:ledger:2026-09-20", `["c2","m2","r2"]`, `{"provider":"y","tokens":[5,10,1,null,0],"sources":"y:o"}`)

	r := invoke(t, "report", "--redis", addr, "--month", "2026-09", "--by", "tuple")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "REPORT OK month=2026-09 source=redis groups=2 rows=2 indexed=2 missing=28")
}

// TestLedgerAndReportDialAsTheAclUser (#3461): the fleet Redis has its default user off and
// the bench password is the ACL user bench's, so a seat password without its user is
// WRONGPASS. `--user bench --password-env X` connects as bench for both verbs; the same seat
// comes from NOVA_SPRINT_REDIS_USER when no --user is given (the one config nova-sprint
// uses); a user whose password variable is empty is refused before any dial.
func TestLedgerAndReportDialAsTheAclUser(t *testing.T) {
	addr, mr := ledgerRedis(t)
	mr.RequireUserAuth("bench", "sesame")
	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "")
	t.Setenv("LEDGER_TEST_PW", "sesame")
	out := t.TempDir()
	var c tokens.Counts
	c.Set(tokens.Input, 10)
	c.Set(tokens.Output, 20)
	day := tokens.DayFile{Day: "2026-09-11", At: "2026-09-12T00:00:00Z", Build: "test", Turns: "1",
		Sources: []string{"openai:o"}, Rows: []tokens.DayRow{
			{Date: "2026-09-11", Model: "gpt", Repo: "schema", Unit: "card-a", Counts: c, Basis: "utc", Sources: []string{"openai:o"}},
		}}
	if err := day.Save(out); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "report", "--redis", addr, "--month", "2026-09", "--password-env", "LEDGER_TEST_PW")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "REPORT FAILED store=redis")

	r = invoke(t, "ledger", "--out", out, "--day", "2026-09-11", "--redis", addr, "--user", "bench", "--password-env", "LEDGER_TEST_PW")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "LEDGER OK day=2026-09-11 days=1")
	r = invoke(t, "report", "--redis", addr, "--month", "2026-09", "--user", "bench", "--password-env", "LEDGER_TEST_PW")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "REPORT OK month=2026-09 source=redis groups=1")

	t.Setenv("NOVA_SPRINT_REDIS_USER", "bench")
	t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "LEDGER_TEST_PW")
	r = invoke(t, "report", "--redis", addr, "--month", "2026-09")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "REPORT OK month=2026-09 source=redis groups=1")

	t.Setenv("NOVA_SPRINT_REDIS_USER", "")
	t.Setenv("LEDGER_EMPTY_PW", "")
	r = invoke(t, "report", "--redis", addr, "--month", "2026-09", "--user", "bench", "--password-env", "LEDGER_EMPTY_PW")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "--user bench but LEDGER_EMPTY_PW is empty; run under nova-secrets exec --only LEDGER_EMPTY_PW")
	if strings.Contains(r.stderr+r.stdout, "sesame") {
		t.Fatal("a refusal printed the password")
	}
}
