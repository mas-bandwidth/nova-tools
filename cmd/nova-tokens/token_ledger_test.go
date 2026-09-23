package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// useLedgerStore points the ledger/report verbs at one store for the test: the in-memory
// fake by default, the real Postgres behind RECORD_TEST_PG (the soak half of the contract).
func useLedgerStore(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("RECORD_TEST_PG"); dsn != "" {
		return dsn
	}
	fake := record.NewFakeStore()
	prev := openLedger
	openLedger = func(dsn string) (record.LedgerStore, error) { return fake, nil }
	t.Cleanup(func() { openLedger = prev })
	return "fake"
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

// TestTheMonthlyTokenReportFromPostgresEqualsTheFoldedTsv is docs/SPEC-STATE.md's red test
// the-monthly-token-report-from-postgres-equals-the-folded-tsv-to-the-token (#2201): the
// day rows the fold writes are indexed into token_ledger, `report` over the table is a
// GROUP BY, and it equals the folded day TSVs for every one of the five types and every
// (day, model, repo) -- while the day files themselves are left byte for byte as the fold
// wrote them.
func TestTheMonthlyTokenReportFromPostgresEqualsTheFoldedTsv(t *testing.T) {
	dsn := useLedgerStore(t)
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

	idx := invoke(t, "ledger", "--out", out, "--month", "2026-09", "--postgres", dsn)
	wantExit(t, idx, 0)
	wantContains(t, idx.stdout, "LEDGER OK month=2026-09 days=3")

	rep := invoke(t, "report", "--postgres", dsn, "--month", "2026-09", "--by", "tuple")
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
	wantContains(t, rep.stdout, "REPORT OK month=2026-09 source=token_ledger groups=4")

	// Re-indexing a day replaces it: the table is the day files' index, not an append log.
	wantExit(t, invoke(t, "ledger", "--out", out, "--day", "2026-09-13", "--postgres", dsn), 0)
	again := invoke(t, "report", "--postgres", dsn, "--month", "2026-09", "--by", "tuple")
	if strings.Join(reportTuples(again.stdout), "\n") != strings.Join(want, "\n") {
		t.Fatalf("re-indexing a day changed the report:\n%s", again.stdout)
	}

	// The per-model group carries cache_write and reasoning too.
	byModel := invoke(t, "report", "--postgres", dsn, "--month", "2026-09")
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

// TestReportPostgresRefusesWithoutMonth: the store report names what it wants rather than
// guessing a month.
func TestReportPostgresRefusesWithoutMonth(t *testing.T) {
	dsn := useLedgerStore(t)
	r := invoke(t, "report", "--postgres", dsn)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--month is required")
	r = invoke(t, "report", "--postgres", dsn, "--month", "2026-09", "--by", "card")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--by is model, repo, day or tuple")
}
