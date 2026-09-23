package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// `fold --units` end to end, over three synthetic child transcripts and the work set this
// bench actually ran on 2026-09-18, copied into testdata.
//
// The question it answers is Glenn's obligation and the repo column cannot: the ledger
// says what the month cost on nova-tools, and what is owed is what ONE PIECE OF WORK cost.

// unitsFixture writes three child transcripts under one directory: one that names its PR,
// one that names its branch, one that names its lane clone directory, and a fourth that
// names none of the three. Each is a separate file, because a unit is attributed per
// TRANSCRIPT and the fixture has to be able to tell the four apart.
func unitsFixture(t *testing.T, dir string) string {
	t.Helper()
	tr := filepath.Join(dir, "transcripts")
	// The certify unit, found by its :pr 1369 -- written the way a child writes it, inside
	// a gh argument rather than as a bare number. No scheme and no host: the CI class rule
	// refuses a real host in a test literal, and the rule under test is the /pull/<n> path.
	write(t, filepath.Join(tr, "child-a.jsonl"),
		msg("a1", "2026-09-18T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 100, "output_tokens": 10},
			"/x/schema/a.go")+"\n"+
			msg("a2", "2026-09-18T10:05:00Z", "claude-opus-5", map[string]int{"input_tokens": 200, "output_tokens": 20},
				"gh pr view mas-bandwidth/nova-tools/pull/1369")+"\n")
	// The work unit, found by its :lane "work" clone directory.
	write(t, filepath.Join(tr, "child-b.jsonl"),
		msg("b1", "2026-09-18T11:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 1000, "output_tokens": 100},
			"/Users/glenn/rowan-working/tmp/lane-work/repo/schema/x.go")+"\n")
	// A transcript that names nothing in the set: its spend is the `-` group, which is the
	// number that says whether the work set is good enough.
	write(t, filepath.Join(tr, "child-c.jsonl"),
		msg("c1", "2026-09-18T12:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 7, "output_tokens": 1},
			"/x/serialize/z.go")+"\n")
	return tr
}

func unitsFile(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "units", "pitstop-2026-09-18-units.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFoldUnitsAttributesEachTranscriptToOneUnit(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := unitsFixture(t, dir)

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-18", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr, "--units", unitsFile(t))
	wantExit(t, r, 0)
	// One line says what was loaded, so a run against the wrong set is visible.
	wantContains(t, lineWith(r.stdout, "TOKENS UNITS"), "set=pitstop-2026-09-18-units")
	wantContains(t, lineWith(r.stdout, "TOKENS UNITS"), "units=20")

	day := read(t, filepath.Join(out, "2026-09-18.tsv"))
	if !strings.Contains(strings.Split(day, "\n")[1], "\tunits") {
		t.Fatalf("the header carries no units column:\n%s", day)
	}
	for _, want := range []struct{ repo, unit string }{
		{"schema", "certify:verb"},   // child-a, by its :pr
		{"schema", "lisp:collision"}, // child-b, by its :lane clone directory
		{"serialize", tokens.Dash},   // child-c, which named nothing
	} {
		if !hasUnitRow(day, want.repo, want.unit) {
			t.Errorf("no row for repo=%s unit=%s:\n%s", want.repo, want.unit, day)
		}
	}
}

// The compatibility claim: a fold with no --units writes the rows it always wrote, with
// the twelfth column `-` on every one of them.
func TestAFoldWithNoUnitsPutsEveryRowOnTheDash(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := unitsFixture(t, dir)

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-18", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr)
	wantExit(t, r, 0)
	if lineWith(r.stdout, "TOKENS UNITS") != "" {
		t.Error("a fold with no --units printed a TOKENS UNITS line")
	}
	day := read(t, filepath.Join(out, "2026-09-18.tsv"))
	for _, line := range strings.Split(strings.TrimSpace(day), "\n")[2:] {
		cells := strings.Split(line, "\t")
		if len(cells) != len(tokens.Columns) {
			t.Fatalf("the row has %d columns, want %d: %q", len(cells), len(tokens.Columns), line)
		}
		if cells[11] != tokens.Dash {
			t.Errorf("a fold with no --units wrote unit %q: %q", cells[11], line)
		}
	}
}

// A day file written before the units column existed still reads, and its rows read as
// `-`: the reader takes either width and nothing has to be refolded to be summed.
func TestTheReaderTakesTheElevenColumnFile(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	write(t, filepath.Join(out, "2026-09-18.tsv"),
		"nova-tokens v1 day=2026-09-18 at=2026-09-18T23:00:00Z build=test turns=3 sources=claude:glenn\n"+
			tokens.HeaderLineV1+"\n"+
			"2026-09-18\tclaude-opus-5\tschema\t100\t10\t-\t-\t-\t0\tutc\tclaude:glenn\n")
	day, findings, err := tokens.ReadDayFile(filepath.Join(out, "2026-09-18.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("an eleven-column file is not a finding: %v", findings)
	}
	if len(day.Rows) != 1 || day.Rows[0].Unit != tokens.Dash {
		t.Fatalf("the row's unit is %+v, want %q", day.Rows, tokens.Dash)
	}
	// And `sum` reads it with every other day.
	r := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, r, 0)
	wantContains(t, lineWith(r.stdout, "SUM OK"), "units=1")
}

func TestSumByUnitPrintsTheUnitsTable(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := unitsFixture(t, dir)
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-18", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr, "--units", unitsFile(t)), 0)

	r := invoke(t, "sum", "--out", out, "--month", "2026-09", "--by", "unit")
	wantExit(t, r, 0)
	// Heaviest first, and the `-` group is PRINTED: the share nobody attributed is the
	// number that says whether the work set is good enough.
	for _, want := range []string{"SUM UNIT unit=lisp:collision", "SUM UNIT unit=certify:verb", "SUM UNIT unit=-"} {
		if lineWith(r.stdout, want) == "" {
			t.Errorf("no %q line:\n%s", want, r.stdout)
		}
	}
	if lineWith(r.stdout, "SUM PAIR") != "" || lineWith(r.stdout, "SUM MODEL") != "" {
		t.Errorf("--by unit printed the (model, repo) tables as well:\n%s", r.stdout)
	}
	wantContains(t, lineWith(r.stdout, "SUM OK"), "units=3")

	// And the default is what it always was.
	d := invoke(t, "sum", "--out", out, "--month", "2026-09")
	wantExit(t, d, 0)
	if lineWith(d.stdout, "SUM PAIR") == "" || lineWith(d.stdout, "SUM UNIT") != "" {
		t.Errorf("the default sum is not the pair tables:\n%s", d.stdout)
	}
}

func TestSumRefusesAByItDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	r := invoke(t, "sum", "--out", out, "--month", "2026-09", "--by", "lane")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--by is pair or unit, got lane")
}

func TestFoldRefusesAUnitsFileItCannotRead(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := unitsFixture(t, dir)
	bad := write(t, filepath.Join(dir, "not-a-set.lisp"), "(:plan \"p\" :nodes ())\n")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-18", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr, "--units", bad)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "--units "+bad)

	missing := filepath.Join(dir, "nowhere.lisp")
	m := invoke(t, "fold", "--out", out, "--day", "2026-09-18", "--repos", reposFile(t, dir),
		"--claude", "glenn="+tr, "--units", missing)
	wantExit(t, m, 2)
	wantContains(t, m.stderr, "--units "+missing)
}

// hasUnitRow reports whether the day file holds a row with this repo and this unit.
func hasUnitRow(day, repo, unit string) bool {
	for _, line := range strings.Split(strings.TrimSpace(day), "\n") {
		cells := strings.Split(line, "\t")
		if len(cells) == len(tokens.Columns) && cells[2] == repo && cells[11] == unit {
			return true
		}
	}
	return false
}
